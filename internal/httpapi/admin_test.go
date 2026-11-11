package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

const adminTestToken = "local-demo-admin-token-0123456789abcdef"

type fakeAdminStore struct {
	calls   int
	changes []store.FlagChange
	setErr  error
	flag    store.Flag
	changed bool
}

func (f *fakeAdminStore) FeatureFlags(context.Context) ([]store.Flag, error) {
	f.calls++
	return []store.Flag{{Game: game.Reflex, Version: 1}, {Game: game.Rhythm, DoublePoints: true, Version: 3}}, nil
}
func (f *fakeAdminStore) SetDoublePoints(_ context.Context, change store.FlagChange) (store.Flag, bool, error) {
	f.calls++
	f.changes = append(f.changes, change)
	return f.flag, f.changed, f.setErr
}
func (f *fakeAdminStore) LiveStats(context.Context, time.Time) (store.LiveStats, error) {
	f.calls++
	return store.LiveStats{OutboxPending: 4}, nil
}
func (f *fakeAdminStore) Flags(context.Context, int) ([]store.FlagRecord, error) {
	f.calls++
	return []store.FlagRecord{{RunID: "run-1", User: "alice"}}, nil
}
func (f *fakeAdminStore) AuditLog(context.Context, int, int64) ([]store.AuditEntry, error) {
	f.calls++
	return []store.AuditEntry{{ID: 1, Actor: "local-demo-admin"}}, nil
}
func (f *fakeAdminStore) ExplainRun(_ context.Context, runID string) (store.RunExplanation, error) {
	f.calls++
	if runID == "missing" {
		return store.RunExplanation{}, store.ErrRunNotFound
	}
	return store.RunExplanation{RunID: runID, Score: 20, BaseScore: 10, Multiplier: 2, FlagVersion: 3}, nil
}

var adminRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/admin/games"},
	{http.MethodPut, "/api/admin/games/rhythm/double-points"},
	{http.MethodGet, "/api/admin/stats"},
	{http.MethodGet, "/api/admin/flagged-runs"},
	{http.MethodGet, "/api/admin/runs/run-1"},
	{http.MethodGet, "/api/admin/audit"},
}

func adminRequest(handler http.Handler, method, path, authorization, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newAdmin(db *fakeAdminStore) http.Handler {
	return AdminHandler(AdminConfig{Token: adminTestToken, Actor: "local-demo-admin", FlagTTL: 5 * time.Second}, db)
}

func TestAdminFailsClosedWithoutAConfiguredToken(t *testing.T) {
	db := &fakeAdminStore{}
	handler := AdminHandler(AdminConfig{Actor: "local-demo-admin"}, db)
	for _, route := range adminRoutes {
		for _, authorization := range []string{"", "Bearer ", "Bearer anything"} {
			response := adminRequest(handler, route.method, route.path, authorization, `{"doublePoints":true,"expectedVersion":1}`)
			if response.Code != http.StatusServiceUnavailable {
				t.Errorf("%s %s auth=%q: status=%d", route.method, route.path, authorization, response.Code)
			}
		}
	}
	if db.calls != 0 {
		t.Fatalf("storage was reached %d times", db.calls)
	}
}

func TestAdminRefusesAMissingOrWrongToken(t *testing.T) {
	db := &fakeAdminStore{}
	handler := newAdmin(db)
	for _, route := range adminRoutes {
		for _, authorization := range []string{
			"", "Bearer ", "Bearer wrong", "Bearer " + adminTestToken[:len(adminTestToken)-1], "Bearer " + adminTestToken + "x",
			"bearer " + adminTestToken, "Basic " + adminTestToken, adminTestToken,
		} {
			response := adminRequest(handler, route.method, route.path, authorization, `{"doublePoints":true,"expectedVersion":1}`)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("%s %s auth=%q: status=%d", route.method, route.path, authorization, response.Code)
			}
		}
	}
	if db.calls != 0 {
		t.Fatalf("storage was reached %d times", db.calls)
	}
}

func TestAdminAuthCheckRunsBeforeRouting(t *testing.T) {
	response := adminRequest(newAdmin(&fakeAdminStore{}), http.MethodGet, "/api/admin/no-such-route", "", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated caller should not learn which routes exist: status=%d", response.Code)
	}
}

func TestAdminReadRoutesAnswerWithTheToken(t *testing.T) {
	db := &fakeAdminStore{}
	handler := newAdmin(db)
	for _, route := range adminRoutes {
		if route.method != http.MethodGet {
			continue
		}
		response := adminRequest(handler, route.method, route.path, "Bearer "+adminTestToken, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("%s: status=%d cache=%q", route.path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	response := adminRequest(handler, http.MethodGet, "/api/admin/games", "Bearer "+adminTestToken, "")
	var games struct {
		Games []struct {
			ID, Title string
			Flag      store.Flag
		}
		FlagMaxStalenessSeconds float64
	}
	if err := json.Unmarshal(response.Body.Bytes(), &games); err != nil || len(games.Games) != 2 || games.Games[1].Title != "Pulse Field" || games.FlagMaxStalenessSeconds != 5 {
		t.Fatalf("games=%+v err=%v", games, err)
	}
	if response := adminRequest(handler, http.MethodGet, "/api/admin/runs/missing", "Bearer "+adminTestToken, ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing run: status=%d", response.Code)
	}
}

func TestAdminFlagChangeUsesTheServerActor(t *testing.T) {
	db := &fakeAdminStore{flag: store.Flag{Game: game.Rhythm, DoublePoints: true, Version: 2}, changed: true}
	handler := newAdmin(db)
	request := httptest.NewRequest(http.MethodPut, "/api/admin/games/rhythm/double-points", strings.NewReader(`{"doublePoints":true,"expectedVersion":1}`))
	request.Header.Set("Authorization", "Bearer "+adminTestToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actor", "someone-else")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(db.changes) != 1 {
		t.Fatalf("status=%d changes=%+v", response.Code, db.changes)
	}
	change := db.changes[0]
	if change.Actor != "local-demo-admin" || change.Game != game.Rhythm || !change.DoublePoints || change.ExpectedVersion != 1 {
		t.Fatalf("change=%+v", change)
	}
	spoofed := adminRequest(handler, http.MethodPut, "/api/admin/games/rhythm/double-points", "Bearer "+adminTestToken, `{"doublePoints":true,"expectedVersion":1,"actor":"someone-else"}`)
	if spoofed.Code != http.StatusBadRequest || len(db.changes) != 1 {
		t.Fatalf("an actor in the body must be refused: status=%d changes=%d", spoofed.Code, len(db.changes))
	}
}

func TestAdminFlagChangeRefusals(t *testing.T) {
	path := "/api/admin/games/rhythm/double-points"
	valid := `{"doublePoints":true,"expectedVersion":1}`
	for _, test := range []struct {
		name, path, body string
		err              error
		want             int
	}{
		{"missing value", path, `{"expectedVersion":1}`, nil, 400},
		{"missing version", path, `{"doublePoints":true}`, nil, 400},
		{"wrong type", path, `{"doublePoints":"yes","expectedVersion":1}`, nil, 400},
		{"unknown game in path", "/api/admin/games/chess/double-points", valid, nil, 404},
		{"stale version", path, valid, store.ErrFlagConflict, 409},
		{"game missing from table", path, valid, store.ErrUnknownGame, 404},
		{"database failure", path, valid, errors.New("database down"), 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newAdmin(&fakeAdminStore{setErr: test.err})
			response := adminRequest(handler, http.MethodPut, test.path, "Bearer "+adminTestToken, test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestAuditRouteIsReadOnly(t *testing.T) {
	db := &fakeAdminStore{}
	handler := newAdmin(db)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/api/admin/audit", "/api/admin/audit/1"} {
			response := adminRequest(handler, method, path, "Bearer "+adminTestToken, "")
			if response.Code != http.StatusMethodNotAllowed && response.Code != http.StatusNotFound {
				t.Errorf("%s %s: status=%d", method, path, response.Code)
			}
		}
	}
	if db.calls != 0 {
		t.Fatalf("storage was reached %d times", db.calls)
	}
}

func TestAdminStoreHasNoAuditMutator(t *testing.T) {
	kind := reflect.TypeOf((*AdminStore)(nil)).Elem()
	for i := range kind.NumMethod() {
		if name := kind.Method(i).Name; regexp.MustCompile(`(?i)delete|remove|clear|purge|truncate|updateaudit|editaudit`).MatchString(name) {
			t.Errorf("AdminStore.%s looks like a way to change history", name)
		}
	}
}

// No non-test Go code may hold SQL that changes existing audit rows. The
// PostgreSQL trigger is checked separately by the opt-in integration test.
func TestNoSourceChangesAuditRows(t *testing.T) {
	mutation := regexp.MustCompile(`(?i)(update\s+admin_audit|delete\s+from\s+admin_audit|truncate\s+(table\s+)?admin_audit)`)
	inserts := 0
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if mutation.Match(contents) {
				t.Errorf("%s changes audit rows", path)
			}
			inserts += strings.Count(string(contents), "INSERT INTO admin_audit")
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if inserts != 1 {
		t.Fatalf("expected one audit insert, inside SetDoublePoints; found %d", inserts)
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	if response := adminRequest(AdminHandler(AdminConfig{}, &fakeAdminStore{}), http.MethodGet, "/health", "", ""); response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestTokenMatches(t *testing.T) {
	if !tokenMatches(adminTestToken, adminTestToken) || tokenMatches(adminTestToken+"x", adminTestToken) || tokenMatches("", adminTestToken) {
		t.Fatal("tokenMatches returned the wrong answer")
	}
}
