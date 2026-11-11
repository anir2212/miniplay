package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"miniplay/internal/game"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

type AttemptStore interface {
	RecordAttempt(context.Context, store.Score, []game.Tap, string) (store.Score, error)
}

// FlagReader returns a game's double-points flag. The score service uses a
// flags.Cache, whose answer is at most one TTL behind the table.
type FlagReader interface {
	Current(context.Context, game.ID) (store.Flag, error)
}
type ScoreCache interface {
	Limiter
	UpdateBoards(context.Context, store.Score) error
}

type Submission struct {
	Token      string     `json:"token"`
	Events     []game.Tap `json:"events"`
	Score      int        `json:"score"`
	DurationMS int        `json:"durationMs"`
}

func validate(claims token.Claims, request Submission, nowMS int64) (int, string) {
	if !game.PlausibleDuration(request.DurationMS, claims.IssuedAt, nowMS) {
		return 0, "implausible duration"
	}
	computed, err := game.Replay(claims.Game, claims.Seed, request.Events)
	if err != nil {
		return 0, "invalid input events"
	}
	if computed != request.Score {
		return 0, "score mismatch"
	}
	return computed, ""
}

// ScoreHandler validates a run, then applies the double-points flag. The rule:
// replay always checks the client's claim against the base score, and the flag
// state this service holds when it accepts the submission decides the
// multiplier. The state when the run was signed and any state during the run do
// not count. The row records the base score, multiplier and flag version used.
func ScoreHandler(signer *token.Signer, db AttemptStore, flags FlagReader, cache ScoreCache, clock func() int64) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("POST /api/scores", func(w http.ResponseWriter, r *http.Request) {
		var request Submission
		if err := decode(w, r, &request); err != nil {
			failure(w, 400, err.Error())
			return
		}
		ip := clientIP(r)
		allowed, err := cache.Allow(r.Context(), "score-ip:"+ip, 30, 1)
		if err != nil {
			failure(w, 503, "rate limiter unavailable")
			return
		}
		if !allowed {
			failure(w, 429, "rate limit exceeded")
			return
		}
		claims, err := signer.Verify(request.Token)
		if err != nil {
			failure(w, 401, "invalid or expired run token")
			return
		}
		allowed, err = cache.Allow(r.Context(), "score-user:"+claims.User, 12, 0.2)
		if err != nil {
			failure(w, 503, "rate limiter unavailable")
			return
		}
		if !allowed {
			failure(w, 429, "rate limit exceeded")
			return
		}
		base, reason := validate(claims, request, clock())
		row := store.Score{RunID: claims.RunID, User: claims.User, Game: claims.Game, DurationMS: request.DurationMS}
		if reason == "" {
			flag, err := flags.Current(r.Context(), claims.Game)
			if err != nil {
				slog.Error("read double-points flag; the run token stays usable", "error", err)
				failure(w, 503, "score service unavailable")
				return
			}
			row.BaseScore, row.Multiplier, row.FlagVersion = base, flag.Multiplier(), flag.Version
			row.Score = base * row.Multiplier
		}
		saved, err := db.RecordAttempt(r.Context(), row, request.Events, reason)
		if errors.Is(err, store.ErrDuplicate) {
			failure(w, 409, "run already submitted")
			return
		}
		if err != nil {
			slog.Error("record attempt", "error", err)
			failure(w, 503, "score service unavailable")
			return
		}
		if reason != "" {
			failure(w, 422, reason)
			return
		}
		if err := cache.UpdateBoards(r.Context(), saved); err != nil {
			slog.Error("leaderboard cache update; PostgreSQL holds accepted score", "error", err)
		}
		respond(w, 200, saved)
	})
	return mux
}
