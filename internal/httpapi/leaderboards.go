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

// completeBoard is shared by the REST route and each live socket's initial
// frame. It reads Redis only when the SQL backfill marker says the sorted set
// is complete, backfills a partial or missing key first, and otherwise
// answers from SQL.
func completeBoard(ctx context.Context, db BoardStore, cache BoardCache, id game.ID, key string, since time.Time, user string) (store.Board, error) {
	if readyBoard(ctx, db, cache, id, key, since) == nil {
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

// completeBoardRanks is completeBoard for many players at once: the live
// broadcaster reads the shared top 20 once and every watching player's rank
// in one batch. The completeness rules are the same.
func completeBoardRanks(ctx context.Context, db BoardStore, cache LiveCache, id game.ID, key string, since time.Time, users []string) (store.Board, map[string]int64, error) {
	if readyBoard(ctx, db, cache, id, key, since) == nil {
		board, err := cache.Board(ctx, key, "")
		if err == nil {
			ranks, err := cache.Ranks(ctx, key, users)
			if err == nil {
				return board, ranks, nil
			}
		}
	}
	rows, err := db.Leaderboard(ctx, id, since, "", 0)
	if err != nil {
		return store.Board{}, nil, err
	}
	wanted := make(map[string]bool, len(users))
	for _, user := range users {
		wanted[user] = true
	}
	ranks := make(map[string]int64, len(users))
	for index, row := range rows {
		if wanted[row.User] {
			ranks[row.User] = int64(index + 1)
		}
	}
	return fromSQL(rows, ""), ranks, nil
}

// readyBoard returns nil when the Redis board can be read as complete,
// backfilling it from SQL first if needed. The Redis key may hold only a
// recent score, so it is never trusted when the backfill could not finish.
func readyBoard(ctx context.Context, db BoardStore, cache BoardCache, id game.ID, key string, since time.Time) error {
	exists, err := cache.HasBoard(ctx, key)
	if err != nil || exists {
		return err
	}
	rows, err := db.BestScores(ctx, id, since)
	if err != nil {
		return err
	}
	return cache.Backfill(ctx, key, rows)
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
