#!/usr/bin/env bash
# Build the CloFix worker package into deploy/worker/dist for remote join.
# Usage: ./deploy/worker/build-worker.sh [bin|deb|rpm|all]
#
# Requires CLOFIX_INSTALLATION_ID (ctl-… from Admin → Settings → License), or
# CLOFIX_ADMIN_HTTP to fetch GET /api/installation-id automatically.
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
WORKER="$ROOT/worker"
TARGET="${1:-deb}"

if [ -f "$SCRIPT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/.env"
  set +a
fi

export VERSION="${VERSION:-0.1.0}"
export GOARCH="${GOARCH:-amd64}"
export SEAWEEDFS_VERSION="${SEAWEEDFS_VERSION:-4.51}"

resolve_installation_id() {
  if [ -n "${CLOFIX_INSTALLATION_ID:-}" ]; then
    return 0
  fi
  local base="${CLOFIX_ADMIN_HTTP:-}"
  [ -n "$base" ] || return 1
  base="${base%/}"
  echo "== Fetching Installation ID from $base/api/installation-id =="
  local json admit used limit reason
  json="$(curl -fsSL --max-time 15 "$base/api/installation-id")" || return 1
  CLOFIX_INSTALLATION_ID="$(printf '%s' "$json" | sed -n 's/.*"installation_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ -n "$CLOFIX_INSTALLATION_ID" ] || return 1
  export CLOFIX_INSTALLATION_ID
  admit="$(printf '%s' "$json" | python3 -c 'import json,sys; d=json.load(sys.stdin); w=d.get("workers") or {}; print("true" if w.get("admit") else "false")' 2>/dev/null || echo true)"
  used="$(printf '%s' "$json" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("workers") or {}).get("used",""))' 2>/dev/null || true)"
  limit="$(printf '%s' "$json" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("workers") or {}).get("limit",""))' 2>/dev/null || true)"
  reason="$(printf '%s' "$json" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("workers") or {}).get("reason",""))' 2>/dev/null || true)"
  if [ "$admit" = "false" ]; then
    echo "WARNING: admin worker license limit full (used=${used:-?} limit=${limit:-?})${reason:+ - $reason}. Package will build, but enroll will be refused until a slot is free." >&2
  elif [ -n "$used" ] || [ -n "$limit" ]; then
    echo "Worker capacity: used=${used:-?} limit=${limit:-?}"
  fi
}

[ -d "$WORKER" ] || { echo "worker/ not found at $WORKER" >&2; exit 1; }
[ -f "$WORKER/internal/engine/lifecycle/redis_cluster.go" ] || {
  echo "missing Redis Cluster worker code: $WORKER/internal/engine/lifecycle/redis_cluster.go" >&2
  exit 1
}
command -v go >/dev/null 2>&1 || { echo 'Go toolchain required to build the worker.' >&2; exit 1; }
export PATH="$(go env GOPATH 2>/dev/null)/bin:${PATH:-}"
if [ "$TARGET" != "bin" ] && ! command -v nfpm >/dev/null 2>&1; then
  echo "nfpm not found - installing…"
  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
  export PATH="$(go env GOPATH)/bin:$PATH"
fi

if ! resolve_installation_id; then
  echo 'Set CLOFIX_INSTALLATION_ID=ctl-… (Admin → Settings → License) or CLOFIX_ADMIN_HTTP=http://ADMIN:8080 in deploy/worker/.env' >&2
  exit 2
fi
case "$CLOFIX_INSTALLATION_ID" in
  ctl-*) ;;
  *)
    echo "CLOFIX_INSTALLATION_ID must look like ctl-… (got: $CLOFIX_INSTALLATION_ID)" >&2
    exit 2
    ;;
esac
export CLOFIX_INSTALLATION_ID

# Drop stale agent binary / packages so Installation ID bake-in is never stale.
mkdir -p "$WORKER/dist"
rm -f "$WORKER/dist/clofix-worker-linux-${GOARCH}" \
  "$WORKER/dist"/clofix-worker_"${VERSION}"_* \
  "$WORKER/dist"/clofix-worker-"${VERSION}"-* \
  "$WORKER/dist"/*.deb \
  "$WORKER/dist"/*.rpm \
  "$WORKER/dist/worker.json"

echo "== Building worker ($TARGET) VERSION=$VERSION GOARCH=$GOARCH InstallationID=$CLOFIX_INSTALLATION_ID =="
(cd "$WORKER" && ./build.sh "$TARGET")

BIN="$WORKER/dist/clofix-worker-linux-${GOARCH}"
[ -x "$BIN" ] || { echo "worker binary missing: $BIN" >&2; exit 1; }
if command -v strings >/dev/null 2>&1; then
  if ! strings "$BIN" | grep -Fq "$CLOFIX_INSTALLATION_ID"; then
    echo "WARNING: baked Installation ID string not found in binary (env/worker.json still required at runtime)" >&2
  fi
  if ! strings "$BIN" | grep -q 'configure-cluster-node'; then
    echo "WARNING: $BIN may lack configure-cluster-node - Redis Cluster configure will fail on this worker" >&2
  fi
fi

mkdir -p "$SCRIPT_DIR/dist"
rm -rf "${SCRIPT_DIR:?}/dist"/*
cp -a "$WORKER/dist"/. "$SCRIPT_DIR/dist/"
[ -n "$(ls -A "$SCRIPT_DIR/dist" 2>/dev/null)" ] || { echo "No artifacts found under $WORKER/dist" >&2; exit 1; }

# Persist resolved ID for push/install scripts when .env exists.
if [ -f "$SCRIPT_DIR/.env" ]; then
  if grep -q '^CLOFIX_INSTALLATION_ID=' "$SCRIPT_DIR/.env"; then
    sed -i.bak "s|^CLOFIX_INSTALLATION_ID=.*|CLOFIX_INSTALLATION_ID=${CLOFIX_INSTALLATION_ID}|" "$SCRIPT_DIR/.env"
  else
    echo "CLOFIX_INSTALLATION_ID=${CLOFIX_INSTALLATION_ID}" >> "$SCRIPT_DIR/.env"
  fi
  rm -f "$SCRIPT_DIR/.env.bak"
fi

echo
echo "Artifacts in $SCRIPT_DIR/dist:"
ls -1 "$SCRIPT_DIR/dist"
echo
echo "Bound Installation ID: $CLOFIX_INSTALLATION_ID"
echo "Next: ./push-join-worker.sh  (worker will refuse a different admin Installation ID)"
