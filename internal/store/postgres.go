package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"miniplay/internal/game"
)

var ErrDuplicate = errors.New("run already submitted")

type Score struct {
	RunID       string    `json:"runId"`
	User        string    `json:"user"`
	Game        game.ID   `json:"game"`
	Score       int       `json:"score"`
	DurationMS  int       `json:"durationMs"`
	SubmittedAt time.Time `json:"submittedAt"`
}

type Event struct {
	ID     string    `json:"eventId"`
	Kind   string    `json:"kind"`
	RunID  string    `json:"runId,omitempty"`
	User   string    `json:"user,omitempty"`
	Game   game.ID   `json:"game,omitempty"`
	Score  int       `json:"score,omitempty"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

type Postgres struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{Pool: pool}, nil
}

// RecordAttempt consumes a run ID and writes the outcome plus its outbox event
// in one SQL transaction. A failed transaction leaves the token retryable.
func (db *Postgres) RecordAttempt(ctx context.Context, row Score, taps []game.Tap, reason string) (Score, error) {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Score{}, err
	}
	defer tx.Rollback(ctx)
	outcome := "accepted"
	if reason != "" {
		outcome = "rejected"
	}
	_, err = tx.Exec(ctx, `INSERT INTO run_attempts(run_id,user_id,game,outcome) VALUES($1,$2,$3,$4)`, row.RunID, row.User, row.Game, outcome)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Score{}, ErrDuplicate
		}
		return Score{}, err
	}
	event := Event{RunID: row.RunID, User: row.User, Game: row.Game, Score: row.Score, Reason: reason, At: time.Now().UTC()}
	if reason == "" {
		encoded, err := json.Marshal(taps)
		if err != nil {
			return Score{}, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO scores(run_id,user_id,game,score,duration_ms,events)
            VALUES($1,$2,$3,$4,$5,$6) RETURNING submitted_at`, row.RunID, row.User, row.Game, row.Score, row.DurationMS, encoded).Scan(&row.SubmittedAt)
		if err != nil {
			return Score{}, err
		}
		event.ID, event.Kind, event.At = "accepted:"+row.RunID, "score.accepted", row.SubmittedAt
	} else {
		event.ID, event.Kind = "rejected:"+row.RunID, "score.rejected"
		_, err = tx.Exec(ctx, `INSERT INTO rejections(event_id,run_id,user_id,game,reason,rejected_at) VALUES($1,$2,$3,$4,$5,$6)`, event.ID, event.RunID, event.User, event.Game, reason, event.At)
		if err != nil {
			return Score{}, err
		}
	}
	payload, _ := json.Marshal(event)
	if _, err = tx.Exec(ctx, `INSERT INTO event_outbox(event_id,payload) VALUES($1,$2)`, event.ID, payload); err != nil {
		return Score{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Score{}, err
	}
	return row, nil
}

func (db *Postgres) Leaderboard(ctx context.Context, id game.ID, since time.Time, friendsOf string, limit int) ([]Score, error) {
	if limit < 0 {
		limit = 20
	}
	rows, err := db.Pool.Query(ctx, `SELECT DISTINCT ON (user_id) run_id,user_id,game,score,duration_ms,submitted_at
      FROM scores WHERE game=$1 AND submitted_at >= $2 AND ($3='' OR user_id=$3 OR user_id IN (SELECT friend_id FROM friends WHERE user_id=$3))
      ORDER BY user_id, score DESC, submitted_at ASC`, id, since, friendsOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := make([]Score, 0)
	for rows.Next() {
		var row Score
		if err := rows.Scan(&row.RunID, &row.User, &row.Game, &row.Score, &row.DurationMS, &row.SubmittedAt); err != nil {
			return nil, err
		}
		best = append(best, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortScores(best)
	if limit > 0 && len(best) > limit {
		best = best[:limit]
	}
	return best, nil
}

func (db *Postgres) BestScores(ctx context.Context, id game.ID, since time.Time) ([]Score, error) {
	return db.Leaderboard(ctx, id, since, "", 0)
}

func (db *Postgres) DoublePoints(ctx context.Context, id game.ID) (bool, error) {
	var enabled bool
	err := db.Pool.QueryRow(ctx, `SELECT double_points FROM feature_flags WHERE game=$1`, id).Scan(&enabled)
	return enabled, err
}
