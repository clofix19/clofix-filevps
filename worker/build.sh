#!/usr/bin/env bash
# Usage: ./build.sh [bin|deb|rpm|all]   env: VERSION=0.1.0 GOARCH=amd64 CLOFIX_INSTALLATION_ID=ctl-…
set -euo pipefail
cd "$(dirname "$0")"
TARGET="${1:-all}"
export VERSION="${VERSION:-0.1.0}"
export GOARCH="${GOARCH:-amd64}"
export SEAWEEDFS_VERSION="${SEAWEEDFS_VERSION:-4.51}"
export PATH="$(go env GOPATH 2>/dev/null)/bin:${PATH:-}"
INSTALL_ID="${CLOFIX_INSTALLATION_ID:-}"
mkdir -p dist

build_bin() {
  # Always rebuild the agent so Redis Cluster ops + Installation ID binding are current.
  rm -f "dist/clofix-worker-linux-${GOARCH}"
  local ldflags="-s -w -X main.version=${VERSION}"
  if [ -n "$INSTALL_ID" ]; then
    ldflags="${ldflags} -X github.com/clofix/filevps/worker/internal/config.BakedInstallationID=${INSTALL_ID}"
  fi
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -trimpath -ldflags "$ldflags" \
    -o "dist/clofix-worker-linux-${GOARCH}" ./cmd/clofix-worker
  echo "built dist/clofix-worker-linux-${GOARCH}"
  if [ -n "$INSTALL_ID" ]; then
    echo "baked Installation ID: $INSTALL_ID"
  else
    echo "WARNING: CLOFIX_INSTALLATION_ID unset - worker must receive it via env/worker.json at enroll time" >&2
  fi
  if [ -x dist/weed ]; then
    echo "reusing dist/weed"
    return 0
  fi
  local weed_archive="dist/seaweedfs-linux_${GOARCH}.tar.gz"
  local weed_tmp weed_path
  weed_tmp="$(mktemp -d)"
  curl -fsSL "https://github.com/seaweedfs/seaweedfs/releases/download/${SEAWEEDFS_VERSION}/linux_${GOARCH}.tar.gz" -o "$weed_archive"
  tar -xzf "$weed_archive" -C "$weed_tmp"
  weed_path="$(find "$weed_tmp" -type f -name weed -print -quit)"
  test -n "$weed_path" || { rm -rf "$weed_tmp"; echo "SeaweedFS release did not contain weed" >&2; exit 1; }
  install -m 0755 "$weed_path" dist/weed
  rm -rf "$weed_tmp"
  rm -f "$weed_archive"
}

write_packaged_config() {
  # nfpm packs dist/worker.json - stamp Installation ID when building packages.
  mkdir -p dist
  if [ -n "$INSTALL_ID" ]; then
    cat > dist/worker.json <<EOF
{
  "controller_url": "",
  "installation_id": "${INSTALL_ID}",
  "host_reserve_mb": 512,
  "zram_mb": 0,
  "heartbeat_seconds": 10,
  "uid_base": 100000,
  "user_ns": true
}
EOF
  else
    cat > dist/worker.json <<'EOF'
{
  "controller_url": "",
  "host_reserve_mb": 512,
  "zram_mb": 0,
  "heartbeat_seconds": 10,
  "uid_base": 100000,
  "user_ns": true
}
EOF
  fi
}

pkg() {
  command -v nfpm >/dev/null || { echo "nfpm not found: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest" >&2; exit 1; }
  write_packaged_config
  # nfpm v2 no longer expands ${GOARCH}/${VERSION} from the process env in contents globs.
  local rendered
  rendered="$(mktemp)"
  sed -e "s/\${GOARCH}/${GOARCH}/g" -e "s/\${VERSION}/${VERSION}/g" packaging/nfpm.yaml > "$rendered"
  nfpm package -f "$rendered" -p "$1" -t dist/
  rm -f "$rendered"
}
case "$TARGET" in
  bin) build_bin ;;
  deb) build_bin; pkg deb ;;
  rpm) build_bin; pkg rpm ;;
  all) build_bin; pkg deb; pkg rpm ;;
  *) echo "usage: $0 [bin|deb|rpm|all]"; exit 2 ;;
esac
ls -1 dist
