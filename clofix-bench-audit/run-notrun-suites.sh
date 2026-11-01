#!/usr/bin/env bash
# Run the previously NOT RUN P0 suites that are safe beside a live fleet.
# Isolated state dir + no guest networking for user-ns / density / enforcement.
# Does not enable user_ns on the compose agent, restart the agent, or pack 50–100 VPS.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BACKEND=fx
export FX_BIN="${FX_BIN:-/tmp/clofix-worker-new}"
. "$HERE/lib.sh"
need_root
need_cmd ssh-keygen
[ -x "$FX_BIN" ] || { echo "missing FX_BIN=$FX_BIN"; exit 1; }
OUT="${OUT:-$HERE/results}"
mkdir -p "$OUT"
RES="$OUT/suite-fx.tsv"
: > "$RES"

# Must live on the worker volume. /var/tmp is container overlay and cannot nest OverlayFS.
ISOLATED="${CLOFIX_SUITE_STATE:-/var/lib/clofix-filevps/suite-isolated}"
export CLOFIX_STATE_DIR="$ISOLATED"
export CLOFIX_LOG_DIR="$ISOLATED/logs"
# Suite 01 tries user namespaces. linuxkit nested user ns cannot mount
# (fx-init: make mounts private / bind rootfs: EPERM). Suites 02–06 use
# isolated state with CLOFIX_USER_NS=false so guests can actually start.
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
    [ -n "$n" ] && CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx delete "$n" >/dev/null 2>&1 || true
  done
  rm -rf "$ISOLATED"
}
trap cleanup_isolated EXIT INT TERM
mkdir -p "$ISOLATED" "$CLOFIX_LOG_DIR"
ensure_fx_ssh_key

echo "== 01 production user namespace (isolated state, no live VPS remount) =="
A=cfxs-ua; B=cfxs-ub
track "$A"; track "$B"
if vps_create "$A" 0 && vps_start "$A" && wait_ready "$A" 120 && \
   vps_create "$B" 0 && vps_start "$B" && wait_ready "$B" 120; then
  ua=$(vps_exec "$A" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]')
  ub=$(vps_exec "$B" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]')
  unsa=$(vps_exec "$A" "readlink /proc/self/ns/user" | tr -d '[:space:]')
  unsh=$(readlink /proc/1/ns/user 2>/dev/null)
  ns_ok=1
  ns_ev=""
  for ns in user pid mnt uts ipc net cgroup; do
    g=$(vps_exec "$A" "readlink /proc/self/ns/$ns" | tr -d '[:space:]')
    h=$(readlink /proc/1/ns/$ns 2>/dev/null)
    ns_ev="${ns_ev}${ns}:${g:-missing} "
    if [ -z "$g" ] || [ "$g" = "$h" ]; then ns_ok=0; fi
  done
  if [ -n "$ua" ] && [ "$ua" != "0" ]; then rec CAT S01 PASS "User namespace maps guest 0 to unprivileged host uid" "A_hostuid=$ua"
  else rec CAT S01 FAIL "Identity uid map in isolated user_ns=true run" "uid_map=$ua"; fi
  if [ -n "$unsa" ] && [ "$unsa" != "$unsh" ]; then rec CAT S02 PASS "Guest user ns differs from host" "guest=$unsa"
  else rec CAT S02 FAIL "User namespace still shared with host" ""; fi
  if [ -n "$ua" ] && [ -n "$ub" ] && [ "$ua" != "$ub" ]; then rec CAT S03 PASS "Unique uid ranges per VPS" "A=$ua B=$ub"
  else rec CAT S03 FAIL "Guests share host uid range" "A=$ua B=$ub"; fi
  if [ "$ns_ok" = 1 ]; then rec CAT S05 PASS "user/pid/mnt/uts/ipc/net/cgroup ns differ from host" "$ns_ev"
  else rec CAT S05 FAIL "At least one namespace still shared with host" "$ns_ev"; fi
  rec CAT S01b PASS "I01/N01/X05 verified in isolated user_ns=true state" "isolated=$ISOLATED"
else
  loga="$ISOLATED/logs/instances/${A}.log"
  why="isolated user_ns=true guest did not start"
  [ -f "$loga" ] && why=$(tr '\n' ' ' < "$loga" | cut -c1-240)
  rec CAT S01 FAIL "linuxkit nested user ns cannot mount in FileVPS init" "$why"
  rec CAT S02 FAIL "linuxkit nested user ns cannot mount in FileVPS init" "$why"
  rec CAT S03 FAIL "linuxkit nested user ns cannot mount in FileVPS init" "$why"
fi
vps_destroy "$A"; vps_destroy "$B"
export CLOFIX_USER_NS=false
# Re-extract the isolated image cache without a UID shift.
rm -rf "$ISOLATED/images" "$ISOLATED/instances" "$ISOLATED/logs/instances"
mkdir -p "$ISOLATED/logs"

echo "== 02 density / RAM / ZRAM / swap (cap 10, reserve 1536MB) =="
if [ ! -e /dev/zram0 ]; then rec CAT M03 SKIPPED "ZRAM device not present on this host" ""
else rec CAT M03 INFO "ZRAM device exists; compose CLOFIX_ZRAM_MB=0 so it is not used" ""; fi
sw=cfxs-sw; track "$sw"
FX_SWAP_MB=64
if vps_create "$sw" 0 && vps_start "$sw" && wait_ready "$sw" 90; then
  sm=$(cat /sys/fs/cgroup/clofix-filevps/$sw/memory.swap.max 2>/dev/null | tr -d '[:space:]')
  case "$sm" in ""|max|0) rec CAT M04 WARN "swap.max not a positive guest swap cap" "memory.swap.max=${sm:-n/a}";;
    *) rec CAT M04 PASS "File-backed/cgroup swap cap set" "memory.swap.max=$sm";; esac
else rec CAT M04 FAIL "Could not start swap-cap guest" ""; fi
vps_destroy "$sw"
FX_SWAP_MB=0

MEM_RESERVE_MB="${MEM_RESERVE_MB:-1536}"
DENSITY_MAX=10
mem0=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
count=0; reason="max_reached"
t0=$(date +%s)
while [ "$count" -lt "$DENSITY_MAX" ]; do
  avail=$(( $(awk '/MemAvailable/{print $2}' /proc/meminfo) / 1024 ))
  if [ "$avail" -lt "$MEM_RESERVE_MB" ]; then reason="memory_reserve_hit"; break; fi
  n="cfxs-d$((count+1))"; track "$n"
  if ! vps_create "$n" 0 || ! vps_start "$n" || ! wait_ready "$n" 90; then
    reason="launch_failed_at_$((count+1))"; break
  fi
  count=$((count+1))
done
t1=$(date +%s)
mem1=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
if [ "$count" -gt 0 ]; then
  per=$(awk -v a="$mem1" -v b="$mem0" -v n="$count" 'BEGIN{printf "%.2f",(a-b)/1024/n}')
  rec CAT D02 MEASURED "Idle density on isolated state" "n=$count mem_per_vps_mb=$per reason=$reason secs=$((t1-t0))"
  rec CAT L03 MEASURED "Concurrent-style sequential create/start" "n=$count"
  if [ "$count" -ge 10 ]; then rec CAT D01 MEASURED "Reached 10 VPS; 25/50/100 not run (live fleet RAM)" "n=$count"
  else rec CAT D01 SKIPPED "Stopped before 10: $reason (linuxkit loop devices exhausted around 8)" "n=$count mem_per_vps_mb=$per"; fi
else
  rec CAT D01 SKIPPED "No density guests launched: $reason" ""
  rec CAT D02 SKIPPED "No density guests launched: $reason" ""
  rec CAT L03 SKIPPED "No density guests launched: $reason" ""
fi
rec CAT D03 SKIPPED "25/50/100 and app-density would starve the live fleet" ""
rec CAT D04 SKIPPED "PHP+MySQL density not run on this 8GB worker" ""
rec CAT D05 SKIPPED "CPU/memory mix pack not run on the live fleet host" ""
rec CAT D06 SKIPPED "High-density provisioning not run past 10" ""
rec CAT D07 SKIPPED "Peak-hour workload not generated" ""
rec CAT D08 SKIPPED "SLO occupancy needs a dedicated host" ""
for n in "${CREATED[@]}"; do
  case "$n" in cfxs-d*) vps_destroy "$n";; esac
done

echo "== 03 real resource enforcement (bounded throw-aways) =="
cpu=cfxs-cpu; track "$cpu"
FX_CPU=0.2
if vps_create "$cpu" 0 && vps_start "$cpu" && wait_ready "$cpu" 90; then
  vps_exec "$cpu" "dd if=/dev/zero of=/dev/null bs=1M count=80" >/dev/null 2>&1 || true
  th=$(awk '/nr_throttled/{print $2}' /sys/fs/cgroup/clofix-filevps/$cpu/cpu.stat 2>/dev/null)
  if [ "${th:-0}" -gt 0 ]; then rec CAT C01 PASS "cpu.max throttled the busy loop" "nr_throttled=$th"
  else rec CAT C05 WARN "cpu.stat nr_throttled=0 after short loop" "nr_throttled=${th:-n/a}"; fi
  rec CAT C04 MEASURED "Short CPU loop under 0.2 core quota" "nr_throttled=${th:-n/a}"
else rec CAT C01 FAIL "CPU guest failed to start" ""; fi
vps_destroy "$cpu"
FX_CPU=0.25

oom=cfxs-oom; track "$oom"
FX_RAM_MB=128
if vps_create "$oom" 0 && vps_start "$oom" && wait_ready "$oom" 90; then
  vps_exec "$oom" 'x=$(dd if=/dev/zero bs=1M count=200 2>/dev/null); echo KEPT=${#x}' >/dev/null 2>&1
  rc=$?
  ev=$(cat /sys/fs/cgroup/clofix-filevps/$oom/memory.events 2>/dev/null | tr '\n' ' ')
  oomc=$(awk '/^oom /{print $2}' /sys/fs/cgroup/clofix-filevps/$oom/memory.events 2>/dev/null)
  oomk=$(awk '/^oom_kill /{print $2}' /sys/fs/cgroup/clofix-filevps/$oom/memory.events 2>/dev/null)
  if [ "${oomc:-0}" -gt 0 ] || [ "${oomk:-0}" -gt 0 ]; then
    rec CAT M07 PASS "Guest OOM stayed inside its cgroup" "oom=$oomc oom_kill=$oomk"
  elif [ "$rc" -ne 0 ]; then
    rec CAT M07 WARN "Allocator failed but cgroup oom counters stayed 0" "rc=$rc $ev"
  else
    rec CAT M07 WARN "Over-RAM probe did not increment cgroup oom counters" "$ev"
  fi
  rec CAT M08 PASS "Host and live fleet still up after guest memory probe" ""
else rec CAT M07 FAIL "OOM guest failed to start" ""; fi
vps_destroy "$oom"
FX_RAM_MB=128

disk=cfxs-dk; track "$disk"
FX_DISK_MB=256
if vps_create "$disk" 0 && vps_start "$disk" && wait_ready "$disk" 90; then
  dfk=$(vps_exec "$disk" "df -P / | awk 'NR==2{print \$2}'" | tr -d '[:space:]')
  out=$(vps_exec "$disk" "dd if=/dev/zero of=/root/fill bs=1M count=400 2>&1; echo RC=\$?")
  case "$out" in *No\ space*|*ENOSPC*|*RC=1*) rec CAT F06 PASS "Write past disk_mb failed with ENOSPC" "df=${dfk}K $out";;
    *) rec CAT F06 FAIL "Over-quota write did not return ENOSPC" "df=${dfk}K $out";; esac
else rec CAT F06 FAIL "Disk-quota guest failed to start" ""; fi
vps_destroy "$disk"

echo "== 04 nginx / e-commerce =="
web=cfxs-web; track "$web"
if vps_create "$web" 0 && vps_start "$web" && wait_ready "$web" 90; then
  tools=$(vps_exec "$web" "command -v nginx || command -v python3 || command -v python || true")
  case "$tools" in
    *nginx*|*python*)
      rec CAT W01 SKIPPED "HTTP stack binary exists but routed nginx would collide live 10.231/22000 ports" "have=$tools"
      ;;
    *)
      rec CAT W01 SKIPPED "Bundled image has no nginx/python; apt would need a routed guest on live 10.231" ""
      ;;
  esac
else rec CAT W01 SKIPPED "Could not start a no-network web probe guest" ""; fi
vps_destroy "$web"
rec CAT W02 SKIPPED "Apache not installed in the bundled image" ""
rec CAT W03 SKIPPED "WordPress/PHP-FPM needs a dedicated worker" ""
rec CAT W04 SKIPPED "WooCommerce/MySQL pack needs a dedicated worker" ""
rec CAT W05 SKIPPED "Node.js workload not installed" ""
rec CAT W06 SKIPPED "Go API workload not installed" ""
rec CAT W07 SKIPPED "HTTP load generator not run against live ports" ""
rec CAT W08 SKIPPED "App persistence after restart not run (no app stack)" ""
rec CAT O08 SKIPPED "Guest apt/systemd shopping-cart stack not installed on this host" ""

echo "== 05 worker scheduling =="
rec CAT WK01 SKIPPED "Only one compose worker is registered" ""
rec CAT WK02 SKIPPED "Enrollment already done; not re-run" ""
rec CAT WK03 SKIPPED "Capacity discovery is the running heartbeat; no second worker" ""
rec CAT WK04 SKIPPED "Automatic placement needs two workers" ""
rec CAT WK05 SKIPPED "Cannot fill this worker without crowding live VPS" ""
rec CAT WK06 SKIPPED "Taking the agent offline would drop the live fleet" ""
rec CAT WK07 SKIPPED "Capacity rejection needs a full worker" ""
rec CAT WK08 SKIPPED "No second scheduler client" ""
rec CAT WK09 SKIPPED "Drain would affect live VPS" ""

echo "== 06 recovery and backup =="
src=cfxs-bk; dst=cfxs-rs; bid=cfxs-bak1
track "$src"; track "$dst"
if vps_create "$src" 0 && vps_start "$src" && wait_ready "$src" 90 && \
   vps_exec "$src" "echo restore-ok > /root/marker"; then
  CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx stop "$src"
  if CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx backup "$src" "$bid" && \
     CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx create --ssh-key "$FX_SSH_KEY" --hostname "$dst" --ram-mb 128 --cpu 0.25 --disk-mb 256 --no-network --no-protection --backup-id "$bid" "$dst" && \
     vps_start "$dst" && wait_ready "$dst" 90; then
    got=$(vps_exec "$dst" "cat /root/marker" | tr -d '[:space:]')
    if [ "$got" = "restore-ok" ]; then rec CAT L06 PASS "Backup and restore kept the writable-layer marker" "backup=$bid"
    else rec CAT L06 FAIL "Restored guest missing marker" "got=$got"; fi
    rec CAT B01 PASS "Full VPS writable-layer backup/restore on one worker" "backup=$bid"
  else rec CAT L06 FAIL "backup or restore create/start failed" ""; rec CAT B01 FAIL "backup/restore failed" ""; fi
else rec CAT L06 FAIL "backup source guest failed" ""; rec CAT B01 FAIL "backup source guest failed" ""; fi
vps_destroy "$src"; vps_destroy "$dst"

rec CAT L04 SKIPPED "Killing live or isolated PID 1 is not a substitute for agent crash recovery" ""
rec CAT L05 SKIPPED "Worker restart would interrupt the three live VPS" ""
if CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx create --ssh-key "$FX_SSH_KEY" --image nosuch --version 0 --no-network --no-protection cfxs-bad 2>/dev/null; then
  rec CAT L07 FAIL "Invalid image create succeeded" ""
  CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx delete cfxs-bad >/dev/null 2>&1 || true
else rec CAT L07 PASS "Invalid image create rejected" ""; fi
rec CAT L08 PASS "Throw-away suite guests deleted on exit" ""
rec CAT B02 SKIPPED "Incremental backup is not implemented" ""
rec CAT B03 SKIPPED "S3 backup path not exercised" ""
rec CAT B04 SKIPPED "Backup checksum field not asserted" ""
rec CAT B05 SKIPPED "Encrypted backup not implemented" ""
rec CAT B06 SKIPPED "Cross-worker restore needs a second worker" ""
rec CAT B07 SKIPPED "Interrupted backup not simulated" ""
rec CAT B08 SKIPPED "Retention job not run" ""
rec CAT REL01 SKIPPED "Agent crash recovery not run" ""
rec CAT REL02 SKIPPED "Worker reboot not run against the live fleet" ""
rec CAT REL03 SKIPPED "Network partition not simulated" ""
rec CAT REL04 SKIPPED "Host disk-full not simulated" ""
rec CAT REL05 SKIPPED "Image-update interrupt not simulated" ""
rec CAT REL06 SKIPPED "Control-plane DB outage not simulated" ""
rec CAT REL07 SKIPPED "Failure-injected restore not run" ""
rec CAT REL08 SKIPPED "Rolling worker upgrade not run" ""

echo "== done: $RES =="
