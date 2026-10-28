-- Tables used by the controller today (text ids, JSONB payloads).
-- 0001 remains the full multi-organization target model from the spec.
CREATE TABLE IF NOT EXISTS cf_tokens (
  hash TEXT PRIMARY KEY, expires_at TIMESTAMPTZ NOT NULL, used_at TIMESTAMPTZ,
  created_by TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS cf_workers (
  id TEXT PRIMARY KEY, hostname TEXT, status TEXT NOT NULL, version TEXT,
  capacity JSONB NOT NULL DEFAULT '{}', last_heartbeat TIMESTAMPTZ);
CREATE TABLE IF NOT EXISTS cf_instances (
  id TEXT PRIMARY KEY, worker_id TEXT NOT NULL, state TEXT NOT NULL, error TEXT,
  placement_method TEXT NOT NULL, spec JSONB NOT NULL, created TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS cf_ops (
  seq BIGSERIAL PRIMARY KEY, worker_id TEXT NOT NULL, op JSONB NOT NULL);
CREATE INDEX IF NOT EXISTS cf_ops_worker ON cf_ops(worker_id, seq);
CREATE TABLE IF NOT EXISTS cf_audit (
  id BIGSERIAL PRIMARY KEY, ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor TEXT, action TEXT NOT NULL, target TEXT, result TEXT);
