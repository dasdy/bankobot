package main

import (
	"testing"
	"time"
)

func TestDontNotifyOnSameDay(t *testing.T) {
	now := time.Date(2020, time.December, 29, 23, 0, 0, 0, time.UTC)
	if ShouldSendReminder("2020-12-29 10:04:44", now) {
		t.Fail()
	}
}

func TestNotifyTwoDaysAfter(t *testing.T) {
	x := time.Date(2020, time.December, 31, 23, 0, 0, 0, time.UTC)
	if !ShouldSendReminder("2020-12-29 10:04:44", x) {
		t.Fail()
	}
}

func TestNotifyOnBadTimestamp(t *testing.T) {
	now := time.Date(2020, time.December, 29, 23, 0, 0, 0, time.UTC)
	if !ShouldSendReminder("not parseable timestamp", now) {
		t.Fail()
	}
}
