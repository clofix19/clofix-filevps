#!/usr/bin/env bash
# Apply license SQL migrations on the remote Postgres.
# Usage: ./deploy/license/migrate-db.sh
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
SQL="$SCRIPT_DIR/sql/001_license_state.sql"
[ -f "$SQL" ] || { echo "missing $SQL" >&2; exit 1; }

REMOTE_TMP="/tmp/clofix-license-migrations-$$"
echo "== License DB migrate → $USER@$HOST db=$DB_NAME =="

ssh_cmd "rm -rf '$REMOTE_TMP' && mkdir -p '$REMOTE_TMP'"
scp_cmd "$SQL" "$USER@$HOST:$REMOTE_TMP/001_license_state.sql"

remote_sudo "
set -e
sudo -u postgres psql -v ON_ERROR_STOP=1 -d '$DB_NAME' -f '$REMOTE_TMP/001_license_state.sql'
rm -rf '$REMOTE_TMP'
echo 'License migrations complete'
"

echo "Done."
