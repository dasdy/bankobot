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
	chat_id int64
	message string
	bot     *tgbotapi.BotAPI
}

func (d DummyJob) Run() {
	msg := tgbotapi.NewMessage(d.chat_id, d.message)
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

func insertStmt(db *sql.DB, chat_id int64, timezone string, message string) {

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
	_, err = stmt.Exec(chat_id, timezone, message)
	if err != nil {
		log.Printf("%q: %d, %s\n", err, chat_id, timezone)
	}
	tx.Commit()

}

func initBot(db *sql.DB, c *cron.Cron, bot *tgbotapi.BotAPI, chat_ids map[int64]bool) {
	rows, err := db.Query("select chat_id, tz, message from foo")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tz string
		var message string
		err = rows.Scan(&id, &tz, &message)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(id, tz, message)

		makeJobs(c, bot, id, chat_ids)
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

	chat_ids := make(map[int64]bool)
	c := cron.New()

	initBot(db, c, bot, chat_ids)
	c.Start()
	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		chat_id := update.Message.Chat.ID

		makeJobs(c, bot, chat_id, chat_ids)
		insertStmt(db, chat_id, "Europe/Kiev", "whatever")
	}
}

func makeJobs(c *cron.Cron, bot *tgbotapi.BotAPI, chat_id int64, chat_ids map[int64]bool) {
	_, ok := chat_ids[chat_id]
	if !ok {
		chat_ids[chat_id] = true
		c.AddFunc("CRON_TZ=Europe/Kiev 20 4 * * *", makeJob(chat_id, "It's /pidor o'clock!", bot))
		c.AddFunc("CRON_TZ=Europe/Kiev 20 16 * * *", makeJob(chat_id, "Che tam po banochkam?", bot))
		log.Printf("Created tasks for %d", chat_id)
	}
}

func makeJob(chat_id int64, message string, bot *tgbotapi.BotAPI) func() {
	return func() {
		log.Printf("messaging to %d", chat_id)
		DummyJob{chat_id, "Che tam po banochkam?", bot}.Run()
	}
}

func main() {
	db := initSqliteDb()
	defer db.Close()
	botLoop(db)
}
