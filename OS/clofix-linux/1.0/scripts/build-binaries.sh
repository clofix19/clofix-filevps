#!/usr/bin/env bash
# Cross-compile clofix + clofix-guard for Linux guests.
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TREE=$(cd "$SCRIPT_DIR/.." && pwd)
OUT="$TREE/dist"
cd "$TREE"

GO=${GO:-go}
mkdir -p "$OUT"

build_one() {
  local goos=$1 goarch=$2
  local dest="$OUT/${goos}-${goarch}"
  mkdir -p "$dest"
  echo "Building $goos/$goarch..."
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO" build -trimpath -ldflags '-s -w' -o "$dest/clofix" ./cmd/clofix
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO" build -trimpath -ldflags '-s -w' -o "$dest/clofix-guard" ./cmd/clofix-guard
}

build_one linux amd64
build_one linux arm64

# Convenience copies for current host arch used by harden-rootfs
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) cp -f "$OUT/linux-amd64/"* "$OUT/" ;;
  aarch64|arm64) cp -f "$OUT/linux-arm64/"* "$OUT/" ;;
esac

echo "Binaries in $OUT"
ls -la "$OUT" "$OUT"/linux-* 2>/dev/null || true
