#!/usr/bin/env bash
# Apply admin/db/migrations/*.up.sql on the remote Postgres (idempotent via schema_migrations).
# Existing DBs (created before schema_migrations) are baselined, then only new migrations run.
# Usage: ./deploy/admin/migrate-db.sh
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
# shellcheck disable=SC1091
. "$SCRIPT_DIR/../_lib/ssh.sh"

if [ -f "$SCRIPT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/.env"
  set +a
fi

load_db_remote_env
ssh_prep

DB_NAME="${DB_NAME:-clofix}"
MIG_DIR="$ROOT/admin/db/migrations"
[ -d "$MIG_DIR" ] || { echo "missing $MIG_DIR" >&2; exit 1; }

REMOTE_TMP="/tmp/clofix-admin-migrations-$$"
echo "== Admin DB migrate → $USER@$HOST db=$DB_NAME =="

ssh_cmd "rm -rf '$REMOTE_TMP' && mkdir -p '$REMOTE_TMP'"
rsync_cmd "$MIG_DIR/" "$USER@$HOST:$REMOTE_TMP/"
# Also ship deploy/admin/sql/*.sql for operators / parity checks
if [ -d "$SCRIPT_DIR/sql" ]; then
  rsync_cmd --exclude 'bootstrap-role.sql.tmpl' "$SCRIPT_DIR/sql/" "$USER@$HOST:$REMOTE_TMP/deploy-sql/" || true
fi

remote_sudo "sudo -u postgres psql -v ON_ERROR_STOP=1 -d '$DB_NAME' -c \"
CREATE TABLE IF NOT EXISTS schema_migrations (
  id TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
\""

# Baseline: if controller tables already exist but schema_migrations is empty,
# mark older migrations applied so we only run new idempotent updates (0011+).
remote_sudo "
set -e
count=\$(sudo -u postgres psql -tAc \"SELECT count(*) FROM schema_migrations\" -d '$DB_NAME' | tr -d '[:space:]')
has_workers=\$(sudo -u postgres psql -tAc \"SELECT to_regclass('public.cf_workers') IS NOT NULL\" -d '$DB_NAME' | tr -d '[:space:]')
if [ \"\$count\" = \"0\" ] && [ \"\$has_workers\" = \"t\" ]; then
  echo 'Baseline existing DB: mark pre-0011 migrations applied'
  for f in \$(ls -1 '$REMOTE_TMP'/*.up.sql 2>/dev/null | sort); do
    id=\$(basename \"\$f\")
    case \"\$id\" in
      0011_*|0012_*|0013_*|0014_*|0015_*) continue ;;
    esac
    sudo -u postgres psql -v ON_ERROR_STOP=1 -d '$DB_NAME' -c \"INSERT INTO schema_migrations(id) VALUES ('\$id') ON CONFLICT DO NOTHING\"
    echo \"BASELINE \$id\"
  done
fi
"

# Apply each pending *.up.sql in sorted order.
remote_sudo "
set -e
for f in \$(ls -1 '$REMOTE_TMP'/*.up.sql 2>/dev/null | sort); do
  id=\$(basename \"\$f\")
  applied=\$(sudo -u postgres psql -tAc \"SELECT 1 FROM schema_migrations WHERE id='\$id'\" -d '$DB_NAME' || true)
  applied=\$(echo \"\$applied\" | tr -d '[:space:]')
  if [ \"\$applied\" = \"1\" ]; then
    echo \"SKIP \$id\"
    continue
  fi
  echo \"APPLY \$id\"
  sudo -u postgres psql -v ON_ERROR_STOP=1 -d '$DB_NAME' -f \"\$f\"
  sudo -u postgres psql -v ON_ERROR_STOP=1 -d '$DB_NAME' -c \"INSERT INTO schema_migrations(id) VALUES ('\$id') ON CONFLICT DO NOTHING\"
done
rm -rf '$REMOTE_TMP'
echo 'Migrations complete'
"

echo "Done. Existing schema updated via SQL migrations."
