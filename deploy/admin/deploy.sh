#!/usr/bin/env bash
# Deploy CloFix admin controller (db + S3 master + admin) to a remote host (step 2).
# Usage: ./deploy/admin/deploy.sh
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
REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-admin}"
[ -n "${DB_PASSWORD:-}" ] || { echo 'Set DB_PASSWORD in deploy/admin/.env' >&2; exit 2; }
[ -n "${CLOFIX_ADMIN_EMAIL:-}" ] || { echo 'Set CLOFIX_ADMIN_EMAIL in deploy/admin/.env' >&2; exit 2; }
[ -n "${CLOFIX_ADMIN_PASSWORD:-}" ] || { echo 'Set CLOFIX_ADMIN_PASSWORD in deploy/admin/.env' >&2; exit 2; }
[ "${#CLOFIX_ADMIN_PASSWORD}" -ge 12 ] || { echo 'CLOFIX_ADMIN_PASSWORD must be ≥12 chars' >&2; exit 2; }
[ -d "$ROOT/admin" ] && [ -d "$ROOT/worker" ] || { echo 'admin/ and worker/ required at repo root' >&2; exit 1; }
[ -f "$ROOT/storage/s3-entrypoint.sh" ] || { echo 'storage/s3-entrypoint.sh missing' >&2; exit 1; }

ssh_prep
echo "== Admin → $USER@$HOST:$REMOTE_DIR =="
ensure_remote_docker

ssh_cmd "mkdir -p '$REMOTE_DIR/admin' '$REMOTE_DIR/worker' '$REMOTE_DIR/storage'"

# Build context for admin/Dockerfile: repo-like tree (admin + worker + storage + compose)
rsync_cmd --exclude '.git' --exclude 'dist' --exclude '**/node_modules' \
  "$ROOT/admin/" "$USER@$HOST:$REMOTE_DIR/admin/"
rsync_cmd --exclude '.git' --exclude 'dist' --exclude '**/node_modules' \
  "$ROOT/worker/" "$USER@$HOST:$REMOTE_DIR/worker/"
scp_cmd "$ROOT/storage/s3-entrypoint.sh" "$USER@$HOST:$REMOTE_DIR/storage/s3-entrypoint.sh"
scp_cmd "$SCRIPT_DIR/docker-compose.yml" "$USER@$HOST:$REMOTE_DIR/docker-compose.yml"

# Compose .env without SSH secrets
tmp="$(mktemp)"
grep -vE '^(CLOFIX_REMOTE_|#)' "$SCRIPT_DIR/.env" | grep -vE '^$' >"$tmp" || true
# Ensure required keys present from loaded env
{
  echo "DB_PASSWORD=${DB_PASSWORD}"
  echo "CLOFIX_ADMIN_EMAIL=${CLOFIX_ADMIN_EMAIL}"
  echo "CLOFIX_ADMIN_PASSWORD=${CLOFIX_ADMIN_PASSWORD}"
  echo "CLOFIX_LICENSE_SERVER_URL=${CLOFIX_LICENSE_SERVER_URL:-}"
  echo "CLOFIX_LICENSE_PUBLIC_URL=${CLOFIX_LICENSE_PUBLIC_URL:-}"
  echo "S3_ACCESS_KEY=${S3_ACCESS_KEY}"
  echo "S3_SECRET_KEY=${S3_SECRET_KEY}"
  echo "S3_DEFAULT_REPLICATION=${S3_DEFAULT_REPLICATION:-000}"
  echo "S3_MASTER_BIND_IP=${S3_MASTER_BIND_IP:-0.0.0.0}"
  echo "CLOFIX_S3_MASTER_ADDRESS=${CLOFIX_S3_MASTER_ADDRESS:-s3pool:9333}"
  echo "ADMIN_HTTP_BIND=${ADMIN_HTTP_BIND:-0.0.0.0}"
  echo "ADMIN_MTLS_BIND=${ADMIN_MTLS_BIND:-0.0.0.0}"
  echo "DNS_BIND=${DNS_BIND:-127.0.0.1}"
  echo "HTTP_BIND=${HTTP_BIND:-127.0.0.1}"
  echo "HTTPS_BIND=${HTTPS_BIND:-127.0.0.1}"
  echo "S3_GATEWAY_BIND=${S3_GATEWAY_BIND:-127.0.0.1}"
} >"$tmp"
scp_cmd "$tmp" "$USER@$HOST:$REMOTE_DIR/.env"
rm -f "$tmp"

# --build picks up Go API changes (Redis Cluster /api/cluster-services, etc.).
# UI under admin/web is bind-mounted; HTML/JS updates do not need an image rebuild.
remote_sudo "cd '$REMOTE_DIR' && docker compose up -d --build && docker compose ps"

echo
echo "Admin up:"
echo "  UI:   http://$HOST:8080/"
echo "  Redis Cluster: http://$HOST:8080/redis-services"
echo "  mTLS: https://$HOST:8443/  (workers)"
echo "  S3 master (for workers): set deploy/worker CLOFIX_S3_MASTER_ADDRESS=$HOST:9333"
echo "Next: create enrollment token in UI, then deploy/worker"
echo "Redis Cluster needs a worker built with ./deploy/worker/build-worker.sh (configure/join ops)."
