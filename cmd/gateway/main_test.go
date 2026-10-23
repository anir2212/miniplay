package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	gateway := httptest.NewServer(newHandler(proxy(sessions.URL, upstreamTransport()), proxy(scores.URL, upstreamTransport()), http.NotFoundHandler(), t.TempDir()))
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

// countUpstreamConnections sends rounds of concurrent requests through the
// proxy and counts the TCP connections the upstream service accepts.
func countUpstreamConnections(t *testing.T, transport http.RoundTripper) int64 {
	t.Helper()
	var opened atomic.Int64
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond) // keep the requests in flight together
		_, _ = w.Write([]byte("ok"))
	}))
	upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	upstream.Start()
	defer upstream.Close()
	front := httptest.NewServer(proxy(upstream.URL, transport))
	defer front.Close()
	clientTransport := http.DefaultTransport.(*http.Transport).Clone()
	clientTransport.MaxIdleConnsPerHost = 64
	client := &http.Client{Transport: clientTransport}
	const concurrent, rounds = 16, 3
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		for i := 0; i < concurrent; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				response, err := client.Get(front.URL + "/api/sessions")
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}()
		}
		wg.Wait()
	}
	return opened.Load()
}

func TestGatewayReusesUpstreamConnectionsUnderConcurrency(t *testing.T) {
	// Three rounds of 16 concurrent requests need at most 16 connections when
	// idle ones are kept. The default transport keeps two per host and opened
	// about 44 in this test; each discarded one sits in TIME_WAIT.
	if got := countUpstreamConnections(t, upstreamTransport()); got > 20 {
		t.Fatalf("upstream accepted %d connections for 3 rounds of 16 concurrent requests; want at most 20", got)
	}
}

func TestGatewayCarriesSameOriginWebSocketOverTLS(t *testing.T) {
	// The release app loads the page over https and opens wss to the same
	// origin. The proxy keeps the browser's Host header, so the leaderboard
	// service's same-origin check still compares the page origin with it.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("board"))
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer upstream.Close()
	gateway := httptest.NewUnstartedServer(newHandler(http.NotFoundHandler(), http.NotFoundHandler(), proxy(upstream.URL, upstreamTransport()), t.TempDir()))
	gateway.EnableHTTP2 = true
	gateway.StartTLS()
	defer gateway.Close()
	roots := gateway.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	host := strings.TrimPrefix(gateway.URL, "https://")
	dial := func(origin string) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
		return websocket.Dial(ctx, "wss://"+host+"/api/boards/live?game=rhythm", &websocket.DialOptions{
			HTTPClient: client, HTTPHeader: http.Header{"Origin": {origin}},
		})
	}
	conn, _, err := dial("https://" + host)
	if err != nil {
		t.Fatalf("same-origin wss through the gateway: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, payload, err := conn.Read(ctx); err != nil || string(payload) != "board" {
		t.Fatalf("read %q, %v", payload, err)
	}
	conn.CloseNow()
	if _, resp, err := dial("https://other.example"); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin wss: err=%v resp=%v, want 403", err, resp)
	}
}
