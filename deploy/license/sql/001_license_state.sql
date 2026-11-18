-- CloFix license-server Postgres schema (idempotent).
CREATE TABLE IF NOT EXISTS license_state (
  id smallint PRIMARY KEY CHECK (id = 1),
  plans jsonb NOT NULL DEFAULT '{}'::jsonb,
  licenses jsonb NOT NULL DEFAULT '{}'::jsonb,
  customers jsonb NOT NULL DEFAULT '{}'::jsonb,
  admins jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS admins jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS customers jsonb NOT NULL DEFAULT '{}'::jsonb;
INSERT INTO license_state (id, plans, licenses, customers, admins)
VALUES (1, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS schema_migrations (
  id TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations (id) VALUES ('001_license_state')
ON CONFLICT (id) DO NOTHING;
