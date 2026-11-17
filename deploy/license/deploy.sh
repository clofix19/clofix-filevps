#!/usr/bin/env bash
# Deploy CloFix license server to a remote host (step 1).
# Usage: ./deploy/license/deploy.sh
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

load_remote_env
REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-license}"
ADMIN_USER="${LICENSE_ADMIN_USER:-}"
ADMIN_PASS="${LICENSE_ADMIN_PASSWORD:-}"
[ -n "$ADMIN_USER" ] || { echo 'Set LICENSE_ADMIN_USER in deploy/license/.env' >&2; exit 2; }
[ "${#ADMIN_PASS}" -ge 12 ] || { echo 'Set LICENSE_ADMIN_PASSWORD (≥12 chars) in deploy/license/.env' >&2; exit 2; }
[ -d "$ROOT/license-server" ] || { echo "license-server/ missing at $ROOT" >&2; exit 1; }

ssh_prep
echo "== License → $USER@$HOST:$REMOTE_DIR =="
ensure_remote_docker

ssh_cmd "mkdir -p '$REMOTE_DIR'"
rsync_cmd \
  --exclude '.env' --exclude '.git' --exclude 'data' \
  "$ROOT/license-server/" "$USER@$HOST:$REMOTE_DIR/"

[ -n "${LICENSE_DB_PASSWORD:-}" ] || {
  echo 'Set LICENSE_DB_PASSWORD in deploy/license/.env' >&2
  exit 2
}
tmp="$(mktemp)"
cat >"$tmp" <<EOF
LICENSE_ADMIN_USER=${ADMIN_USER}
LICENSE_ADMIN_PASSWORD=${ADMIN_PASS}
LICENSE_BIND_IP=${LICENSE_BIND_IP:-0.0.0.0}
LICENSE_PORT=${LICENSE_PORT:-8090}
LICENSE_DATA_VOLUME=${LICENSE_DATA_VOLUME:-clofix-license-server-data}
LICENSE_DB_PASSWORD=${LICENSE_DB_PASSWORD}
LICENSE_DB_VOLUME=${LICENSE_DB_VOLUME:-clofix-license-server-db}
EOF
scp_cmd "$tmp" "$USER@$HOST:$REMOTE_DIR/.env"
rm -f "$tmp"

remote_sudo "cd '$REMOTE_DIR' && docker compose up -d --build && docker compose ps"

echo
echo "License server up. Health: http://$HOST:${LICENSE_PORT:-8090}/healthz"
echo "Next: deploy/admin (set CLOFIX_LICENSE_SERVER_URL=http://$HOST:${LICENSE_PORT:-8090})"
