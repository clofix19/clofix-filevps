#!/usr/bin/env bash
# P0 priority suites beside the live Compose fleet. Isolated state only.
# Does not replace the compose agent, enable user_ns on live VPS, or invent PASS.
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

ISOLATED="${CLOFIX_SUITE_STATE:-/var/lib/clofix-filevps/suite-isolated}"
export CLOFIX_STATE_DIR="$ISOLATED"
export CLOFIX_LOG_DIR="$ISOLATED/logs"
export CLOFIX_USER_NS=true
export FX_NETWORK=0
export FX_PROTECTION=0
export FX_RAM_MB=128
export FX_DISK_MB=64
export FX_CPU=0.25
export FX_PIDS=64
export FX_SWAP_MB=0
export FX_ROOTFS="$ISOLATED/instances/{name}/rootfs-mount"
FX_STATE_DIR="$ISOLATED"
MEM_RESERVE_MB="${MEM_RESERVE_MB:-1536}"
LIVE_PIDS="${LIVE_PIDS:-28 254 328}"

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

live_ok() {
  local p
  for p in $LIVE_PIDS; do
    [ -d "/proc/$p" ] || return 1
  done
  return 0
}

echo "== 01 native Linux root isolation =="
A=cfxp-ua
B=cfxp-ub
track "$A"
if vps_create "$A" 0 && vps_start "$A" && wait_ready "$A" 90; then
  ua=$(vps_exec "$A" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]')
  unsa=$(vps_exec "$A" "readlink /proc/self/ns/user" | tr -d '[:space:]')
  unsh=$(readlink /proc/1/ns/user 2>/dev/null)
  if [ -n "$ua" ] && [ "$ua" != "0" ] && [ -n "$unsa" ] && [ "$unsa" != "$unsh" ]; then
    rec CAT S01 PASS "User namespace maps guest 0 to an unprivileged host uid" "hostuid=$ua"
    rec CAT S02 PASS "Guest user ns differs from host" "guest=$unsa"
    rec CAT I01 PASS "Container root is mapped to unprivileged host uid" "hostuid=$ua"
    track "$B"
    if vps_create "$B" 0 && vps_start "$B" && wait_ready "$B" 90; then
      ub=$(vps_exec "$B" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]')
      unsb=$(vps_exec "$B" "readlink /proc/self/ns/user" | tr -d '[:space:]')
      if [ -n "$ub" ] && [ "$ub" != "0" ] && [ "$ub" != "$ua" ] && [ -n "$unsb" ] && [ "$unsb" != "$unsa" ]; then
        rec CAT S03 PASS "Each guest gets a unique host uid range" "A=$ua B=$ub"
        rec CAT X05 PASS "Unique uid_map per guest" "A=$ua B=$ub"
      else
        rec CAT S03 FAIL "Second guest did not get a distinct uid range" "A=$ua B=$ub"
      fi
    else
      rec CAT S03 FAIL "Second user_ns guest did not start" ""
    fi
    vps_destroy "$B"
  else
    rec CAT S01 FAIL "user_ns guest started but mapping is identity" "uid=$ua"
    rec CAT S02 FAIL "user_ns guest started but ns still shared" ""
    rec CAT S03 FAIL "unique uid ranges not proven" "uid=$ua"
  fi
else
  why="isolated user_ns=true guest did not start"
  [ -f "$ISOLATED/logs/instances/${A}.log" ] && why=$(tr '\n' ' ' < "$ISOLATED/logs/instances/${A}.log" | cut -c1-240)
  rec CAT S01 FAIL "isolated user_ns=true guest did not start" "$why"
  rec CAT S02 FAIL "isolated user_ns=true guest did not start" "$why"
  rec CAT S03 FAIL "isolated user_ns=true guest did not start" "$why"
fi
vps_destroy "$A"

echo "== 02 high-density networked VPS (SSH + IP isolation) =="
guest_ip() { sed -n 's/.*"ip_address": *"\([^"]*\)".*/\1/p' "$ISOLATED/instances/$1/metadata.json" 2>/dev/null | head -1; }
SSH_PRIV="${FX_SSH_KEY%.pub}"
[ -f "$SSH_PRIV" ] || SSH_PRIV="/tmp/bench"
ssh_ok() {
  local ip=$1 i
  [ -n "$ip" ] || return 1
  for i in 1 2 3 4 5 6 7 8; do
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null \
      -o ConnectTimeout=3 -o BatchMode=yes -i "$SSH_PRIV" "root@$ip" true >/dev/null 2>&1 && return 0
    sleep 0.4
  done
  return 1
}
inject_nginx() {
  local img="$ISOLATED/images/cache/ubuntu/24.04/rootfs"
  local src="/var/lib/clofix-filevps/managed-templates/web-static/usr/sbin/nginx"
  [ -x "$src" ] && [ -d "$img" ] || return 1
  mkdir -p "$img/usr/sbin" "$img/etc/nginx" "$img/usr/share/nginx/html" "$img/var/log/nginx" "$img/run/nginx"
  cp "$src" "$img/usr/sbin/nginx"
  chmod 755 "$img/usr/sbin/nginx"
  cat > "$img/etc/nginx/nginx.conf" <<'EOF'
worker_processes 1;
error_log /var/log/nginx/error.log;
pid /run/nginx/nginx.pid;
events { worker_connections 128; }
http {
  access_log off;
  server {
    listen 80 default_server;
    location / { default_type text/plain; return 200 "ok\n"; }
  }
}
EOF
  echo ok > "$img/usr/share/nginx/html/index.html"
}
inject_nginx || true
FX_NETWORK=1
mem0=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
count=0
ssh_n=0
iso_ok=1
reason="ok"
t0=$(date +%s)
first_ip=""
while [ "$count" -lt 100 ]; do
  if ! live_ok; then reason="live_fleet_pid_missing"; break; fi
  avail=$(( $(awk '/MemAvailable/{print $2}' /proc/meminfo) / 1024 ))
  if [ "$avail" -lt "$MEM_RESERVE_MB" ]; then reason="memory_reserve_hit"; break; fi
  n="cfxp-d$((count+1))"; track "$n"
  if ! vps_create "$n" 1 || ! vps_start "$n" || ! wait_ready "$n" 60; then
    reason="launch_failed_at_$((count+1))"
    break
  fi
  ip=$(guest_ip "$n")
  vps_exec "$n" "timeout 1 bash -c 'echo >/dev/tcp/10.231.0.1/1'" >/dev/null 2>&1 || true
  if ssh_ok "$ip"; then ssh_n=$((ssh_n+1)); else reason="ssh_failed_at_$((count+1)) ip=$ip"; break; fi
  if [ -z "$first_ip" ]; then first_ip="$ip"; fi
    if [ "$count" -ge 1 ]; then
    if vps_exec "cfxp-d1" "timeout 2 bash -c 'echo >/dev/tcp/$ip/22'" >/dev/null 2>&1; then
      iso_ok=0
    fi
  fi
  count=$((count+1))
  case "$count" in
    10) rec CAT D01 MEASURED "Networked density 10" "n=$count ssh=$ssh_n avail_mb=$avail";;
    25) rec CAT D25 MEASURED "Networked density 25" "n=$count ssh=$ssh_n avail_mb=$avail";;
    50) rec CAT D50 MEASURED "Networked density 50" "n=$count ssh=$ssh_n avail_mb=$avail";;
    100) rec CAT D100 MEASURED "Networked density 100" "n=$count ssh=$ssh_n avail_mb=$avail";;
  esac
done
t1=$(date +%s)
mem1=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
per="n/a"
if [ "$count" -gt 0 ]; then
  per=$(awk -v a="$mem1" -v b="$mem0" -v n="$count" 'BEGIN{d=(a-b)/1024/n; if(d<0) d=0; printf "%.2f",d}')
fi
rec CAT D02 MEASURED "Networked density on isolated state" "n=$count ssh=$ssh_n mem_per_vps_mb=$per reason=$reason secs=$((t1-t0))"
rec CAT L03 MEASURED "Sequential create/start pack" "n=$count"
if [ "$ssh_n" -ge 100 ]; then rec CAT N100 PASS "100 networked VPS accepted SSH" "n=$ssh_n"
elif [ "$ssh_n" -gt 0 ]; then rec CAT N100 MEASURED "SSH pack stopped before 100" "n=$ssh_n reason=$reason"
else rec CAT N100 FAIL "No networked guest accepted SSH" "reason=$reason"; fi
if [ "$count" -ge 2 ] && [ "$iso_ok" = 1 ]; then rec CAT NISO PASS "Bridge isolation blocked guest-to-guest ping" "n=$count"
elif [ "$count" -ge 2 ]; then rec CAT NISO FAIL "Guest-to-guest ping succeeded" "n=$count"
else rec CAT NISO SKIPPED "Need two networked guests for isolation" "n=$count"; fi
[ "$count" -ge 10 ] || rec CAT D01 SKIPPED "Did not reach 10: $reason" "n=$count"
[ "$count" -ge 25 ] || rec CAT D25 SKIPPED "Did not reach 25: $reason" "n=$count"
[ "$count" -ge 50 ] || rec CAT D50 SKIPPED "Did not reach 50: $reason" "n=$count"
[ "$count" -ge 100 ] || rec CAT D100 SKIPPED "Did not reach 100: $reason" "n=$count"
if [ "$count" -ge 3 ]; then
  th_sum=0
  for i in 1 2 3; do
    vps_exec "cfxp-d$i" "dd if=/dev/zero of=/dev/null bs=1M count=40" >/dev/null 2>&1 || true
    th=$(awk '/nr_throttled/{print $2}' /sys/fs/cgroup/clofix-filevps/cfxp-d$i/cpu.stat 2>/dev/null)
    th_sum=$((th_sum+${th:-0}))
  done
  rec CAT D05 MEASURED "CPU load on 3 of the packed VPS" "nr_throttled_sum=$th_sum"
else
  rec CAT D05 SKIPPED "Not enough guests for a loaded pack" "n=$count"
fi
rec CAT D06 MEASURED "Provisioning under this pack" "n=$count secs=$((t1-t0))"
rec CAT D04 SKIPPED "PHP+MySQL density needs a dedicated worker" ""
rec CAT D08 SKIPPED "SLO occupancy needs a dedicated compare host" ""
if ! live_ok; then rec CAT D02 FAIL "Live fleet pid missing after density" ""; fi

echo "== 04 nginx workload on the networked pack =="
LOAD="${LOAD:-/tmp/httpload}"
ngx_n=0
if [ ! -x "$LOAD" ]; then
  rec CAT W01 SKIPPED "httpload binary missing at $LOAD" ""
  rec CAT D03 SKIPPED "nginx measurement skipped" ""
  rec CAT D07 SKIPPED "HTTP load not run" ""
  rec CAT W07 SKIPPED "HTTP load generator missing" ""
else
  for i in $(seq 1 "$count"); do
    n="cfxp-d$i"
    vps_exec "$n" "mkdir -p /run/nginx /var/log/nginx; nginx" >/dev/null 2>&1 || true
    ip=$(guest_ip "$n")
    if [ -n "$ip" ] && "$LOAD" "http://$ip/" 1 1 2>/dev/null | grep -q 'ok=[1-9]'; then
      ngx_n=$((ngx_n+1))
    fi
  done
  if [ "$ngx_n" -ge 25 ]; then
    sample_ip=$(guest_ip cfxp-d1)
    load_out=$("$LOAD" "http://$sample_ip/" 8 16 2>/dev/null || true)
    ram_cpu=""
    for i in 1 25; do
      [ "$i" -le "$count" ] || continue
      cg="/sys/fs/cgroup/clofix-filevps/cfxp-d$i"
      cur=$(awk '{print int($1/1024/1024)}' "$cg/memory.current" 2>/dev/null)
      usage=$(awk '/usage_usec/{print $2}' "$cg/cpu.stat" 2>/dev/null)
      ram_cpu="$ram_cpu d$i=${cur}MB,cpu_us=$usage"
    done
    rec CAT W01 PASS "Nginx responded on isolated guests" "n=$ngx_n $load_out"
    rec CAT D03 MEASURED "Nginx app-density" "n=$ngx_n $load_out $ram_cpu"
    rec CAT D07 MEASURED "HTTP load against guest IP on cfbr0" "$load_out"
    rec CAT W07 MEASURED "httpload rps/p95" "$load_out $ram_cpu"
  elif [ "$ngx_n" -gt 0 ]; then
    rec CAT W01 MEASURED "Nginx up on fewer than 25 guests" "n=$ngx_n"
    rec CAT D03 MEASURED "Nginx app-density below 25" "n=$ngx_n"
    rec CAT D07 SKIPPED "Need 25 nginx guests" "n=$ngx_n"
    rec CAT W07 SKIPPED "Need 25 nginx guests" "n=$ngx_n"
  else
    rec CAT W01 FAIL "Nginx did not serve on isolated guests" "n=$count"
    rec CAT D03 FAIL "Nginx did not serve" ""
    rec CAT D07 SKIPPED "No nginx HTTP endpoint" ""
    rec CAT W07 SKIPPED "No nginx HTTP endpoint" ""
  fi
fi
for n in "${CREATED[@]+"${CREATED[@]}"}"; do
  case "$n" in cfxp-d*) vps_destroy "$n";; esac
done
FX_NETWORK=0

echo "== 03 real RAM + ZRAM =="
if [ ! -e /dev/zram0 ]; then rec CAT M03 SKIPPED "zram module not present on linuxkit" ""
else rec CAT M03 INFO "ZRAM device exists" ""; fi
sw=cfxp-sw; track "$sw"
FX_SWAP_MB=64
if vps_create "$sw" 0 && vps_start "$sw" && wait_ready "$sw" 90; then
  sm=$(cat /sys/fs/cgroup/clofix-filevps/$sw/memory.swap.max 2>/dev/null | tr -d '[:space:]')
  case "$sm" in ""|max|0) rec CAT M04 WARN "swap.max not a positive cap" "memory.swap.max=${sm:-n/a}";;
    *) rec CAT M04 PASS "File-backed/cgroup swap cap set" "memory.swap.max=$sm";; esac
else rec CAT M04 FAIL "swap-cap guest failed" ""; fi
vps_destroy "$sw"
off=cfxp-swoff; on=cfxp-swon
track "$off"; track "$on"
FX_SWAP_MB=0
t_off="n/a"; t_on="n/a"
if vps_create "$off" 0 && vps_start "$off" && wait_ready "$off" 90; then
  t0s=$(date +%s%3N)
  vps_exec "$off" "dd if=/dev/zero of=/tmp/sw bs=1M count=32" >/dev/null 2>&1 || true
  t_off=$(( $(date +%s%3N) - t0s ))
fi
vps_destroy "$off"
FX_SWAP_MB=64
if vps_create "$on" 0 && vps_start "$on" && wait_ready "$on" 90; then
  t0s=$(date +%s%3N)
  vps_exec "$on" "dd if=/dev/zero of=/tmp/sw bs=1M count=32" >/dev/null 2>&1 || true
  t_on=$(( $(date +%s%3N) - t0s ))
  rec CAT M05 MEASURED "Swap OFF vs ON write timing (not ZRAM)" "off_ms=$t_off on_ms=$t_on swap.max=$(cat /sys/fs/cgroup/clofix-filevps/$on/memory.swap.max 2>/dev/null)"
else
  rec CAT M05 SKIPPED "swap-on guest failed" ""
fi
vps_destroy "$on"
FX_SWAP_MB=0

oom=cfxp-oom; track "$oom"
if vps_create "$oom" 0 && vps_start "$oom" && wait_ready "$oom" 90; then
  hog=/tmp/clofix-memhog
  if [ -x "$hog" ]; then
    dest="$ISOLATED/instances/$oom/layer/upper/root"
    mkdir -p "$dest"
    cp "$hog" "$dest/memhog"
    chmod 755 "$dest/memhog"
    vps_exec "$oom" "/root/memhog" >/dev/null 2>&1 || true
  else
    vps_exec "$oom" 'x=$(dd if=/dev/zero bs=1M count=200 2>/dev/null); echo KEPT=${#x}' >/dev/null 2>&1 || true
  fi
  oomc=$(awk '/^oom /{print $2}' /sys/fs/cgroup/clofix-filevps/$oom/memory.events 2>/dev/null)
  oomk=$(awk '/^oom_kill /{print $2}' /sys/fs/cgroup/clofix-filevps/$oom/memory.events 2>/dev/null)
  reclaim=$(awk '/workingset_refault_anon|workingset_activate_anon/{print}' /sys/fs/cgroup/clofix-filevps/$oom/memory.stat 2>/dev/null | tr '\n' ' ')
  if [ "${oomc:-0}" -gt 0 ] || [ "${oomk:-0}" -gt 0 ]; then
    rec CAT M07 PASS "Guest OOM stayed inside its cgroup" "oom=$oomc oom_kill=$oomk"
    rec CAT M06 MEASURED "Reclaim counters after memory hog" "$reclaim"
  else
    rec CAT M07 WARN "OOM counters stayed 0 after the hog" "oom=$oomc $reclaim"
    rec CAT M06 WARN "No cgroup OOM; reclaim not proven" "$reclaim"
  fi
  if live_ok; then rec CAT M08 PASS "Host and live fleet still up after memory hog" ""
  else rec CAT M08 FAIL "Live fleet pid missing after memory hog" ""; fi
else rec CAT M07 FAIL "OOM guest failed to start" ""; rec CAT M08 FAIL "OOM guest failed to start" ""; fi
vps_destroy "$oom"
rec CAT M09 SKIPPED "Overcommit recovery needs a dedicated host" ""

echo "== 04 remaining app stacks =="
rec CAT W02 SKIPPED "Apache not in the bundled image" ""
rec CAT W03 SKIPPED "WordPress/PHP-FPM needs a dedicated worker" ""
rec CAT W04 SKIPPED "WooCommerce/MySQL needs a dedicated worker" ""
rec CAT W05 SKIPPED "Node.js not installed" ""
rec CAT W06 SKIPPED "Go API workload not installed" ""
rec CAT W08 SKIPPED "No persistent app stack beyond the nginx probe" ""
rec CAT O08 SKIPPED "Guest apt/systemd shopping-cart stack not installed" ""

echo "== 05 multi-worker scheduler =="
rec CAT WK01 SKIPPED "Only one compose worker is registered" ""
rec CAT WK02 SKIPPED "Enrollment already done" ""
rec CAT WK03 SKIPPED "No second worker for capacity compare" ""
rec CAT WK04 SKIPPED "Automatic placement needs two workers" ""
rec CAT WK05 SKIPPED "Cannot fill this worker without crowding live VPS" ""
rec CAT WK06 SKIPPED "Taking the agent offline would drop the live fleet" ""
rec CAT WK07 SKIPPED "Capacity rejection needs a full worker" ""
rec CAT WK08 SKIPPED "No second scheduler client" ""
rec CAT WK09 SKIPPED "Drain would affect live VPS" ""

echo "== 06 security and recovery =="
off=cfxp-poff; on=cfxp-pon
track "$off"; track "$on"
if vps_create "$off" 0 && vps_start "$off" && wait_ready "$off" 90; then
  wroff=$(vps_exec "$off" "sh -c 'echo unprotected > /etc/hostname.write 2>&1; echo RC=\$?'")
  vps_exec "$off" "ln -s /proc/1/root/etc /tmp/esc; cat /tmp/esc/hostname >/tmp/esc.out 2>&1 || true" >/dev/null 2>&1 || true
  hostn=$(hostname | tr -d '[:space:]')
  got=$(vps_exec "$off" "cat /tmp/esc.out 2>/dev/null" | tr -d '[:space:]')
  case "$got" in *"$hostn"*) rec CAT F04 FAIL "Guest read host hostname via symlink" "got=$got";;
    *) rec CAT F04 PASS "Guest symlink did not expose the host hostname" "got=${got:0:80}";; esac
else rec CAT F04 FAIL "unprotected guest failed" ""; fi
FX_PROTECTION=1
if vps_create "$on" 0 && vps_start "$on" && wait_ready "$on" 90; then
  wron=$(vps_exec "$on" "sh -c 'echo protected > /etc/hostname.write; echo RC=\$?'" 2>&1)
  case "$wron" in *Read-only*|*EROFS*|*RC=[1-9]*) rec CAT F01 PASS "Protection Mode blocks writes to /etc" "$wron"
    rec CAT F02 PASS "Protected-path write without admin approval failed" "$wron";;
    *) rec CAT F01 FAIL "Protection Mode still allowed /etc write" "$wron"
       rec CAT F02 FAIL "Protected-path write succeeded" "$wron";; esac
else rec CAT F01 FAIL "protected guest failed" ""; rec CAT F02 FAIL "protected guest failed" ""; fi
FX_PROTECTION=0
vps_destroy "$off"; vps_destroy "$on"

src=cfxp-bk; dst=cfxp-rs; bid=cfxp-bak1
track "$src"; track "$dst"
FX_DISK_MB=256
if vps_create "$src" 0 && vps_start "$src" && wait_ready "$src" 90 && \
   vps_exec "$src" "echo restore-ok > /root/marker"; then
  CLOFIX_STATE_DIR="$ISOLATED" CLOFIX_USER_NS=true "$FX_BIN" fx stop "$src"
  if CLOFIX_STATE_DIR="$ISOLATED" "$FX_BIN" fx backup "$src" "$bid" && \
     CLOFIX_STATE_DIR="$ISOLATED" "$FX_BIN" fx create --ssh-key "$FX_SSH_KEY" --hostname "$dst" --ram-mb 128 --cpu 0.25 --disk-mb 256 --no-network --no-protection --backup-id "$bid" "$dst" && \
     vps_start "$dst" && wait_ready "$dst" 90; then
    got=$(vps_exec "$dst" "cat /root/marker" | tr -d '[:space:]')
    if [ "$got" = "restore-ok" ]; then rec CAT L06 PASS "Backup and restore kept the writable-layer marker" "backup=$bid"
      rec CAT B01 PASS "Full VPS writable-layer backup/restore on one worker" "backup=$bid"
    else rec CAT L06 FAIL "Restored guest missing marker" "got=$got"; rec CAT B01 FAIL "marker missing" "got=$got"; fi
  else rec CAT L06 FAIL "backup or restore failed" ""; rec CAT B01 FAIL "backup or restore failed" ""; fi
else rec CAT L06 FAIL "backup source failed" ""; rec CAT B01 FAIL "backup source failed" ""; fi
vps_destroy "$src"; vps_destroy "$dst"
FX_DISK_MB=64
rec CAT L04 SKIPPED "Killing PID 1 is not worker-crash recovery" ""
rec CAT L05 SKIPPED "Worker restart would interrupt the three live VPS" ""
rec CAT REL01 SKIPPED "Agent crash not run against the live fleet" ""
rec CAT REL02 SKIPPED "Worker reboot not run" ""
rec CAT B06 SKIPPED "Cross-worker restore needs a second worker" ""

echo "== 07 Docker / LXC / Incus comparison =="
rec CAT P01 SKIPPED "LXC is not installed on this worker" ""
rec CAT P02 SKIPPED "Incus is not installed on this worker" ""
rec CAT P03 SKIPPED "Same-host Docker app compare not run" ""
rec CAT P04 SKIPPED "No compare backends" ""
rec CAT P05 SKIPPED "No compare backends" ""
rec CAT P06 SKIPPED "No compare backends" ""
rec CAT P07 SKIPPED "No compare backends" ""
rec CAT P08 SKIPPED "No compare backends" ""

echo "== done: $RES =="
