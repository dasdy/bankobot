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

type BotMessage struct {
	chatID    int64
	message   string
	isSticker bool
}

func initSqliteDb() *sql.DB {
	db, err := sql.Open("sqlite3", "./foo.db")
	if err != nil {
		log.Fatal(err)
	}

	sqlStmt := `
	create table foo (chat_id int not null primary key, tz text not null, last_imtestamp text);
	delete from foo;
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %v\n", err, sqlStmt)
	}

	return db
}

func insertStmt(db *sql.DB, chatID int64, timezone, message string) {
	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := tx.Prepare(`insert into foo(chat_id, tz, message, last_timestamp) values(?, ?, ?, datetime('now'))
	on conflict(chat_id) do update set tz=excluded.tz, message=excluded.message, last_timestamp=excluded.last_timestamp`)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(chatID, timezone, message)
	if err != nil {
		log.Printf("%q: %d, %v\n", err, chatID, timezone)
	}
	tx.Commit()
}

func refreshTimestamp(db *sql.DB) {
	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}

	stmt, err := tx.Prepare(`update foo set last_timestamp=datetime('now')`)
	if err != nil {
		log.Fatal(err)
	}

	defer stmt.Close()

	_, err = stmt.Exec()

	if err != nil {
		log.Printf("%v\n", err)
	}

	tx.Commit()
}

var DEBUG = false

type Messager struct {
	api *tgbotapi.BotAPI
}

func (m *Messager) sendMessage(msg BotMessage) {
	log.Printf("Got a message request!")
	var botMsg tgbotapi.Chattable
	if msg.isSticker {
		botMsg = tgbotapi.NewStickerShare(msg.chatID, msg.message)
	} else {
		botMsg = tgbotapi.NewMessage(msg.chatID, msg.message)
	}
	log.Printf("Sending a message: %#v", botMsg)
	m.api.Send(botMsg)
}

type ChatConfig struct {
	shouldSendReminder bool
	timezone           string
}

func (cc *ChatConfig) String() string {
	return fmt.Sprintf("ChatConfig{%v, %v}", cc.shouldSendReminder, cc.timezone)
}

type TimeZoneConfig struct {
	cron  *cron.Cron
	chats map[int64]bool
}

func (cc *TimeZoneConfig) String() string {
	return fmt.Sprintf("TimeZoneConfig{%v, %v}", cc.cron, cc.chats)
}

func ShouldSendReminder(lastTimestamp string, now time.Time) bool {
	const layout string = "2006-01-02 15:04:05"
	timestamp, err := time.Parse(layout, lastTimestamp)
	if err != nil {
		return true
	}

	shouldSendReminder := false

	y1, m1, d1 := now.Date()
	y2, m2, d2 := timestamp.Date()
	if d1 != d2 || m1 != m2 || y1 != y2 {
		shouldSendReminder = true
	}

	return shouldSendReminder
}

type RowReader interface {
	LoadRow(*int64, *string, *string, *string) error
}

type SqlRowReader struct {
	DbRows *sql.Rows
}

func (r SqlRowReader) LoadRow(a *int64, b, c, d *string) error {
	return r.DbRows.Scan(a, b, c, d)
}

func jobTaskFromRow(row RowReader, now time.Time) (int64, string, bool) {
	var ID int64
	var tz string
	var message string
	var lastTimestamp string
	err := row.LoadRow(&ID, &tz, &message, &lastTimestamp)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Row: %v,%v,%v,%v", ID, tz, message, lastTimestamp)

	shouldSendReminder := ShouldSendReminder(lastTimestamp, now)

	return ID, tz, shouldSendReminder
}

func initBot(db *sql.DB, bot *tgbotapi.BotAPI) *BotConfig {
	chatIDs := make(map[int64]*ChatConfig)
	timezones := make(map[string]*TimeZoneConfig)
	c := cron.New()

	regMsg := make(chan string)
	wedMsg := make(chan string)

	go linesGenerator("420_msg.txt", regMsg)
	go linesGenerator("wednesday.txt", wedMsg)

	rows, err := db.Query("select chat_id, tz, message, last_timestamp from foo")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	bc := BotConfig{
		db:            db,
		cron:          c,
		regMsg:        regMsg,
		wedMsg:        wedMsg,
		chatIDs:       chatIDs,
		timeZones:     timezones,
		messager:      Messager{api: bot},
		notifyChannel: make(chan Command),
	}

	now := time.Now()
	reader := SqlRowReader{DbRows: rows}
	for rows.Next() {
		ID, tz, shouldSendReminder := jobTaskFromRow(reader, now)

		bc.makeJobsUnsafe(ID, tz, shouldSendReminder)
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	c.AddFunc("CRON_TZ=Europe/Kiev 30 23 * * *", bc.resetJob())
	c.AddFunc("CRON_TZ=Europe/Kiev 0 23 * * *", bc.remindJob(db))

	if DEBUG {
		c.AddFunc("CRON_TZ=Europe/Kiev */10 * * * *", bc.resetJob())
		c.AddFunc("CRON_TZ=Europe/Kiev */2 * * * *", bc.remindJob(db))
	}

	c.Start()

	return &bc
}

func tgbotApiKey() string {
	return os.Getenv("TG_API_KEY")
}

func initBotConfig(db *sql.DB) (*tgbotapi.UpdatesChannel, *BotConfig) {
	bot, err := tgbotapi.NewBotAPI(tgbotApiKey())
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %v", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 600

	updates, err := bot.GetUpdatesChan(u)

	return &updates, initBot(db, bot)
}

func botLoop(db *sql.DB) {
	updates, botConfig := initBotConfig(db)
	worker := Worker{
		commandCh: botConfig.notifyChannel,
		botConfig: botConfig,
		db:        db,
	}

	go worker.botConfigWorker()

	for update := range *updates {
		if update.Message == nil || !update.Message.IsCommand() {
			continue
		}

		log.Printf("[%v] %v", update.Message.From.UserName, update.Message.Text)

		worker.commandCh <- InsertCommand{chatID: update.Message.Chat.ID, timezone: "Europe/Kiev"}

		switch update.Message.Command() {
		case "pidor":
			worker.commandCh <- NotifyReceivedCommand{update.Message.Chat.ID}
		case "settz":
			worker.commandCh <- ChangeTimezoneCommand{chatID: update.Message.Chat.ID, timezone: update.Message.CommandArguments()}
		}
	}
}

type InsertCommand struct {
	chatID   int64
	timezone string
}

func (c InsertCommand) Do(botConfig BankoBotInterface, db *sql.DB) {
	botConfig.addNewChat(db, c.chatID)
}

type NotifyCommand BotMessage

func (c NotifyCommand) Do(botConfig BankoBotInterface, db *sql.DB) {
	botConfig.sendMessage(BotMessage(c))
}

type NotifyReceivedCommand struct {
	chatID int64
}

func (c NotifyReceivedCommand) Do(botConfig BankoBotInterface, db *sql.DB) {
	botConfig.notifyPidorAccepted(c.chatID)
}

func (c ChangeTimezoneCommand) Do(botConfig BankoBotInterface, db *sql.DB) {
	botConfig.setTimezone(c.chatID, c.timezone)
}

type ChangeTimezoneCommand InsertCommand

type Command interface {
	Do(botConfig BankoBotInterface, db *sql.DB)
}

type Worker struct {
	commandCh chan Command
	botConfig BankoBotInterface
	db        *sql.DB
}

func (w *Worker) botConfigWorker() {
	log.Printf("Starting worker job.")
	for c := range w.commandCh {
		log.Printf("Got a job: %#v", c)
		c.Do(w.botConfig, w.db)
	}
}

type MessageGenerator interface {
	Get() string
}

type (
	ChanMessageGenerator  chan string
	ConstMessageGenerator string
)

func (msgsPool ChanMessageGenerator) Get() string {
	return <-msgsPool
}

func (msgsPool ConstMessageGenerator) Get() string {
	return string(msgsPool)
}

func addCronFunc(c *cron.Cron, timezone, frame string, job func()) {
	c.AddFunc(fmt.Sprintf("CRON_TZ=%v %v", timezone, frame), job)
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
