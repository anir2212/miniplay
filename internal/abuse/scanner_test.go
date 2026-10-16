package abuse

import (
	"context"
	"fmt"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

type fakeRepository struct {
	runs  []store.ScoredRun
	flags []store.FlagRecord
}

func (r *fakeRepository) RecentRuns(context.Context, time.Time) ([]store.ScoredRun, error) {
	return r.runs, nil
}
func (r *fakeRepository) SaveFlag(_ context.Context, flag store.FlagRecord) error {
	r.flags = append(r.flags, flag)
	return nil
}

func TestScannerRecordsEvidenceForHighOutlier(t *testing.T) {
	repository := &fakeRepository{}
	for i := 0; i < 31; i++ {
		id := fmt.Sprintf("run-%02d", i)
		targets, _ := game.Targets(game.Rhythm, id)
		count := 1
		if i == 30 {
			count = len(targets)
		}
		taps := make([]game.Tap, 0, count)
		for _, target := range targets[:count] {
			taps = append(taps, game.Tap{MS: target.MS, X: target.X, Y: target.Y})
		}
		repository.runs = append(repository.runs, store.ScoredRun{Score: store.Score{RunID: id, User: id, Game: game.Rhythm, Score: 10 * count, DurationMS: 10000, SubmittedAt: time.Now()}, Events: taps})
	}
	report, err := Scan(context.Background(), repository, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 31 || report.Flagged < 1 {
		t.Fatalf("report=%+v", report)
	}
	found := false
	for _, flag := range repository.flags {
		if flag.RunID == "run-30" {
			found = true
			if flag.Evidence["scoreFraction"] != 1 {
				t.Fatalf("evidence=%+v", flag.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("high outlier missing from flags: %+v", repository.flags)
	}
}
