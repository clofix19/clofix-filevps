CREATE TABLE IF NOT EXISTS cf_accounts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  parent_id TEXT,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS email TEXT;
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'iam';
ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS account_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS cf_iam_users_email ON cf_iam_users (lower(email)) WHERE email IS NOT NULL AND email <> '';

ALTER TABLE cf_iam_groups ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_iam_policies ADD COLUMN IF NOT EXISTS account_id TEXT;

CREATE TABLE IF NOT EXISTS cf_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES cf_iam_users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS cf_sessions_user ON cf_sessions(user_id, expires);

ALTER TABLE cf_instances ADD COLUMN IF NOT EXISTS account_id TEXT;
ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS account_id TEXT;
