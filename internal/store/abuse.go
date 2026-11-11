package store

import (
	"context"
	"encoding/json"
	"time"

	"miniplay/internal/game"
)

type ScoredRun struct {
	Score
	Events []game.Tap `json:"events"`
}

func (db *Postgres) RecentRuns(ctx context.Context, since time.Time) ([]ScoredRun, error) {
	rows, err := db.Pool.Query(ctx, `SELECT run_id,user_id,game,score,base_score,multiplier,flag_version,duration_ms,submitted_at,events
      FROM scores WHERE submitted_at >= $1 ORDER BY submitted_at DESC LIMIT 50000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ScoredRun, 0)
	for rows.Next() {
		var run ScoredRun
		var encoded []byte
		if err := rows.Scan(&run.RunID, &run.User, &run.Game, &run.Score.Score, &run.BaseScore, &run.Multiplier, &run.FlagVersion, &run.DurationMS, &run.SubmittedAt, &encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &run.Events); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

type FlagRecord struct {
	RunID          string             `json:"runId"`
	User           string             `json:"user"`
	Game           game.ID            `json:"game"`
	IsolationScore float64            `json:"isolationScore"`
	ZScore         float64            `json:"zScore"`
	Reasons        []string           `json:"reasons"`
	Evidence       map[string]float64 `json:"evidence"`
	FlaggedAt      time.Time          `json:"flaggedAt"`
	// Score, BaseScore and Multiplier come from the scores table on reads;
	// SaveFlag ignores them.
	Score      int `json:"score"`
	BaseScore  int `json:"baseScore"`
	Multiplier int `json:"multiplier"`
}

func (db *Postgres) SaveFlag(ctx context.Context, flag FlagRecord) error {
	reasons, _ := json.Marshal(flag.Reasons)
	evidence, _ := json.Marshal(flag.Evidence)
	_, err := db.Pool.Exec(ctx, `INSERT INTO flagged_runs(run_id,user_id,game,isolation_score,z_score,reasons,evidence)
      VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id) DO UPDATE SET
      isolation_score=EXCLUDED.isolation_score,z_score=EXCLUDED.z_score,reasons=EXCLUDED.reasons,
      evidence=EXCLUDED.evidence,flagged_at=NOW()`, flag.RunID, flag.User, flag.Game, flag.IsolationScore, flag.ZScore, reasons, evidence)
	return err
}

// Flags lists runs the offline scanner raised for review, newest first. These
// are review signals, not cheating verdicts; see the abuse evaluation limits.
func (db *Postgres) Flags(ctx context.Context, limit int) ([]FlagRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := db.Pool.Query(ctx, `SELECT f.run_id,f.user_id,f.game,f.isolation_score,f.z_score,f.reasons,f.evidence,f.flagged_at,s.score,s.base_score,s.multiplier
      FROM flagged_runs f JOIN scores s ON s.run_id=f.run_id ORDER BY f.flagged_at DESC,f.run_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FlagRecord, 0)
	for rows.Next() {
		var flag FlagRecord
		var reasons, evidence []byte
		if err := rows.Scan(&flag.RunID, &flag.User, &flag.Game, &flag.IsolationScore, &flag.ZScore, &reasons, &evidence, &flag.FlaggedAt, &flag.Score, &flag.BaseScore, &flag.Multiplier); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(reasons, &flag.Reasons); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &flag.Evidence); err != nil {
			return nil, err
		}
		result = append(result, flag)
	}
	return result, rows.Err()
}
