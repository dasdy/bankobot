package main_test

import (
	"database/sql"
	"testing"
	"time"

	main "github.com/dasdy/420-bot"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

func TestDontNotifyOnSameDay(t *testing.T) {
	t.Parallel()

	now := time.Date(2020, time.December, 29, 23, 0, 0, 0, time.UTC)
	if main.ShouldSendReminder("2020-12-29 10:04:44", now) {
		t.Fail()
	}
}

func TestNotifyTwoDaysAfter(t *testing.T) {
	t.Parallel()

	x := time.Date(2020, time.December, 31, 23, 0, 0, 0, time.UTC)
	if !main.ShouldSendReminder("2020-12-29 10:04:44", x) {
		t.Fail()
	}
}

func TestNotifyOnBadTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2020, time.December, 29, 23, 0, 0, 0, time.UTC)
	if !main.ShouldSendReminder("not parseable timestamp", now) {
		t.Fail()
	}
}

type MockRowReader struct {
	id            int64
	tz, timestamp string
}

func (r MockRowReader) LoadRow(a *int64, b, c, d *string) error {
	*a = r.id
	*b = r.tz
	*c = "some message lol"
	*d = r.timestamp

	return nil
}

func TestJobTaskFromRow(t *testing.T) {
	t.Parallel()

	now := time.Date(2020, time.December, 29, 23, 0, 0, 0, time.UTC)
	reader := MockRowReader{id: 10, tz: "sometimezone", timestamp: "2020-12-23 10:00: 24"}
	ID, tz, shouldSendReminder := main.JobTaskFromRow(reader, now)

	if ID != 10 || tz != "sometimezone" || shouldSendReminder != true {
		t.Error(ID, tz, shouldSendReminder)
	}
}

type TgAPIMock struct {
	chattableLog []tgbotapi.Chattable
}

func (api *TgAPIMock) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	api.chattableLog = append(api.chattableLog, c)
	return tgbotapi.Message{}, nil //nolint
}

func TestSendMessageToApi(t *testing.T) {
	t.Parallel()

	api := TgAPIMock{chattableLog: make([]tgbotapi.Chattable, 0, 1)}
	messager := main.Messager{API: &api}

	messager.SendMessage(main.BotMessage{ChatID: 100, Message: "some test message", IsSticker: false})

	v, ok := api.chattableLog[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Errorf("could not cast value %v", api.chattableLog[0])
	}

	if v.Text != "some test message" || v.ChatID != 100 {
		t.Errorf("strange values in the message: %v", v)
	}
}

type SQLConnectionMock struct{}

func (s SQLConnectionMock) Exec(query string, args ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (s SQLConnectionMock) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func TestBroadcastMessages(t *testing.T) {
	t.Parallel()

	api := TgAPIMock{chattableLog: make([]tgbotapi.Chattable, 0, 3)}
	messager := main.Messager{API: &api}
	bc := main.BotConfig{Messager: messager, ChatIDs: map[int64]*main.ChatConfig{ // nolint
		100: {ShouldSendReminder: true},
		200: {ShouldSendReminder: false},
		300: {ShouldSendReminder: true},
	}}

	bc.RemindJob(SQLConnectionMock{})()

	if len(api.chattableLog) != 2 {
		t.Errorf("Got wrong amt of items: %v", len(api.chattableLog))
	}

	v, ok := api.chattableLog[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Errorf("could not cast value %v", api.chattableLog[0])
	}

	if v.Text != "This is a reminder to call /pidor" || v.ChatID != 100 {
		t.Errorf("strange values in the message: %v", v)
	}

	v, ok = api.chattableLog[1].(tgbotapi.MessageConfig)
	if !ok {
		t.Errorf("could not cast value %v", api.chattableLog[0])
	}

	if v.Text != "This is a reminder to call /pidor" || v.ChatID != 300 {
		t.Errorf("strange values in the message: %v", v)
	}
}
