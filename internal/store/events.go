package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/game"
)

type OutboxRow struct {
	ID      string
	Payload []byte
}

func (db *Postgres) Pending(ctx context.Context, limit int) ([]OutboxRow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := db.Pool.Query(ctx, `SELECT event_id,payload FROM event_outbox WHERE published_at IS NULL ORDER BY created_at,event_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OutboxRow, 0, limit)
	for rows.Next() {
		var row OutboxRow
		if err := rows.Scan(&row.ID, &row.Payload); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (db *Postgres) MarkPublished(ctx context.Context, id string) error {
	tag, err := db.Pool.Exec(ctx, `UPDATE event_outbox SET published_at=NOW() WHERE event_id=$1 AND published_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("outbox row already marked or missing")
	}
	return nil
}

// ApplyEvent commits the dedupe key and aggregate update together. Replayed
// Kafka records, including after a worker crash, cannot double count.
func (db *Postgres) ApplyEvent(ctx context.Context, event Event) (bool, error) {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO processed_events(event_id) VALUES($1) ON CONFLICT DO NOTHING`, event.ID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	day := time.Date(event.At.UTC().Year(), event.At.UTC().Month(), event.At.UTC().Day(), 0, 0, 0, 0, time.UTC)
	accepted, rejected, score := 0, 0, 0
	if event.Kind == "score.accepted" {
		accepted, score = 1, event.Score
	} else {
		rejected = 1
	}
	_, err = tx.Exec(ctx, `INSERT INTO daily_stats(game,day,accepted,rejected,score_sum) VALUES($1,$2,$3,$4,$5)
        ON CONFLICT(game,day) DO UPDATE SET accepted=daily_stats.accepted+EXCLUDED.accepted,
        rejected=daily_stats.rejected+EXCLUDED.rejected,score_sum=daily_stats.score_sum+EXCLUDED.score_sum`, event.Game, day, accepted, rejected, score)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

type DailyStat struct {
	Game        game.ID   `json:"game"`
	Day         time.Time `json:"day"`
	Accepted    int       `json:"accepted"`
	Rejected    int       `json:"rejected"`
	ScoreSum    int64     `json:"scoreSum"`
	MedianScore float64   `json:"medianScore"`
}

func (db *Postgres) Stats(ctx context.Context, limit int) ([]DailyStat, error) {
	if limit < 1 || limit > 365 {
		limit = 30
	}
	rows, err := db.Pool.Query(ctx, `SELECT d.game,d.day,d.accepted,d.rejected,d.score_sum,
      COALESCE((SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY s.score) FROM scores s
        WHERE s.game=d.game AND s.submitted_at>=d.day AND s.submitted_at<d.day+INTERVAL '1 day'),0)
      FROM daily_stats d ORDER BY d.day DESC,d.game LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DailyStat, 0)
	for rows.Next() {
		var stat DailyStat
		if err := rows.Scan(&stat.Game, &stat.Day, &stat.Accepted, &stat.Rejected, &stat.ScoreSum, &stat.MedianScore); err != nil {
			return nil, err
		}
		result = append(result, stat)
	}
	return result, rows.Err()
}

func DecodeEvent(payload []byte) (Event, error) {
	var event Event
	if len(payload) == 0 || len(payload) > 16<<10 {
		return event, errors.New("invalid event size")
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return event, err
	}
	return event, nil
}
