#!/usr/bin/env bash
# Deploy isolated Docker daemon for CloFix managed databases on a remote worker.
# Sets DOCKER_HOST=unix:///var/run/clofix-docker/docker.sock for clofix-worker.
# Usage: ./deploy/worker/deploy-database-runtime.sh
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

load_remote_env
ssh_prep

REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-worker}"
echo "== Database runtime (isolated dockerd) → $USER@$HOST =="

ssh_cmd "mkdir -p '$REMOTE_DIR/database-runtime'"
scp_cmd "$SCRIPT_DIR/systemd/clofix-database-runtime.service" \
  "$USER@$HOST:$REMOTE_DIR/database-runtime/clofix-database-runtime.service"

remote_sudo "
set -e
export DEBIAN_FRONTEND=noninteractive
if ! command -v dockerd >/dev/null; then
  apt-get update -qq
  apt-get install -y --no-install-recommends ca-certificates curl
  curl -fsSL https://get.docker.com | sh
fi
install -m 0644 '$REMOTE_DIR/database-runtime/clofix-database-runtime.service' \
  /etc/systemd/system/clofix-database-runtime.service
mkdir -p /var/run/clofix-docker /var/lib/clofix-database-runtime /dev/shm/clofix-database
systemctl daemon-reload
systemctl enable --now clofix-database-runtime
sleep 2
docker -H unix:///var/run/clofix-docker/docker.sock info >/dev/null
echo 'Isolated dockerd OK'

# Point worker at this socket when present
mkdir -p /etc/clofix-filevps
if [ -d /etc/systemd/system/clofix-worker.service.d ] || systemctl cat clofix-worker >/dev/null 2>&1; then
  mkdir -p /etc/systemd/system/clofix-worker.service.d
  cat >/etc/systemd/system/clofix-worker.service.d/database-runtime.conf <<'EOF'
[Service]
Environment=DOCKER_HOST=unix:///var/run/clofix-docker/docker.sock
Environment=CLOFIX_DATABASE_DIRECTORY_STORAGE=true
EOF
  systemctl daemon-reload
  systemctl try-restart clofix-worker || true
fi
systemctl --no-pager --full status clofix-database-runtime || true
"

echo
echo "Database runtime up on $HOST"
echo "  Socket: unix:///var/run/clofix-docker/docker.sock"
echo "  Data:   /var/lib/clofix-database-runtime"
echo "Worker DOCKER_HOST override installed when clofix-worker unit exists."
