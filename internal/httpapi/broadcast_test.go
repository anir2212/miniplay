package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"miniplay/internal/game"
	"miniplay/internal/store"
)

// countingCache records how often the live path reaches Redis.
type countingCache struct {
	mu         sync.Mutex
	board      store.Board
	ranks      map[string]int64
	updates    chan struct{}
	subscribes int
	closes     int
	boardReads int
	rankCalls  [][]string
}

func (c *countingCache) HasBoard(context.Context, string) (bool, error)        { return true, nil }
func (c *countingCache) Backfill(context.Context, string, []store.Score) error { return nil }
func (c *countingCache) Board(context.Context, string, string) (store.Board, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.boardReads++
	return c.board, nil
}
func (c *countingCache) Ranks(_ context.Context, _ string, users []string) (map[string]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rankCalls = append(c.rankCalls, append([]string(nil), users...))
	return c.ranks, nil
}
func (c *countingCache) SubscribeBoard(context.Context, string) (<-chan struct{}, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribes++
	return c.updates, func() { c.mu.Lock(); c.closes++; c.mu.Unlock() }, nil
}
func (c *countingCache) counts() (subscribes, closes, boardReads int, rankCalls [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribes, c.closes, c.boardReads, append([][]string(nil), c.rankCalls...)
}

func dialLive(t *testing.T, ctx context.Context, url, user string) *websocket.Conn {
	t.Helper()
	target := "ws" + strings.TrimPrefix(url, "http") + "?game=rhythm"
	if user != "" {
		target += "&user=" + user
	}
	conn, _, err := websocket.Dial(ctx, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func readFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) liveMessage {
	t.Helper()
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

func TestLiveSocketsOnOneBoardShareSubscriptionAndBoardRead(t *testing.T) {
	cache := &countingCache{
		board:   store.Board{Leaders: []store.Leader{{User: "alice", Score: 30}}},
		ranks:   map[string]int64{"alice": 1},
		updates: make(chan struct{}, 1),
	}
	server := httptest.NewServer(LiveHandler(liveStore{}, cache, time.Now))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	player := dialLive(t, ctx, server.URL, "alice")
	defer player.CloseNow()
	watcher := dialLive(t, ctx, server.URL, "")
	defer watcher.CloseNow()
	readFrame(t, ctx, player) // initial frames: each socket has joined
	readFrame(t, ctx, watcher)
	_, _, readsBefore, _ := cache.counts()

	cache.updates <- struct{}{}
	playerFrame, watcherFrame := readFrame(t, ctx, player), readFrame(t, ctx, watcher)
	if playerFrame.Board.MyRank == nil || *playerFrame.Board.MyRank != 1 || playerFrame.Board.Leaders[0].User != "alice" {
		t.Fatalf("player frame = %+v", playerFrame)
	}
	if watcherFrame.Board.MyRank != nil || watcherFrame.Board.Leaders[0].User != "alice" {
		t.Fatalf("watcher frame = %+v", watcherFrame)
	}
	subscribes, _, reads, rankCalls := cache.counts()
	if subscribes != 1 {
		t.Fatalf("two sockets on one board made %d Redis subscriptions; want 1", subscribes)
	}
	if reads-readsBefore != 1 || len(rankCalls) != 1 || !reflect.DeepEqual(rankCalls[0], []string{"alice"}) {
		t.Fatalf("one change made %d board reads and rank calls %v; want 1 read and one call for alice", reads-readsBefore, rankCalls)
	}
}

func TestLiveBoardSubscriptionEndsWithLastSocket(t *testing.T) {
	cache := &countingCache{updates: make(chan struct{}, 1), ranks: map[string]int64{}}
	server := httptest.NewServer(LiveHandler(liveStore{}, cache, time.Now))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := dialLive(t, ctx, server.URL, "")
	readFrame(t, ctx, first)
	first.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, closes, _, _ := cache.counts(); closes == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription still open after the last socket left")
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := dialLive(t, ctx, server.URL, "")
	defer second.CloseNow()
	readFrame(t, ctx, second)
	if subscribes, _, _, _ := cache.counts(); subscribes != 2 {
		t.Fatalf("subscribes = %d; a new socket after the hub closed should subscribe again", subscribes)
	}
}

func TestOfferKeepsOnlyNewestFrame(t *testing.T) {
	member := &liveMember{frames: make(chan []byte, 1), gone: make(chan struct{})}
	member.offer([]byte("old"))
	member.offer([]byte("new"))
	if got := string(<-member.frames); got != "new" {
		t.Fatalf("frame = %q; a slow socket should get the newest board", got)
	}
	select {
	case extra := <-member.frames:
		t.Fatalf("stale frame %q left queued", extra)
	default:
	}
}

func TestHubFrameMatchesLiveMessage(t *testing.T) {
	rank := int64(4)
	board := store.Board{Leaders: []store.Leader{{User: "bob", Score: 20}, {User: "ann", Score: 10}}, MyRank: &rank}
	hub := &boardHub{id: game.Rhythm, period: store.Weekly}
	leaders, _ := json.Marshal(board.Leaders)
	want, _ := json.Marshal(liveMessage{Type: "board", Game: game.Rhythm, Period: store.Weekly, Board: board})
	if got := hubFrame(hub, leaders, &rank); string(got) != string(want) {
		t.Fatalf("hub frame\n%s\nwant\n%s", got, want)
	}
}

// brokenCache cannot say whether its board is complete.
type brokenCache struct{ countingCache }

func (*brokenCache) HasBoard(context.Context, string) (bool, error) {
	return false, errors.New("redis down")
}

type rankedStore struct{ rows []store.Score }

func (s rankedStore) Leaderboard(context.Context, game.ID, time.Time, string, int) ([]store.Score, error) {
	return s.rows, nil
}
func (rankedStore) BestScores(context.Context, game.ID, time.Time) ([]store.Score, error) {
	return nil, nil
}

func TestBroadcastRanksFallBackToSQL(t *testing.T) {
	db := rankedStore{rows: []store.Score{{User: "cat", Score: 50}, {User: "dan", Score: 40}, {User: "eve", Score: 30}}}
	cache := &brokenCache{}
	board, ranks, err := completeBoardRanks(context.Background(), db, cache, game.Rhythm, "board:rhythm:all", time.Time{}, []string{"eve", "cat", "zed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Leaders) != 3 || board.Leaders[0].User != "cat" || board.MyRank != nil {
		t.Fatalf("board = %+v", board)
	}
	if !reflect.DeepEqual(ranks, map[string]int64{"cat": 1, "eve": 3}) {
		t.Fatalf("ranks = %v", ranks)
	}
	if _, _, reads, rankCalls := cache.counts(); reads != 0 || len(rankCalls) != 0 {
		t.Fatalf("read the unverified Redis board: %d reads, %d rank calls", reads, len(rankCalls))
	}
}
