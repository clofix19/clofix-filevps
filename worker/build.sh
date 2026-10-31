#!/usr/bin/env bash
# Usage: ./build.sh [bin|deb|rpm|all]   env: VERSION=0.1.0 GOARCH=amd64
set -euo pipefail
cd "$(dirname "$0")"
TARGET="${1:-all}"
export VERSION="${VERSION:-0.1.0}"
export GOARCH="${GOARCH:-amd64}"
export SEAWEEDFS_VERSION="${SEAWEEDFS_VERSION:-4.51}"
mkdir -p dist

build_bin() {
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o "dist/clofix-worker-linux-${GOARCH}" ./cmd/clofix-worker
  echo "built dist/clofix-worker-linux-${GOARCH}"
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
pkg() {
  command -v nfpm >/dev/null || { echo "nfpm not found: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; exit 1; }
  nfpm package -f packaging/nfpm.yaml -p "$1" -t dist/
}
case "$TARGET" in
  bin) build_bin ;;
  deb) build_bin; pkg deb ;;
  rpm) build_bin; pkg rpm ;;
  all) build_bin; pkg deb; pkg rpm ;;
  *) echo "usage: $0 [bin|deb|rpm|all]"; exit 2 ;;
esac
ls -1 dist
