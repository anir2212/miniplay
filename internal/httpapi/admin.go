package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

// AdminStore is everything the admin API can do to storage. There is no method
// that updates or deletes an audit row.
type AdminStore interface {
	FeatureFlags(context.Context) ([]store.Flag, error)
	SetDoublePoints(context.Context, store.FlagChange) (store.Flag, bool, error)
	LiveStats(context.Context, time.Time) (store.LiveStats, error)
	Flags(context.Context, int) ([]store.FlagRecord, error)
	AuditLog(context.Context, int, int64) ([]store.AuditEntry, error)
	ExplainRun(context.Context, string) (store.RunExplanation, error)
}

// AdminConfig holds the server-side settings for the admin API.
type AdminConfig struct {
	// Token is the shared local demo credential. Empty means every admin
	// request is refused: the API fails closed rather than opening up.
	Token string
	// Actor is what the audit log records. It comes from this configuration and
	// not the request, because a client could otherwise claim any name.
	Actor string
	// FlagTTL is the score service's cache limit, shown to the admin so a toggle
	// reports when it is certain to apply.
	FlagTTL time.Duration
	// Now is replaceable in tests. Nil means time.Now.
	Now func() time.Time
}

// tokenMatches hashes both values first, so the comparison takes the same time
// whatever the lengths and however many leading bytes match.
func tokenMatches(presented, expected string) bool {
	left, right := sha256.Sum256([]byte(presented)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func requireAdmin(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			failure(w, http.StatusServiceUnavailable, "admin access is not configured")
			return
		}
		presented, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !found || !tokenMatches(presented, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="miniplay-admin"`)
			failure(w, http.StatusUnauthorized, "admin token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type adminFlagRequest struct {
	DoublePoints    *bool  `json:"doublePoints"`
	ExpectedVersion *int64 `json:"expectedVersion"`
}

type adminGame struct {
	ID    game.ID    `json:"id"`
	Title string     `json:"title"`
	Flag  store.Flag `json:"flag"`
}

var gameTitles = map[game.ID]string{game.Rhythm: "Pulse Field", game.Reflex: "Comet Catch"}

func adminLimit(r *http.Request, fallback int) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		return fallback
	}
	return limit
}

// AdminHandler serves /api/admin/*. Every route except /health sits behind the
// token check, and the audit route accepts GET only.
func AdminHandler(config AdminConfig, db AdminStore) http.Handler {
	if config.Now == nil {
		config.Now = time.Now
	}
	unavailable := func(w http.ResponseWriter, action string, err error) {
		slog.Error("admin "+action, "error", err)
		failure(w, http.StatusServiceUnavailable, "admin data unavailable")
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /api/admin/games", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.FeatureFlags(r.Context())
		if err != nil {
			unavailable(w, "read flags", err)
			return
		}
		games := make([]adminGame, 0, len(rows))
		for _, flag := range rows {
			games = append(games, adminGame{ID: flag.Game, Title: gameTitles[flag.Game], Flag: flag})
		}
		respond(w, http.StatusOK, map[string]any{"games": games, "flagMaxStalenessSeconds": config.FlagTTL.Seconds()})
	})
	api.HandleFunc("PUT /api/admin/games/{game}/double-points", func(w http.ResponseWriter, r *http.Request) {
		id := game.ID(r.PathValue("game"))
		if !game.ValidID(id) {
			failure(w, http.StatusNotFound, "unknown game")
			return
		}
		var request adminFlagRequest
		if err := decode(w, r, &request); err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		if request.DoublePoints == nil || request.ExpectedVersion == nil {
			failure(w, http.StatusBadRequest, "doublePoints and expectedVersion are required")
			return
		}
		flag, changed, err := db.SetDoublePoints(r.Context(), store.FlagChange{
			Actor: config.Actor, Game: id, DoublePoints: *request.DoublePoints, ExpectedVersion: *request.ExpectedVersion,
			Details: map[string]string{"clientIp": clientIP(r)},
		})
		if errors.Is(err, store.ErrFlagConflict) {
			respond(w, http.StatusConflict, map[string]any{"error": "flag changed since it was read", "flag": flag})
			return
		}
		if errors.Is(err, store.ErrUnknownGame) {
			failure(w, http.StatusNotFound, "unknown game")
			return
		}
		if err != nil {
			unavailable(w, "set flag", err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"flag": flag, "changed": changed, "flagMaxStalenessSeconds": config.FlagTTL.Seconds()})
	})
	api.HandleFunc("GET /api/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		stats, err := db.LiveStats(r.Context(), config.Now().Add(-24*time.Hour))
		if err != nil {
			unavailable(w, "read stats", err)
			return
		}
		respond(w, http.StatusOK, stats)
	})
	api.HandleFunc("GET /api/admin/flagged-runs", func(w http.ResponseWriter, r *http.Request) {
		runs, err := db.Flags(r.Context(), adminLimit(r, 100))
		if err != nil {
			unavailable(w, "read flagged runs", err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"runs": runs})
	})
	api.HandleFunc("GET /api/admin/runs/{runId}", func(w http.ResponseWriter, r *http.Request) {
		run, err := db.ExplainRun(r.Context(), r.PathValue("runId"))
		if errors.Is(err, store.ErrRunNotFound) {
			failure(w, http.StatusNotFound, "no accepted run with that ID")
			return
		}
		if err != nil {
			unavailable(w, "explain run", err)
			return
		}
		respond(w, http.StatusOK, run)
	})
	api.HandleFunc("GET /api/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		entries, err := db.AuditLog(r.Context(), adminLimit(r, 50), before)
		if err != nil {
			unavailable(w, "read audit log", err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"entries": entries})
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("/api/admin/", requireAdmin(config.Token, api))
	return mux
}
