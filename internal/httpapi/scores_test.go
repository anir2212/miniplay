package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

type fakeStore struct {
	mu       sync.Mutex
	used     map[string]bool
	accepted int
	rejected int
}

func (s *fakeStore) RecordAttempt(_ context.Context, row store.Score, _ []game.Tap, reason string) (store.Score, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[row.RunID] {
		return store.Score{}, store.ErrDuplicate
	}
	s.used[row.RunID] = true
	if reason == "" {
		s.accepted++
	} else {
		s.rejected++
	}
	row.SubmittedAt = time.Now()
	return row, nil
}
func (*fakeStore) DoublePoints(context.Context, game.ID) (bool, error) { return false, nil }

type fakeCache struct{}

func (*fakeCache) Allow(context.Context, string, int, float64) (bool, error) { return true, nil }
func (*fakeCache) UpdateBoards(context.Context, store.Score) error           { return nil }

func postScore(t *testing.T, handler http.Handler, submission Submission) int {
	t.Helper()
	body, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/scores", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func TestScoreSubmissionConsumesRunExactlyOnce(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, _ := token.New(strings.Repeat("s", 32), func() time.Time { return now })
	started, _ := signer.Issue("alice", game.Rhythm)
	now = now.Add(10 * time.Second)
	target, _ := game.Targets(game.Rhythm, started.Seed)
	submission := Submission{Token: started.Token, Events: []game.Tap{{MS: target[0].MS, X: target[0].X, Y: target[0].Y}}, Score: 10, DurationMS: 10000}
	db := &fakeStore{used: map[string]bool{}}
	handler := ScoreHandler(signer, db, &fakeCache{}, func() int64 { return now.UnixMilli() })
	const concurrent = 24
	statuses := make(chan int, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); statuses <- postScore(t, handler, submission) }()
	}
	wg.Wait()
	close(statuses)
	accepted, duplicates := 0, 0
	for status := range statuses {
		if status == 200 {
			accepted++
		} else if status == 409 {
			duplicates++
		} else {
			t.Fatalf("unexpected status %d", status)
		}
	}
	if accepted != 1 || duplicates != concurrent-1 || db.accepted != 1 {
		t.Fatalf("accepted=%d duplicate=%d rows=%d", accepted, duplicates, db.accepted)
	}
}

func TestInvalidScoreConsumesRunAndRecordsRejection(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, _ := token.New(strings.Repeat("s", 32), func() time.Time { return now })
	started, _ := signer.Issue("bob", game.Reflex)
	now = now.Add(10 * time.Second)
	db := &fakeStore{used: map[string]bool{}}
	handler := ScoreHandler(signer, db, &fakeCache{}, func() int64 { return now.UnixMilli() })
	submission := Submission{Token: started.Token, Score: 15, DurationMS: 10000}
	if status := postScore(t, handler, submission); status != 422 {
		t.Fatalf("status=%d", status)
	}
	if status := postScore(t, handler, submission); status != 409 {
		t.Fatalf("duplicate status=%d", status)
	}
	if db.rejected != 1 || db.accepted != 0 {
		t.Fatalf("rejected=%d accepted=%d", db.rejected, db.accepted)
	}
}
