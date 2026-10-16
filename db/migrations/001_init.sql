CREATE TABLE IF NOT EXISTS scores (
    run_id TEXT PRIMARY KEY,
    user_id VARCHAR(40) NOT NULL,
    game TEXT NOT NULL CHECK (game IN ('rhythm', 'reflex')),
    score INTEGER NOT NULL CHECK (score >= 0),
    duration_ms INTEGER NOT NULL CHECK (duration_ms BETWEEN 9500 AND 12000),
    events JSONB NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS scores_game_time ON scores (game, submitted_at DESC);
CREATE INDEX IF NOT EXISTS scores_game_score ON scores (game, score DESC);

CREATE TABLE IF NOT EXISTS run_attempts (
    run_id TEXT PRIMARY KEY,
    user_id VARCHAR(40) NOT NULL,
    game TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('accepted','rejected')),
    at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rejections (
    event_id TEXT PRIMARY KEY,
    run_id TEXT,
    user_id VARCHAR(40),
    game TEXT,
    reason TEXT NOT NULL,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS event_outbox (
    event_id TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS event_outbox_pending ON event_outbox (created_at) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS daily_stats (
    game TEXT NOT NULL,
    day DATE NOT NULL,
    accepted INTEGER NOT NULL DEFAULT 0,
    rejected INTEGER NOT NULL DEFAULT 0,
    score_sum BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (game, day)
);

CREATE TABLE IF NOT EXISTS friends (
    user_id VARCHAR(40) NOT NULL,
    friend_id VARCHAR(40) NOT NULL,
    PRIMARY KEY (user_id, friend_id),
    CHECK (user_id <> friend_id)
);

CREATE TABLE IF NOT EXISTS feature_flags (
    game TEXT PRIMARY KEY CHECK (game IN ('rhythm', 'reflex')),
    double_points BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO feature_flags(game) VALUES ('rhythm'), ('reflex') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS admin_audit (
    id BIGSERIAL PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    game TEXT,
    details JSONB NOT NULL,
    at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
