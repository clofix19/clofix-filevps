#!/usr/bin/env bash
# clofix-bench.sh — performance benchmark for fx (CloFix FileVPS) | incus | lxc
# Usage: sudo ./clofix-bench.sh <fx|incus|lxc>
# Run on a DEDICATED TEST HOST, once per backend, same machine.
# FileVPS uses the worker's bundled Ubuntu 24.04 image. Incus/LXC default to Debian 12
# unless INCUS_IMAGE / BASE_DISTRO+BASE_REL are overridden to match.
set -uo pipefail
BACKEND="${1:?usage: $0 <fx|incus|lxc>}"
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
need_root
need_backend

N_START="${N_START:-5}"              # startup-time repetitions
N_IDLE="${N_IDLE:-10}"               # instances for idle overhead
DENSITY_MAX="${DENSITY_MAX:-200}"    # hard cap for density test
DENSITY_STEP="${DENSITY_STEP:-10}"   # how often to check memory reserve
MEM_RESERVE_MB="${MEM_RESERVE_MB:-1024}"
SETTLE_S="${SETTLE_S:-30}"
OUT="${OUT:-$HERE/results}"; mkdir -p "$OUT"
RES="$OUT/bench-$BACKEND.tsv"; : > "$RES"
PFX="cfxb-$BACKEND"

rec() { printf '%s\t%s\n' "$1" "$2" >> "$RES"; printf '  %-28s %s\n' "$1" "$2"; }
mb()  { awk -v v="$1" 'BEGIN{printf "%.1f", v/1048576}'; }
per() { awk -v a="$1" -v n="$2" 'BEGIN{printf "%.2f", a/n}'; }

echo "== Benchmark: $BACKEND on $(hostname) =="
rec backend "$BACKEND"
rec host_cores "$(ncpu)"
rec host_mem_total_mb "$(awk -v k="$(mem_total_kb)" 'BEGIN{printf "%.0f",k/1024}')"
rec kernel "$(uname -r)"

# ---- 0. warm-up (image download / cache) -----------------------------------
echo "-- warm-up"
w="$PFX-warm"; track "$w"
vps_create "$w" && vps_start "$w" && wait_ready "$w" 180 || { echo "warm-up failed — check adapter in lib.sh"; exit 2; }
vps_destroy "$w"; sleep 3

# ---- 1. startup time -------------------------------------------------------
echo "-- startup time ($N_START runs)"
for i in $(seq 1 "$N_START"); do
  n="$PFX-s$i"; track "$n"
  t0=$(now_ms); vps_create "$n" || { rec startup_error "create failed run $i"; break; }
  t1=$(now_ms); vps_start "$n"; wait_ready "$n" 90 || { rec startup_error "not ready run $i"; vps_destroy "$n"; break; }
  t2=$(now_ms)
  rec create_ms $((t1-t0)); rec start_ready_ms $((t2-t1)); rec total_ms $((t2-t0))
  vps_destroy "$n"; sleep 2
done

# ---- 2. idle overhead: RAM / disk / CPU / processes ------------------------
echo "-- idle overhead ($N_IDLE instances)"
sleep 5
mem0=$(mem_used_kb); disk0=$(disk_used_b); procs0=$(proc_count)
anon0=$(mi_kb AnonPages); cache0=$(( $(mi_kb Cached)+$(mi_kb Buffers) )); kmem0=$(kmem_kb); ksm0=$(ksm_sharing)
cpu0=$(cpu_busy_pct 15)
IDLE=()
for i in $(seq 1 "$N_IDLE"); do
  n="$PFX-i$i"; track "$n"; vps_create "$n" && IDLE+=("$n")
done
disk_created=$(disk_used_b)
for n in "${IDLE[@]}"; do vps_start "$n"; done
for n in "${IDLE[@]}"; do wait_ready "$n" 90; done
sleep "$SETTLE_S"
mem1=$(mem_used_kb); disk1=$(disk_used_b); procs1=$(proc_count)
anon1=$(mi_kb AnonPages); cache1=$(( $(mi_kb Cached)+$(mi_kb Buffers) )); kmem1=$(kmem_kb); ksm1=$(ksm_sharing)
cpu1=$(cpu_busy_pct 15)
N=${#IDLE[@]}
if [ "$N" -gt 0 ]; then
  rec idle_instances "$N"
  mem_per=$(awk -v a="$mem1" -v b="$mem0" -v n="$N" 'BEGIN{printf "%.2f",(a-b)/1024/n}')
  if awk -v m="$mem_per" 'BEGIN{exit !(m<0)}'; then
    rec idle_mem_per_vps_mb "n/a (measurement noise)"
  else
    rec idle_mem_per_vps_mb "$mem_per"
    tot_mb=$(awk -v k="$(mem_total_kb)" 'BEGIN{printf "%.0f",k/1024}')
    rec projected_idle_density "$(awk -v t="$tot_mb" -v r="$MEM_RESERVE_MB" -v m="$mem_per" 'BEGIN{ if(m<=0){print "n/a"}else{printf "%.0f",(t-r)/m} }')"
  fi
  rec idle_disk_create_per_vps_mb "$(per "$(mb $((disk_created-disk0)))" "$N")"
  rec idle_disk_running_per_vps_mb "$(per "$(mb $((disk1-disk0)))" "$N")"
  rec idle_host_procs_per_vps "$(per $((procs1-procs0)) "$N")"
  rec cpu_baseline_pct "$cpu0"
  rec cpu_idle_with_vps_pct "$cpu1"
  cpu_over=$(awk -v a="$cpu1" -v b="$cpu0" 'BEGIN{printf "%.2f",a-b}')
  if awk -v c="$cpu_over" 'BEGIN{exit !(c<0)}'; then
    rec cpu_idle_overhead_pct "n/a (measurement noise)"
  else
    rec cpu_idle_overhead_pct "$cpu_over"
  fi

  # --- kernel share: where does the per-VPS memory go? (shared kernel => small kernel/slab cost)
  kb2mb() { awk -v a="$1" -v n="$N" 'BEGIN{printf "%.2f",a/1024/n}'; }
  rec kernel_shared_mem_per_vps_mb "$(kb2mb $((kmem1-kmem0)))"      # Slab+KernelStack+PageTables
  rec anon_mem_per_vps_mb          "$(kb2mb $((anon1-anon0)))"      # process memory
  rec page_cache_per_vps_mb        "$(kb2mb $((cache1-cache0)))"    # file cache (shared base OS => low)
  rec ksm_pages_sharing_delta      "$((ksm1-ksm0))"
  # --- OS share: how much of each rootfs is physically shared (hardlink/reflink/overlay/COW)?
  rfA=$(vps_rootfs "${IDLE[0]}")
  if [ -d "$rfA" ]; then
    app=$(du -sx --apparent-size -B1 "$rfA" 2>/dev/null | cut -f1)
    rec rootfs_apparent_mb "$(mb "${app:-0}")"
    rec os_share_ratio "$(awk -v a="${app:-0}" -v d="$(( (disk_created-disk0)/N ))" 'BEGIN{ if(d<=0){print "n/a"}else{printf "%.1f",a/d} }')"  # >1 => base OS shared
    ia=$(stat -c %i "$rfA/etc/os-release" 2>/dev/null); ib=$(stat -c %i "$(vps_rootfs "${IDLE[1]:-${IDLE[0]}}")/etc/os-release" 2>/dev/null)
    [ -n "$ia" ] && [ "$ia" = "$ib" ] && rec os_share_mode "shared-inode (hardlink/overlay lower)" || rec os_share_mode "separate-copy-or-cow"
  else
    rec os_share_mode "rootfs path not found (set FX_ROOTFS/INCUS_ROOTFS)"
  fi

  first="${IDLE[0]}"
  echo "-- exec latency + in-VPS CPU loop"
  for r in $(seq 1 10); do
    t0=$(now_ms); vps_exec "$first" true >/dev/null 2>&1; t1=$(now_ms); rec exec_latency_ms $((t1-t0))
  done
  for r in 1 2 3; do
    t0=$(now_ms); vps_exec "$first" "awk 'BEGIN{for(i=0;i<30000000;i++)s+=i}'" >/dev/null 2>&1; t1=$(now_ms)
    rec cpu_loop_ms $((t1-t0))
  done
fi
for n in "${IDLE[@]}"; do vps_destroy "$n"; done
sleep 5

# ---- 3. density ------------------------------------------------------------
echo "-- density (cap $DENSITY_MAX, reserve ${MEM_RESERVE_MB}MB)"
if [ "$DENSITY_MAX" -le 0 ]; then
  rec density_vps_count "skipped"
  rec density_stop_reason "skipped (DENSITY_MAX=0)"
else
  dm0=$(mem_used_kb); dt0=$(now_ms); count=0; reason="max_reached"
  while [ "$count" -lt "$DENSITY_MAX" ]; do
    n="$PFX-d$((count+1))"; track "$n"
    # FileVPS only has 100 SSH host ports; density does not need guest networking.
    if ! vps_create "$n" 0 || ! vps_start "$n" || ! wait_ready "$n" 90; then
      reason="launch_failed_at_$((count+1))"; break
    fi
    count=$((count+1))
    if [ $((count % DENSITY_STEP)) -eq 0 ]; then
      echo "   $count running, avail $(( $(mem_avail_kb)/1024 ))MB"
      [ $(( $(mem_avail_kb)/1024 )) -lt "$MEM_RESERVE_MB" ] && { reason="memory_reserve_hit"; break; }
    fi
  done
  dm1=$(mem_used_kb); dt1=$(now_ms)
  rec density_vps_count "$count"
  rec density_stop_reason "$reason"
  [ "$count" -gt 0 ] && rec density_mem_per_vps_mb "$(awk -v a="$dm1" -v b="$dm0" -v n="$count" 'BEGIN{printf "%.2f",(a-b)/1024/n}')"
  [ "$count" -gt 0 ] && rec density_avg_launch_s "$(awk -v a="$dt1" -v b="$dt0" -v n="$count" 'BEGIN{printf "%.2f",(a-b)/1000/n}')"
  rec density_host_load1 "$(cut -d' ' -f1 /proc/loadavg)"
fi

echo "== done: $RES =="
