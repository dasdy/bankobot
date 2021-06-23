package main_test

import (
	"testing"
	"time"

	main "github.com/dasdy/420-bot"
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
