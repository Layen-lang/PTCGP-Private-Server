package store

import (
	"testing"
	"time"
)

func TestGameDayResetsAtSixUTC(t *testing.T) {
	before := time.Date(2026, time.August, 25, 5, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	if gameDay(before) == gameDay(after) {
		t.Fatal("expected a new game day at 06:00 UTC")
	}
	want := time.Date(2026, time.August, 25, 6, 0, 0, 0, time.UTC)
	if got := gameDayStart(after); !got.Equal(want) {
		t.Fatalf("game day start = %s, want %s", got, want)
	}
	if got := gameDayStart(before); !got.Equal(want.Add(-24 * time.Hour)) {
		t.Fatalf("pre-reset game day start = %s, want %s", got, want.Add(-24*time.Hour))
	}
}
