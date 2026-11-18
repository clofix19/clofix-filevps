#!/usr/bin/env bash
# Install PostgreSQL on a remote host, create the license DB/role, then migrate.
# Usage: ./deploy/license/install-db.sh
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
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

DB_NAME="${LICENSE_DB_NAME:-clofix_license}"
DB_USER="${LICENSE_DB_USER:-clofix}"
DB_PASSWORD="${LICENSE_DB_PASSWORD:-}"
[ -n "$DB_PASSWORD" ] || { echo 'Set LICENSE_DB_PASSWORD in deploy/license/.env' >&2; exit 2; }

echo "== License DB install → $USER@$HOST =="
ensure_remote_postgres

sql_escape() { printf "%s" "$1" | sed "s/'/''/g"; }
tmp="$(mktemp)"
DB_USER="$DB_USER" DB_PASSWORD_SQL="$(sql_escape "$DB_PASSWORD")" python3 - "$SCRIPT_DIR/sql/bootstrap-role.sql.tmpl" "$tmp" <<'PY'
import os, pathlib, sys
src, dst = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
text = src.read_text()
text = text.replace("__DB_USER__", os.environ["DB_USER"]).replace("__DB_PASSWORD__", os.environ["DB_PASSWORD_SQL"])
dst.write_text(text)
PY
scp_cmd "$tmp" "$USER@$HOST:/tmp/clofix-license-bootstrap-role.sql"
rm -f "$tmp"

remote_psql_file_as_postgres /tmp/clofix-license-bootstrap-role.sql postgres
remote_sudo "sudo -u postgres psql -tAc \"SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'\" | grep -q 1 \
  || sudo -u postgres createdb -O '${DB_USER}' '${DB_NAME}'"
remote_sudo "sudo -u postgres psql -v ON_ERROR_STOP=1 -d '${DB_NAME}' -c \"
  GRANT ALL ON SCHEMA public TO ${DB_USER};
  ALTER SCHEMA public OWNER TO ${DB_USER};
\""
remote_sudo "rm -f /tmp/clofix-license-bootstrap-role.sql"

remote_sudo "
  PG_HBA=\$(ls /etc/postgresql/*/main/pg_hba.conf 2>/dev/null | head -1)
  if [ -n \"\$PG_HBA\" ] && ! grep -q 'clofix-license-native' \"\$PG_HBA\"; then
    printf '\\n# clofix-license-native\\nhost %s %s 127.0.0.1/32 scram-sha-256\\nhost %s %s ::1/128 scram-sha-256\\n' \
      '${DB_NAME}' '${DB_USER}' '${DB_NAME}' '${DB_USER}' >> \"\$PG_HBA\"
    systemctl reload postgresql || systemctl restart postgresql
  fi
"

echo "== Running migrations =="
"$SCRIPT_DIR/migrate-db.sh"

echo
echo "License Postgres ready on $HOST"
echo "  DB:   ${DB_NAME}"
echo "  User: ${DB_USER}"
echo "Next: ./deploy/license/deploy-native.sh"
