-- Account-scoped audit trail: root and IAM users see their own account activity.
ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS detail JSONB;
CREATE INDEX IF NOT EXISTS cf_audit_account_id_id ON cf_audit (account_id, id DESC);
CREATE INDEX IF NOT EXISTS cf_audit_actor_id ON cf_audit (actor_id) WHERE actor_id <> '';
