#!/usr/bin/env bash
# Deploy CloFix admin controller as a native systemd service (no Docker).
# Optional runtimes (default on): database (Postgres) + S3 pool (SeaweedFS).
#   WITH_DB_RUNTIME=1 WITH_S3_POOL=1 ./deploy/admin/deploy-native.sh
# Usage: ./deploy/admin/deploy-native.sh
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

REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-admin}"
GOARCH="${GOARCH:-amd64}"
GOOS="${GOOS:-linux}"
DB_NAME="${DB_NAME:-clofix}"
DB_USER="${DB_USER:-clofix}"
DB_PASSWORD="${DB_PASSWORD:-}"
DB_HOST_ADDR="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-5432}"
DB_SSLMODE="${DB_SSLMODE:-disable}"
WITH_DB_RUNTIME="${WITH_DB_RUNTIME:-1}"
WITH_S3_POOL="${WITH_S3_POOL:-1}"

[ -n "${CLOFIX_ADMIN_EMAIL:-}" ] || { echo 'Set CLOFIX_ADMIN_EMAIL in deploy/admin/.env' >&2; exit 2; }
[ -n "${CLOFIX_ADMIN_PASSWORD:-}" ] || { echo 'Set CLOFIX_ADMIN_PASSWORD in deploy/admin/.env' >&2; exit 2; }
[ "${#CLOFIX_ADMIN_PASSWORD}" -ge 12 ] || { echo 'CLOFIX_ADMIN_PASSWORD must be ≥12 chars' >&2; exit 2; }
[ -n "$DB_PASSWORD" ] || { echo 'Set DB_PASSWORD in deploy/admin/.env' >&2; exit 2; }
[ -d "$ROOT/admin" ] || { echo "admin/ missing at $ROOT" >&2; exit 1; }
command -v go >/dev/null || { echo 'Go toolchain required to build clofix-controller' >&2; exit 1; }

if [ "$WITH_DB_RUNTIME" = "1" ]; then
  echo "== Database runtime =="
  "$SCRIPT_DIR/deploy-db-runtime.sh"
fi

if [ "$WITH_S3_POOL" = "1" ]; then
  echo "== S3 pool runtime =="
  "$SCRIPT_DIR/deploy-s3pool.sh"
  # Advertise reachable master to workers when unset
  if [ -z "${CLOFIX_S3_MASTER_ADDRESS:-}" ]; then
    CLOFIX_S3_MASTER_ADDRESS="${HOST}:9333"
    export CLOFIX_S3_MASTER_ADDRESS
  fi
fi

BIN_DIR="$SCRIPT_DIR/dist"
mkdir -p "$BIN_DIR"
BIN="$BIN_DIR/clofix-controller-${GOOS}-${GOARCH}"

echo "== Building controller ($GOOS/$GOARCH, tags=postgres) =="
(
  cd "$ROOT/admin"
  go get github.com/jackc/pgx/v5/stdlib@v5.7.2 >/dev/null 2>&1 || true
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -tags postgres \
    -o "$BIN" ./cmd/clofix-controller
)
[ -x "$BIN" ] || { echo "build failed: $BIN" >&2; exit 1; }

DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST_ADDR}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}"

if [ "$WITH_DB_RUNTIME" != "1" ]; then
  echo "== Ensure DB schema is current =="
  "$SCRIPT_DIR/migrate-db.sh"
fi

echo "== Admin native → $USER@$HOST:$REMOTE_DIR =="
ssh_cmd "mkdir -p '$REMOTE_DIR/web' '$REMOTE_DIR/bin'"

rsync_cmd --exclude '.git' --exclude '**/node_modules' \
  "$ROOT/admin/web/" "$USER@$HOST:$REMOTE_DIR/web/"
scp_cmd "$BIN" "$USER@$HOST:$REMOTE_DIR/bin/clofix-controller"
scp_cmd "$SCRIPT_DIR/systemd/clofix-controller.service" "$USER@$HOST:$REMOTE_DIR/clofix-controller.service"

# Runtime env (no SSH secrets)
tmp="$(mktemp)"
cat >"$tmp" <<EOF
CLOFIX_ADMIN_EMAIL=${CLOFIX_ADMIN_EMAIL}
CLOFIX_ADMIN_PASSWORD=${CLOFIX_ADMIN_PASSWORD}
CLOFIX_DB_URL=${DB_URL}
CLOFIX_DATA_DIR=/var/lib/clofix-controller
CLOFIX_UI_DIR=${REMOTE_DIR}/web/admin-ui
CLOFIX_ADMIN_LISTEN=${CLOFIX_ADMIN_LISTEN:-:8080}
CLOFIX_WORKER_LISTEN=${CLOFIX_WORKER_LISTEN:-:8443}
CLOFIX_DNS_LISTEN=${CLOFIX_DNS_LISTEN:-127.0.0.1:53}
CLOFIX_HTTP_LISTEN=${CLOFIX_HTTP_LISTEN:-127.0.0.1:80}
CLOFIX_HTTPS_LISTEN=${CLOFIX_HTTPS_LISTEN:-127.0.0.1:443}
CLOFIX_LICENSE_SERVER_URL=${CLOFIX_LICENSE_SERVER_URL:-}
CLOFIX_LICENSE_PUBLIC_URL=${CLOFIX_LICENSE_PUBLIC_URL:-}
CLOFIX_TLS_NAMES=${CLOFIX_TLS_NAMES:-localhost,127.0.0.1,${HOST}}
CLOFIX_S3_MASTER_ADDRESS=${CLOFIX_S3_MASTER_ADDRESS:-}
EOF
scp_cmd "$tmp" "$USER@$HOST:$REMOTE_DIR/controller.env"
rm -f "$tmp"

remote_sudo "
set -e
id clofix >/dev/null 2>&1 || useradd --system --home /var/lib/clofix-controller --shell /usr/sbin/nologin clofix
mkdir -p /var/lib/clofix-controller /etc/clofix
install -m 0755 '$REMOTE_DIR/bin/clofix-controller' /usr/local/bin/clofix-controller
install -m 0640 '$REMOTE_DIR/controller.env' /etc/clofix/controller.env
chown root:clofix /etc/clofix/controller.env
chmod 0640 /etc/clofix/controller.env
chown -R clofix:clofix /var/lib/clofix-controller '$REMOTE_DIR/web'
install -m 0644 '$REMOTE_DIR/clofix-controller.service' /etc/systemd/system/clofix-controller.service
systemctl daemon-reload
systemctl enable --now clofix-controller
systemctl --no-pager --full status clofix-controller || true
"

echo
echo "Admin (native) up:"
echo "  UI:   http://$HOST:8080/"
echo "  mTLS: https://$HOST:8443/  (workers)"
[ "$WITH_S3_POOL" = "1" ] && echo "  S3:   master ${CLOFIX_S3_MASTER_ADDRESS:-$HOST:9333}  gateway ${HOST}:8333"
[ "$WITH_DB_RUNTIME" = "1" ] && echo "  DB:   postgres://${DB_USER}@${DB_HOST_ADDR}:${DB_PORT}/${DB_NAME}"
echo "Next: create enrollment token, then ./deploy/worker/deploy.sh"
echo "      (worker: set CLOFIX_S3_MASTER_ADDRESS=${CLOFIX_S3_MASTER_ADDRESS:-$HOST:9333})"
