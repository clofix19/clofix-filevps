#!/usr/bin/env bash
# Build CloFix Linux 1.0 rootfs.tar.gz from Ubuntu 24.04.
# Brands and validates inside Docker (works on macOS hosts). Does NOT build the worker image.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TREE=$(cd "$SCRIPT_DIR/.." && pwd)
OUT="${CLOFIX_ROOTFS_OUT:-$TREE/rootfs.tar.gz}"
WORKDIR="${CLOFIX_ROOTFS_WORK:-$TREE/.build}"
BASE_IMAGE="${CLOFIX_BASE_IMAGE:-ubuntu:24.04}"
CTR="clofix-linux-rootfs-build-$$"
RUN_APT_SMOKE="${CLOFIX_APT_SMOKE:-1}"

cleanup() {
  docker rm -f "$CTR" >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v docker >/dev/null || { echo "docker required" >&2; exit 1; }

echo "== CloFix Linux 1.0 rootfs build =="
echo "Base: $BASE_IMAGE"
echo "Out:  $OUT"

rm -rf "$WORKDIR"
mkdir -p "$WORKDIR/reports"

docker pull "$BASE_IMAGE"
docker create --name "$CTR" -v "$TREE:/opt/clofix-linux-1.0:ro" "$BASE_IMAGE" sleep 7200 >/dev/null
docker start "$CTR" >/dev/null

echo "== Bootstrap packages =="
docker exec "$CTR" bash -lc 'export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends openssh-server iproute2 ca-certificates systemd
  rm -rf /var/lib/apt/lists/*
  rm -f /etc/ssh/ssh_host_*_key*'

echo "== Build Guard/CLI binaries (host) =="
"$SCRIPT_DIR/build-binaries.sh"

echo "== Stage branding toolkit (writable copy) =="
docker exec "$CTR" bash -lc 'rm -rf /tmp/clofix-linux-1.0
  cp -a /opt/clofix-linux-1.0 /tmp/clofix-linux-1.0
  chmod +x /tmp/clofix-linux-1.0/scripts/*.sh /tmp/clofix-linux-1.0/packages/clofix-release/usr/bin/clofix
  mkdir -p /tmp/clofix-linux-1.0/dist
  cp -a /opt/clofix-linux-1.0/dist/. /tmp/clofix-linux-1.0/dist/ 2>/dev/null || true'

echo "== Inspect (pre-brand) =="
docker exec "$CTR" bash -lc '/tmp/clofix-linux-1.0/scripts/inspect-rootfs.sh / /tmp/inspect-pre.md'
docker cp "$CTR:/tmp/inspect-pre.md" "$WORKDIR/reports/inspect-pre.md" 2>/dev/null || true

echo "== Brand =="
docker exec "$CTR" bash -lc '/tmp/clofix-linux-1.0/scripts/brand-rootfs.sh / --no-backup'
echo "== Harden =="
docker exec "$CTR" bash -lc '/tmp/clofix-linux-1.0/scripts/harden-rootfs.sh / --no-backup'
docker exec "$CTR" bash -lc 'ls -t /var/lib/clofix/clofix-brand-report-*.txt 2>/dev/null | head -1' > "$WORKDIR/reports/.brand-report-path" || true
BRAND_REP=$(cat "$WORKDIR/reports/.brand-report-path" 2>/dev/null || true)
if [[ -n "${BRAND_REP:-}" ]]; then
  docker cp "$CTR:$BRAND_REP" "$WORKDIR/reports/changed-files.txt" || true
fi

echo "== Validate =="
docker exec "$CTR" bash -lc '/tmp/clofix-linux-1.0/scripts/validate-rootfs.sh /' | tee "$WORKDIR/reports/validate.txt"

if [[ "$RUN_APT_SMOKE" == "1" ]]; then
  echo "== APT + Guard smoke =="
  set +e
  docker exec "$CTR" bash -lc 'export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends curl
    clofix version
    clofix info
    clofix security scan >/tmp/clofix-scan.json
    clofix security score || true
    clofix security ssh status
    clofix security firewall status
    clofix security process status
    clofix security mode list
    clofix-guard -once
    test -s /var/lib/clofix/guard/score.json
    grep -q noble /etc/os-release
    ! grep -R --include="*.sources" --include="*.list" -E "Suites: (jammy|focal)|/jammy|/focal" /etc/apt 2>/dev/null
    echo GUARD_SMOKE_OK' | tee "$WORKDIR/reports/apt-smoke.txt"
  APT_RC=${PIPESTATUS[0]}
  set -e
  if [[ $APT_RC -ne 0 ]]; then
    echo "APT/Guard smoke FAILED (rc=$APT_RC)" | tee -a "$WORKDIR/reports/apt-smoke.txt"
    exit "$APT_RC"
  fi
  docker cp "$CTR:/tmp/clofix-scan.json" "$WORKDIR/reports/security-scan.json" 2>/dev/null || true
  docker cp "$CTR:/var/lib/clofix/guard/score.json" "$WORKDIR/reports/score.json" 2>/dev/null || true
  docker exec "$CTR" bash -lc 'rm -rf /var/lib/apt/lists/*; rm -f /etc/ssh/ssh_host_*_key*'
fi

echo "== Pack =="
docker exec "$CTR" bash -lc 'mkdir -p /opt/rootfs-mps/proc /opt/rootfs-mps/sys /opt/rootfs-mps/dev /opt/rootfs-mps/run /opt/rootfs-mps/tmp
  chmod 1777 /opt/rootfs-mps/tmp
  tar --numeric-owner --xattrs --xattrs-include="*" --acls -cf /tmp/rootfs.tar \
    --exclude=./proc --exclude=./sys --exclude=./dev --exclude=./run --exclude=./tmp \
    --exclude=./opt/clofix-linux-1.0 --exclude=./tmp/clofix-linux-1.0 --exclude=./opt/rootfs-mps \
    -C / .
  tar --numeric-owner -rf /tmp/rootfs.tar -C /opt/rootfs-mps proc sys dev run tmp
  gzip -f /tmp/rootfs.tar'
docker cp "$CTR:/tmp/rootfs.tar.gz" "$OUT.tmp"
mv -f "$OUT.tmp" "$OUT"

# Compatibility note
cat > "$WORKDIR/reports/COMPATIBILITY-TEST.md" <<EOF
# CloFix Linux 1.0 compatibility test results

- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Base image: $BASE_IMAGE
- Validate: see validate.txt
- APT smoke: ${RUN_APT_SMOKE} (see apt-smoke.txt if enabled)
- Archive: $OUT

Tests run inside an isolated Docker container. Do not treat unchecked claims as complete without these reports.
EOF

echo "== Done =="
echo "Archive: $OUT ($(du -h "$OUT" | awk '{print $1}'))"
echo "Reports: $WORKDIR/reports/"
ls -la "$WORKDIR/reports/"
