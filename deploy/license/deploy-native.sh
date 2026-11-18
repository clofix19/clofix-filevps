#!/usr/bin/env bash
# Deploy CloFix license server as a native systemd service (no Docker).
# Prerequisites: ./install-db.sh
# Usage: ./deploy/license/deploy-native.sh
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
ssh_prep

REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-license}"
GOARCH="${GOARCH:-amd64}"
GOOS="${GOOS:-linux}"
ADMIN_USER="${LICENSE_ADMIN_USER:-}"
ADMIN_PASS="${LICENSE_ADMIN_PASSWORD:-}"
DB_NAME="${LICENSE_DB_NAME:-clofix_license}"
DB_USER="${LICENSE_DB_USER:-clofix}"
DB_PASSWORD="${LICENSE_DB_PASSWORD:-}"
DB_HOST_ADDR="${LICENSE_DB_HOST:-127.0.0.1}"
DB_PORT="${LICENSE_DB_PORT:-5432}"
DB_SSLMODE="${LICENSE_DB_SSLMODE:-disable}"
LISTEN="${CLOFIX_LICENSE_LISTEN:-:8090}"
PORT_PUBLIC="${LICENSE_PORT:-8090}"

[ -n "$ADMIN_USER" ] || { echo 'Set LICENSE_ADMIN_USER in deploy/license/.env' >&2; exit 2; }
[ "${#ADMIN_PASS}" -ge 12 ] || { echo 'Set LICENSE_ADMIN_PASSWORD (≥12 chars) in deploy/license/.env' >&2; exit 2; }
[ -n "$DB_PASSWORD" ] || { echo 'Set LICENSE_DB_PASSWORD in deploy/license/.env' >&2; exit 2; }
[ -d "$ROOT/license-server" ] || { echo "license-server/ missing at $ROOT" >&2; exit 1; }
command -v go >/dev/null || { echo 'Go toolchain required to build clofix-license' >&2; exit 1; }

BIN_DIR="$SCRIPT_DIR/dist"
mkdir -p "$BIN_DIR"
BIN="$BIN_DIR/clofix-license-${GOOS}-${GOARCH}"

echo "== Building license server ($GOOS/$GOARCH) =="
(
  cd "$ROOT/license-server"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -o "$BIN" ./cmd/clofix-license
)
[ -x "$BIN" ] || { echo "build failed: $BIN" >&2; exit 1; }

DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST_ADDR}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}"

echo "== Ensure license DB schema is current =="
"$SCRIPT_DIR/migrate-db.sh"

echo "== License native → $USER@$HOST:$REMOTE_DIR =="
ssh_cmd "mkdir -p '$REMOTE_DIR/bin'"

scp_cmd "$BIN" "$USER@$HOST:$REMOTE_DIR/bin/clofix-license"
scp_cmd "$SCRIPT_DIR/systemd/clofix-license.service" "$USER@$HOST:$REMOTE_DIR/clofix-license.service"

tmp="$(mktemp)"
cat >"$tmp" <<EOF
CLOFIX_LICENSE_ADMIN_USER=${ADMIN_USER}
CLOFIX_LICENSE_ADMIN_PASSWORD=${ADMIN_PASS}
CLOFIX_LICENSE_DATA_DIR=/var/lib/clofix-license
CLOFIX_LICENSE_LISTEN=${LISTEN}
CLOFIX_LICENSE_DATABASE_URL=${DB_URL}
EOF
scp_cmd "$tmp" "$USER@$HOST:$REMOTE_DIR/license.env"
rm -f "$tmp"

remote_sudo "
set -e
id clofix >/dev/null 2>&1 || useradd --system --home /var/lib/clofix-license --shell /usr/sbin/nologin clofix
mkdir -p /var/lib/clofix-license /etc/clofix
install -m 0755 '$REMOTE_DIR/bin/clofix-license' /usr/local/bin/clofix-license
install -m 0640 '$REMOTE_DIR/license.env' /etc/clofix/license.env
chown root:clofix /etc/clofix/license.env
chmod 0640 /etc/clofix/license.env
chown -R clofix:clofix /var/lib/clofix-license
install -m 0644 '$REMOTE_DIR/clofix-license.service' /etc/systemd/system/clofix-license.service
systemctl daemon-reload
systemctl enable --now clofix-license
systemctl --no-pager --full status clofix-license || true
"

echo
echo "License server (native) up. Health: http://$HOST:${PORT_PUBLIC}/healthz"
echo "Next: deploy/admin (set CLOFIX_LICENSE_SERVER_URL=http://$HOST:${PORT_PUBLIC})"
