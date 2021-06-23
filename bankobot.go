package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron"
)

type BotConfig struct {
	db             *sql.DB
	regMsg, wedMsg chan string
	notifyChannel  chan Command
	chatIDs        map[int64]*ChatConfig
	timeZones      map[string]*TimeZoneConfig
	cron           *cron.Cron
	messager       Messager
}

type BankoBotInterface interface {
	addNewChat(db *sql.DB, chatID int64)
	notifyPidorAccepted(chatID int64)
	setTimezone(chatID int64, timezone string)
	sendText(chatID int64, message MessageGenerator)
	sendMessage(m BotMessage)
}

func (bc *BotConfig) sendMessage(m BotMessage) {
	bc.messager.sendMessage(m)
}

func (bc *BotConfig) String() string {
	return fmt.Sprintf("BotConfig{%v, %v}", bc.chatIDs, bc.timeZones)
}

func (bc *BotConfig) addNewChat(db *sql.DB, chatID int64) {
	_, ok := bc.chatIDs[chatID]
	if !ok {
		bc.makeJobsUnsafe(chatID, "Europe/Kiev", true)
		insertStmt(db, chatID, "Europe/Kiev", "whatever")
	}
}

func (bc *BotConfig) notifyPidorAccepted(chatID int64) {
	log.Printf("Detected /pidor command at %d. All chat ids:", chatID)
	log.Println(bc.chatIDs)

	v := bc.chatIDs[chatID]
	if v.shouldSendReminder {
		v.shouldSendReminder = false
		message := BotMessage{
			chatID:    chatID,
			message:   "CAACAgIAAxkBAAIBq1_I9VKJwdOKaGlg7VrGfj2-9gHlAAIeAQAC0t1pBceuDjBghrA8HgQ",
			isSticker: true,
		}
		bc.messager.sendMessage(message)
	} else {
		log.Println("already remembered this chat id")
	}
}

func (bc *BotConfig) setTimezone(chatID int64, timezone string) {
	_, err := time.LoadLocation(timezone)
	if err != nil {
		bc.sendText(chatID,
			ConstMessageGenerator(fmt.Sprintf("don't know abt timezone '%v'. Try something easier, like Europe/Kiev", timezone)),
		)
	} else {
		log.Printf("Changing %d's timezone to %v", chatID, timezone)
		shouldNotify := bc.chatIDs[chatID].shouldSendReminder
		bc.deleteJobUnsafe(chatID)
		bc.makeJobsUnsafe(chatID, timezone, shouldNotify)

		insertStmt(bc.db, chatID, timezone, "whatever")

		bc.sendText(chatID, ConstMessageGenerator(fmt.Sprintf("OK: changed timezone to %v", timezone)))
	}
}

func (bc *BotConfig) makeJobsUnsafe(chatID int64, timezone string, shouldNotify bool) {
	if _, ok := bc.chatIDs[chatID]; ok {
		log.Printf("%d is already registered. Skipping", chatID)

		return
	}

	bc.addJobsUnsafe(timezone, chatID, shouldNotify)
}

func (bc *BotConfig) registerChat(timezone string, chatID int64) {
	v := bc.timeZones[timezone]
	v.chats[chatID] = true
}

func (bc *BotConfig) addJobsUnsafe(timezone string, chatID int64, shouldNotify bool) {
	_, ok := bc.timeZones[timezone]
	if !ok {
		log.Printf("Creating new cron instance for timezone %v", timezone)

		timeZoneCron := cron.New()
		conf := TimeZoneConfig{cron: timeZoneCron, chats: make(map[int64]bool)}

		bc.timeZones[timezone] = &conf

		regMsgGen := ChanMessageGenerator(bc.regMsg)
		regularJob := bc.timeZoneJob(timezone, func(chatId int64) {
			log.Printf("A regular job started: sending message to %v", chatId)
			bc.notifyChannel <- NotifyCommand{chatID: chatId, message: regMsgGen.Get(), isSticker: false}
		})
		wedMsgGen := ChanMessageGenerator(bc.wedMsg)
		wednesdayJob := bc.timeZoneJob(timezone, func(chatId int64) {
			bc.notifyChannel <- NotifyCommand{chatID: chatId, message: wedMsgGen.Get(), isSticker: false}
		})

		addCronFunc(timeZoneCron, timezone, "20 4 * * *", regularJob)
		addCronFunc(timeZoneCron, timezone, "20 16 * * 0-2,4-6", regularJob)
		addCronFunc(timeZoneCron, timezone, "20 16 * * 3", wednesdayJob)

		if DEBUG {
			addCronFunc(timeZoneCron, timezone, "* * * * *", regularJob)
		}

		timeZoneCron.Start()
	}

	log.Printf("Registering chat %d", chatID)

	bc.chatIDs[chatID] = &ChatConfig{shouldSendReminder: shouldNotify, timezone: timezone}

	bc.registerChat(timezone, chatID)
}

func (bc *BotConfig) deleteJobUnsafe(chatID int64) {
	chatConf := bc.chatIDs[chatID]
	v := bc.timeZones[chatConf.timezone]

	delete(v.chats, chatID)
	delete(bc.chatIDs, chatID)
}

func (bc *BotConfig) timeZoneJob(timezone string, job func(int64)) func() {
	return func() {
		jobs := bc.timeZones[timezone]
		for chatID := range jobs.chats {
			job(chatID)
		}
	}
}

func (bc *BotConfig) sendText(chatID int64, message MessageGenerator) {
	log.Printf("messaging to %d", chatID)

	msg := message.Get()

	bc.sendMessage(BotMessage{message: msg, chatID: chatID, isSticker: false})
}

func (bc *BotConfig) remindJob(db *sql.DB) func() {
	return func() {
		for k := range bc.chatIDs {
			x := bc.chatIDs[k]
			if x.shouldSendReminder {
				bc.sendText(k, ConstMessageGenerator("This is a reminder to call /pidor"))
			}

			log.Printf("Resetting item %d", k)
		}

		refreshTimestamp(db)
	}
}

func (bc *BotConfig) resetJob() func() {
	return func() {
		for k := range bc.chatIDs {
			x := bc.chatIDs[k]
			x.shouldSendReminder = true

			log.Printf("Resetting item %d", k)
		}
	}
}
