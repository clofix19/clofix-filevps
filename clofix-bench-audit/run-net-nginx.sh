#!/usr/bin/env bash
# Isolated networked pack + nginx load. Does not touch the live compose agent.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BACKEND=fx
export FX_BIN="${FX_BIN:-/tmp/clofix-worker-new}"
. "$HERE/lib.sh"
need_root
[ -x "$FX_BIN" ] || { echo "missing FX_BIN=$FX_BIN"; exit 1; }
OUT="${OUT:-$HERE/results}"
mkdir -p "$OUT"
RES="$OUT/suite-fx.tsv"

ISOLATED="${CLOFIX_SUITE_STATE:-/var/lib/clofix-filevps/suite-isolated}"
export CLOFIX_STATE_DIR="$ISOLATED"
export CLOFIX_LOG_DIR="$ISOLATED/logs"
export CLOFIX_USER_NS=true
export FX_NETWORK=1
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
LOAD="${LOAD:-/tmp/httpload}"

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
start_nginx() {
  vps_exec "$1" "mkdir -p /tmp/nginx/body /tmp/nginx/proxy /tmp/nginx/fastcgi /tmp/nginx/uwsgi /tmp/nginx/scgi /var/log/nginx; cat > /tmp/nginx.conf <<'EOF'
worker_processes 1;
error_log /tmp/nginx/error.log;
pid /tmp/nginx/nginx.pid;
events { worker_connections 128; }
http {
  access_log off;
  client_body_temp_path /tmp/nginx/body;
  proxy_temp_path /tmp/nginx/proxy;
  fastcgi_temp_path /tmp/nginx/fastcgi;
  uwsgi_temp_path /tmp/nginx/uwsgi;
  scgi_temp_path /tmp/nginx/scgi;
  server { listen 80 default_server; location / { default_type text/plain; return 200 \"ok\\n\"; } }
}
EOF
nginx -c /tmp/nginx.conf" >/dev/null 2>&1
}

echo "== networked SSH + IP isolation + nginx =="
mem0=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
count=0
ssh_n=0
iso_ok=1
ngx_n=0
reason="ok"
t0=$(date +%s)
TARGET="${TARGET:-100}"
while [ "$count" -lt "$TARGET" ]; do
  if ! live_ok; then reason="live_fleet_pid_missing"; break; fi
  avail=$(( $(awk '/MemAvailable/{print $2}' /proc/meminfo) / 1024 ))
  if [ "$avail" -lt "$MEM_RESERVE_MB" ]; then reason="memory_reserve_hit"; break; fi
  n="cfxn-$((count+1))"; track "$n"
  if ! vps_create "$n" 1 || ! vps_start "$n" || ! wait_ready "$n" 60; then
    reason="launch_failed_at_$((count+1))"
    break
  fi
  ip=$(guest_ip "$n")
  vps_exec "$n" "timeout 1 bash -c 'echo >/dev/tcp/10.231.0.1/1'" >/dev/null 2>&1 || true
  if ssh_ok "$ip"; then ssh_n=$((ssh_n+1)); else reason="ssh_failed_at_$((count+1)) ip=$ip"; break; fi
  if [ "$count" -ge 1 ]; then
    if vps_exec "cfxn-1" "timeout 2 bash -c 'echo >/dev/tcp/$ip/22'" >/dev/null 2>&1; then
      iso_ok=0
    fi
  fi
  start_nginx "$n" || true
  if [ -n "$ip" ] && timeout 2 bash -c "echo >/dev/tcp/$ip/80" >/dev/null 2>&1; then
    ngx_n=$((ngx_n+1))
  fi
  count=$((count+1))
  case "$count" in
    10) rec CAT D01 MEASURED "Networked density 10" "n=$count ssh=$ssh_n nginx=$ngx_n avail_mb=$avail";;
    25) rec CAT D25 MEASURED "Networked density 25" "n=$count ssh=$ssh_n nginx=$ngx_n avail_mb=$avail";;
    50) rec CAT D50 MEASURED "Networked density 50" "n=$count ssh=$ssh_n nginx=$ngx_n avail_mb=$avail";;
    100) rec CAT D100 MEASURED "Networked density 100" "n=$count ssh=$ssh_n nginx=$ngx_n avail_mb=$avail";;
  esac
done
t1=$(date +%s)
mem1=$(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t-a}' /proc/meminfo)
per="n/a"
if [ "$count" -gt 0 ]; then
  per=$(awk -v a="$mem1" -v b="$mem0" -v n="$count" 'BEGIN{d=(a-b)/1024/n; if(d<0) d=0; printf "%.2f",d}')
fi
rec CAT D02 MEASURED "Networked density on isolated state" "n=$count ssh=$ssh_n nginx=$ngx_n mem_per_vps_mb=$per reason=$reason secs=$((t1-t0))"
rec CAT L03 MEASURED "Sequential create/start pack" "n=$count"
if [ "$TARGET" -ge 100 ]; then
  if [ "$ssh_n" -ge 100 ]; then rec CAT N100 PASS "100 networked VPS accepted SSH" "n=$ssh_n"
  elif [ "$ssh_n" -gt 0 ]; then rec CAT N100 MEASURED "SSH pack stopped before 100" "n=$ssh_n reason=$reason"
  else rec CAT N100 FAIL "No networked guest accepted SSH" "reason=$reason"; fi
fi
if [ "$count" -ge 2 ] && [ "$iso_ok" = 1 ]; then rec CAT NISO PASS "Bridge isolation blocked guest-to-guest TCP/22" "n=$count"
elif [ "$count" -ge 2 ]; then rec CAT NISO FAIL "Guest-to-guest TCP/22 succeeded" "n=$count"
else rec CAT NISO SKIPPED "Need two networked guests" "n=$count"; fi
[ "$count" -ge 10 ] || rec CAT D01 SKIPPED "Did not reach 10: $reason" "n=$count"
[ "$count" -ge 25 ] || rec CAT D25 SKIPPED "Did not reach 25: $reason" "n=$count"
[ "$count" -ge 50 ] || rec CAT D50 SKIPPED "Did not reach 50: $reason" "n=$count"
[ "$count" -ge 100 ] || rec CAT D100 SKIPPED "Did not reach 100: $reason" "n=$count"

if [ "$ngx_n" -ge 25 ] && [ -x "$LOAD" ]; then
  sample_ip=$(guest_ip cfxn-1)
  load_out=$("$LOAD" "http://$sample_ip/" 8 16 2>/dev/null || true)
  ram_cpu=""
  for i in 1 25; do
    [ "$i" -le "$count" ] || continue
    cg="/sys/fs/cgroup/clofix-filevps/cfxn-$i"
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
if ! live_ok; then rec CAT D02 FAIL "Live fleet pid missing after density" ""; fi
echo "== done net/nginx: $RES =="
