-- Retention index: clients retain 90d (query window); admin 180d; rows older than 180d are purged.
CREATE INDEX IF NOT EXISTS cf_audit_ts ON cf_audit (ts DESC);
CREATE INDEX IF NOT EXISTS cf_audit_action_ts ON cf_audit (action, ts DESC);
