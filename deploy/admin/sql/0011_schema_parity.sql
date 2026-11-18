-- Bring existing controller DBs in line with EnsureSchema() / current store queries.
-- Safe to re-run: CREATE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS cf_workers (
  id TEXT PRIMARY KEY, hostname TEXT, status TEXT NOT NULL, version TEXT,
  capacity JSONB NOT NULL DEFAULT '{}', last_heartbeat TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS cf_instances (
  id TEXT PRIMARY KEY, worker_id TEXT NOT NULL, state TEXT NOT NULL, error TEXT,
  placement_method TEXT NOT NULL, spec JSONB NOT NULL, created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_volumes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  worker_id TEXT NOT NULL,
  state TEXT NOT NULL,
  instance_id TEXT,
  guest_path TEXT,
  usage_bytes BIGINT NOT NULL DEFAULT 0,
  error TEXT,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_iam_users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL,
  status TEXT NOT NULL,
  group_ids JSONB NOT NULL DEFAULT '[]',
  policy_ids JSONB NOT NULL DEFAULT '[]',
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_iam_groups (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  policy_ids JSONB NOT NULL DEFAULT '[]',
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_iam_policies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  permissions JSONB NOT NULL DEFAULT '[]',
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_iam_access_keys (
  access_key_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES cf_iam_users(id) ON DELETE CASCADE,
  secret_hash TEXT NOT NULL,
  active BOOLEAN NOT NULL DEFAULT true,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cf_iam_keys_user ON cf_iam_access_keys(user_id, created);
CREATE TABLE IF NOT EXISTS cf_accounts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  parent_id TEXT,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES cf_iam_users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS cf_sessions_user ON cf_sessions(user_id, expires);
CREATE TABLE IF NOT EXISTS cf_managed_database_state (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  state JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_kubernetes_state (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  state JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cf_metrics_samples (
  id BIGSERIAL PRIMARY KEY,
  series TEXT NOT NULL,
  at TIMESTAMPTZ NOT NULL,
  sample JSONB NOT NULL,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cf_metrics_samples_series_at ON cf_metrics_samples(series, at DESC);
CREATE INDEX IF NOT EXISTS cf_metrics_samples_at ON cf_metrics_samples(at);

ALTER TABLE cf_accounts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE cf_workers ADD COLUMN IF NOT EXISTS installation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS delete_protection BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS limit_mb BIGINT NOT NULL DEFAULT 0;
ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_instances ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS email TEXT;
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'iam';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS mfa_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS mfa_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS allow_ips JSONB NOT NULL DEFAULT '[]';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS deny_ips JSONB NOT NULL DEFAULT '[]';
ALTER TABLE cf_iam_groups ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_iam_policies ADD COLUMN IF NOT EXISTS account_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS cf_iam_users_email
  ON cf_iam_users (lower(email)) WHERE email IS NOT NULL AND email <> '';
