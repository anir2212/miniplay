package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"miniplay/internal/game"
	"miniplay/internal/store"
)

type liveStore struct{}

func (liveStore) Leaderboard(context.Context, game.ID, time.Time, string, int) ([]store.Score, error) {
	return nil, nil
}
func (liveStore) BestScores(context.Context, game.ID, time.Time) ([]store.Score, error) {
	return nil, nil
}

type liveCache struct {
	mu      sync.Mutex
	board   store.Board
	updates chan struct{}
}

func (c *liveCache) HasBoard(context.Context, string) (bool, error)        { return true, nil }
func (c *liveCache) Backfill(context.Context, string, []store.Score) error { return nil }
func (c *liveCache) Board(context.Context, string, string) (store.Board, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.board, nil
}
func (c *liveCache) SubscribeBoard(context.Context, string) (<-chan struct{}, func(), error) {
	return c.updates, func() {}, nil
}

func TestLiveLeaderboardSendsInitialAndUpdatedBoard(t *testing.T) {
	cache := &liveCache{board: store.Board{Leaders: []store.Leader{{User: "alice", Score: 10}}}, updates: make(chan struct{}, 1)}
	server := httptest.NewServer(LiveHandler(liveStore{}, cache, time.Now))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?game=rhythm", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func() liveMessage {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message liveMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	first := read()
	if first.Board.Leaders[0].Score != 10 {
		t.Fatalf("initial=%+v", first)
	}
	cache.mu.Lock()
	cache.board.Leaders = []store.Leader{{User: "bob", Score: 20}}
	cache.mu.Unlock()
	cache.updates <- struct{}{}
	second := read()
	if second.Board.Leaders[0].User != "bob" {
		t.Fatalf("update=%+v", second)
	}
}
