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
