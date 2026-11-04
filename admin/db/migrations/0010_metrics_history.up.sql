-- Durable chart / usage samples for Home, FileVPS, Workers, Docker, LB, WAF.
CREATE TABLE IF NOT EXISTS cf_metrics_samples (
  id BIGSERIAL PRIMARY KEY,
  series TEXT NOT NULL,
  at TIMESTAMPTZ NOT NULL,
  sample JSONB NOT NULL,
  created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cf_metrics_samples_series_at ON cf_metrics_samples(series, at DESC);
CREATE INDEX IF NOT EXISTS cf_metrics_samples_at ON cf_metrics_samples(at);
