#!/usr/bin/env bash
# Step 3 - Push deploy/worker to a remote Ubuntu host and run install-join-worker.sh.
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
REMOTE_DIR="${CLOFIX_REMOTE_DIR:-/opt/clofix-worker}"
[ -n "${CLOFIX_CONTROLLER:-}" ] || { echo 'Set CLOFIX_CONTROLLER in deploy/worker/.env' >&2; exit 2; }
[ -n "${CLOFIX_TOKEN:-}" ] || { echo 'Set CLOFIX_TOKEN in deploy/worker/.env' >&2; exit 2; }

DEB=""
for f in "$SCRIPT_DIR"/dist/clofix-worker*.deb "$SCRIPT_DIR"/dist/*.deb; do
  [ -f "$f" ] || continue
  if [ -z "$DEB" ] || [ "$f" -nt "$DEB" ]; then DEB="$f"; fi
done
[ -n "$DEB" ] || { echo 'No .deb - run ./build-worker.sh deb first' >&2; exit 2; }
[ -f "$SCRIPT_DIR/install-join-worker.sh" ] || { echo 'install-join-worker.sh missing' >&2; exit 2; }
[ -f "$SCRIPT_DIR/build-managed-templates.sh" ] || { echo 'build-managed-templates.sh missing' >&2; exit 2; }

ssh_prep
echo "== Worker → $USER@$HOST:$REMOTE_DIR =="
ssh_cmd "mkdir -p '$REMOTE_DIR/dist'"

scp_cmd \
  "$SCRIPT_DIR/install-join-worker.sh" \
  "$SCRIPT_DIR/build-managed-templates.sh" \
  "$SCRIPT_DIR/.env" \
  "$USER@$HOST:$REMOTE_DIR/"
scp_cmd "$DEB" "$USER@$HOST:$REMOTE_DIR/dist/"

ssh_cmd "cd '$REMOTE_DIR' && chmod +x install-join-worker.sh build-managed-templates.sh"
if [ "$USER" = "root" ]; then
  ssh_cmd "cd '$REMOTE_DIR' && ./install-join-worker.sh"
elif ssh_cmd "sudo -n true" 2>/dev/null; then
  ssh_cmd "cd '$REMOTE_DIR' && sudo ./install-join-worker.sh"
elif [ -n "${PASS:-}" ]; then
  printf '%s\n' "$PASS" | ssh_cmd "cd '$REMOTE_DIR' && sudo -S -p '' ./install-join-worker.sh"
else
  echo 'Need root, passwordless sudo, or CLOFIX_REMOTE_PASSWORD' >&2
  exit 1
fi

echo
echo "Done. Check Admin UI → Workers for heartbeat."
