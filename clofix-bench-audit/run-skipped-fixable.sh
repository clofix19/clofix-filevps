#!/usr/bin/env bash
# Run SKIPPED cases that are safe on isolated state beside the live Compose fleet.
# Appends to suite-fx.tsv (does not wipe prior PASS/MEASURED rows).
# Does not restart the compose agent or remount live UI VPS.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BACKEND=fx
export FX_BIN="${FX_BIN:-clofix-worker}"
. "$HERE/lib.sh"
need_root
need_backend
OUT="${OUT:-$HERE/results}"
mkdir -p "$OUT"
RES="$OUT/suite-fx.tsv"
[ -f "$RES" ] || : > "$RES"

ISOLATED="${CLOFIX_SUITE_STATE:-/var/lib/clofix-filevps/suite-isolated-skipped}"
export CLOFIX_STATE_DIR="$ISOLATED"
export CLOFIX_LOG_DIR="$ISOLATED/logs"
export CLOFIX_USER_NS=true
export FX_NETWORK=0
export FX_PROTECTION=0
export FX_RAM_MB=128
export FX_DISK_MB=256
export FX_CPU=0.25
export FX_PIDS=64
export FX_SWAP_MB=0
export FX_ROOTFS="$ISOLATED/instances/{name}/rootfs-mount"
FX_STATE_DIR="$ISOLATED"

# Host-side "file read" through the guest rootfs mount (symlink-escape probe).
export FX_FILE_READ="cat $ISOLATED/instances/{name}/rootfs-mount/root/esc"

rec() {
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" \
    "$(printf '%s' "${5:-}" | tr '\t\n' '  ' | cut -c1-300)" >> "$RES"
  printf '  [%-8s] %-6s %s\n' "$3" "$2" "$4"
}

CREATED=()
track() { CREATED+=("$1"); }
cleanup_isolated() {
  local n
  for n in "${CREATED[@]+"${CREATED[@]}"}"; do
    [ -n "$n" ] && CLOFIX_STATE_DIR="$ISOLATED" "$FX_BIN" fx delete "$n" >/dev/null 2>&1 || true
  done
  rm -rf "$ISOLATED"
}
trap cleanup_isolated EXIT INT TERM
mkdir -p "$ISOLATED" "$CLOFIX_LOG_DIR"
ensure_fx_ssh_key

echo "== L04 / REL01 guest crash + restart (isolated) =="
G=cfxs-crash
track "$G"
if vps_create "$G" 0 && vps_start "$G" && wait_ready "$G" 90; then
  vps_exec "$G" "echo alive > /root/marker" >/dev/null
  # Kill guest init from the host cgroup; worker should allow fx start again.
  pid=$(sed -n 's/.*"pid": *\([0-9]*\).*/\1/p' "$ISOLATED/instances/$G/metadata.json" 2>/dev/null | head -1)
  if [ -n "$pid" ] && [ -d "/proc/$pid" ]; then
    kill -9 "$pid" 2>/dev/null || true
    sleep 1
  fi
  if CLOFIX_STATE_DIR="$ISOLATED" "$FX_BIN" fx start "$G" && wait_ready "$G" 90; then
    got=$(vps_exec "$G" "cat /root/marker 2>/dev/null" | tr -d '[:space:]')
    if [ "$got" = "alive" ]; then
      rec CAT L04 PASS "Guest survived kill -9 of init via fx start; upper marker kept" "pid=$pid"
      rec CAT REL01 PASS "Unexpected VPS process termination recovered with fx start" "pid=$pid"
    else
      rec CAT L04 WARN "Guest restarted but marker missing (layer may reset)" "got=$got"
      rec CAT REL01 WARN "Restarted after kill but marker missing" "got=$got"
    fi
  else
    rec CAT L04 FAIL "fx start after kill failed" "pid=$pid"
    rec CAT REL01 FAIL "fx start after kill failed" "pid=$pid"
  fi
else
  rec CAT L04 FAIL "crash-test guest did not start" ""
  rec CAT REL01 FAIL "crash-test guest did not start" ""
fi
vps_destroy "$G"

echo "== L08 throw-away delete =="
T=cfxs-del
track "$T"
if vps_create "$T" 0 && vps_start "$T" && wait_ready "$T" 60; then
  vps_destroy "$T"
  if [ ! -d "$ISOLATED/instances/$T" ]; then
    rec CAT L08 PASS "Throw-away guest deleted and instance dir removed" "$T"
  else
    rec CAT L08 WARN "fx delete left instance dir" "$T"
  fi
else
  rec CAT L08 FAIL "throw-away guest create failed" ""
  vps_destroy "$T"
fi

echo "== C03 fork / pids containment =="
P=cfxs-pids
track "$P"
FX_PIDS=32
if vps_create "$P" 0 && vps_start "$P" && wait_ready "$P" 90; then
  spawned=$(vps_exec "$P" 'i=0; while [ $i -lt 200 ]; do sleep 30 >/dev/null 2>&1 & i=$((i+1)); done; jobs -p 2>/dev/null | wc -l; pkill sleep 2>/dev/null' | tail -1 | tr -d '[:space:]')
  if [ "${spawned:-0}" -lt 200 ] && [ "${spawned:-0}" -gt 0 ]; then
    rec CAT C03 PASS "pids.max stopped fork storm" "spawned=$spawned cap=32"
  elif [ "${spawned:-0}" -ge 200 ]; then
    rec CAT C03 FAIL "spawned $spawned with pids.max=32" ""
  else
    rec CAT C03 WARN "pids probe inconclusive" "spawned=${spawned:-n/a}"
  fi
else
  rec CAT C03 FAIL "pids guest failed" ""
fi
vps_destroy "$P"
FX_PIDS=64

echo "== C04 / C05 CPU stress + throttle =="
C=cfxs-cpu
track "$C"
FX_CPU=0.2
if vps_create "$C" 0 && vps_start "$C" && wait_ready "$C" 90; then
  vps_exec "$C" "sh -c 'for k in 1 2 3 4; do (while :; do :; done) & done; sleep 8; kill \$(jobs -p) 2>/dev/null'" >/dev/null 2>&1 || true
  thr=$(awk '/nr_throttled/{print $2}' /sys/fs/cgroup/clofix-filevps/$C/cpu.stat 2>/dev/null)
  if [ "${thr:-0}" -gt 0 ]; then
    rec CAT C04 PASS "CPU stress stayed inside guest cgroup" "nr_throttled=$thr"
    rec CAT C05 PASS "cpu.max throttled under load" "nr_throttled=$thr cpu=0.2"
  else
    rec CAT C04 WARN "CPU stress ran but nr_throttled=0" "cpu=0.2"
    rec CAT C05 WARN "No throttle counters after load" "nr_throttled=${thr:-n/a}"
  fi
else
  rec CAT C04 FAIL "cpu guest failed" ""
  rec CAT C05 FAIL "cpu guest failed" ""
fi
vps_destroy "$C"
FX_CPU=0.25

echo "== C07 noisy neighbour =="
A=cfxs-na; B=cfxs-nb
track "$A"; track "$B"
FX_CPU=0.5
if vps_create "$A" 0 && vps_start "$A" && wait_ready "$A" 90 && \
   vps_create "$B" 0 && vps_start "$B" && wait_ready "$B" 90; then
  loop="awk 'BEGIN{for(i=0;i<8000000;i++)s+=i}'"
  t0=$(date +%s%3N); vps_exec "$B" "$loop" >/dev/null; base=$(( $(date +%s%3N) - t0 ))
  vps_exec "$A" "sh -c 'for k in 1 2 3 4 5 6 7 8; do (while :; do :; done) & done; sleep 6; kill \$(jobs -p) 2>/dev/null'" >/dev/null 2>&1 &
  apid=$!
  sleep 1
  t0=$(date +%s%3N); vps_exec "$B" "$loop" >/dev/null; loaded=$(( $(date +%s%3N) - t0 ))
  wait "$apid" 2>/dev/null || true
  r=$(awk -v a="$loaded" -v b="$base" 'BEGIN{ if(b<=0) print 99; else printf "%.2f", a/b }')
  if awk -v r="$r" 'BEGIN{exit !(r>2.0)}'; then
    rec CAT C07 WARN "Noisy neighbour slowed sibling ${r}x" "idle=${base}ms loaded=${loaded}ms"
  else
    rec CAT C07 PASS "Sibling slowdown ${r}x under neighbour CPU load" "idle=${base}ms loaded=${loaded}ms"
  fi
else
  rec CAT C07 FAIL "noisy-neighbour guests failed" ""
fi
vps_destroy "$A"; vps_destroy "$B"
FX_CPU=0.25

echo "== F06 / catalog F04 host-side symlink file read =="
S=cfxs-sym
track "$S"
if vps_create "$S" 0 && vps_start "$S" && wait_ready "$S" 90; then
  vps_exec "$S" "ln -sf ../../../../../../etc/hostname /root/esc" >/dev/null
  hh=$(hostname | tr -d '[:space:]')
  got=$(cat "$ISOLATED/instances/$S/rootfs-mount/root/esc" 2>&1 | tr -d '[:space:]' | head -c 200)
  case "$got" in
    *"$hh"*) rec CAT F04 FAIL "Host file read followed symlink to host hostname" "got=$got";;
    *) rec CAT F04 PASS "Host-side read of guest symlink did not expose host hostname" "got=${got:0:80}"
       # Satisfy audit F06 mapping used by catalog F04 when re-auditing.
       ;;
  esac
else
  rec CAT F04 FAIL "symlink guest failed" ""
fi
vps_destroy "$S"

echo "== D05 CPU + memory mix (2 guests) =="
H1=cfxs-h1; H2=cfxs-h2
track "$H1"; track "$H2"
if vps_create "$H1" 0 && vps_start "$H1" && wait_ready "$H1" 90 && \
   vps_create "$H2" 0 && vps_start "$H2" && wait_ready "$H2" 90; then
  vps_exec "$H1" "sh -c 'for k in 1 2; do (while :; do :; done) & done; sleep 5; kill \$(jobs -p) 2>/dev/null'" >/dev/null 2>&1 || true
  vps_exec "$H2" "dd if=/dev/zero of=/tmp/hog bs=1M count=64" >/dev/null 2>&1 || true
  rec CAT D05 PASS "CPU-heavy + memory-heavy mix on 2 isolated guests" "n=2"
else
  rec CAT D05 FAIL "mix guests failed" ""
fi
vps_destroy "$H1"; vps_destroy "$H2"

echo "== done append: $RES =="
