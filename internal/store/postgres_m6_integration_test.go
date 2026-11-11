package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/game"
	"miniplay/internal/migrate"
)

// Run against a disposable miniplay_it database only. These tests use random
// run IDs and read deltas, so they tolerate earlier rows. Run packages one at a
// time (go test -p 1): the flag rows are shared across packages.
func openM6(t *testing.T) (*Postgres, context.Context) {
	t.Helper()
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
	var name string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "miniplay_it" {
		t.Fatalf("refusing to write to %q; use a disposable miniplay_it database", name)
	}
	if _, err := migrate.Apply(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	return db, ctx
}

func randomID(t *testing.T, prefix string) string {
	t.Helper()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return prefix + "-" + hex.EncodeToString(raw)
}

func auditCount(t *testing.T, ctx context.Context, db *Postgres) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin_audit`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestFlagChangeAndAuditCommitTogetherIntegration(t *testing.T) {
	db, ctx := openM6(t)
	before, err := db.FeatureFlag(ctx, game.Rhythm)
	if err != nil {
		t.Fatal(err)
	}
	audits := auditCount(t, ctx, db)

	after, changed, err := db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Rhythm, DoublePoints: !before.DoublePoints,
		ExpectedVersion: before.Version, Details: map[string]string{"clientIp": "127.0.0.1"}})
	if err != nil || !changed || after.DoublePoints == before.DoublePoints || after.Version != before.Version+1 {
		t.Fatalf("change: after=%+v changed=%t err=%v", after, changed, err)
	}
	entries, err := db.AuditLog(ctx, 1, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit: %+v err=%v", entries, err)
	}
	entry := entries[0]
	var was, now flagAuditValue
	if json.Unmarshal(entry.Before, &was) != nil || json.Unmarshal(entry.After, &now) != nil {
		t.Fatalf("audit values: %s %s", entry.Before, entry.After)
	}
	if entry.Actor != "integration" || entry.Action != AuditSetDoublePoints || entry.Game != game.Rhythm ||
		was.DoublePoints != before.DoublePoints || was.Version != before.Version || now.DoublePoints != after.DoublePoints || now.Version != after.Version {
		t.Fatalf("audit entry: %+v", entry)
	}
	// NOW() is the transaction's start time, so equal timestamps show one transaction.
	if !entry.At.Equal(after.UpdatedAt) || entry.Details["clientIp"] != "127.0.0.1" {
		t.Fatalf("audit at=%s flag updated=%s details=%v", entry.At, after.UpdatedAt, entry.Details)
	}

	// Setting the value it already holds changes nothing and writes no row.
	same, changed, err := db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Rhythm, DoublePoints: after.DoublePoints, ExpectedVersion: after.Version})
	if err != nil || changed || same.Version != after.Version || auditCount(t, ctx, db) != audits+1 {
		t.Fatalf("no-op: flag=%+v changed=%t err=%v", same, changed, err)
	}

	// A stale version is refused and returns the current state.
	current, _, err := db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Rhythm, DoublePoints: before.DoublePoints, ExpectedVersion: before.Version})
	if !errors.Is(err, ErrFlagConflict) || current.Version != after.Version || auditCount(t, ctx, db) != audits+1 {
		t.Fatalf("stale: flag=%+v err=%v", current, err)
	}

	// The empty actor passes the flag UPDATE and then fails the audit table's
	// CHECK. The flag must roll back with it: no flag change without its audit row.
	if _, _, err = db.SetDoublePoints(ctx, FlagChange{Actor: "", Game: game.Rhythm, DoublePoints: before.DoublePoints, ExpectedVersion: after.Version}); err == nil {
		t.Fatal("a failed audit insert must fail the change")
	}
	unchanged, err := db.FeatureFlag(ctx, game.Rhythm)
	if err != nil || unchanged.Version != after.Version || unchanged.DoublePoints != after.DoublePoints || auditCount(t, ctx, db) != audits+1 {
		t.Fatalf("after failed audit: %+v err=%v", unchanged, err)
	}

	// Of many admins acting on the same version, exactly one wins.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Rhythm, DoublePoints: before.DoublePoints, ExpectedVersion: after.Version})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won, lost := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, ErrFlagConflict) {
			lost++
		} else {
			t.Fatal(err)
		}
	}
	final, _ := db.FeatureFlag(ctx, game.Rhythm)
	if won != 1 || lost != 7 || final.Version != after.Version+1 || auditCount(t, ctx, db) != audits+2 {
		t.Fatalf("concurrent: won=%d lost=%d flag=%+v", won, lost, final)
	}
	if _, _, err := db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: "chess", ExpectedVersion: 1}); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game: %v", err)
	}
}

func TestAuditIsAppendOnlyIntegration(t *testing.T) {
	db, ctx := openM6(t)
	flag, err := db.FeatureFlag(ctx, game.Reflex)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Reflex, DoublePoints: !flag.DoublePoints, ExpectedVersion: flag.Version}); err != nil {
		t.Fatal(err)
	}
	entries, err := db.AuditLog(ctx, 1, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	count := auditCount(t, ctx, db)
	for _, statement := range []string{
		`UPDATE admin_audit SET actor='tampered'`,
		`UPDATE admin_audit SET actor='tampered' WHERE id=-1`, // matches nothing; still refused
		`DELETE FROM admin_audit`,
		`DELETE FROM admin_audit WHERE id=-1`,
		`TRUNCATE admin_audit`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err=%v", statement, err)
		}
	}
	again, err := db.AuditLog(ctx, 1, 0)
	if err != nil || len(again) != 1 || again[0].Actor != entries[0].Actor || auditCount(t, ctx, db) != count {
		t.Fatalf("audit changed: %+v err=%v", again, err)
	}
	older, err := db.AuditLog(ctx, 200, entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range older {
		if entry.ID >= entries[0].ID {
			t.Fatalf("page before %d returned %d", entries[0].ID, entry.ID)
		}
	}
}

func TestStoredScoresStayExplainableIntegration(t *testing.T) {
	db, ctx := openM6(t)
	statsBefore, err := db.LiveStats(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	flag, err := db.FeatureFlag(ctx, game.Rhythm)
	if err != nil {
		t.Fatal(err)
	}
	if !flag.DoublePoints {
		if flag, _, err = db.SetDoublePoints(ctx, FlagChange{Actor: "integration", Game: game.Rhythm, DoublePoints: true, ExpectedVersion: flag.Version}); err != nil {
			t.Fatal(err)
		}
	}
	runID, user := randomID(t, "explain"), randomID(t, "u")[:20]
	row := Score{RunID: runID, User: user, Game: game.Rhythm, Score: 60, BaseScore: 30, Multiplier: 2, FlagVersion: flag.Version, DurationMS: game.RunMilliseconds}
	saved, err := db.RecordAttempt(ctx, row, []game.Tap{}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.ExplainRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Score != 60 || run.BaseScore != 30 || run.Multiplier != 2 || run.FlagVersion != flag.Version || !run.SubmittedAt.Equal(saved.SubmittedAt) {
		t.Fatalf("explanation: %+v", run)
	}
	if run.FlagChange == nil || run.FlagChange.Actor != "integration" {
		t.Fatalf("the explanation should name the change that set version %d: %+v", flag.Version, run.FlagChange)
	}

	// A retried row with different numbers is a duplicate; the first row stands.
	retry := row
	retry.Score, retry.BaseScore, retry.Multiplier = 120, 60, 2
	if _, err := db.RecordAttempt(ctx, retry, []game.Tap{}, ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("retry: %v", err)
	}
	if again, err := db.ExplainRun(ctx, runID); err != nil || again.Score != 60 {
		t.Fatalf("after retry: %+v err=%v", again, err)
	}

	// The table refuses a row whose numbers do not explain themselves.
	for name, bad := range map[string]Score{
		"score is not base times multiplier": {Score: 70, BaseScore: 30, Multiplier: 2},
		"multiplier that does not exist":     {Score: 90, BaseScore: 30, Multiplier: 3},
		"no multiplier recorded":             {Score: 30, BaseScore: 30},
	} {
		bad.RunID, bad.User, bad.Game, bad.DurationMS = randomID(t, "bad"), user, game.Rhythm, game.RunMilliseconds
		if _, err := db.RecordAttempt(ctx, bad, []game.Tap{}, ""); err == nil {
			t.Errorf("%s: row accepted", name)
		}
		var attempts int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM run_attempts WHERE run_id=$1`, bad.RunID).Scan(&attempts); err != nil || attempts != 0 {
			t.Errorf("%s: the failed row left %d attempts, err=%v", name, attempts, err)
		}
	}

	// The outbox event carries the same explanation.
	var payload []byte
	if err := db.Pool.QueryRow(ctx, `SELECT payload FROM event_outbox WHERE event_id=$1`, "accepted:"+runID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	event, err := DecodeEvent(payload)
	if err != nil || event.Score != 60 || event.BaseScore != 30 || event.Multiplier != 2 || event.FlagVersion != flag.Version {
		t.Fatalf("event=%+v err=%v", event, err)
	}

	// Abuse features read the base score, and the review list joins it back.
	runs, err := db.RecentRuns(ctx, saved.SubmittedAt.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, recent := range runs {
		found = found || (recent.RunID == runID && recent.BaseScore == 30 && recent.Score.Score == 60)
	}
	if !found {
		t.Fatalf("run %s missing from RecentRuns", runID)
	}
	if err := db.SaveFlag(ctx, FlagRecord{RunID: runID, User: user, Game: game.Rhythm, IsolationScore: 0.7, ZScore: 3.2, Reasons: []string{"high-score z-score"}, Evidence: map[string]float64{"tapCount": 3}}); err != nil {
		t.Fatal(err)
	}
	flagged, err := db.Flags(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, record := range flagged {
		found = found || (record.RunID == runID && record.Score == 60 && record.BaseScore == 30 && record.Multiplier == 2 && record.Evidence["tapCount"] == 3)
	}
	if !found {
		t.Fatalf("run %s missing from Flags", runID)
	}

	// Live figures read the tables directly: one more accepted and doubled run,
	// one more player, and one more pending outbox event.
	stats, err := db.LiveStats(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var was, now GameLiveStats
	for i := range stats.Games {
		if stats.Games[i].Game == game.Rhythm {
			now = stats.Games[i]
		}
	}
	for i := range statsBefore.Games {
		if statsBefore.Games[i].Game == game.Rhythm {
			was = statsBefore.Games[i]
		}
	}
	if now.Accepted != was.Accepted+1 || now.Doubled != was.Doubled+1 || now.Players != was.Players+1 || stats.OutboxPending != statsBefore.OutboxPending+1 || now.LastAcceptedAt == nil {
		t.Fatalf("live stats before=%+v after=%+v outbox %d to %d", was, now, statsBefore.OutboxPending, stats.OutboxPending)
	}

	if _, err := db.ExplainRun(ctx, "no-such-run"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

// Migration 003 must keep rows that existed before it, and the runner must cope
// with a function body containing semicolons. It runs in a throwaway schema.
func TestMigrationBackfillsExistingScoresIntegration(t *testing.T) {
	db, ctx := openM6(t)
	schema := strings.ReplaceAll(randomID(t, "m6_backfill"), "-", "_")
	if _, err := db.Pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	conn, err := pgx.Connect(ctx, os.Getenv("MINIPLAY_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	old := t.TempDir()
	for _, name := range []string{"001_init.sql", "002_abuse.sql"} {
		contents, err := os.ReadFile(filepath.Join("../../db/migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := migrate.Apply(ctx, conn, old); err != nil || applied != 2 {
		t.Fatalf("old migrations: applied=%d err=%v", applied, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO scores(run_id,user_id,game,score,duration_ms,events) VALUES('old-run','old-user','rhythm',40,10000,'[]')`); err != nil {
		t.Fatal(err)
	}
	if applied, err := migrate.Apply(ctx, conn, "../../db/migrations"); err != nil || applied != 1 {
		t.Fatalf("migration 003: applied=%d err=%v", applied, err)
	}
	var score, base, multiplier int
	var flagVersion int64
	err = conn.QueryRow(ctx, `SELECT score,base_score,multiplier,flag_version FROM scores WHERE run_id='old-run'`).Scan(&score, &base, &multiplier, &flagVersion)
	if err != nil || score != 40 || base != 40 || multiplier != 1 || flagVersion != 0 {
		t.Fatalf("backfill: score=%d base=%d multiplier=%d version=%d err=%v", score, base, multiplier, flagVersion, err)
	}
	var version int64
	if err := conn.QueryRow(ctx, `SELECT version FROM feature_flags WHERE game='rhythm'`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("flag version=%d err=%v", version, err)
	}
}
