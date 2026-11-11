package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/flags"
	"miniplay/internal/game"
	"miniplay/internal/migrate"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

// The real handlers, flag cache and PostgreSQL together. Only the Redis limiter
// and board are fakes here. Opt-in, disposable miniplay_it database, go test -p 1.
func TestDoublePointsEndToEndOverPostgresIntegration(t *testing.T) {
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
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil || name != "miniplay_it" {
		t.Fatalf("refusing database %q: %v", name, err)
	}
	if _, err := migrate.Apply(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()

	now := time.Now()
	clock := func() time.Time { return now }
	signer, err := token.New(strings.Repeat("s", 32), clock)
	if err != nil {
		t.Fatal(err)
	}
	const ttl = 5 * time.Second
	cache, err := flags.NewCache(db, ttl, clock)
	if err != nil {
		t.Fatal(err)
	}
	boards := &countingBoards{}
	scores := ScoreHandler(signer, db, cache, boards, func() int64 { return now.UnixMilli() })
	admin := AdminHandler(AdminConfig{Token: adminTestToken, Actor: "integration-admin", FlagTTL: ttl}, db)

	setFlag := func(on bool) store.Flag {
		t.Helper()
		current, err := db.FeatureFlag(ctx, game.Reflex)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"doublePoints": on, "expectedVersion": current.Version})
		response := adminRequest(admin, http.MethodPut, "/api/admin/games/reflex/double-points", "Bearer "+adminTestToken, string(body))
		if response.Code != http.StatusOK {
			t.Fatalf("set flag: status=%d body=%s", response.Code, response.Body)
		}
		var result struct{ Flag store.Flag }
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Flag
	}
	start := func() (Submission, string) {
		t.Helper()
		started, err := signer.Issue("it-"+time.Now().Format("150405.000000")[7:], game.Reflex)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := game.Targets(game.Reflex, started.Seed)
		if err != nil {
			t.Fatal(err)
		}
		return Submission{Token: started.Token, Events: []game.Tap{{MS: targets[0].MS, X: targets[0].X, Y: targets[0].Y}}, Score: 15, DurationMS: 10000}, started.RunID
	}
	submit := func(submission Submission) (int, store.Score) {
		t.Helper()
		body, _ := json.Marshal(submission)
		request := httptest.NewRequest(http.MethodPost, "/api/scores", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		scores.ServeHTTP(response, request)
		var saved store.Score
		_ = json.Unmarshal(response.Body.Bytes(), &saved)
		return response.Code, saved
	}
	explain := func(runID string) store.RunExplanation {
		t.Helper()
		response := adminRequest(admin, http.MethodGet, "/api/admin/runs/"+runID, "Bearer "+adminTestToken, "")
		var run store.RunExplanation
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &run) != nil {
			t.Fatalf("explain %s: status=%d body=%s", runID, response.Code, response.Body)
		}
		return run
	}
	advance := func(by time.Duration) { now = now.Add(by) }

	off := setFlag(false)
	advance(ttl)
	first, firstID := start()
	stale, staleID := start()
	fresh, freshID := start()
	afterOff, afterOffID := start()
	advance(10 * time.Second)

	// Off: multiplier 1.
	if status, saved := submit(first); status != 200 || saved.Score != 15 || saved.Multiplier != 1 || saved.FlagVersion != off.Version {
		t.Fatalf("flag off: status=%d saved=%+v", status, saved)
	}

	// The toggle commits, but inside the bound this service may still hold the old
	// state. That run must say so in its record.
	on := setFlag(true)
	if on.Version != off.Version+1 && off.DoublePoints {
		t.Fatalf("on=%+v off=%+v", on, off)
	}
	advance(ttl - time.Millisecond)
	if status, saved := submit(stale); status != 200 || saved.Score != 15 || saved.Multiplier != 1 || saved.FlagVersion != off.Version {
		t.Fatalf("inside the bound: status=%d saved=%+v", status, saved)
	}

	// At the bound the real table's new state applies. The run was signed before
	// the toggle, so this also shows the signing state does not count.
	advance(time.Millisecond)
	if status, saved := submit(fresh); status != 200 || saved.Score != 30 || saved.BaseScore != 15 || saved.Multiplier != 2 || saved.FlagVersion != on.Version {
		t.Fatalf("at the bound: status=%d saved=%+v", status, saved)
	}

	// The same submission again: 409, and the stored row keeps its numbers even
	// though the flag has since turned off.
	offAgain := setFlag(false)
	advance(ttl)
	if status, _ := submit(fresh); status != http.StatusConflict {
		t.Fatalf("resubmission: status=%d", status)
	}
	if status, saved := submit(afterOff); status != 200 || saved.Score != 15 || saved.FlagVersion != offAgain.Version {
		t.Fatalf("after turning off: status=%d saved=%+v", status, saved)
	}

	for runID, want := range map[string]struct {
		score, base, multiplier int
		version                 int64
	}{
		firstID:    {15, 15, 1, off.Version},
		staleID:    {15, 15, 1, off.Version},
		freshID:    {30, 15, 2, on.Version},
		afterOffID: {15, 15, 1, offAgain.Version},
	} {
		run := explain(runID)
		if run.Score != want.score || run.BaseScore != want.base || run.Multiplier != want.multiplier || run.FlagVersion != want.version {
			t.Errorf("run %s: %+v, want %+v", runID, run, want)
		}
	}
	doubled := explain(freshID)
	if doubled.FlagChange == nil || doubled.FlagChange.Actor != "integration-admin" {
		t.Fatalf("the doubled run should link to the audit row that turned the flag on: %+v", doubled.FlagChange)
	}
	var after struct {
		DoublePoints bool  `json:"doublePoints"`
		Version      int64 `json:"version"`
	}
	if err := json.Unmarshal(doubled.FlagChange.After, &after); err != nil || !after.DoublePoints || after.Version != on.Version {
		t.Fatalf("audit after=%s err=%v", doubled.FlagChange.After, err)
	}
	if len(boards.updates) != 4 {
		t.Fatalf("board updates=%d, want one per accepted run", len(boards.updates))
	}
}
