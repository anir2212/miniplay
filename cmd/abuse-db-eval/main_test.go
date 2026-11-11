package main

import (
	"testing"

	"miniplay/internal/game"
)

func TestFrozenFixtureReplaysAndKeepsLabelsOutOfRows(t *testing.T) {
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		counts := map[string]int{}
		doubled := 0
		for index := 0; index < 100; index++ {
			row, taps, label, err := fixture(id, index)
			if err != nil {
				t.Fatal(err)
			}
			base, err := game.Replay(id, row.RunID, taps)
			if err != nil || base != row.BaseScore || row.Score != base*row.Multiplier {
				t.Fatalf("%s %03d: base=%d row=%+v err=%v", id, index, base, row, err)
			}
			if row.User != row.RunID || label.game != id || row.DurationMS != 10000 {
				t.Fatalf("%s %03d: row=%+v label=%+v", id, index, row, label)
			}
			counts[label.class]++
			if label.doubled {
				doubled++
				if row.Multiplier != 2 || row.FlagVersion != 1 {
					t.Fatalf("%s %03d: doubled row=%+v", id, index, row)
				}
			} else if row.Multiplier != 1 || row.FlagVersion != 0 {
				t.Fatalf("%s %03d: plain row=%+v", id, index, row)
			}
		}
		if counts["clean"] != 60 || counts["overt"] != 20 || counts["mimic"] != 20 || doubled != 20 {
			t.Fatalf("%s: classes=%v doubled=%d", id, counts, doubled)
		}
	}
}
