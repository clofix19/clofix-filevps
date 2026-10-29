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
CREATE INDEX IF NOT EXISTS cf_volumes_worker ON cf_volumes(worker_id);
