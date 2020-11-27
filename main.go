package main

import (
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
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

func main() {
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
	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		chat_id := update.Message.Chat.ID

		_, ok := chat_ids[chat_id]
		if !ok {
			log.Printf("Adding chat %d", chat_id)
			chat_ids[chat_id] = true
			c.AddFunc("CRON_TZ=Europe/Kiev 20 4 * * *", func() {
				log.Printf("messaging to %d", chat_id)
				DummyJob{chat_id, "It's /game o'clock!", bot}.Run()
			})
			c.AddFunc("CRON_TZ=Europe/Kiev 20 16 * * *", func() {
				log.Printf("messaging to %d", chat_id)
				DummyJob{chat_id, "wazzuuup", bot}.Run()
			})
			c.Start()
		}
	}
}
