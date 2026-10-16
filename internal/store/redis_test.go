package store

import (
	"miniplay/internal/game"
	"testing"
	"time"
)

func TestBoardCalendarBoundaries(t *testing.T) {
	at := time.Date(2026, 1, 1, 15, 0, 0, 0, time.FixedZone("SGT", 8*3600))
	key, since, err := BoardKey(game.Rhythm, Weekly, at)
	if err != nil || key != "board:rhythm:week:2026-01" || since.Format("2006-01-02") != "2025-12-29" {
		t.Fatalf("key=%s since=%v err=%v", key, since, err)
	}
	day, _, _ := BoardKey(game.Reflex, Daily, at)
	if day != "board:reflex:day:2026-01-01" {
		t.Fatal(day)
	}
}
