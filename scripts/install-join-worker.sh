#!/bin/sh
# Compatibility wrapper — canonical path is deploy/worker/install-join-worker.sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
exec "$ROOT/deploy/worker/install-join-worker.sh" "$@"
