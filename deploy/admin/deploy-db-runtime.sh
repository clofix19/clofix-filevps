#!/usr/bin/env bash
# Deploy Postgres as the admin "database runtime" on a remote host (systemd).
# Installs packages, creates role/DB, migrates schema, ensures postgresql is enabled.
# Usage: ./deploy/admin/deploy-db-runtime.sh
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

if [ -f "$SCRIPT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/.env"
  set +a
fi

echo "== Database runtime (Postgres) =="
"$SCRIPT_DIR/install-db.sh"

# shellcheck disable=SC1091
. "$SCRIPT_DIR/../_lib/ssh.sh"
load_db_remote_env
ssh_prep

remote_sudo '
set -e
systemctl enable postgresql >/dev/null 2>&1 || systemctl enable postgresql@* >/dev/null 2>&1 || true
systemctl restart postgresql || systemctl start postgresql
systemctl --no-pager --full is-active postgresql || systemctl --no-pager --full is-active "postgresql@*" || true
pg_isready -h 127.0.0.1 || sudo -u postgres pg_isready
echo "Postgres runtime active"
'

echo
echo "Database runtime ready."
echo "  Next: ./deploy/admin/deploy-s3pool.sh && ./deploy/admin/deploy-native.sh"
