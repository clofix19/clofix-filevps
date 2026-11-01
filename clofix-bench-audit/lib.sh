#!/usr/bin/env bash
# lib.sh — shared helpers + backend adapters for CloFix FileVPS / Incus / LXC
# BACKEND must be set before sourcing: fx | incus | lxc

STORAGE_PATH="${STORAGE_PATH:-/var/lib}"
BASE_DISTRO="${BASE_DISTRO:-debian}"
BASE_REL="${BASE_REL:-bookworm}"
INCUS_IMAGE="${INCUS_IMAGE:-images:debian/12}"

FX_BIN="${FX_BIN:-clofix-worker}"
FX_IMAGE="${FX_IMAGE:-ubuntu}"
FX_VERSION="${FX_VERSION:-24.04}"
FX_RAM_MB="${FX_RAM_MB:-256}"
FX_CPU="${FX_CPU:-0.5}"
FX_DISK_MB="${FX_DISK_MB:-2048}"
FX_PIDS="${FX_PIDS:-256}"
FX_SWAP_MB="${FX_SWAP_MB:-0}"
FX_NETWORK="${FX_NETWORK:-1}"
FX_PROTECTION="${FX_PROTECTION:-0}"
FX_STATE_DIR="${CLOFIX_STATE_DIR:-/var/lib/clofix-filevps}"
[ -z "${FX_ROOTFS:-}" ] && FX_ROOTFS="$FX_STATE_DIR/instances/{name}/rootfs-mount"
# Optional override templates. When unset, the real clofix-worker fx CLI is used.
FX_CREATE="${FX_CREATE:-}"
FX_START="${FX_START:-}"
FX_EXEC="${FX_EXEC:-}"
FX_DESTROY="${FX_DESTROY:-}"
FX_FILE_READ="${FX_FILE_READ:-}"

HERE_LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FX_SSH_KEY="${FX_SSH_KEY:-$HERE_LIB/results/.bench-ssh.pub}"

tpl() { local t="${1//\{name\}/$2}"; eval "$t"; }

lxc_arch() { case "$(uname -m)" in x86_64) echo amd64;; aarch64) echo arm64;; *) uname -m;; esac; }

ensure_fx_ssh_key() {
  [ -f "$FX_SSH_KEY" ] && return 0
  mkdir -p "$(dirname "$FX_SSH_KEY")"
  local priv="${FX_SSH_KEY%.pub}"
  [ "$priv" = "$FX_SSH_KEY" ] && priv="$FX_SSH_KEY.key"
  ssh-keygen -q -t ed25519 -N "" -f "$priv" -C "clofix-bench"
  [ -f "$FX_SSH_KEY" ] || cp "$priv.pub" "$FX_SSH_KEY"
  chmod 600 "$priv" 2>/dev/null || true
  chmod 644 "$FX_SSH_KEY"
}

run_quiet() {
  local out rc
  out="$("$@" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '%s\n' "$out" >&2
    return "$rc"
  fi
  return 0
}

fx_create() {
  local n=$1 net="${2:-$FX_NETWORK}"
  ensure_fx_ssh_key
  if [ -n "$FX_CREATE" ]; then
    tpl "$FX_CREATE" "$n"
    return
  fi
  # Worker flag.Parse stops at the first positional, so flags must come before <id>.
  local args=(fx create
    --image "$FX_IMAGE" --version "$FX_VERSION"
    --ram-mb "$FX_RAM_MB" --cpu "$FX_CPU" --disk-mb "$FX_DISK_MB"
    --pids "$FX_PIDS" --swap-mb "$FX_SWAP_MB"
    --ssh-key "$FX_SSH_KEY" --hostname "$n")
  if [ "$net" = 1 ]; then args+=(--network); else args+=(--no-network); fi
  if [ "$FX_PROTECTION" = 1 ]; then args+=(--protection); else args+=(--no-protection); fi
  args+=("$n")
  run_quiet "$FX_BIN" "${args[@]}"
}

vps_create() {
  local n=$1 net="${2:-$FX_NETWORK}"
  case $BACKEND in
    fx)    fx_create "$n" "$net" ;;
    incus) run_quiet incus init "$INCUS_IMAGE" "$n" ;;
    lxc)   run_quiet lxc-create -n "$n" -t download -- -d "$BASE_DISTRO" -r "$BASE_REL" -a "$(lxc_arch)" ;;
    *) echo "unknown backend: $BACKEND" >&2; return 2 ;;
  esac
}

vps_start() {
  local n=$1
  case $BACKEND in
    fx)
      if [ -n "$FX_START" ]; then tpl "$FX_START" "$n"; else run_quiet "$FX_BIN" fx start "$n"; fi
      ;;
    incus) run_quiet incus start "$n" ;;
    lxc)   run_quiet lxc-start -n "$n" ;;
  esac
}

vps_exec() {
  local n=$1; shift
  export CFX_CMD="$*"
  case $BACKEND in
    fx)
      if [ -n "$FX_EXEC" ]; then tpl "$FX_EXEC" "$n"; else "$FX_BIN" fx exec "$n" -- sh -c "$CFX_CMD"; fi
      ;;
    incus) incus exec "$n" -- sh -c "$CFX_CMD" ;;
    lxc)   lxc-attach -n "$n" -- sh -c "$CFX_CMD" ;;
  esac
}

vps_destroy() {
  local n=$1
  case $BACKEND in
    fx)
      if [ -n "$FX_DESTROY" ]; then tpl "$FX_DESTROY" "$n"; else "$FX_BIN" fx delete "$n" >/dev/null 2>&1; fi
      ;;
    incus) incus delete -f "$n" >/dev/null 2>&1 ;;
    lxc)   lxc-destroy -n "$n" -f >/dev/null 2>&1 ;;
  esac
}

vps_rootfs() {
  local n=$1
  case $BACKEND in
    fx)    echo "${FX_ROOTFS//\{name\}/$n}" ;;
    incus) echo "${INCUS_ROOTFS:-/var/lib/incus/storage-pools/default/containers/$n/rootfs}" ;;
    lxc)   echo "/var/lib/lxc/$n/rootfs" ;;
  esac
}

wait_ready() { # name [timeout_s]
  local n=$1 to=${2:-60} i=0
  while ! vps_exec "$n" true >/dev/null 2>&1; do
    i=$((i+1)); [ "$i" -ge $((to*5)) ] && return 1; sleep 0.2
  done
}

need_backend() {
  case $BACKEND in
    fx)
      need_cmd "$FX_BIN"
      need_cmd ssh-keygen
      "$FX_BIN" version >/dev/null 2>&1 || { echo "cannot run $FX_BIN"; exit 1; }
      ;;
    incus) need_cmd incus ;;
    lxc)   need_cmd lxc-create; need_cmd lxc-start; need_cmd lxc-attach; need_cmd lxc-destroy ;;
    *) echo "usage: $0 <fx|incus|lxc>"; exit 2 ;;
  esac
}

# ---------------------------------------------------------------------------
# Tracking + cleanup (always runs on exit)
# ---------------------------------------------------------------------------
CREATED=()
track() { CREATED+=("$1"); }
cleanup() {
  trap - EXIT INT TERM
  echo "[cleanup] removing ${#CREATED[@]} instance(s)..."
  local n
  for n in "${CREATED[@]:-}"; do [ -n "$n" ] && vps_destroy "$n"; done
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Measurement helpers
# ---------------------------------------------------------------------------
now_ms()        { echo $(( $(date +%s%N) / 1000000 )); }
mem_used_kb()   { awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo; }
mem_avail_kb()  { awk '/MemAvailable/{print $2}' /proc/meminfo; }
mem_total_kb()  { awk '/MemTotal/{print $2}' /proc/meminfo; }
disk_used_b()   { df --output=used -B1 "$STORAGE_PATH" | tail -1 | tr -d ' '; }
disk_total_b()  { df --output=size -B1 "$STORAGE_PATH" | tail -1 | tr -d ' '; }
mi_kb()         { awk -v k="$1:" '$1==k{print $2}' /proc/meminfo; }
kmem_kb()       { echo $(( $(mi_kb Slab) + $(mi_kb KernelStack) + $(mi_kb PageTables) )); }
ksm_sharing()   { cat /sys/kernel/mm/ksm/pages_sharing 2>/dev/null || echo 0; }
proc_count()    { ls /proc | grep -c '^[0-9]'; }
ncpu()          { nproc; }

cpu_busy_pct() { # seconds -> host CPU busy % over interval
  local s=${1:-10} u n sy i w q sq t1 i1 t2 i2
  read -r _ u n sy i w q sq _ < /proc/stat; t1=$((u+n+sy+i+w+q+sq)); i1=$((i+w))
  sleep "$s"
  read -r _ u n sy i w q sq _ < /proc/stat; t2=$((u+n+sy+i+w+q+sq)); i2=$((i+w))
  awk -v t="$((t2-t1))" -v i="$((i2-i1))" 'BEGIN{ if(t<=0){print 0}else{printf "%.2f",100*(t-i)/t} }'
}

need_root() { [ "$(id -u)" -eq 0 ] || { echo "run as root"; exit 1; }; }
need_cmd()  { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1"; exit 1; }; }
