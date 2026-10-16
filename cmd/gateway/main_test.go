package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/httpapi"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

type testCache struct{}

func (testCache) Allow(context.Context, string, int, float64) (bool, error) { return true, nil }
func (testCache) UpdateBoards(context.Context, store.Score) error           { return nil }

type testAttempts struct{ used map[string]bool }

func (db *testAttempts) RecordAttempt(_ context.Context, row store.Score, _ []game.Tap, reason string) (store.Score, error) {
	if db.used[row.RunID] {
		return store.Score{}, store.ErrDuplicate
	}
	db.used[row.RunID] = true
	row.SubmittedAt = time.Now()
	return row, nil
}
func (*testAttempts) DoublePoints(context.Context, game.ID) (bool, error) { return false, nil }

func TestGatewayRoutesAPIsAndServesBuiltClient(t *testing.T) {
	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("MiniPlay client"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "app.js"), []byte("console.log('game')"), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream", name)
			w.WriteHeader(http.StatusAccepted)
		})
	}
	handler := newHandler(upstream("session"), upstream("score"), upstream("leaderboard"), webRoot)
	tests := []struct {
		method, path, upstream string
		status                 int
		body                   string
	}{
		{"POST", "/api/sessions", "session", 202, ""},
		{"POST", "/api/scores", "score", 202, ""},
		{"GET", "/api/leaderboards?game=rhythm", "leaderboard", 202, ""},
		{"GET", "/api/boards/live?game=reflex", "leaderboard", 202, ""},
		{"GET", "/api/health", "", 200, `{"status":"ok"}`},
		{"GET", "/", "", 200, "MiniPlay client"},
		{"GET", "/games/rhythm", "", 200, "MiniPlay client"},
		{"GET", "/app.js", "", 200, "console.log('game')"},
		{"GET", "/missing.js", "", 404, ""},
		{"GET", "/api/unknown", "", 404, ""},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status || response.Header().Get("X-Upstream") != test.upstream || !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("status=%d upstream=%q body=%q", response.Code, response.Header().Get("X-Upstream"), response.Body.String())
			}
		})
	}
}

func TestGatewayRealHTTPSessionToScoreFlowWithFakes(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, err := token.New(strings.Repeat("s", 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	db := &testAttempts{used: make(map[string]bool)}
	sessions := httptest.NewServer(httpapi.SessionHandler(signer, testCache{}))
	defer sessions.Close()
	scores := httptest.NewServer(httpapi.ScoreHandler(signer, db, testCache{}, func() int64 { return now.Add(10 * time.Second).UnixMilli() }))
	defer scores.Close()
	gateway := httptest.NewServer(newHandler(proxy(sessions.URL), proxy(scores.URL), http.NotFoundHandler(), t.TempDir()))
	defer gateway.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	post := func(path string, value any) *http.Response {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, gateway.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	startedResponse := post("/api/sessions", map[string]string{"user": "alice", "game": "rhythm"})
	defer startedResponse.Body.Close()
	if startedResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startedResponse.Body)
		t.Fatalf("session status=%d body=%s", startedResponse.StatusCode, body)
	}
	var started token.Start
	if err := json.NewDecoder(startedResponse.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	targets, err := game.Targets(game.Rhythm, started.Seed)
	if err != nil {
		t.Fatal(err)
	}
	submission := httpapi.Submission{
		Token: started.Token, Events: []game.Tap{{MS: targets[0].MS, X: targets[0].X, Y: targets[0].Y}},
		Score: 10, DurationMS: 10000,
	}
	accepted := post("/api/scores", submission)
	defer accepted.Body.Close()
	if accepted.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(accepted.Body)
		t.Fatalf("score status=%d body=%s", accepted.StatusCode, body)
	}
	var saved store.Score
	if err := json.NewDecoder(accepted.Body).Decode(&saved); err != nil || saved.Score != 10 || saved.RunID != started.RunID {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	duplicate := post("/api/scores", submission)
	defer duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict || len(db.used) != 1 {
		t.Fatalf("duplicate status=%d stored runs=%d", duplicate.StatusCode, len(db.used))
	}
}
