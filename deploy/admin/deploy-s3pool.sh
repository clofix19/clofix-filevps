#!/usr/bin/env bash
# Deploy CloFix S3 pool (SeaweedFS master + filer + S3 gateway) as systemd on remote.
# Usage: ./deploy/admin/deploy-s3pool.sh
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
SEAWEEDFS_VERSION="${SEAWEEDFS_VERSION:-4.51}"
S3_ACCESS_KEY="${S3_ACCESS_KEY:-}"
S3_SECRET_KEY="${S3_SECRET_KEY:-}"
S3_DEFAULT_REPLICATION="${S3_DEFAULT_REPLICATION:-000}"
S3_WITH_LOCAL_VOLUME="${S3_WITH_LOCAL_VOLUME:-1}"
S3_MASTER_BIND_IP="${S3_MASTER_BIND_IP:-0.0.0.0}"
S3_GATEWAY_BIND="${S3_GATEWAY_BIND:-127.0.0.1}"
S3_DATA_DIR="${S3_DATA_DIR:-/var/lib/clofix-s3pool}"

[ -n "$S3_ACCESS_KEY" ] || { echo 'Set S3_ACCESS_KEY in deploy/admin/.env' >&2; exit 2; }
[ -n "$S3_SECRET_KEY" ] || { echo 'Set S3_SECRET_KEY in deploy/admin/.env' >&2; exit 2; }
case "$S3_ACCESS_KEY$S3_SECRET_KEY" in
  *[!A-Za-z0-9_-]*) echo 'S3_ACCESS_KEY/S3_SECRET_KEY: letters, numbers, _ and - only' >&2; exit 2 ;;
esac

# Map GOARCH → SeaweedFS release arch name
case "$GOARCH" in
  amd64|x86_64) WEED_ARCH=amd64 ;;
  arm64|aarch64) WEED_ARCH=arm64 ;;
  *) echo "Unsupported GOARCH=$GOARCH for SeaweedFS" >&2; exit 2 ;;
esac

echo "== S3 pool → $USER@$HOST (SeaweedFS $SEAWEEDFS_VERSION / $WEED_ARCH) =="
ssh_cmd "mkdir -p '$REMOTE_DIR/s3pool'"
scp_cmd "$SCRIPT_DIR/scripts/clofix-s3pool-start.sh" "$USER@$HOST:$REMOTE_DIR/s3pool/clofix-s3pool-start.sh"
scp_cmd "$SCRIPT_DIR/systemd/clofix-s3pool.service" "$USER@$HOST:$REMOTE_DIR/s3pool/clofix-s3pool.service"
# Keep a copy of the compose entrypoint for reference
[ -f "$ROOT/storage/s3-entrypoint.sh" ] && scp_cmd "$ROOT/storage/s3-entrypoint.sh" "$USER@$HOST:$REMOTE_DIR/s3pool/s3-entrypoint.sh" || true

tmp="$(mktemp)"
cat >"$tmp" <<EOF
S3_ACCESS_KEY=${S3_ACCESS_KEY}
S3_SECRET_KEY=${S3_SECRET_KEY}
S3_DEFAULT_REPLICATION=${S3_DEFAULT_REPLICATION}
S3_WITH_LOCAL_VOLUME=${S3_WITH_LOCAL_VOLUME}
S3_DATA_DIR=${S3_DATA_DIR}
S3_CONFIG=/etc/clofix/seaweed-s3.json
S3_MASTER_PORT=9333
S3_MASTER_GRPC_PORT=19333
S3_GATEWAY_PORT=8333
WEED_BIN=/usr/local/bin/weed
EOF
scp_cmd "$tmp" "$USER@$HOST:$REMOTE_DIR/s3pool/s3pool.env"
rm -f "$tmp"

WEED_URL="https://github.com/seaweedfs/seaweedfs/releases/download/${SEAWEEDFS_VERSION}/linux_${WEED_ARCH}.tar.gz"

remote_sudo "
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends ca-certificates curl tar
id clofix >/dev/null 2>&1 || useradd --system --home /var/lib/clofix-controller --shell /usr/sbin/nologin clofix
mkdir -p '$S3_DATA_DIR' /etc/clofix /usr/local/bin
chown -R clofix:clofix '$S3_DATA_DIR'

if ! /usr/local/bin/weed version 2>/dev/null | grep -q '${SEAWEEDFS_VERSION}'; then
  echo 'Downloading SeaweedFS ${SEAWEEDFS_VERSION}…'
  tmpd=\$(mktemp -d)
  curl -fsSL '$WEED_URL' -o \"\$tmpd/weed.tgz\"
  tar -xzf \"\$tmpd/weed.tgz\" -C \"\$tmpd\"
  install -m 0755 \"\$tmpd/weed\" /usr/local/bin/weed
  rm -rf \"\$tmpd\"
fi
/usr/local/bin/weed version || true

install -m 0755 '$REMOTE_DIR/s3pool/clofix-s3pool-start.sh' /usr/local/bin/clofix-s3pool-start
install -m 0640 '$REMOTE_DIR/s3pool/s3pool.env' /etc/clofix/s3pool.env
chown root:clofix /etc/clofix/s3pool.env
chmod 0640 /etc/clofix/s3pool.env
install -m 0644 '$REMOTE_DIR/s3pool/clofix-s3pool.service' /etc/systemd/system/clofix-s3pool.service
systemctl daemon-reload
systemctl enable --now clofix-s3pool
sleep 2
systemctl --no-pager --full status clofix-s3pool || true
"

# Default advertise address for workers / controller
if [ -z "${CLOFIX_S3_MASTER_ADDRESS:-}" ]; then
  ADV="${HOST}:9333"
  echo "Hint: set CLOFIX_S3_MASTER_ADDRESS=$ADV in deploy/admin/.env (and worker .env)"
else
  ADV="$CLOFIX_S3_MASTER_ADDRESS"
fi

echo
echo "S3 pool up on $HOST"
echo "  Master:  ${S3_MASTER_BIND_IP}:9333  (advertise: $ADV)"
echo "  gRPC:    ${S3_MASTER_BIND_IP}:19333"
echo "  S3 API:  ${S3_GATEWAY_BIND}:8333"
echo "  Local volume: S3_WITH_LOCAL_VOLUME=${S3_WITH_LOCAL_VOLUME}"
echo "Next: ./deploy/admin/deploy-native.sh"
