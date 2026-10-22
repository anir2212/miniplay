package store

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"miniplay/internal/game"
)

// This test uses only its own keys in an explicitly selected disposable Redis
// database. It is opt-in because the normal suite does not start Redis.
func TestRedisBoardRecoveryIntegration(t *testing.T) {
	url := os.Getenv("MINIPLAY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set MINIPLAY_TEST_REDIS_URL to a disposable loopback Redis database 15")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(opts.Addr)
	ip := net.ParseIP(host)
	if err != nil || opts.DB != 15 || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("integration test requires a loopback Redis URL selecting database 15")
	}
	ctx := context.Background()
	cache, err := OpenRedis(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Client.Close()
	at := time.Date(2037, 7, 21, 12, 0, 0, 0, time.UTC)
	keys := make([]string, 0, 8)
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		for _, period := range []Period{All, Daily, Weekly} {
			key, _, err := BoardKey(id, period, at)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, key, boardMarker(key))
		}
	}
	if count, err := cache.Client.Exists(ctx, keys...).Result(); err != nil || count != 0 {
		t.Fatalf("test keys already exist (count=%d, err=%v); use an empty disposable Redis DB 15", count, err)
	}
	// Defers run in reverse order: remove test keys before closing the client.
	defer func() { _ = cache.Client.Del(context.Background(), keys...).Err() }()
	key, _, _ := BoardKey(game.Rhythm, All, at)
	first := Score{User: "alice", Game: game.Rhythm, Score: 10, SubmittedAt: at}
	if err := cache.UpdateBoards(ctx, first); err != nil {
		t.Fatal(err)
	}
	if ready, err := cache.HasBoard(ctx, key); err != nil || ready {
		t.Fatalf("partial board ready=%t err=%v", ready, err)
	}
	if err := cache.Backfill(ctx, key, []Score{first, {User: "bob", Score: 20}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := cache.HasBoard(ctx, key); err != nil || !ready {
		t.Fatalf("backfilled board ready=%t err=%v", ready, err)
	}
	board, err := cache.Board(ctx, key, "alice")
	if err != nil || len(board.Leaders) != 2 || board.Leaders[0].User != "bob" || board.Leaders[1].Score != 10 || board.MyRank == nil || *board.MyRank != 2 {
		t.Fatalf("backfilled board=%+v err=%v", board, err)
	}
	first.Score = 5
	if err := cache.UpdateBoards(ctx, first); err != nil {
		t.Fatal(err)
	}
	board, err = cache.Board(ctx, key, "alice")
	if err != nil || board.Leaders[1].Score != 10 {
		t.Fatalf("lower replay changed best score: %+v err=%v", board, err)
	}
	// Simulate cache eviction, then a single accepted event arriving before a
	// read. The new one-member key must still be treated as incomplete.
	if err := cache.Client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	first.User, first.Score = "charlie", 7
	if err := cache.UpdateBoards(ctx, first); err != nil {
		t.Fatal(err)
	}
	if ready, err := cache.HasBoard(ctx, key); err != nil || ready {
		t.Fatalf("post-eviction partial board ready=%t err=%v", ready, err)
	}
	if err := cache.Backfill(ctx, key, []Score{{User: "alice", Score: 10}, {User: "bob", Score: 20}, first}); err != nil {
		t.Fatal(err)
	}
	board, err = cache.Board(ctx, key, "charlie")
	if err != nil || len(board.Leaders) != 3 || board.MyRank == nil || *board.MyRank != 3 {
		t.Fatalf("recovered board=%+v err=%v", board, err)
	}
	emptyKey, _, _ := BoardKey(game.Reflex, Daily, at)
	if err := cache.Backfill(ctx, emptyKey, nil); err != nil {
		t.Fatal(err)
	}
	if ready, err := cache.HasBoard(ctx, emptyKey); err != nil || !ready {
		t.Fatalf("empty board ready=%t err=%v", ready, err)
	}
	if ttl, err := cache.Client.TTL(ctx, boardMarker(emptyKey)).Result(); err != nil || ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("empty marker TTL=%v err=%v", ttl, err)
	}
	if err := cache.UpdateBoards(ctx, Score{User: "dave", Game: game.Reflex, Score: 15, SubmittedAt: at}); err != nil {
		t.Fatal(err)
	}
	if ready, err := cache.HasBoard(ctx, emptyKey); err != nil || ready {
		t.Fatalf("new score should invalidate empty marker: ready=%t err=%v", ready, err)
	}
}

// A replayed or lower score leaves the board unchanged and must not wake live sockets.
func TestRedisBoardPublishesOnlyChangesIntegration(t *testing.T) {
	url := os.Getenv("MINIPLAY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set MINIPLAY_TEST_REDIS_URL to a disposable loopback Redis database 15")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(opts.Addr)
	ip := net.ParseIP(host)
	if err != nil || opts.DB != 15 || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("integration test requires a loopback Redis URL selecting database 15")
	}
	ctx := context.Background()
	cache, err := OpenRedis(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Client.Close()
	at := time.Date(2038, 3, 9, 12, 0, 0, 0, time.UTC)
	var keys []string
	for _, period := range []Period{All, Daily, Weekly} {
		key, _, err := BoardKey(game.Rhythm, period, at)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key, boardMarker(key))
	}
	if count, err := cache.Client.Exists(ctx, keys...).Result(); err != nil || count != 0 {
		t.Fatalf("test keys already exist (count=%d, err=%v); use an empty disposable Redis DB 15", count, err)
	}
	defer func() { _ = cache.Client.Del(context.Background(), keys...).Err() }()
	key, _, _ := BoardKey(game.Rhythm, All, at)
	sub := cache.Client.Subscribe(ctx, "updates:"+key)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	messages := sub.Channel()
	for _, score := range []int{30, 30, 20, 45} { // new, replay, lower, higher
		if err := cache.UpdateBoards(ctx, Score{User: "erin", Game: game.Rhythm, Score: score, SubmittedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	got := 0
	timeout := time.After(500 * time.Millisecond)
collect:
	for {
		select {
		case <-messages:
			got++
		case <-timeout:
			break collect
		}
	}
	if got != 2 {
		t.Fatalf("published %d updates for new, replay, lower, higher; want 2", got)
	}
}
