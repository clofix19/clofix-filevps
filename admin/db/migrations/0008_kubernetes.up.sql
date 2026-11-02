-- Encrypted kubeconfig and job payloads live inside a controller-owned snapshot.
CREATE TABLE IF NOT EXISTS cf_kubernetes_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    state JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
