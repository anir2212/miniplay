CREATE TABLE IF NOT EXISTS flagged_runs (
    run_id TEXT PRIMARY KEY REFERENCES scores(run_id) ON DELETE CASCADE,
    user_id VARCHAR(40) NOT NULL,
    game TEXT NOT NULL CHECK (game IN ('rhythm','reflex')),
    isolation_score DOUBLE PRECISION NOT NULL,
    z_score DOUBLE PRECISION NOT NULL,
    reasons JSONB NOT NULL,
    evidence JSONB NOT NULL,
    flagged_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS flagged_runs_recent ON flagged_runs (flagged_at DESC);
