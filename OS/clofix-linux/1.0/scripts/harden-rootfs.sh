#!/usr/bin/env bash
# Apply auditable CloFix security baseline to an extracted rootfs (dry-run capable).
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TREE=$(cd "$SCRIPT_DIR/.." && pwd)
ROOT=""
DRY=0
BACKUP=1

usage() { echo "Usage: $0 <rootfs-dir> [--dry-run] [--no-backup]"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY=1; shift ;;
    --no-backup) BACKUP=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) [[ -z "$ROOT" ]] && ROOT=$1 && shift || { echo "Unknown: $1"; exit 1; } ;;
  esac
done
[[ -n "$ROOT" && -d "$ROOT" ]] || { usage; exit 1; }
ROOT=$(cd "$ROOT" && pwd)
REPORT="$ROOT/../clofix-harden-report-$(date +%Y%m%d%H%M%S).txt"
[[ "$ROOT" == "/" ]] && REPORT="/var/lib/clofix/clofix-harden-report-$(date +%Y%m%d%H%M%S).txt" && mkdir -p /var/lib/clofix
: > "$REPORT"

apply() {
  local dest=$1
  shift
  echo "APPLY $dest $*" | tee -a "$REPORT"
  [[ $DRY -eq 1 ]] && return 0
  "$@"
}

backup() {
  local f=$1
  [[ $BACKUP -eq 1 && -e "$f" && ! -e "${f}.clofix-harden-bak" ]] || return 0
  cp -a "$f" "${f}.clofix-harden-bak"
  echo "BACKUP $f" >> "$REPORT"
}

echo "# harden $ROOT" >> "$REPORT"

# Secure tmp sticky bits
apply "$ROOT/tmp" mkdir -p "$ROOT/tmp" "$ROOT/var/tmp"
apply chmod chmod 1777 "$ROOT/tmp" "$ROOT/var/tmp"

# Kernel security sysctl baseline (guest). Prefer canonical file under config/sysctl/.
mkdir -p "$ROOT/etc/sysctl.d"
SYSCTL_SRC="$TREE/config/sysctl/99-clofix-kernel-security.conf"
SYSCTL="$ROOT/etc/sysctl.d/99-clofix-kernel-security.conf"
LEGACY="$ROOT/etc/sysctl.d/99-clofix-hardening.conf"
backup "$SYSCTL"
backup "$LEGACY"
if [[ $DRY -eq 0 ]]; then
  if [[ -f "$SYSCTL_SRC" ]]; then
    install -m 0644 "$SYSCTL_SRC" "$SYSCTL"
  else
    echo "WARN missing $SYSCTL_SRC" | tee -a "$REPORT"
  fi
  # Replace older short baseline if present so only one CloFix kernel policy applies.
  [[ -f "$LEGACY" ]] && rm -f "$LEGACY"
fi
echo "WRITE $SYSCTL" >> "$REPORT"

# SSH: validate-only recommendations file (do not lock out)
mkdir -p "$ROOT/etc/ssh/sshd_config.d" "$ROOT/etc/clofix/guard"
SSHREC="$ROOT/etc/clofix/guard/ssh-recommendations.conf"
[[ $DRY -eq 0 ]] && cat > "$SSHREC" <<'EOF'
# Recommended (apply manually after verifying key login):
# PasswordAuthentication no
# PermitRootLogin prohibit-password
# KbdInteractiveAuthentication no
# CloFix Guard will NOT auto-apply these when they would remove sole admin access.
EOF
echo "WRITE $SSHREC" >> "$REPORT"

# Install Guard config + empty malware DB
install -m 0644 "$TREE/config/security/config.json" "$ROOT/etc/clofix/guard/config.json" 2>/dev/null || {
  mkdir -p "$ROOT/etc/clofix/guard"
  [[ $DRY -eq 0 ]] && cp "$TREE/config/security/config.json" "$ROOT/etc/clofix/guard/config.json"
}
[[ $DRY -eq 0 ]] && cp "$TREE/config/security/malware-hashes.txt" "$ROOT/etc/clofix/guard/malware-hashes.txt"

# systemd unit if systemd layout exists
if [[ -d "$ROOT/lib/systemd/system" || -d "$ROOT/usr/lib/systemd/system" ]]; then
  dest="$ROOT/lib/systemd/system"
  [[ -d "$ROOT/usr/lib/systemd/system" ]] && dest="$ROOT/usr/lib/systemd/system"
  [[ $DRY -eq 0 ]] && cp "$TREE/config/systemd/clofix-guard.service" "$dest/clofix-guard.service"
  echo "WRITE $dest/clofix-guard.service" >> "$REPORT"
fi

# Install binaries if built (prefer dist/, then arch-specific)
for b in clofix clofix-guard; do
  src=""
  for cand in "$TREE/dist/$b" "$TREE/dist/linux-arm64/$b" "$TREE/dist/linux-amd64/$b"; do
    [[ -x "$cand" ]] && src=$cand && break
  done
  if [[ -n "$src" ]]; then
    [[ $DRY -eq 0 ]] && install -m 0755 "$src" "$ROOT/usr/bin/$b"
    echo "WRITE /usr/bin/$b from $src" >> "$REPORT"
  else
    echo "WARN missing binary $b - run scripts/build-binaries.sh" >> "$REPORT"
  fi
done

mkdir -p "$ROOT/var/lib/clofix/guard"
chmod 0750 "$ROOT/var/lib/clofix/guard" 2>/dev/null || true

# nftables baseline file (not applied automatically)
mkdir -p "$ROOT/etc/nftables.d"
[[ $DRY -eq 0 ]] && cp "$TREE/config/nftables/clofix-baseline.nft" "$ROOT/etc/nftables.d/clofix-baseline.nft" 2>/dev/null || true

echo "Harden complete. Report: $REPORT"
