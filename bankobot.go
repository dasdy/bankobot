package main

import (
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron"
)

type BotConfig struct {
	DB                                 SQLConnection
	RegularMessages, WednesdayMessages chan string
	NotifyChannel                      chan Command
	ChatIDs                            map[int64]*ChatConfig
	TimeZones                          map[string]*TimeZoneConfig
	Cron                               CronRepo
	Messager                           Messager
}

type BankoBotInterface interface {
	AddNewChat(db SQLConnection, chatID int64)
	notifyAccepted(chatID int64)
	setTimezone(chatID int64, timezone string)
	sendText(chatID int64, message MessageGenerator)
	sendMessage(m BotMessage)
	commandChannel() chan Command
	resetJob() func()
	RemindJob(db SQLConnection) func()
}

func (bc *BotConfig) commandChannel() chan Command {
	return bc.NotifyChannel
}

func (bc *BotConfig) sendMessage(m BotMessage) {
	bc.Messager.SendMessage(m)
}

func (bc *BotConfig) String() string {
	return fmt.Sprintf("BotConfig{%v, %v}", bc.ChatIDs, bc.TimeZones)
}

func (bc *BotConfig) AddNewChat(db SQLConnection, chatID int64) {
	_, ok := bc.ChatIDs[chatID]
	if !ok {
		bc.makeJobsUnsafe(chatID, "Europe/Kiev", true)
		insertStmt(db, chatID, "Europe/Kiev", "whatever")
	}
}

func (bc *BotConfig) notifyAccepted(chatID int64) {
	log.Printf("Detected /game command at %d. All chat ids:", chatID)
	log.Println(bc.ChatIDs)

	v := bc.ChatIDs[chatID]
	if v.ShouldSendReminder {
		v.ShouldSendReminder = false
		message := BotMessage{
			ChatID:    chatID,
			Message:   "CAACAgIAAxkBAAIBq1_I9VKJwdOKaGlg7VrGfj2-9gHlAAIeAQAC0t1pBceuDjBghrA8HgQ",
			IsSticker: true,
		}
		bc.Messager.SendMessage(message)
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
		shouldNotify := bc.ChatIDs[chatID].ShouldSendReminder
		bc.deleteJobUnsafe(chatID)
		bc.makeJobsUnsafe(chatID, timezone, shouldNotify)

		insertStmt(bc.DB, chatID, timezone, "whatever")

		bc.sendText(chatID, ConstMessageGenerator(fmt.Sprintf("OK: changed timezone to %v", timezone)))
	}
}

func (bc *BotConfig) makeJobsUnsafe(chatID int64, timezone string, shouldNotify bool) {
	if _, ok := bc.ChatIDs[chatID]; ok {
		log.Printf("%d is already registered. Skipping", chatID)

		return
	}

	bc.addJobsUnsafe(timezone, chatID, shouldNotify)
}

func (bc *BotConfig) registerChat(timezone string, chatID int64) {
	v := bc.TimeZones[timezone]
	v.chats[chatID] = true
}

func (bc *BotConfig) addJobsUnsafe(timezone string, chatID int64, shouldNotify bool) {
	_, ok := bc.TimeZones[timezone]
	if !ok {
		log.Printf("Creating new cron instance for timezone %v", timezone)

		timeZoneCron := cron.New()
		conf := TimeZoneConfig{cron: timeZoneCron, chats: make(map[int64]bool)}

		bc.TimeZones[timezone] = &conf

		regMsgGen := ChanMessageGenerator(bc.RegularMessages)
		regularJob := bc.timeZoneJob(timezone, func(chatId int64) {
			log.Printf("A regular job started: sending message to %v", chatId)
			bc.NotifyChannel <- NotifyCommand{ChatID: chatId, Message: regMsgGen.Get(), IsSticker: false}
		})
		// wedMsgGen := ChanMessageGenerator(bc.WednesdayMessages)
		// wednesdayJob := bc.timeZoneJob(timezone, func(chatId int64) {
		// 	bc.NotifyChannel <- NotifyCommand{ChatID: chatId, Message: wedMsgGen.Get(), IsSticker: false}
		// })

		// addCronFunc(timeZoneCron, timezone, "20 4 * * *", regularJob)
		// addCronFunc(timeZoneCron, timezone, "20 16 * * 0-2,4-6", regularJob)
		// addCronFunc(timeZoneCron, timezone, "20 16 * * 3", wednesdayJob)

		if DEBUG {
			addCronFunc(timeZoneCron, timezone, "* * * * *", regularJob)
		}

		timeZoneCron.Start()
	}

	log.Printf("Registering chat %d", chatID)

	bc.ChatIDs[chatID] = &ChatConfig{ShouldSendReminder: shouldNotify, Timezone: timezone}

	bc.registerChat(timezone, chatID)
}

func (bc *BotConfig) deleteJobUnsafe(chatID int64) {
	chatConf := bc.ChatIDs[chatID]
	v := bc.TimeZones[chatConf.Timezone]

	delete(v.chats, chatID)
	delete(bc.ChatIDs, chatID)
}

func (bc *BotConfig) timeZoneJob(timezone string, job func(int64)) func() {
	return func() {
		jobs := bc.TimeZones[timezone]
		for chatID := range jobs.chats {
			job(chatID)
		}
	}
}

func (bc *BotConfig) sendText(chatID int64, message MessageGenerator) {
	log.Printf("messaging to %d", chatID)

	msg := message.Get()

	bc.sendMessage(BotMessage{Message: msg, ChatID: chatID, IsSticker: false})
}

func (bc *BotConfig) RemindJob(db SQLConnection) func() {
	return func() {
		for k := range bc.ChatIDs {
			chatID := bc.ChatIDs[k]
			if chatID.ShouldSendReminder {
				bc.sendText(k, ConstMessageGenerator("This is a reminder to call /game"))
			}

			log.Printf("Resetting item %d", k)
		}

		refreshTimestamp(db)
	}
}

func (bc *BotConfig) resetJob() func() {
	return func() {
		for k := range bc.ChatIDs {
			x := bc.ChatIDs[k]
			x.ShouldSendReminder = true

			log.Printf("Resetting item %d", k)
		}
	}
}
