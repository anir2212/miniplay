package httpapi

import (
	"context"
	"net/http"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

type BoardStore interface {
	Leaderboard(context.Context, game.ID, time.Time, string, int) ([]store.Score, error)
	BestScores(context.Context, game.ID, time.Time) ([]store.Score, error)
}
type BoardCache interface {
	HasBoard(context.Context, string) (bool, error)
	Backfill(context.Context, string, []store.Score) error
	Board(context.Context, string, string) (store.Board, error)
}

func LeaderboardHandler(db BoardStore, cache BoardCache, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /api/leaderboards", func(w http.ResponseWriter, r *http.Request) {
		id := game.ID(r.URL.Query().Get("game"))
		period := store.Period(r.URL.Query().Get("period"))
		if period == "" {
			period = store.All
		}
		user := r.URL.Query().Get("user")
		if user != "" && !token.ValidUser(user) {
			failure(w, 400, "invalid user")
			return
		}
		if period == "friends" {
			if user == "" || !game.ValidID(id) {
				failure(w, 400, "friends board requires a valid user and game")
				return
			}
			rows, err := db.Leaderboard(r.Context(), id, time.Time{}, user, 0)
			if err != nil {
				failure(w, 503, "leaderboard unavailable")
				return
			}
			respond(w, 200, fromSQL(rows, user))
			return
		}
		key, since, err := store.BoardKey(id, period, now())
		if err != nil {
			failure(w, 400, "invalid game or period")
			return
		}
		result, err := completeBoard(r.Context(), db, cache, id, key, since, user)
		if err != nil {
			failure(w, 503, "leaderboard unavailable")
			return
		}
		respond(w, 200, result)
	})
	return mux
}

// completeBoard is shared by the REST and live board routes. It reads Redis
// only when the SQL backfill marker says the sorted set is complete, backfills
// a partial or missing key first, and otherwise answers from SQL.
func completeBoard(ctx context.Context, db BoardStore, cache BoardCache, id game.ID, key string, since time.Time, user string) (store.Board, error) {
	exists, cacheErr := cache.HasBoard(ctx, key)
	if cacheErr == nil && !exists {
		rows, dbErr := db.BestScores(ctx, id, since)
		if dbErr == nil {
			cacheErr = cache.Backfill(ctx, key, rows)
		} else {
			// The Redis key may contain only a recent score. Never read it
			// as a complete board when SQL backfill could not finish.
			cacheErr = dbErr
		}
	}
	if cacheErr == nil {
		result, err := cache.Board(ctx, key, user)
		if err == nil {
			return result, nil
		}
	}
	// Redis is a cache. SQL gives a correct answer during cache outages.
	rows, err := db.Leaderboard(ctx, id, since, "", 0)
	if err != nil {
		return store.Board{}, err
	}
	return fromSQL(rows, user), nil
}

func fromSQL(rows []store.Score, user string) store.Board {
	result := store.Board{Leaders: make([]store.Leader, 0, min(len(rows), 20))}
	for index, row := range rows {
		if index < 20 {
			result.Leaders = append(result.Leaders, store.Leader{User: row.User, Score: row.Score})
		}
		if row.User == user && user != "" {
			rank := int64(index + 1)
			result.MyRank = &rank
		}
	}
	return result
}
