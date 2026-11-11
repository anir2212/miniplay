package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"miniplay/internal/game"
)

var (
	ErrUnknownGame  = errors.New("unknown game")
	ErrFlagConflict = errors.New("flag changed since it was read")
	ErrRunNotFound  = errors.New("run not found")
)

// Flag is one row of feature_flags. Version rises by one on each change.
type Flag struct {
	Game         game.ID   `json:"game"`
	DoublePoints bool      `json:"doublePoints"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Multiplier is the factor this flag state applies to an accepted base score.
func (f Flag) Multiplier() int {
	if f.DoublePoints {
		return 2
	}
	return 1
}

const flagColumns = `game,double_points,version,updated_at`

func scanFlag(row pgx.Row) (Flag, error) {
	var flag Flag
	err := row.Scan(&flag.Game, &flag.DoublePoints, &flag.Version, &flag.UpdatedAt)
	return flag, err
}

// FeatureFlag reads one game's current flag from the table.
func (db *Postgres) FeatureFlag(ctx context.Context, id game.ID) (Flag, error) {
	flag, err := scanFlag(db.Pool.QueryRow(ctx, `SELECT `+flagColumns+` FROM feature_flags WHERE game=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, ErrUnknownGame
	}
	return flag, err
}

// FeatureFlags lists every game's current flag.
func (db *Postgres) FeatureFlags(ctx context.Context) ([]Flag, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+flagColumns+` FROM feature_flags ORDER BY game`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Flag, 0, 2)
	for rows.Next() {
		flag, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, flag)
	}
	return result, rows.Err()
}

// FlagChange asks to set a game's double-points flag. ExpectedVersion is the
// version the admin was looking at, so two admins cannot overwrite each other
// unknowingly. Actor comes from the server's configuration, never the request.
type FlagChange struct {
	Actor           string
	Game            game.ID
	DoublePoints    bool
	ExpectedVersion int64
	Details         map[string]string
}

type flagAuditValue struct {
	DoublePoints bool  `json:"doublePoints"`
	Version      int64 `json:"version"`
}

// SetDoublePoints changes the flag and writes its audit row in one transaction:
// either both are stored or neither is. Setting the value it already holds
// changes nothing and writes no audit row (changed=false).
func (db *Postgres) SetDoublePoints(ctx context.Context, change FlagChange) (flag Flag, changed bool, err error) {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Flag{}, false, err
	}
	defer tx.Rollback(ctx)
	before, err := scanFlag(tx.QueryRow(ctx, `SELECT `+flagColumns+` FROM feature_flags WHERE game=$1 FOR UPDATE`, change.Game))
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, false, ErrUnknownGame
	}
	if err != nil {
		return Flag{}, false, err
	}
	if before.Version != change.ExpectedVersion {
		return before, false, ErrFlagConflict
	}
	if before.DoublePoints == change.DoublePoints {
		return before, false, nil
	}
	after, err := scanFlag(tx.QueryRow(ctx, `UPDATE feature_flags SET double_points=$2,version=version+1,updated_at=NOW()
      WHERE game=$1 RETURNING `+flagColumns, change.Game, change.DoublePoints))
	if err != nil {
		return Flag{}, false, err
	}
	beforeValue, _ := json.Marshal(flagAuditValue{before.DoublePoints, before.Version})
	afterValue, _ := json.Marshal(flagAuditValue{after.DoublePoints, after.Version})
	details, _ := json.Marshal(change.Details)
	if change.Details == nil {
		details = []byte(`{}`)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_audit(actor,action,game,before_value,after_value,details) VALUES($1,$2,$3,$4,$5,$6)`,
		change.Actor, AuditSetDoublePoints, change.Game, beforeValue, afterValue, details); err != nil {
		return Flag{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Flag{}, false, err
	}
	return after, true, nil
}

// AuditSetDoublePoints is the action recorded for a double-points change.
const AuditSetDoublePoints = "feature_flag.double_points.set"

// AuditEntry is one row of the append-only admin_audit table. This package has
// an insert (inside SetDoublePoints) and reads, and no update or delete path;
// the table's trigger also refuses them.
type AuditEntry struct {
	ID      int64             `json:"id"`
	Actor   string            `json:"actor"`
	Action  string            `json:"action"`
	Game    game.ID           `json:"game"`
	Before  json.RawMessage   `json:"before"`
	After   json.RawMessage   `json:"after"`
	Details map[string]string `json:"details"`
	At      time.Time         `json:"at"`
}

const auditColumns = `id,actor,action,COALESCE(game,''),before_value,after_value,details,at`

func scanAudit(row pgx.Row) (AuditEntry, error) {
	var entry AuditEntry
	var details []byte
	if err := row.Scan(&entry.ID, &entry.Actor, &entry.Action, &entry.Game, &entry.Before, &entry.After, &details, &entry.At); err != nil {
		return AuditEntry{}, err
	}
	if err := json.Unmarshal(details, &entry.Details); err != nil {
		return AuditEntry{}, err
	}
	return entry, nil
}

// AuditLog reads audit rows newest first. A beforeID above zero pages backward.
func (db *Postgres) AuditLog(ctx context.Context, limit int, beforeID int64) ([]AuditEntry, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := db.Pool.Query(ctx, `SELECT `+auditColumns+` FROM admin_audit WHERE ($1::bigint<=0 OR id<$1) ORDER BY id DESC LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AuditEntry, 0)
	for rows.Next() {
		entry, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// GameLiveStats is read directly from the scores and rejections tables, so it
// does not depend on Kafka or the stats worker.
type GameLiveStats struct {
	Game           game.ID    `json:"game"`
	Accepted       int        `json:"accepted"`
	Rejected       int        `json:"rejected"`
	Doubled        int        `json:"doubled"`
	Players        int        `json:"players"`
	LastAcceptedAt *time.Time `json:"lastAcceptedAt"`
}

// LiveStats separates its sources. Games and the outbox figures are direct SQL
// reads; Aggregates comes from daily_stats, which only moves when the outbox
// publisher, Kafka and the stats worker are running.
type LiveStats struct {
	GeneratedAt     time.Time       `json:"generatedAt"`
	WindowStart     time.Time       `json:"windowStart"`
	Games           []GameLiveStats `json:"games"`
	OutboxPending   int             `json:"outboxPending"`
	OldestPendingAt *time.Time      `json:"oldestPendingAt"`
	Aggregates      []DailyStat     `json:"aggregates"`
}

func (db *Postgres) LiveStats(ctx context.Context, since time.Time) (LiveStats, error) {
	result := LiveStats{WindowStart: since, Games: make([]GameLiveStats, 0, 2)}
	rows, err := db.Pool.Query(ctx, `SELECT f.game,s.accepted,s.doubled,s.players,s.last_at,
        (SELECT COUNT(*) FROM rejections r WHERE r.game=f.game AND r.rejected_at>=$1)
      FROM feature_flags f CROSS JOIN LATERAL (
        SELECT COUNT(*) AS accepted,COUNT(*) FILTER (WHERE multiplier>1) AS doubled,
          COUNT(DISTINCT user_id) AS players,MAX(submitted_at) AS last_at
        FROM scores WHERE game=f.game AND submitted_at>=$1) s
      ORDER BY f.game`, since)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var stat GameLiveStats
		if err := rows.Scan(&stat.Game, &stat.Accepted, &stat.Doubled, &stat.Players, &stat.LastAcceptedAt, &stat.Rejected); err != nil {
			return result, err
		}
		result.Games = append(result.Games, stat)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	err = db.Pool.QueryRow(ctx, `SELECT COUNT(*),MIN(created_at),NOW() FROM event_outbox WHERE published_at IS NULL`).
		Scan(&result.OutboxPending, &result.OldestPendingAt, &result.GeneratedAt)
	if err != nil {
		return result, err
	}
	result.Aggregates, err = db.Stats(ctx, 14)
	return result, err
}

// RunExplanation shows how one stored score came about. FlagChange is the audit
// row that produced the recorded flag version; it is nil for version 1 (the
// flag's initial, off state) and for version 0 (a row older than flag versions).
type RunExplanation struct {
	RunID       string      `json:"runId"`
	User        string      `json:"user"`
	Game        game.ID     `json:"game"`
	Score       int         `json:"score"`
	BaseScore   int         `json:"baseScore"`
	Multiplier  int         `json:"multiplier"`
	FlagVersion int64       `json:"flagVersion"`
	DurationMS  int         `json:"durationMs"`
	SubmittedAt time.Time   `json:"submittedAt"`
	FlagChange  *AuditEntry `json:"flagChange"`
}

func (db *Postgres) ExplainRun(ctx context.Context, runID string) (RunExplanation, error) {
	var run RunExplanation
	err := db.Pool.QueryRow(ctx, `SELECT run_id,user_id,game,score,base_score,multiplier,flag_version,duration_ms,submitted_at
      FROM scores WHERE run_id=$1`, runID).Scan(&run.RunID, &run.User, &run.Game, &run.Score, &run.BaseScore, &run.Multiplier, &run.FlagVersion, &run.DurationMS, &run.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, ErrRunNotFound
	}
	if err != nil {
		return run, err
	}
	entry, err := scanAudit(db.Pool.QueryRow(ctx, `SELECT `+auditColumns+` FROM admin_audit
      WHERE game=$1 AND action=$2 AND (after_value->>'version')::bigint=$3 ORDER BY id LIMIT 1`, run.Game, AuditSetDoublePoints, run.FlagVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return run, nil
	}
	if err != nil {
		return run, err
	}
	run.FlagChange = &entry
	return run, nil
}
