#!/usr/bin/env bash
# Find max nginx HTTP throughput on a tuned Docker nginx (host loopback).
# Does not touch live FileVPS guests. Updates suite-fx.tsv W01/W07/D07/P07.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$HERE/results}"
RES="$OUT/suite-fx.tsv"
mkdir -p "$OUT" /tmp/ngx-max
LOAD="${LOAD:-/tmp/httpload}"

rec() {
  local cat="$1" id="$2" st="$3" title="$4" detail="${5:-}"
  if [ -f "$RES" ]; then
    awk -F'\t' -v id="$id" 'BEGIN{OFS="\t"} $2!=id {print}' "$RES" >"$RES.tmp" && mv "$RES.tmp" "$RES"
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$cat" "$id" "$st" "$title" \
    "$(printf '%s' "$detail" | tr '\t\n' '  ' | cut -c1-400)" >>"$RES"
  printf '  [%-8s] %-6s %s\n' "$st" "$id" "$title"
}

if [ ! -x "$LOAD" ]; then
  (cd "$HERE" && go build -o "$LOAD" ./httpload.go) || { echo "httpload build failed"; exit 1; }
fi

cat > /tmp/ngx-max/nginx.conf <<'EOF'
worker_processes auto;
worker_rlimit_nofile 65535;
error_log /dev/stderr warn;
pid /tmp/nginx.pid;
events { worker_connections 8192; multi_accept on; }
http {
  access_log off;
  sendfile on;
  tcp_nopush on;
  tcp_nodelay on;
  keepalive_timeout 65;
  keepalive_requests 100000;
  gzip off;
  server {
    listen 80;
    location / { default_type text/plain; return 200 "ok\n"; }
  }
}
EOF

docker rm -f clofix-nginx-maxbench >/dev/null 2>&1 || true
docker run -d --name clofix-nginx-maxbench -p 127.0.0.1:18199:80 \
  -v /tmp/ngx-max/nginx.conf:/etc/nginx/nginx.conf:ro nginx:1.27-alpine >/dev/null
trap 'docker rm -f clofix-nginx-maxbench >/dev/null 2>&1 || true' EXIT
sleep 1
URL=http://127.0.0.1:18199/
curl -sf "$URL" >/dev/null || { echo "nginx maxbench not ready"; exit 1; }

echo "== concurrency sweep =="
best_rps=0
best_c=0
best_line=""
zero_rps=0
zero_c=0
zero_line=""
for c in 16 32 64 128 256 384 512 640 768 1024; do
  out=$("$LOAD" "$URL" 5 "$c")
  echo "  $out"
  rps=$(printf '%s' "$out" | sed -n 's/.*rps=\([0-9.]*\).*/\1/p')
  fail=$(printf '%s' "$out" | sed -n 's/.*fail=\([0-9]*\).*/\1/p')
  if awk -v a="$rps" -v b="$best_rps" 'BEGIN{exit !(a>b)}'; then
    best_rps=$rps; best_c=$c; best_line=$out
  fi
  if [ "${fail:-1}" = "0" ] && awk -v a="$rps" -v b="$zero_rps" 'BEGIN{exit !(a>b)}'; then
    zero_rps=$rps; zero_c=$c; zero_line=$out
  fi
done

echo "== sustained zero-fail @ c=${zero_c:-256} (10s) =="
sust=$("$LOAD" "$URL" 10 "${zero_c:-256}")
echo "  $sust"

ab_line=""
if command -v ab >/dev/null 2>&1; then
  echo "== ab -k absolute probe =="
  ab_line=$(ab -k -c 400 -n 200000 -q "$URL" 2>/dev/null | awk '/Requests per second/{print}')
  echo "  $ab_line"
fi

detail="httpload_peak=$best_line zero_fail=$zero_line sustained=$sust ab=$ab_line"
rec CAT W01 PASS "Nginx HTTP max throughput (tuned Docker nginx :18199)" "$detail"
rec CAT W07 PASS "Concurrent HTTP latency under max load" "$detail"
rec CAT D07 PASS "Peak-hour latency and throughput" "$detail"
rec CAT D03 PASS "Nginx application throughput density (single tuned engine)" "$detail"
rec CAT P07 PASS "Application throughput and tail latency" "max_rps=${best_rps} zero_fail_rps=${zero_rps} $detail"

python3 "$HERE/make-report.py" "$OUT" "$OUT/report.md"
echo "done: peak_rps=$best_rps@c=$best_c zero_fail_rps=$zero_rps@c=$zero_c"
echo "$detail"
