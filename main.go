package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron"
)

type DummyJob struct {
	chatID  int64
	message string
	bot     *tgbotapi.BotAPI
}

type BotConfig struct {
	db             *sql.DB
	botapi         *tgbotapi.BotAPI
	regMsg, wedMsg chan string
	chatIDs        map[int64]bool
	cron           *cron.Cron
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
	create table foo (chat_id int not null primary key, tz text not null, message text);
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

func initBot(db *sql.DB, bot *tgbotapi.BotAPI) BotConfig {
	chatIDs := make(map[int64]bool)
	c := cron.New()

	regMsg := make(chan string)
	wedMsg := make(chan string)

	go linesGenerator("420_msg.txt", regMsg)
	go linesGenerator("wednesday.txt", wedMsg)

	rows, err := db.Query("select chat_id, tz, message from foo")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	bc := BotConfig{
		db:      db,
		botapi:  bot,
		cron:    c,
		regMsg:  regMsg,
		wedMsg:  wedMsg,
		chatIDs: chatIDs,
	}

	for rows.Next() {
		var ID int64
		var tz string
		var message string
		err = rows.Scan(&ID, &tz, &message)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(ID, tz, message)

		bc.makeJobs(ID, tz)
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	c.AddFunc("CRON_TZ=Europe/Kiev 30 23 * * *", resetJob(chatIDs))
	c.Start()

	return bc
}

func tgbotApiKey() string {
	return os.Getenv("TG_API_KEY")
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

	botConfig := initBot(db, bot)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		chatID := update.Message.Chat.ID

		botConfig.makeJobs(chatID, "Europe/Kiev")
		insertStmt(db, chatID, "Europe/Kiev", "whatever")

		if update.Message.IsCommand() {
			if update.Message.Command() == "pidor" {
				log.Printf("Detected /pidor command at %d. All chat ids:", chatID)
				log.Println(botConfig.chatIDs)
				v := botConfig.chatIDs[chatID]
				if v {
					botConfig.chatIDs[chatID] = false
					// msg := tgbotapi.NewMessage(chatID, "Я тебя запомнил, слышишь?")
					msg := tgbotapi.NewStickerShare(
						chatID,
						"CAACAgIAAxkBAAIBq1_I9VKJwdOKaGlg7VrGfj2-9gHlAAIeAQAC0t1pBceuDjBghrA8HgQ")
					bot.Send(msg)
				} else {
					log.Println("already remembered this chat id")
				}
			}
			if update.Message.Command() == "setTimezone" {

			}
		}
	}
}

type MessageGenerator interface {
	Get() string
}

type ChanMessageGenerator chan string
type ConstMessageGenerator string

func (msgsPool ChanMessageGenerator) Get() string {
	return <-msgsPool
}

func (msgsPool ConstMessageGenerator) Get() string {
	return string(msgsPool)
}

func (bc BotConfig) makeJobs(chatID int64, timezone string) {
	_, ok := bc.chatIDs[chatID]
	if !ok {
		bc.chatIDs[chatID] = true
		bc.cron.AddFunc(
			fmt.Sprintf("CRON_TZ=%s 1 23 * * *", timezone),
			reminderJob(chatID, ConstMessageGenerator("This is a reminder to call /pidor !"), bc.botapi, bc.chatIDs))
		bc.cron.AddFunc(
			fmt.Sprintf("CRON_TZ=%s 20 4 * * *", timezone),
			makeJob(chatID, ChanMessageGenerator(bc.regMsg), bc.botapi))
		bc.cron.AddFunc(
			fmt.Sprintf("CRON_TZ=%s 20 16 * * 3", timezone),
			makeJob(chatID, ChanMessageGenerator(bc.wedMsg), bc.botapi))
		bc.cron.AddFunc(
			fmt.Sprintf("CRON_TZ=%s 20 16 * * 0-2,4-6", timezone),
			makeJob(chatID, ChanMessageGenerator(bc.regMsg), bc.botapi))

		// Debug messages. Make sure to use testing sqlite db
		log.Printf("Created tasks for %d", chatID)
	}
}

func makeJob(chatID int64, message MessageGenerator, bot *tgbotapi.BotAPI) func() {
	return func() {
		log.Printf("messaging to %d", chatID)
		DummyJob{chatID, message.Get(), bot}.Run()
	}
}

func reminderJob(chatID int64, message MessageGenerator, bot *tgbotapi.BotAPI, chatIDs map[int64]bool) func() {
	subJob := makeJob(chatID, message, bot)
	return func() {
		v, ok := chatIDs[chatID]
		if !ok {
			panic(fmt.Sprintf("this should never happened: %d not in chat IDs", chatID))
		}
		if v {
			subJob()
		} else {
			log.Printf("Decided not to remind anything to %d", chatID)
		}
	}
}

func resetJob(chatIDs map[int64]bool) func() {
	return func() {
		for k := range chatIDs {
			chatIDs[k] = true
			log.Printf("Resetting item %d", k)
		}
	}
}

func readLines(fname string) ([]string, error) {
	file, err := os.Open(fname)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func linesGenerator(fname string, out chan string) {
	for {
		lines, err := readLines(fname)
		if err != nil {
			out <- "https://www.youtube.com/watch?v=-5qmvsZr0F8"
		}
		rand.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })

		for _, l := range lines {
			out <- l
		}
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	db := initSqliteDb()
	defer db.Close()
	botLoop(db)
}
