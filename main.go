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

type BotConfig struct {
	db             *sql.DB
	regMsg, wedMsg chan string
	sendingChan    chan BotMessage
	chatIDs        map[int64]*ChatConfig
	timeZones      map[string]intSet
	cron           *cron.Cron
	modifyLock     *sync.RWMutex
}

func initSqliteDb() *sql.DB {
	db, err := sql.Open("sqlite3", "./foo.db")
	if err != nil {
		log.Fatal(err)
	}

	sqlStmt := `
	create table foo (chat_id int not null primary key, tz text not null);
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
	cron               *cron.Cron
}

type intSet map[int64]bool

func initBot(db *sql.DB, bot *tgbotapi.BotAPI) BotConfig {
	chatIDs := make(map[int64]*ChatConfig)
	timezones := make(map[string]intSet)
	c := cron.New()

	regMsg := make(chan string)
	wedMsg := make(chan string)
	sendingChan := make(chan BotMessage)

	go linesGenerator("420_msg.txt", regMsg)
	go linesGenerator("wednesday.txt", wedMsg)
	go sendMessages(sendingChan, bot)

	rows, err := db.Query("select chat_id, tz, message from foo")
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

	for rows.Next() {
		var ID int64
		var tz string
		var message string
		err = rows.Scan(&ID, &tz, &message)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(ID, tz, message)

		bc.modifyLock.Lock()
		bc.makeJobsUnsafe(ID, tz)
		bc.modifyLock.Unlock()
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}

	c.AddFunc("CRON_TZ=Europe/Kiev 30 23 * * *", resetJob(chatIDs))

	if DEBUG {
		c.AddFunc("CRON_TZ=Europe/Kiev */10 * * * *", resetJob(chatIDs))
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

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 600

	updates, err := bot.GetUpdatesChan(u)

	botConfig := initBot(db, bot)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

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
		bc.makeJobsUnsafe(chatID, "Europe/Kiev")
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
	log.Printf("Got text: '%s', args: '%s'", update.Message.Text, args)

	_, err := time.LoadLocation(args)
	if err != nil {
		bc.sendingChan <- BotMessage{
			chatID:  chatID,
			message: fmt.Sprintf("don't know abt timezone '%s'. Try something easier, like Europe/Kiev", args),
		}
	} else {
		log.Printf("Changing %d's timezone to %s", chatID, args)

		bc.modifyLock.Lock()
		bc.deleteJobUnsafe(chatID)
		bc.makeJobsUnsafe(chatID, args)
		bc.modifyLock.Unlock()

		insertStmt(bc.db, chatID, args, "whatever")

		bc.sendingChan <- BotMessage{
			chatID:  chatID,
			message: fmt.Sprintf("OK: changed timezone to %s", args),
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

func (bc *BotConfig) makeJobsUnsafe(chatID int64, timezone string) {
	_, ok := bc.chatIDs[chatID]
	if ok {
		log.Printf("%d is already registered. Skipping", chatID)
		return
	}

	chatIdCron := cron.New()
	bc.chatIDs[chatID] = &ChatConfig{shouldSendReminder: true, timezone: timezone, cron: chatIdCron}

	regMsgGen := ChanMessageGenerator(bc.regMsg)

	regularJob := timeZoneJob(bc, timezone, func(chatId int64) {
		bc.sendText(chatId, regMsgGen)
	})

	wedMsgGen := ChanMessageGenerator(bc.wedMsg)
	wednesdayJob := timeZoneJob(bc, timezone, func(chatId int64) {
		bc.sendText(chatId, wedMsgGen)
	})

	reminderJob := timeZoneJob(bc, timezone, func(chatId int64) {
		bc.modifyLock.RLock()
		v, ok := bc.chatIDs[chatID]
		bc.modifyLock.RUnlock()
		if !ok {
			panic(fmt.Sprintf("this should never happened: %d not in chat IDs", chatID))
		}
		if v.shouldSendReminder {
			bc.sendText(chatId, ConstMessageGenerator("This is a reminder to call /pidor"))
		} else {
			log.Printf("Decided not to remind anything to %d", chatID)
		}
	},
	)

	bc.addJobUnsafe(timezone, "1 23 * * *", chatID, reminderJob, chatIdCron)
	bc.addJobUnsafe(timezone, "20 4 * * *", chatID, regularJob, chatIdCron)
	bc.addJobUnsafe(timezone, "20 16 * * 0-2,4-6", chatID, regularJob, chatIdCron)
	bc.addJobUnsafe(timezone, "20 16 * * 3", chatID, wednesdayJob, chatIdCron)

	// Debug messages. Make sure to use testing sqlite db
	log.Printf("Created tasks for %d", chatID)

	if DEBUG {
		bc.addJobUnsafe(timezone, "* * * * *", chatID, regularJob, chatIdCron)
		bc.addJobUnsafe(timezone, "* * * * *", chatID, reminderJob, chatIdCron)
	}
	chatIdCron.Start()

}

func (bc *BotConfig) addJobUnsafe(timezone string, timeframe string, chatID int64, job func(), c *cron.Cron) {
	v := bc.timeZones[timezone]
	if v == nil {
		v = make(intSet)
		bc.timeZones[timezone] = make(intSet)
	}
	c.AddFunc(fmt.Sprintf("CRON_TZ=%s %s", timezone, timeframe), job)
	v[chatID] = true
}

func (bc *BotConfig) deleteJobUnsafe(chatID int64) {
	chatConf := bc.chatIDs[chatID]
	v := bc.timeZones[chatConf.timezone]
	chatConf.cron.Stop()
	delete(v, chatID)
	delete(bc.chatIDs, chatID)
}

func timeZoneJob(bc *BotConfig, timezone string, job func(int64)) func() {
	return func() {
		bc.modifyLock.RLock()
		jobs := bc.timeZones[timezone]
		bc.modifyLock.RUnlock()
		for chatID := range jobs {
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
