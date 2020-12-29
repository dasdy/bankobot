package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron"
)

type BotMessage struct {
	chatID  int64
	message string
}

func (cc *BotMessage) String() string {
	return fmt.Sprintf("BotMessage{%v, %v}", cc.chatID, cc.message)
}

type BotConfig struct {
	db             *sql.DB
	regMsg, wedMsg chan string
	sendingChan    chan BotMessage
	chatIDs        map[int64]*ChatConfig
	timeZones      map[string]*TimeZoneConfig
	cron           *cron.Cron
	modifyLock     *sync.RWMutex
}

func (cc *BotConfig) String() string {
	return fmt.Sprintf("BotConfig{%v, %v}", cc.chatIDs, cc.timeZones)
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

func insertStmt(db *sql.DB, chatID int64, timezone string, message string) {

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

var DEBUG = false

func sendMessages(ch chan BotMessage, bot *tgbotapi.BotAPI) {
	for {
		msg := <-ch
		botMsg := tgbotapi.NewMessage(msg.chatID, msg.message)
		bot.Send(botMsg)
	}
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

func initBot(db *sql.DB, bot *tgbotapi.BotAPI) BotConfig {
	chatIDs := make(map[int64]*ChatConfig)
	timezones := make(map[string]*TimeZoneConfig)
	c := cron.New()

	regMsg := make(chan string)
	wedMsg := make(chan string)
	sendingChan := make(chan BotMessage)

	go linesGenerator("420_msg.txt", regMsg)
	go linesGenerator("wednesday.txt", wedMsg)
	go sendMessages(sendingChan, bot)

	rows, err := db.Query("select chat_id, tz, message, last_timestamp from foo")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	bc := BotConfig{
		db:          db,
		cron:        c,
		regMsg:      regMsg,
		wedMsg:      wedMsg,
		chatIDs:     chatIDs,
		sendingChan: sendingChan,
		modifyLock:  &sync.RWMutex{},
		timeZones:   timezones,
	}

	y1, m1, d1 := time.Now().Date()
	for rows.Next() {
		var ID int64
		var tz string
		var message string
		var lastTimestamp string
		err = rows.Scan(&ID, &tz, &message, &lastTimestamp)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(ID, tz, message, lastTimestamp)

		const layout string = "2006-01-02 15:04:05"
		timestamp, err := time.Parse(layout, lastTimestamp)

		shouldSendReminder := false
		y2, m2, d2 := timestamp.Date()

		if y1 != y2 || m1 != m2 || d1 != d2 {
			shouldSendReminder = true
		}

		if err != nil {
			log.Fatal(err)
		}

		bc.modifyLock.Lock()
		bc.makeJobsUnsafe(ID, tz, shouldSendReminder)
		bc.modifyLock.Unlock()
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	c.AddFunc("CRON_TZ=Europe/Kiev 30 23 * * *", resetJob(chatIDs))
	c.AddFunc("CRON_TZ=Europe/Kiev 0 23 * * *", remindJob(&bc))

	if DEBUG {
		c.AddFunc("CRON_TZ=Europe/Kiev */10 * * * *", resetJob(chatIDs))
		c.AddFunc("CRON_TZ=Europe/Kiev * * * * *", remindJob(&bc))
	}

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

	log.Printf("Authorized on account %v", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 600

	updates, err := bot.GetUpdatesChan(u)

	botConfig := initBot(db, bot)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%v] %v", update.Message.From.UserName, update.Message.Text)

		chatID := update.Message.Chat.ID

		botConfig.addNewChat(db, chatID)

		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "pidor":
				notifyPidorAccepted(&update, &botConfig, bot)
			case "settz":
				setTimezone(&update, &botConfig, bot)
			}
		}
	}
}

func (bc *BotConfig) addNewChat(db *sql.DB, chatID int64) {
	bc.modifyLock.RLock()
	_, ok := bc.chatIDs[chatID]
	bc.modifyLock.RUnlock()
	if !ok {
		bc.modifyLock.Lock()
		bc.makeJobsUnsafe(chatID, "Europe/Kiev", true)
		insertStmt(db, chatID, "Europe/Kiev", "whatever")
		bc.modifyLock.Unlock()
	}
}

func notifyPidorAccepted(update *tgbotapi.Update, bc *BotConfig, bot *tgbotapi.BotAPI) {
	chatID := update.Message.Chat.ID
	log.Printf("Detected /pidor command at %d. All chat ids:", chatID)
	log.Println(bc.chatIDs)
	bc.modifyLock.RLock()
	v := bc.chatIDs[chatID]
	bc.modifyLock.RUnlock()
	if v.shouldSendReminder {
		v.shouldSendReminder = false
		msg := tgbotapi.NewStickerShare(
			chatID,
			"CAACAgIAAxkBAAIBq1_I9VKJwdOKaGlg7VrGfj2-9gHlAAIeAQAC0t1pBceuDjBghrA8HgQ")
		bot.Send(msg)
	} else {
		log.Println("already remembered this chat id")
	}
}

func setTimezone(update *tgbotapi.Update, bc *BotConfig, bot *tgbotapi.BotAPI) {
	chatID := update.Message.Chat.ID
	args := update.Message.CommandArguments()
	log.Printf("Got text: '%v', args: '%v'", update.Message.Text, args)

	_, err := time.LoadLocation(args)
	if err != nil {
		bc.sendingChan <- BotMessage{
			chatID:  chatID,
			message: fmt.Sprintf("don't know abt timezone '%v'. Try something easier, like Europe/Kiev", args),
		}
	} else {
		log.Printf("Changing %d's timezone to %v", chatID, args)
		bc.modifyLock.Lock()
		shouldNotify := bc.chatIDs[chatID].shouldSendReminder
		bc.deleteJobUnsafe(chatID)
		bc.makeJobsUnsafe(chatID, args, shouldNotify)
		bc.modifyLock.Unlock()

		insertStmt(bc.db, chatID, args, "whatever")

		bc.sendingChan <- BotMessage{
			chatID:  chatID,
			message: fmt.Sprintf("OK: changed timezone to %v", args),
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

func (bc *BotConfig) makeJobsUnsafe(chatID int64, timezone string, shouldNotify bool) {
	_, ok := bc.chatIDs[chatID]
	if ok {
		log.Printf("%d is already registered. Skipping", chatID)
		return
	}

	bc.addJobsUnsafe(timezone, chatID, shouldNotify)
}

func (bc *BotConfig) registerChat(timezone string, chatID int64) {
	v := bc.timeZones[timezone]
	v.chats[chatID] = true
}

func addCronFunc(c *cron.Cron, timezone, frame string, job func()) {
	c.AddFunc(fmt.Sprintf("CRON_TZ=%v %v", timezone, frame), job)
}

func (bc *BotConfig) addJobsUnsafe(timezone string, chatID int64, shouldNotify bool) {
	_, ok := bc.timeZones[timezone]
	if !ok {
		log.Printf("Creating new cron instance for timezone %v", timezone)
		timeZoneCron := cron.New()
		conf := TimeZoneConfig{cron: timeZoneCron, chats: make(map[int64]bool)}
		bc.timeZones[timezone] = &conf

		regMsgGen := ChanMessageGenerator(bc.regMsg)

		regularJob := timeZoneJob(bc, timezone, func(chatId int64) {
			bc.sendText(chatId, regMsgGen)
		})

		wedMsgGen := ChanMessageGenerator(bc.wedMsg)
		wednesdayJob := timeZoneJob(bc, timezone, func(chatId int64) {
			bc.sendText(chatId, wedMsgGen)
		})

		addCronFunc(timeZoneCron, timezone, "20 4 * * *", regularJob)
		addCronFunc(timeZoneCron, timezone, "20 16 * * 0-2,4-6", regularJob)
		addCronFunc(timeZoneCron, timezone, "20 16 * * 3", wednesdayJob)

		log.Printf("Created tasks for %d", chatID)

		if DEBUG {
			addCronFunc(timeZoneCron, timezone, "* * * * *", regularJob)
		}
		timeZoneCron.Start()
	}
	bc.chatIDs[chatID] = &ChatConfig{shouldSendReminder: shouldNotify, timezone: timezone}
	bc.registerChat(timezone, chatID)

}

func (bc *BotConfig) deleteJobUnsafe(chatID int64) {
	chatConf := bc.chatIDs[chatID]
	v := bc.timeZones[chatConf.timezone]
	delete(v.chats, chatID)
	delete(bc.chatIDs, chatID)
}

func timeZoneJob(bc *BotConfig, timezone string, job func(int64)) func() {
	return func() {
		bc.modifyLock.RLock()
		jobs := bc.timeZones[timezone]
		bc.modifyLock.RUnlock()
		for chatID := range jobs.chats {
			job(chatID)
		}
	}
}

func (bc *BotConfig) sendText(chatID int64, message MessageGenerator) {
	log.Printf("messaging to %d", chatID)
	msg := message.Get()
	botMessage := BotMessage{message: msg, chatID: chatID}
	bc.sendingChan <- botMessage
}

func resetJob(chatIDs map[int64]*ChatConfig) func() {
	return func() {
		for k := range chatIDs {
			x := chatIDs[k]
			x.shouldSendReminder = true
			log.Printf("Resetting item %d", k)
		}
	}
}

func remindJob(bc *BotConfig) func() {
	return func() {
		chatIDs := bc.chatIDs
		for k := range chatIDs {
			x := chatIDs[k]
			if x.shouldSendReminder {
				bc.sendText(k, ConstMessageGenerator("This is a reminder to call /pidor"))
			}
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
