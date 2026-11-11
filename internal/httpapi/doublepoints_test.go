package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"miniplay/internal/flags"
	"miniplay/internal/game"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

// recordingStore keeps accepted rows so a test can read what would be stored.
type recordingStore struct {
	mu       sync.Mutex
	used     map[string]bool
	accepted []store.Score
	rejected []string
}

func (s *recordingStore) RecordAttempt(_ context.Context, row store.Score, _ []game.Tap, reason string) (store.Score, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[row.RunID] {
		return store.Score{}, store.ErrDuplicate
	}
	s.used[row.RunID] = true
	if reason != "" {
		s.rejected = append(s.rejected, reason)
		return row, nil
	}
	row.SubmittedAt = time.Unix(1800000000, 0)
	s.accepted = append(s.accepted, row)
	return row, nil
}

type countingBoards struct {
	mu      sync.Mutex
	updates []store.Score
}

func (*countingBoards) Allow(context.Context, string, int, float64) (bool, error) { return true, nil }
func (b *countingBoards) UpdateBoards(_ context.Context, row store.Score) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, row)
	return nil
}

// tableFlag stands in for feature_flags behind a real flags.Cache.
type tableFlag struct {
	mu    sync.Mutex
	flag  store.Flag
	err   error
	reads int
}

func (t *tableFlag) FeatureFlag(context.Context, game.ID) (store.Flag, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads++
	return t.flag, t.err
}
func (t *tableFlag) set(on bool, version int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flag = store.Flag{DoublePoints: on, Version: version}
}

// bench is one score service with a controllable clock and flag table.
type bench struct {
	now     time.Time
	signer  *token.Signer
	table   *tableFlag
	db      *recordingStore
	boards  *countingBoards
	handler http.Handler
}

const benchTTL = 5 * time.Second

func newBench(t *testing.T) *bench {
	t.Helper()
	b := &bench{now: time.Unix(1800000000, 0), table: &tableFlag{}, db: &recordingStore{used: map[string]bool{}}, boards: &countingBoards{}}
	b.table.set(false, 1)
	clock := func() time.Time { return b.now }
	var err error
	if b.signer, err = token.New(strings.Repeat("s", 32), clock); err != nil {
		t.Fatal(err)
	}
	cache, err := flags.NewCache(b.table, benchTTL, clock)
	if err != nil {
		t.Fatal(err)
	}
	b.handler = ScoreHandler(b.signer, b.db, cache, b.boards, func() int64 { return b.now.UnixMilli() })
	return b
}

// start issues a run and returns a submission tapping its first target: base score 10.
func (b *bench) start(t *testing.T, user string) Submission {
	t.Helper()
	started, err := b.signer.Issue(user, game.Rhythm)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := game.Targets(game.Rhythm, started.Seed)
	if err != nil {
		t.Fatal(err)
	}
	return Submission{Token: started.Token, Events: []game.Tap{{MS: targets[0].MS, X: targets[0].X, Y: targets[0].Y}}, Score: 10, DurationMS: 10000}
}

func (b *bench) submit(t *testing.T, submission Submission) (int, store.Score) {
	t.Helper()
	body, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/scores", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	var saved store.Score
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, saved
}

func TestDoublePointsAppliesAfterReplayAndIsExplained(t *testing.T) {
	b := newBench(t)
	b.table.set(true, 7)
	submission := b.start(t, "alice")
	b.now = b.now.Add(10 * time.Second)
	status, saved := b.submit(t, submission)
	if status != 200 || saved.Score != 20 || saved.BaseScore != 10 || saved.Multiplier != 2 || saved.FlagVersion != 7 {
		t.Fatalf("status=%d saved=%+v", status, saved)
	}
	if len(b.db.accepted) != 1 || b.db.accepted[0].Score != 20 || b.db.accepted[0].BaseScore != 10 || b.db.accepted[0].Multiplier != 2 || b.db.accepted[0].FlagVersion != 7 {
		t.Fatalf("stored=%+v", b.db.accepted)
	}
	if len(b.boards.updates) != 1 || b.boards.updates[0].Score != 20 {
		t.Fatalf("the board gets the final score: %+v", b.boards.updates)
	}
}

func TestFlagOffRecordsMultiplierOne(t *testing.T) {
	b := newBench(t)
	submission := b.start(t, "alice")
	b.now = b.now.Add(10 * time.Second)
	status, saved := b.submit(t, submission)
	if status != 200 || saved.Score != 10 || saved.BaseScore != 10 || saved.Multiplier != 1 || saved.FlagVersion != 1 {
		t.Fatalf("status=%d saved=%+v", status, saved)
	}
}

// Replay validates the base score. Claiming the doubled number is a mismatch,
// and the flag cannot rescue it.
func TestReplayStillValidatesTheBaseScoreDuringAnEvent(t *testing.T) {
	b := newBench(t)
	b.table.set(true, 2)
	claimed := b.start(t, "mallory")
	claimed.Score = 20
	b.now = b.now.Add(10 * time.Second)
	if status, _ := b.submit(t, claimed); status != http.StatusUnprocessableEntity {
		t.Fatalf("claiming the doubled score: status=%d", status)
	}
	if len(b.db.accepted) != 0 || len(b.db.rejected) != 1 || b.db.rejected[0] != "score mismatch" {
		t.Fatalf("accepted=%+v rejected=%v", b.db.accepted, b.db.rejected)
	}
}

func TestClientCannotSupplyAMultiplier(t *testing.T) {
	b := newBench(t)
	submission := b.start(t, "mallory")
	b.now = b.now.Add(10 * time.Second)
	body, _ := json.Marshal(map[string]any{"token": submission.Token, "events": submission.Events, "score": 10, "durationMs": 10000, "multiplier": 2})
	request := httptest.NewRequest(http.MethodPost, "/api/scores", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(b.db.used) != 0 {
		t.Fatalf("status=%d attempts=%d", response.Code, len(b.db.used))
	}
}

// The rule: the state the service holds at submission decides. The state when
// the run was signed does not count, in either direction.
func TestFlagChangeDuringARun(t *testing.T) {
	for _, test := range []struct {
		name                string
		signedOn, submitOn  bool
		wantScore, wantMult int
	}{
		{"off when signed, on at submission", false, true, 20, 2},
		{"on when signed, off at submission", true, false, 10, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := newBench(t)
			b.table.set(test.signedOn, 4)
			submission := b.start(t, "alice")
			b.now = b.now.Add(2 * time.Second)
			b.table.set(test.submitOn, 5)
			b.now = b.now.Add(8 * time.Second)
			status, saved := b.submit(t, submission)
			if status != 200 || saved.Score != test.wantScore || saved.BaseScore != 10 || saved.Multiplier != test.wantMult || saved.FlagVersion != 5 {
				t.Fatalf("status=%d saved=%+v", status, saved)
			}
		})
	}
}

// Inside the cache's bound a submission may use the old state. It must record
// that old version, so the score is still explainable, and must stop doing so
// at the bound.
func TestCachedFlagIsRecordedHonestlyAndExpiresAtTheBound(t *testing.T) {
	b := newBench(t)
	first := b.start(t, "alice")
	second := b.start(t, "bob")
	third := b.start(t, "carol")
	b.now = b.now.Add(10 * time.Second)
	if status, saved := b.submit(t, first); status != 200 || saved.Multiplier != 1 || saved.FlagVersion != 1 {
		t.Fatalf("status=%d saved=%+v", status, saved)
	}
	b.table.set(true, 2)
	b.now = b.now.Add(benchTTL - time.Millisecond)
	if status, saved := b.submit(t, second); status != 200 || saved.Score != 10 || saved.Multiplier != 1 || saved.FlagVersion != 1 {
		t.Fatalf("inside the bound: status=%d saved=%+v", status, saved)
	}
	b.now = b.now.Add(time.Millisecond)
	if status, saved := b.submit(t, third); status != 200 || saved.Score != 20 || saved.Multiplier != 2 || saved.FlagVersion != 2 {
		t.Fatalf("at the bound: status=%d saved=%+v", status, saved)
	}
}

func TestResubmissionDoesNotApplyTheFlagAgain(t *testing.T) {
	b := newBench(t)
	submission := b.start(t, "alice")
	b.now = b.now.Add(10 * time.Second)
	if status, saved := b.submit(t, submission); status != 200 || saved.Score != 10 {
		t.Fatalf("first: status=%d saved=%+v", status, saved)
	}
	b.table.set(true, 2)
	b.now = b.now.Add(benchTTL)
	if status, _ := b.submit(t, submission); status != http.StatusConflict {
		t.Fatalf("resubmission: status=%d", status)
	}
	if len(b.db.accepted) != 1 || b.db.accepted[0].Score != 10 || b.db.accepted[0].Multiplier != 1 || len(b.boards.updates) != 1 {
		t.Fatalf("stored=%+v board updates=%d", b.db.accepted, len(b.boards.updates))
	}
}

func TestConcurrentResubmissionsDoubleOnce(t *testing.T) {
	b := newBench(t)
	b.table.set(true, 2)
	submission := b.start(t, "alice")
	b.now = b.now.Add(10 * time.Second)
	statuses := make(chan int, 24)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); status, _ := b.submit(t, submission); statuses <- status }()
	}
	wg.Wait()
	close(statuses)
	accepted := 0
	for status := range statuses {
		if status == 200 {
			accepted++
		} else if status != http.StatusConflict {
			t.Fatalf("status=%d", status)
		}
	}
	if accepted != 1 || len(b.db.accepted) != 1 || b.db.accepted[0].Score != 20 || len(b.boards.updates) != 1 {
		t.Fatalf("accepted=%d stored=%+v boards=%d", accepted, b.db.accepted, len(b.boards.updates))
	}
}

// The score service does not guess when it cannot read the flag: it answers 503
// and leaves the run unconsumed, so the same token still works afterward.
func TestFlagReadFailureLeavesTheRunUsable(t *testing.T) {
	b := newBench(t)
	submission := b.start(t, "alice")
	b.now = b.now.Add(10 * time.Second)
	b.table.err = errors.New("database down")
	if status, _ := b.submit(t, submission); status != http.StatusServiceUnavailable || len(b.db.used) != 0 {
		t.Fatalf("status=%d attempts=%d", status, len(b.db.used))
	}
	b.table.err = nil
	if status, saved := b.submit(t, submission); status != 200 || saved.Score != 10 {
		t.Fatalf("retry: status=%d saved=%+v", status, saved)
	}
}

// A rejected run has no multiplier, so it does not need the flag and is still
// recorded when the flag cannot be read.
func TestRejectedRunDoesNotReadTheFlag(t *testing.T) {
	b := newBench(t)
	submission := b.start(t, "mallory")
	submission.Score = 999
	b.now = b.now.Add(10 * time.Second)
	b.table.err = errors.New("database down")
	if status, _ := b.submit(t, submission); status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", status)
	}
	if b.table.reads != 0 || len(b.db.rejected) != 1 {
		t.Fatalf("flag reads=%d rejected=%v", b.table.reads, b.db.rejected)
	}
}
