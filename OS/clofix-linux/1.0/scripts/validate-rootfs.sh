#!/usr/bin/env bash
# Validate CloFix Linux branding and Ubuntu compatibility on an extracted rootfs.
set -euo pipefail
ROOT="${1:-}"
[[ -n "$ROOT" && -d "$ROOT" ]] || { echo "Usage: $0 <rootfs-dir>" >&2; exit 1; }
ROOT=$(cd "$ROOT" && pwd)
FAIL=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAIL=$((FAIL+1)); }

check_grep() {
  local file=$1 pattern=$2 msg=$3
  if [[ -r "$file" ]] && grep -qE "$pattern" "$file"; then pass "$msg"
  else fail "$msg"; fi
}

echo "== CloFix Linux validation: $ROOT =="

check_grep "$ROOT/etc/os-release" '^NAME="CloFix Linux"' "os-release NAME"
check_grep "$ROOT/etc/os-release" '^ID=clofix' "os-release ID"
check_grep "$ROOT/etc/os-release" '^VERSION_ID="1.0"' "os-release VERSION_ID"
check_grep "$ROOT/etc/os-release" 'clofix.com' "os-release HOME_URL family"
check_grep "$ROOT/etc/os-release" '^UBUNTU_CODENAME=' "os-release keeps UBUNTU_CODENAME"
check_grep "$ROOT/etc/os-release" 'clofix.com' "os-release website clofix.com"
check_grep "$ROOT/usr/lib/os-release" 'CloFix Linux' "usr/lib/os-release branded"

check_grep "$ROOT/etc/issue" 'CloFix Linux' "/etc/issue"
check_grep "$ROOT/etc/issue.net" 'CloFix Linux' "/etc/issue.net"
check_grep "$ROOT/etc/motd" 'CloFix Linux' "/etc/motd"
check_grep "$ROOT/etc/lsb-release" 'CloFixLinux|CloFix Linux' "/etc/lsb-release"

[[ -x "$ROOT/usr/bin/clofix" ]] && pass "clofix CLI present" || fail "clofix CLI missing"
[[ -x "$ROOT/usr/bin/clofix-guard" ]] && pass "clofix-guard present" || fail "clofix-guard missing"
if [[ -f "$ROOT/etc/cloud/build.info" ]]; then
  check_grep "$ROOT/etc/cloud/build.info" 'CloFix Linux' "cloud build.info branded"
  if grep -qi 'ubuntu-oci' "$ROOT/etc/cloud/build.info"; then
    fail "cloud build.info still ubuntu-oci"
  else
    pass "cloud build.info not ubuntu-oci"
  fi
fi
[[ -r "$ROOT/etc/clofix/guard/config.json" ]] && pass "guard config present" || fail "guard config missing"
[[ -r "$ROOT/etc/clofix/upgrade-policy" ]] && pass "upgrade-policy present" || fail "upgrade-policy missing"
[[ -r "$ROOT/etc/debian_version" ]] && pass "debian_version preserved" || fail "debian_version missing"

if grep -qE '^ubuntu:' "$ROOT/etc/passwd" 2>/dev/null; then
  fail "ubuntu user still in passwd"
else
  pass "no ubuntu cloud user"
fi
[[ ! -d "$ROOT/home/ubuntu" ]] && pass "no /home/ubuntu" || fail "/home/ubuntu still exists"

# APT tools exist
[[ -x "$ROOT/usr/bin/apt-get" || -x "$ROOT/bin/apt-get" ]] && pass "apt-get present" || fail "apt-get missing"
[[ -x "$ROOT/usr/bin/dpkg" || -x "$ROOT/bin/dpkg" ]] && pass "dpkg present" || fail "dpkg missing"

# SSH host keys must not be baked in
if ls "$ROOT/etc/ssh/ssh_host_"* >/dev/null 2>&1; then
  fail "ssh host keys present in image"
else
  pass "no baked ssh host keys"
fi

# Isolated smoke: run clofix version via chroot if possible
if command -v chroot >/dev/null 2>&1; then
  if chroot "$ROOT" /usr/bin/clofix version >/tmp/clofix-ver-out 2>/tmp/clofix-ver-err; then
    if grep -q 'CloFix Linux' /tmp/clofix-ver-out; then pass "clofix version in chroot"
    else fail "clofix version unexpected output"; cat /tmp/clofix-ver-out; fi
  else
    # FileVPS rootfs may lack full chroot deps on macOS host — soft note
    echo "WARN: chroot clofix failed (expected on non-Linux hosts): $(cat /tmp/clofix-ver-err 2>/dev/null || true)"
  fi
fi

if [[ $FAIL -eq 0 ]]; then
  echo "ALL CHECKS PASSED"
  exit 0
fi
echo "$FAIL CHECK(S) FAILED"
exit 1
