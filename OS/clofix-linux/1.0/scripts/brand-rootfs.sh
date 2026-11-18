#!/usr/bin/env bash
# Apply CloFix Linux branding to an extracted Ubuntu rootfs (idempotent).
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TREE=$(cd "$SCRIPT_DIR/.." && pwd)
BRAND="$TREE/branding"
PKG="$TREE/packages/clofix-release"
APT_CFG="$TREE/config/apt"
ROOT=""
BACKUP=1
STRIP_UBUNTU=1

usage() {
  cat <<EOF
Usage: $0 <rootfs-dir> [--no-backup] [--keep-ubuntu-user]
Idempotent CloFix Linux branding for an Ubuntu-based rootfs tree.
EOF
}

# Portable in-place sed (GNU + BSD)
sed_inplace() {
  local file=$1
  shift
  local tmp
  tmp=$(mktemp)
  sed "$@" "$file" > "$tmp"
  cat "$tmp" > "$file"
  rm -f "$tmp"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-backup) BACKUP=0; shift ;;
    --keep-ubuntu-user) STRIP_UBUNTU=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      if [[ -z "$ROOT" ]]; then ROOT=$1; shift
      else echo "Unknown arg: $1" >&2; exit 1; fi
      ;;
  esac
done

[[ -n "$ROOT" && -d "$ROOT" ]] || { usage; exit 1; }
ROOT=$(cd "$ROOT" && pwd)

REPORT_DIR=$ROOT
if [[ "$ROOT" == "/" ]]; then
  REPORT_DIR=/var/lib/clofix
  mkdir -p "$REPORT_DIR"
fi
REPORT="$REPORT_DIR/clofix-brand-report-$(date +%Y%m%d%H%M%S).txt"
CHANGED=()

log_change() { CHANGED+=("$1"); echo "$1" >> "$REPORT"; }

backup_file() {
  local f=$1
  [[ $BACKUP -eq 1 && -e "$f" ]] || return 0
  local b="${f}.clofix-bak"
  [[ -e "$b" ]] && return 0
  cp -a "$f" "$b"
}

# Write without unlinking when possible (EBUSY on bind-mounted /etc/hostname etc.)
safe_install() {
  local src=$1 dest=$2 mode=${3:-0644}
  mkdir -p "$(dirname "$dest")"
  if [[ -L "$dest" ]]; then
    rm -f "$dest"
    install -m "$mode" "$src" "$dest"
  elif [[ -e "$dest" ]]; then
    cat "$src" > "$dest"
    chmod "$mode" "$dest" 2>/dev/null || true
  else
    install -m "$mode" "$src" "$dest"
  fi
}

install_file() {
  local src=$1 dest=$2 mode=${3:-0644}
  backup_file "$dest"
  safe_install "$src" "$dest" "$mode"
  log_change "WRITE $dest"
}

write_text() {
  local dest=$1 mode=${2:-0644}
  local tmp
  tmp=$(mktemp)
  cat > "$tmp"
  backup_file "$dest"
  safe_install "$tmp" "$dest" "$mode"
  rm -f "$tmp"
  log_change "WRITE $dest"
}

detect_ubuntu_codename() {
  local c=
  if [[ -r "$ROOT/etc/os-release" ]]; then
    c=$(grep -E '^VERSION_CODENAME=' "$ROOT/etc/os-release" | head -1 | cut -d= -f2- | tr -d '"')
  fi
  if [[ -z "$c" && -r "$ROOT/usr/lib/os-release" ]]; then
    c=$(grep -E '^VERSION_CODENAME=' "$ROOT/usr/lib/os-release" | head -1 | cut -d= -f2- | tr -d '"')
  fi
  if [[ -r "$ROOT/etc/os-release" ]]; then
    local u
    u=$(grep -E '^UBUNTU_CODENAME=' "$ROOT/etc/os-release" | head -1 | cut -d= -f2- | tr -d '"' || true)
    # Prefer Ubuntu packaging codename over CloFix VERSION_CODENAME=clofix
    if [[ -n "$u" && "$u" != "clofix" ]]; then
      c=$u
    elif [[ "$c" == "clofix" && -n "$u" ]]; then
      c=$u
    fi
  fi
  echo "${c:-noble}"
}

strip_ubuntu_user() {
  [[ $STRIP_UBUNTU -eq 1 ]] || return 0
  local u=ubuntu
  # Live root: prefer userdel so home + mail spool go too
  if [[ "$ROOT" == "/" ]] && command -v userdel >/dev/null 2>&1; then
    if id "$u" >/dev/null 2>&1; then
      userdel -r "$u" 2>/dev/null || userdel "$u" 2>/dev/null || true
      log_change "USERDEL ubuntu"
    fi
  fi
  for f in passwd shadow group gshadow subuid subgid; do
    local p="$ROOT/etc/$f"
    [[ -f "$p" ]] || continue
    if grep -q "^${u}:" "$p"; then
      backup_file "$p"
      grep -v "^${u}:" "$p" > "${p}.new"
      cat "${p}.new" > "$p"
      rm -f "${p}.new"
      log_change "STRIP_USER $p"
    fi
  done
  if [[ -e "$ROOT/home/ubuntu" || -L "$ROOT/home/ubuntu" ]]; then
    rm -rf "$ROOT/home/ubuntu"
    log_change "RMDIR /home/ubuntu"
  fi
}

: > "$REPORT"
echo "# CloFix brand report for $ROOT" >> "$REPORT"
echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$REPORT"

CODENAME=$(detect_ubuntu_codename)
echo "Ubuntu package codename: $CODENAME"

tmp_os=$(mktemp)
sed "s/^UBUNTU_CODENAME=.*/UBUNTU_CODENAME=${CODENAME}/" "$BRAND/os-release" > "$tmp_os"
if ! grep -q '^UBUNTU_CODENAME=' "$tmp_os"; then
  echo "UBUNTU_CODENAME=${CODENAME}" >> "$tmp_os"
fi
install_file "$tmp_os" "$ROOT/etc/os-release" 0644
mkdir -p "$ROOT/usr/lib"
install_file "$tmp_os" "$ROOT/usr/lib/os-release" 0644
rm -f "$tmp_os"

install_file "$BRAND/lsb-release" "$ROOT/etc/lsb-release" 0644
install_file "$BRAND/issue" "$ROOT/etc/issue" 0644
install_file "$BRAND/issue.net" "$ROOT/etc/issue.net" 0644
install_file "$BRAND/motd" "$ROOT/etc/motd" 0644

if [[ -f "$ROOT/etc/debian_version" ]]; then
  log_change "KEEP /etc/debian_version ($(cat "$ROOT/etc/debian_version"))"
fi

# hostname/hosts may be bind-mounted read-only in Docker BuildKit - best-effort
if printf 'clofix-linux\n' > "$ROOT/etc/hostname" 2>/dev/null; then
  log_change "WRITE /etc/hostname"
else
  log_change "SKIP /etc/hostname (read-only)"
fi
if [[ -f "$ROOT/etc/hosts" ]]; then
  if ! grep -qE '[[:space:]]clofix-linux([[:space:]]|$)' "$ROOT/etc/hosts"; then
    tmp_hosts=$(mktemp)
    if grep -qE '^127\.0\.1\.1' "$ROOT/etc/hosts"; then
      sed -E 's/^127\.0\.1\.1.*/127.0.1.1	clofix-linux/' "$ROOT/etc/hosts" > "$tmp_hosts"
    else
      cat "$ROOT/etc/hosts" > "$tmp_hosts"
      printf '127.0.1.1\tclofix-linux\n' >> "$tmp_hosts"
    fi
    if cat "$tmp_hosts" > "$ROOT/etc/hosts" 2>/dev/null; then
      log_change "WRITE /etc/hosts"
    else
      log_change "SKIP /etc/hosts (read-only; instance lifecycle sets hostname)"
    fi
    rm -f "$tmp_hosts"
  fi
fi

install_file "$BRAND/clofix-prompt.sh" "$ROOT/etc/profile.d/clofix-prompt.sh" 0644
install_file "$BRAND/99-clofix-history.sh" "$ROOT/etc/profile.d/99-clofix-history.sh" 0644
install_file "$BRAND/skel.bashrc" "$ROOT/etc/skel/.bashrc" 0644
if [[ -d "$ROOT/root" ]]; then
  install_file "$BRAND/skel.bashrc" "$ROOT/root/.bashrc" 0644
fi

mkdir -p "$ROOT/etc/update-motd.d"
install_file "$BRAND/00-clofix-header" "$ROOT/etc/update-motd.d/00-clofix-header" 0755
shopt -s nullglob
for f in "$ROOT/etc/update-motd.d"/*; do
  base=$(basename "$f")
  [[ "$base" == "00-clofix-header" ]] && continue
  [[ "$base" == *.clofix-disabled ]] && continue
  if [[ -x "$f" || -f "$f" ]]; then
    backup_file "$f"
    mv "$f" "${f}.clofix-disabled"
    log_change "DISABLE /etc/update-motd.d/$base"
  fi
done
shopt -u nullglob

mkdir -p "$ROOT/etc/apt/apt.conf.d"
install_file "$APT_CFG/99clofix-release-upgrade" "$ROOT/etc/apt/apt.conf.d/99clofix-release-upgrade" 0644
install_file "$APT_CFG/20auto-upgrades-clofix" "$ROOT/etc/apt/apt.conf.d/20auto-upgrades-clofix" 0644

if [[ -f "$ROOT/etc/update-manager/release-upgrades" ]]; then
  backup_file "$ROOT/etc/update-manager/release-upgrades"
  sed_inplace "$ROOT/etc/update-manager/release-upgrades" -E 's/^Prompt=.*/Prompt=never/'
  log_change "WRITE /etc/update-manager/release-upgrades Prompt=never"
fi

install_file "$PKG/etc/clofix/version" "$ROOT/etc/clofix/version" 0644
install_file "$PKG/etc/clofix/upgrade-policy" "$ROOT/etc/clofix/upgrade-policy" 0644

# Prefer Go-built CLI/Guard when present; else shell fallback from package tree
mkdir -p "$ROOT/usr/bin" "$ROOT/etc/clofix/guard"
if [[ -x "$TREE/dist/clofix" ]]; then
  install_file "$TREE/dist/clofix" "$ROOT/usr/bin/clofix" 0755
elif [[ -x "$TREE/dist/linux-arm64/clofix" ]] && [[ "$(uname -m)" == "aarch64" || "$(uname -m)" == "arm64" ]]; then
  install_file "$TREE/dist/linux-arm64/clofix" "$ROOT/usr/bin/clofix" 0755
elif [[ -x "$TREE/dist/linux-amd64/clofix" ]]; then
  install_file "$TREE/dist/linux-amd64/clofix" "$ROOT/usr/bin/clofix" 0755
else
  install_file "$PKG/usr/bin/clofix" "$ROOT/usr/bin/clofix" 0755
fi
if [[ -x "$TREE/dist/clofix-guard" ]]; then
  install_file "$TREE/dist/clofix-guard" "$ROOT/usr/bin/clofix-guard" 0755
elif [[ -x "$TREE/dist/linux-arm64/clofix-guard" ]] && [[ "$(uname -m)" == "aarch64" || "$(uname -m)" == "arm64" ]]; then
  install_file "$TREE/dist/linux-arm64/clofix-guard" "$ROOT/usr/bin/clofix-guard" 0755
elif [[ -x "$TREE/dist/linux-amd64/clofix-guard" ]]; then
  install_file "$TREE/dist/linux-amd64/clofix-guard" "$ROOT/usr/bin/clofix-guard" 0755
fi
if [[ -f "$TREE/config/security/config.json" ]]; then
  install_file "$TREE/config/security/config.json" "$ROOT/etc/clofix/guard/config.json" 0644
fi
if [[ -f "$TREE/config/security/malware-hashes.txt" ]]; then
  install_file "$TREE/config/security/malware-hashes.txt" "$ROOT/etc/clofix/guard/malware-hashes.txt" 0644
fi
# Guest kernel security sysctl (net/fs + best-effort kernel.* on shared kernel)
if [[ -f "$TREE/config/sysctl/99-clofix-kernel-security.conf" ]]; then
  mkdir -p "$ROOT/etc/sysctl.d"
  install_file "$TREE/config/sysctl/99-clofix-kernel-security.conf" "$ROOT/etc/sysctl.d/99-clofix-kernel-security.conf" 0644
  # Drop legacy short baseline if an older image left it behind.
  if [[ -f "$ROOT/etc/sysctl.d/99-clofix-hardening.conf" ]]; then
    rm -f "$ROOT/etc/sysctl.d/99-clofix-hardening.conf"
    log_change "REMOVE /etc/sysctl.d/99-clofix-hardening.conf (superseded)"
  fi
fi

mkdir -p "$ROOT/var/lib/clofix"
write_text "$ROOT/var/lib/clofix/branded" 0644 <<EOF
clofix-linux=1.0
ubuntu_codename=$CODENAME
branded_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

if [[ -f "$ROOT/etc/default/grub" ]]; then
  backup_file "$ROOT/etc/default/grub"
  if grep -q '^GRUB_DISTRIBUTOR=' "$ROOT/etc/default/grub"; then
    sed_inplace "$ROOT/etc/default/grub" -E 's/^GRUB_DISTRIBUTOR=.*/GRUB_DISTRIBUTOR="CloFix Linux"/'
  else
    echo 'GRUB_DISTRIBUTOR="CloFix Linux"' >> "$ROOT/etc/default/grub"
  fi
  log_change "WRITE /etc/default/grub GRUB_DISTRIBUTOR"
fi
if [[ -d "$ROOT/usr/share/plymouth/themes" ]]; then
  write_text "$ROOT/etc/clofix/plymouth-note" 0644 <<< "Plymouth present; ship CloFix theme assets under branding/plymouth when required."
fi

# cloud-init: CloFix product branding (distro module must stay "ubuntu" for APT/cloud-init)
if [[ -d "$ROOT/etc/cloud" ]]; then
  write_text "$ROOT/etc/cloud/build.info" 0644 <<EOF
build_name: CloFix Linux 1.0
product: CloFix Linux
vendor: CloFix
homepage: https://clofix.com
serial: $(date -u +%Y%m%d)
package_base: ubuntu-${CODENAME}
note: User-facing OS is CloFix Linux; Ubuntu is the package source only
EOF
  mkdir -p "$ROOT/etc/cloud/cloud.cfg.d"
  write_text "$ROOT/etc/cloud/cloud.cfg.d/99-clofix.cfg" 0644 <<'EOF'
# CloFix Linux - cloud-init defaults
# Product name: CloFix Linux (see /etc/os-release).
# system_info.distro must remain a cloud-init module id ("ubuntu") so
# package/APT helpers work; it is NOT the marketing OS name.
system_info:
  distro: ubuntu
  default_user:
    name: root
    lock_passwd: false
EOF
fi

rm -f "$ROOT/etc/ssh/ssh_host_"* 2>/dev/null || true
log_change "RM ssh_host_* keys"

strip_ubuntu_user

mkdir -p "$ROOT/tmp" "$ROOT/var/tmp"
chmod 1777 "$ROOT/tmp" "$ROOT/var/tmp"

echo "Branding complete. ${#CHANGED[@]} actions. Report: $REPORT"
printf '%s\n' "${CHANGED[@]}"
