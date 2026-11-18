#!/usr/bin/env bash
# Inspect an extracted Ubuntu rootfs and write a branding inventory.
set -euo pipefail
ROOT="${1:-}"
OUT="${2:-}"
if [[ -z "$ROOT" || ! -d "$ROOT" ]]; then
  echo "Usage: $0 <rootfs-dir> [inventory.md]" >&2
  exit 1
fi
ROOT=$(cd "$ROOT" && pwd)
OUT="${OUT:-$ROOT/../clofix-inspect-$(date +%Y%m%d%H%M%S).md}"

# If ROOT is an archive path ending in .tar.gz, refuse unsafe members then note only
if [[ -f "$1" && "$1" == *.tar.gz ]]; then
  echo "Refusing direct mutate; listing archive safely..." >&2
  if tar -tzf "$1" | grep -E '(^/|\.\./)'; then
    echo "FAIL: unsafe archive members (absolute or ..)" >&2
    exit 1
  fi
fi

read_file() { [[ -e "$1" ]] && cat "$1" || echo "(missing)"; }
exists() { [[ -e "$1" ]] && echo yes || echo no; }
filetype() {
  if [[ -L "$1" ]]; then echo "symlink -> $(readlink "$1")"
  elif [[ -f "$1" ]]; then echo file
  elif [[ -d "$1" ]]; then echo dir
  else echo missing; fi
}

UBUNTU_CODENAME=$(grep -E '^VERSION_CODENAME=' "$ROOT/etc/os-release" 2>/dev/null | cut -d= -f2- | tr -d '"' || true)
VERSION_ID=$(grep -E '^VERSION_ID=' "$ROOT/etc/os-release" 2>/dev/null | cut -d= -f2- | tr -d '"' || true)
ARCH=$(chroot "$ROOT" dpkg --print-architecture 2>/dev/null || uname -m)
PKG=$( [[ -x "$ROOT/usr/bin/apt-get" ]] && echo apt || echo unknown )

{
  echo "# CloFix rootfs inspection"
  echo
  echo "- Root: \`$ROOT\`"
  echo "- Inspected: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- Detected VERSION_ID: ${VERSION_ID:-unknown}"
  echo "- Detected VERSION_CODENAME: ${UBUNTU_CODENAME:-unknown}"
  echo "- Architecture: ${ARCH:-unknown}"
  echo "- Package manager: $PKG"
  echo
  echo "## Release metadata"
  echo
  echo '```'
  echo "### /etc/os-release ($(filetype "$ROOT/etc/os-release"))"
  read_file "$ROOT/etc/os-release"
  echo
  echo "### /usr/lib/os-release ($(filetype "$ROOT/usr/lib/os-release"))"
  read_file "$ROOT/usr/lib/os-release"
  echo
  echo "### /etc/lsb-release ($(filetype "$ROOT/etc/lsb-release"))"
  read_file "$ROOT/etc/lsb-release"
  echo
  echo "### /etc/debian_version ($(filetype "$ROOT/etc/debian_version"))"
  read_file "$ROOT/etc/debian_version"
  echo '```'
  echo
  echo "## Branding inventory"
  echo
  echo "| Path | Present | Type |"
  echo "|------|---------|------|"
  for p in \
    etc/os-release usr/lib/os-release etc/issue etc/issue.net etc/motd \
    etc/update-motd.d etc/hostname etc/hosts etc/lsb-release etc/debian_version \
    etc/default/grub boot/grub etc/cloud etc/profile.d etc/skel \
    usr/share/plymouth usr/bin/clofix etc/clofix; do
    fp="$ROOT/$p"
    echo "| /$p | $(exists "$fp") | $(filetype "$fp") |"
  done
  echo
  echo "## Login / MOTD fragments"
  echo
  if [[ -d "$ROOT/etc/update-motd.d" ]]; then
    ls -la "$ROOT/etc/update-motd.d" | sed 's/^/    /'
  else
    echo "(no /etc/update-motd.d)"
  fi
  echo
  echo "## APT sources (names only)"
  echo
  ls -la "$ROOT/etc/apt/sources.list" "$ROOT/etc/apt/sources.list.d" 2>/dev/null | sed 's/^/    /' || true
  echo
  echo "## Cloud-init"
  echo
  ls -la "$ROOT/etc/cloud" 2>/dev/null | sed 's/^/    /' || echo "(none)"
  echo
  echo "## Users (human-looking)"
  echo
  awk -F: '$3>=1000 && $1!="nobody"{print}' "$ROOT/etc/passwd" 2>/dev/null | sed 's/^/    /' || true
  echo
  echo "## Notes"
  echo
  echo "- Preserve \`/etc/debian_version\` for dpkg/apt compatibility."
  echo "- Keep Ubuntu APT mirrors for codename \`${UBUNTU_CODENAME:-?}\`."
  echo "- CloFix branding should be owned by package \`clofix-release\`."
} > "$OUT"

echo "Wrote inventory: $OUT"
