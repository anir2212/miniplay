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
	DoublePoints(context.Context, game.ID) (bool, error)
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

func ScoreHandler(signer *token.Signer, db AttemptStore, cache ScoreCache, clock func() int64) http.Handler {
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
		doubled, err := db.DoublePoints(r.Context(), claims.Game)
		if err != nil {
			failure(w, 503, "score service unavailable")
			return
		}
		computed, reason := validate(claims, request, clock())
		if reason == "" && doubled {
			computed *= 2
		}
		row := store.Score{RunID: claims.RunID, User: claims.User, Game: claims.Game, Score: computed, DurationMS: request.DurationMS}
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
