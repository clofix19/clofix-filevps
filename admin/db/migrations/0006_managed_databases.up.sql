-- Encrypted payloads/keys live inside a controller-owned snapshot. One controller
-- serializes mutations and scheduler reservations; worker results are job-fenced.
CREATE TABLE IF NOT EXISTS cf_managed_database_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    state JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
