#!/usr/bin/env bash
# Deploy / join a remote FileVPS worker to the admin controller.
# Wrapper around push-join-worker.sh (build .deb first when needed).
#
# Usage:
#   cp deploy/worker/.env.example deploy/worker/.env
#   # set CLOFIX_REMOTE_IP, USER, PASSWORD
#   # set CLOFIX_CONTROLLER, CLOFIX_ADMIN_HTTP, CLOFIX_TOKEN, CLOFIX_INSTALLATION_ID
#   ./deploy/worker/build-worker.sh deb    # once / after code changes
#   ./deploy/worker/deploy.sh
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

WITH_DATABASE_RUNTIME="${WITH_DATABASE_RUNTIME:-0}"

if [ "${1:-}" = "--build" ] || [ "${1:-}" = "-b" ]; then
  "$SCRIPT_DIR/build-worker.sh" deb
  shift || true
fi

DEB=""
for f in "$SCRIPT_DIR"/dist/clofix-worker*.deb "$SCRIPT_DIR"/dist/*.deb; do
  [ -f "$f" ] || continue
  if [ -z "$DEB" ] || [ "$f" -nt "$DEB" ]; then DEB="$f"; fi
done
if [ -z "$DEB" ]; then
  echo 'No worker .deb found - building…'
  "$SCRIPT_DIR/build-worker.sh" deb
fi

"$SCRIPT_DIR/push-join-worker.sh" "$@"

if [ "$WITH_DATABASE_RUNTIME" = "1" ]; then
  echo "== Database runtime (managed DB dockerd) =="
  "$SCRIPT_DIR/deploy-database-runtime.sh"
fi
