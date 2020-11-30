package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron"
)

type DummyJob struct {
	chatID  int64
	message string
	bot     *tgbotapi.BotAPI
}

func (d DummyJob) Run() {
	msg := tgbotapi.NewMessage(d.chatID, d.message)
	d.bot.Send(msg)
}

func initSqliteDb() *sql.DB {
	db, err := sql.Open("sqlite3", "./foo.db")
	if err != nil {
		log.Fatal(err)
	}

	sqlStmt := `
	create table foo (chat_id int not null primary key, tz text, message text);
	delete from foo;
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlStmt)
	}

	return db
}

func insertStmt(db *sql.DB, chatID int64, timezone string, message string) {

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := tx.Prepare(`insert into foo(chat_id, tz, message) values(?, ?, ?)
	on conflict(chat_id) do update set tz=excluded.tz, message=excluded.message`)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(chatID, timezone, message)
	if err != nil {
		log.Printf("%q: %d, %s\n", err, chatID, timezone)
	}
	tx.Commit()

}

func initBot(db *sql.DB, c *cron.Cron, bot *tgbotapi.BotAPI, chatIDs map[int64]bool) {
	rows, err := db.Query("select chat_id, tz, message from foo")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ID int64
		var tz string
		var message string
		err = rows.Scan(&ID, &tz, &message)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(ID, tz, message)

		makeJobs(c, bot, ID, chatIDs)
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}
}

func botLoop(db *sql.DB) {
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TG_API_KEY"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)

	chatIDs := make(map[int64]bool)
	c := cron.New()

	initBot(db, c, bot, chatIDs)

	c.AddFunc("CRON_TZ=Europe/Kiev 59 0 * * *", resetJob(chatIDs))
	c.Start()
	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		chatID := update.Message.Chat.ID

		makeJobs(c, bot, chatID, chatIDs)
		insertStmt(db, chatID, "Europe/Kiev", "whatever")

		if update.Message.IsCommand() {
			if update.Message.Command() == "pidor" {
				log.Println(chatIDs)
				v := chatIDs[chatID]
				if !v {
					chatIDs[chatID] = true
					msg := tgbotapi.NewMessage(chatID, "Я тебя запомнил, слышишь?")
					bot.Send(msg)
				} else {
					log.Println("already remembered this chat id")
				}
			}
		}
	}
}

func makeJobs(c *cron.Cron, bot *tgbotapi.BotAPI, chatID int64, chatIDs map[int64]bool) {
	_, ok := chatIDs[chatID]
	if !ok {
		chatIDs[chatID] = false
		c.AddFunc("CRON_TZ=Europe/Kiev 1 23 * * *", reminderJob(chatID, "This is a reminder to call /pidor !", bot, chatIDs))
		c.AddFunc("CRON_TZ=Europe/Kiev 20 4 * * *", makeJob(chatID, "Банку-раздуплянку полуночникам!", bot))
		c.AddFunc("CRON_TZ=Europe/Kiev 20 16 * * *", makeJob(chatID, "Ну ты понел", bot))
		log.Printf("Created tasks for %d", chatID)
	}
}

func makeJob(chatID int64, message string, bot *tgbotapi.BotAPI) func() {
	return func() {

		log.Printf("messaging to %d", chatID)
		DummyJob{chatID, message, bot}.Run()
	}
}

func reminderJob(chatID int64, message string, bot *tgbotapi.BotAPI, chatIDs map[int64]bool) func() {
	subJob := makeJob(chatID, message, bot)
	return func() {
		v, ok := chatIDs[chatID]
		if !ok {
			panic(fmt.Sprintf("this should never happened: %d not in chat IDs", chatID))
		}
		if v {
			subJob()
		}
	}
}

func resetJob(chatIDs map[int64]bool) func() {
	return func() {
		for k := range chatIDs {
			chatIDs[k] = false
			log.Println("Resetting item %d", k)
		}
	}
}

func main() {
	db := initSqliteDb()
	defer db.Close()
	botLoop(db)
}
