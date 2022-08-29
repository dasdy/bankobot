package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron"
)

type BotMessage struct {
	ChatID    int64
	Message   string
	IsSticker bool
}

func initSqliteDB() *sql.DB {
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

type SQLConnection interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

type TelegramAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

func insertStmt(db SQLConnection, chatID int64, timezone, message string) {
	_, err := db.Exec(`insert into foo(chat_id, tz, message, last_timestamp) 
	    values(?, ?, ?, datetime('now'))
		on conflict(chat_id) 
		do update set tz=excluded.tz, message=excluded.message, last_timestamp=excluded.last_timestamp`,
		chatID, timezone, message)
	if err != nil {
		log.Fatalf("%q: %d, %v\n", err, chatID, timezone)
	}
}

func refreshTimestamp(db SQLConnection) {
	_, err := db.Exec(`update foo set last_timestamp=datetime('now')`)
	if err != nil {
		log.Printf("%v\n", err)
	}
}

var DEBUG = false //nolint

type Messager struct {
	API TelegramAPI
}

func (m *Messager) SendMessage(msg BotMessage) {
	log.Printf("Got a message request!")

	var botMsg tgbotapi.Chattable

	if msg.IsSticker {
		botMsg = tgbotapi.NewSticker(msg.ChatID, tgbotapi.FileID(msg.Message))
	} else {
		botMsg = tgbotapi.NewMessage(msg.ChatID, msg.Message)
	}

	log.Printf("Sending a message: %#v", botMsg)

	if _, err := m.API.Send(botMsg); err != nil {
		log.Printf("%v\n", err)
	}
}

type ChatConfig struct {
	ShouldSendReminder bool
	Timezone           string
}

func (cc *ChatConfig) String() string {
	return fmt.Sprintf("ChatConfig{%v, %v}", cc.ShouldSendReminder, cc.Timezone)
}

type TimeZoneConfig struct {
	cron  CronRepo
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

type SQLRowReader struct {
	DBRows *sql.Rows
}

func (r SQLRowReader) LoadRow(a *int64, b, c, d *string) error {
	if err := r.DBRows.Scan(a, b, c, d); err != nil {
		return fmt.Errorf("failed to load row: %w", err)
	}

	return nil
}

func JobTaskFromRow(row RowReader, now time.Time) (int64, string, bool) {
	var ID int64

	var tz, lastTimestamp, message string

	err := row.LoadRow(&ID, &tz, &message, &lastTimestamp)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Row: %v,%v,%v,%v", ID, tz, message, lastTimestamp)

	shouldSendReminder := ShouldSendReminder(lastTimestamp, now)

	return ID, tz, shouldSendReminder
}

func initBot(db SQLConnection, bot TelegramAPI) BankoBotInterface {
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
		DB:                db,
		Cron:              c,
		RegularMessages:   regMsg,
		WednesdayMessages: wedMsg,
		ChatIDs:           chatIDs,
		TimeZones:         timezones,
		Messager:          Messager{API: bot},
		NotifyChannel:     make(chan Command),
	}
	now := time.Now()
	reader := SQLRowReader{DBRows: rows}

	for rows.Next() {
		ID, tz, shouldSendReminder := JobTaskFromRow(reader, now)

		bc.makeJobsUnsafe(ID, tz, shouldSendReminder)
	}

	err = rows.Err()
	if err != nil {
		log.Printf("%v", err)
	}

	setupCronTasks(&bc, db, c)
	c.Start()

	return &bc
}

func setupCronTasks(bc BankoBotInterface, db SQLConnection, c CronRepo) {
	err := c.AddFunc("CRON_TZ=Europe/Kiev 30 23 * * *", bc.resetJob())
	if err != nil {
		log.Fatalf("%v\n", err)
	}

	err = c.AddFunc("CRON_TZ=Europe/Kiev 0 23 * * *", bc.RemindJob(db))
	if err != nil {
		log.Fatalf("%v\n", err)
	}

	if DEBUG {
		err = c.AddFunc("CRON_TZ=Europe/Kiev */10 * * * *", bc.resetJob())
		if err != nil {
			log.Fatalf("%v\n", err)
		}

		err = c.AddFunc("CRON_TZ=Europe/Kiev */2 * * * *", bc.RemindJob(db))
		if err != nil {
			log.Fatalf("%v\n", err)
		}
	}
}

func tgbotAPIKey() string {
	return os.Getenv("TG_API_KEY")
}

func initBotConfig(db SQLConnection) (*tgbotapi.UpdatesChannel, BankoBotInterface) {
	bot, err := tgbotapi.NewBotAPI(tgbotAPIKey())
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %v", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 600

	updates := bot.GetUpdatesChan(u)

	return &updates, initBot(db, bot)
}

func botLoop(db SQLConnection) {
	updates, botConfig := initBotConfig(db)
	worker := Worker{
		commandCh: botConfig.commandChannel(),
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

		command := update.Message.Command()

		if strings.HasPrefix(command, "pidor") {
			worker.commandCh <- NotifyReceivedCommand{update.Message.Chat.ID}
		} else if strings.HasPrefix(command, "settz") {
			command := ChangeTimezoneCommand{
				chatID: update.Message.Chat.ID, timezone: update.Message.CommandArguments(),
			}
			worker.commandCh <- command
		}
	}
}

type InsertCommand struct {
	chatID   int64
	timezone string
}

func (c InsertCommand) Do(botConfig BankoBotInterface, db SQLConnection) {
	botConfig.AddNewChat(db, c.chatID)
}

type NotifyCommand BotMessage

func (c NotifyCommand) Do(botConfig BankoBotInterface, db SQLConnection) {
	botConfig.sendMessage(BotMessage(c))
}

type NotifyReceivedCommand struct {
	chatID int64
}

func (c NotifyReceivedCommand) Do(botConfig BankoBotInterface, db SQLConnection) {
	botConfig.notifyPidorAccepted(c.chatID)
}

func (c ChangeTimezoneCommand) Do(botConfig BankoBotInterface, db SQLConnection) {
	botConfig.setTimezone(c.chatID, c.timezone)
}

type ChangeTimezoneCommand InsertCommand

type Command interface {
	Do(botConfig BankoBotInterface, db SQLConnection)
}

type Worker struct {
	commandCh chan Command
	botConfig BankoBotInterface
	db        SQLConnection
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

type CronRepo interface {
	AddFunc(spec string, cmd func()) error
}

func addCronFunc(c CronRepo, timezone, frame string, job func()) {
	err := c.AddFunc(fmt.Sprintf("CRON_TZ=%v %v", timezone, frame), job)
	if err != nil {
		log.Fatalf("Could not add cron func: %v, frame: %s", err, frame)
	}
}

func readLines(fname string) ([]string, error) {
	file, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("could not read contents of %v, got %w", fname, err)
	}

	defer file.Close()

	var lines []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	err = scanner.Err()
	if err != nil {
		err = fmt.Errorf("error while reading file %v, got %w", fname, err)
	}

	return lines, err
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

func loadEnv() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}
}

func main() {
	loadEnv()
	rand.Seed(time.Now().UnixNano())

	db := initSqliteDB()

	defer db.Close()
	botLoop(db)
}
