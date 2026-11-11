-- M6: a versioned double-points flag, explainable scores and an append-only
-- audit log. Before this migration nothing wrote to feature_flags or
-- admin_audit, so no application path could have changed either table.

ALTER TABLE feature_flags ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1);

-- A stored score is always base_score * multiplier. flag_version 0 marks a row
-- written before flag versions existed; such rows count as multiplier 1.
ALTER TABLE scores ADD COLUMN base_score INTEGER;
ALTER TABLE scores ADD COLUMN multiplier SMALLINT;
ALTER TABLE scores ADD COLUMN flag_version BIGINT;
UPDATE scores SET base_score = score, multiplier = 1, flag_version = 0;
ALTER TABLE scores ALTER COLUMN base_score SET NOT NULL;
ALTER TABLE scores ALTER COLUMN multiplier SET NOT NULL;
ALTER TABLE scores ALTER COLUMN flag_version SET NOT NULL;
ALTER TABLE scores ADD CONSTRAINT scores_base_score_valid CHECK (base_score >= 0);
ALTER TABLE scores ADD CONSTRAINT scores_multiplier_valid CHECK (multiplier IN (1, 2));
ALTER TABLE scores ADD CONSTRAINT scores_flag_version_valid CHECK (flag_version >= 0);
ALTER TABLE scores ADD CONSTRAINT scores_explainable CHECK (score = base_score * multiplier);

ALTER TABLE admin_audit ADD COLUMN before_value JSONB NOT NULL DEFAULT '{}';
ALTER TABLE admin_audit ADD COLUMN after_value JSONB NOT NULL DEFAULT '{}';
ALTER TABLE admin_audit ALTER COLUMN before_value DROP DEFAULT;
ALTER TABLE admin_audit ALTER COLUMN after_value DROP DEFAULT;
ALTER TABLE admin_audit ALTER COLUMN details SET DEFAULT '{}';
ALTER TABLE admin_audit ADD CONSTRAINT admin_audit_actor_valid CHECK (length(actor) BETWEEN 1 AND 80);
CREATE INDEX admin_audit_game_time ON admin_audit (game, at DESC);

-- Statement-level, so an UPDATE or DELETE matching no rows is refused as well.
-- A table owner can still disable the trigger; see the README security notes.
CREATE FUNCTION admin_audit_refuse_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit is append-only: % refused', TG_OP USING ERRCODE = 'integrity_constraint_violation';
END;
$$;
CREATE TRIGGER admin_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON admin_audit
    FOR EACH STATEMENT EXECUTE FUNCTION admin_audit_refuse_change();
