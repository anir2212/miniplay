package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/game"
	"miniplay/internal/migrate"
)

// Run against a disposable database only. The normal unit suite skips this test.
func TestPostgresAttemptAndEventIntegration(t *testing.T) {
	url := os.Getenv("MINIPLAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set MINIPLAY_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var databaseName string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if databaseName != "miniplay_it" {
		t.Fatalf("refusing to write to %q; use a disposable miniplay_it database", databaseName)
	}
	if applied, err := migrate.Apply(ctx, conn, "../../db/migrations"); err != nil || applied != 2 {
		t.Fatalf("first migration pass: applied=%d err=%v", applied, err)
	}
	if applied, err := migrate.Apply(ctx, conn, "../../db/migrations"); err != nil || applied != 0 {
		t.Fatalf("idempotent migration pass: applied=%d err=%v", applied, err)
	}
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()

	row := Score{RunID: "integration-accepted", User: "integration-user", Game: game.Rhythm,
		Score: 17, DurationMS: game.RunMilliseconds}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.RecordAttempt(ctx, row, []game.Tap{}, "")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, duplicates := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrDuplicate):
			duplicates++
		default:
			t.Fatalf("unexpected concurrent attempt error: %v", err)
		}
	}
	if ok != 1 || duplicates != 11 {
		t.Fatalf("accepted=%d duplicate=%d, want 1/11", ok, duplicates)
	}
	var attempts, scores, outbox int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM run_attempts),
		(SELECT COUNT(*) FROM scores),(SELECT COUNT(*) FROM event_outbox)`).Scan(&attempts, &scores, &outbox); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || scores != 1 || outbox != 1 {
		t.Fatalf("atomic accepted transaction: attempts=%d scores=%d outbox=%d", attempts, scores, outbox)
	}
	pending, err := db.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending outbox: rows=%d err=%v", len(pending), err)
	}
	event, err := DecodeEvent(pending[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		applied, err := db.ApplyEvent(ctx, event)
		if err != nil || applied != (i == 0) {
			t.Fatalf("event application %d: applied=%t err=%v", i, applied, err)
		}
	}
	stats, err := db.Stats(ctx, 10)
	if err != nil || len(stats) != 1 || stats[0].Accepted != 1 || stats[0].ScoreSum != 17 {
		t.Fatalf("deduplicated stats: rows=%v err=%v", stats, err)
	}
	if err := db.MarkPublished(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if pending, err = db.Pending(ctx, 10); err != nil || len(pending) != 0 {
		t.Fatalf("marked outbox: rows=%d err=%v", len(pending), err)
	}

	rejected := Score{RunID: "integration-rejected", User: "integration-user", Game: game.Reflex,
		DurationMS: game.RunMilliseconds}
	if _, err := db.RecordAttempt(ctx, rejected, nil, "score_mismatch"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordAttempt(ctx, rejected, nil, "score_mismatch"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("rejected run replay: %v", err)
	}
	var rejections int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rejections`).Scan(&rejections); err != nil || rejections != 1 {
		t.Fatalf("rejected transaction: rows=%d err=%v", rejections, err)
	}
}
