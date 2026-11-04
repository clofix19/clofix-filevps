#!/bin/sh
# Prepare trusted root-owned native Quick App templates on a Linux worker.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root on the Linux worker.' >&2; exit 1; }
command -v debootstrap >/dev/null || { echo 'Install debootstrap first.' >&2; exit 1; }
STATE=${CLOFIX_STATE_DIR:-/var/lib/clofix-filevps}
ROOT="$STATE/managed-templates"
ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64) MIRROR=http://archive.ubuntu.com/ubuntu ;; *) MIRROR=http://ports.ubuntu.com/ubuntu-ports ;; esac
mkdir -p "$ROOT"
fetch() {
  dest=$1 url=$2
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$dest" "$url"
  else echo 'Install curl or wget to download official Quick App archives.' >&2; return 1
  fi
}
case "$ARCH" in amd64) BINARCH=amd64; OTELARCH=amd64 ;; arm64) BINARCH=arm64; OTELARCH=arm64 ;; *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;; esac
prepare_jenkins_war() {
  version=${CLOFIX_JENKINS_VERSION:-latest}
  war=$(mktemp)
  sums=$(mktemp)
  fetch "$war" "https://get.jenkins.io/war-stable/${version}/jenkins.war" || { rm -f "$war" "$sums"; return 1; }
  fetch "$sums" "https://get.jenkins.io/war-stable/${version}/jenkins.war.sha256" || { rm -f "$war" "$sums"; return 1; }
  hash=$(awk '{ print $1; exit }' "$sums")
  echo "$hash  $war" | sha256sum -c - >/dev/null || { rm -f "$war" "$sums"; return 1; }
  CLOFIX_JENKINS_WAR=$war
  rm -f "$sums"
}
prepare_grafana_archive() {
  version=${CLOFIX_GRAFANA_VERSION:-12.1.0}
  archive=$(mktemp)
  fetch "$archive" "https://dl.grafana.com/oss/release/grafana-${version}.linux-${BINARCH}.tar.gz" || { rm -f "$archive"; return 1; }
  CLOFIX_GRAFANA_ARCHIVE=$archive
  CLOFIX_GRAFANA_VERSION=$version
}
prepare_prometheus_archive() {
  version=${CLOFIX_PROMETHEUS_VERSION:-2.55.1}
  archive=$(mktemp)
  sums=$(mktemp)
  base="prometheus-${version}.linux-${BINARCH}"
  fetch "$archive" "https://github.com/prometheus/prometheus/releases/download/v${version}/${base}.tar.gz" || { rm -f "$archive" "$sums"; return 1; }
  if fetch "$sums" "https://github.com/prometheus/prometheus/releases/download/v${version}/sha256sums.txt"; then
    want=$(awk -v f="${base}.tar.gz" '$2==f { print $1; exit }' "$sums")
    if [ -n "$want" ]; then echo "$want  $archive" | sha256sum -c - >/dev/null || { rm -f "$archive" "$sums"; return 1; }; fi
  fi
  rm -f "$sums"
  CLOFIX_PROMETHEUS_ARCHIVE=$archive
  CLOFIX_PROMETHEUS_VERSION=$version
}
prepare_loki_archive() {
  if [ -n "${CLOFIX_LOKI_ARCHIVE:-}" ] && [ -f "$CLOFIX_LOKI_ARCHIVE" ]; then return 0; fi
  version=${CLOFIX_LOKI_VERSION:-3.2.1}
  archive=$(mktemp)
  fetch "$archive" "https://github.com/grafana/loki/releases/download/v${version}/loki-linux-${BINARCH}.zip" || { rm -f "$archive"; return 1; }
  CLOFIX_LOKI_ARCHIVE=$archive
}
prepare_jaeger_archive() {
  if [ -n "${CLOFIX_JAEGER_ARCHIVE:-}" ] && [ -f "$CLOFIX_JAEGER_ARCHIVE" ]; then return 0; fi
  version=${CLOFIX_JAEGER_VERSION:-1.62.0}
  archive=$(mktemp)
  base="jaeger-${version}-linux-${BINARCH}"
  fetch "$archive" "https://github.com/jaegertracing/jaeger/releases/download/v${version}/${base}.tar.gz" || { rm -f "$archive"; return 1; }
  CLOFIX_JAEGER_ARCHIVE=$archive
  CLOFIX_JAEGER_VERSION=$version
}
prepare_otel_archive() {
  if [ -n "${CLOFIX_OTEL_ARCHIVE:-}" ] && [ -f "$CLOFIX_OTEL_ARCHIVE" ]; then return 0; fi
  version=${CLOFIX_OTELCOL_VERSION:-0.114.0}
  archive=$(mktemp)
  base="otelcol-contrib_${version}_linux_${OTELARCH}"
  fetch "$archive" "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${version}/${base}.tar.gz" || { rm -f "$archive"; return 1; }
  CLOFIX_OTEL_ARCHIVE=$archive
}
for app in ${CLOFIX_MANAGED_TEMPLATES:-adminer jenkins grafana prometheus loki jaeger otel-collector web-static web-php web-nodejs web-nextjs web-wordpress}; do
  case "$app" in
    adminer) packages=ca-certificates,nginx,php-fpm,php-cli,php-mysql,php-pgsql,php-sqlite3,adminer ;;
    jenkins)
      prepare_jenkins_war || { echo 'Skipping jenkins: could not download the official LTS war and SHA-256.'; continue; }
      packages=ca-certificates,default-jre-headless,fontconfig,fonts-dejavu-core,libfreetype6,libharfbuzz0b ;;
    grafana)
      prepare_grafana_archive || { echo 'Skipping grafana: could not download the official OSS archive.'; continue; }
      packages=ca-certificates,libfontconfig1,fonts-dejavu-core,musl ;;
    prometheus)
      prepare_prometheus_archive || { echo 'Skipping prometheus: could not download the official release archive.'; continue; }
      packages=ca-certificates ;;
    loki)
      prepare_loki_archive || { echo 'Skipping loki: could not download the official release archive.'; continue; }
      packages=ca-certificates ;;
    jaeger)
      prepare_jaeger_archive || { echo 'Skipping jaeger: could not download the official release archive.'; continue; }
      packages=ca-certificates ;;
    otel-collector)
      prepare_otel_archive || { echo 'Skipping otel-collector: could not download the official contrib archive.'; continue; }
      packages=ca-certificates ;;
    web-static) packages=ca-certificates,nginx ;;
    web-php) packages=ca-certificates,nginx,php-fpm,php-cli,php-mysql,php-pgsql,php-sqlite3 ;;
    web-nodejs|web-nextjs) packages=ca-certificates,nodejs,npm ;;
    web-wordpress) packages=ca-certificates,nginx,php-fpm,php-cli,php-mysql,php-pgsql,php-sqlite3,wordpress ;;
    *) echo "Unknown template: $app" >&2; exit 1 ;;
  esac
  target="$ROOT/$app"
  if [ -x "$target/app/start" ]; then echo "Keeping existing template: $target"; continue; fi
  if [ -e "$target" ]; then echo "Replacing incomplete template: $target"; rm -rf "$target"; fi
  staging="$ROOT/.building-$app-$$"
  unmount_staging() {
    if [ -n "${staging:-}" ]; then
      umount "$staging/proc" 2>/dev/null || true
      umount "$staging/sys" 2>/dev/null || true
    fi
  }
  trap 'unmount_staging; if [ -n "${staging:-}" ] && [ -d "$staging" ]; then rm -rf "$staging"; fi' EXIT HUP INT TERM
  debootstrap --components=main,universe --variant=minbase --include=ca-certificates noble "$staging" "$MIRROR"
  printf '#!/bin/sh\nexit 101\n' > "$staging/usr/sbin/policy-rc.d"
  chmod 0755 "$staging/usr/sbin/policy-rc.d"
  printf 'deb %s noble main universe\ndeb %s noble-updates main universe\ndeb %s noble-security main universe\n' "$MIRROR" "$MIRROR" "$MIRROR" > "$staging/etc/apt/sources.list"
  mkdir -p "$staging/proc" "$staging/sys"
  mount -t proc proc "$staging/proc"
  mount -t sysfs sysfs "$staging/sys"
  chroot "$staging" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update
  saved_ifs=$IFS
  IFS=,
  set -- $packages
  IFS=$saved_ifs
  chroot "$staging" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
  chroot "$staging" apt-get clean
  unmount_staging
  rm -f "$staging/usr/sbin/policy-rc.d"
  mkdir -p "$staging/app" "$staging/data" "$staging/proc" "$staging/dev" "$staging/tmp" "$staging/run" "$staging/var/log/nginx" "$staging/var/cache/nginx" "$staging/var/lib/apt/lists" "$staging/var/cache/apt" "$staging/usr/local"
  case "$app" in
    adminer)
      printf '%s\n' "<?php require '/usr/share/adminer/adminer.php';" > "$staging/app/index.php"
      cat > "$staging/app/nginx.conf" <<'CONFIG'
worker_processes 1;
pid /tmp/nginx.pid;
error_log stderr;
events { worker_connections 256; }
http {
 access_log /dev/stdout;
 client_body_temp_path /tmp/client-body;
 fastcgi_temp_path /tmp/fastcgi;
 proxy_temp_path /tmp/proxy;
 uwsgi_temp_path /tmp/uwsgi;
 scgi_temp_path /tmp/scgi;
 server {
  listen 8080;
  root /app;
  index index.php;
  location / { try_files $uri $uri/ /index.php?$query_string; }
  location ~ \.php$ {
   include /etc/nginx/fastcgi_params;
   fastcgi_param SCRIPT_FILENAME /app/index.php;
   fastcgi_pass 127.0.0.1:9000;
  }
 }
}
CONFIG
      cat > "$staging/app/php-fpm.conf" <<'CONFIG'
[global]
daemonize = no
error_log = /proc/self/fd/2
[www]
listen = 127.0.0.1:9000
user = nobody
group = nogroup
pm = ondemand
pm.max_children = 8
catch_workers_output = yes
clear_env = no
chdir = /app
php_admin_value[session.save_path] = /tmp
php_admin_value[upload_tmp_dir] = /tmp
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /tmp/client-body /tmp/fastcgi /tmp/proxy /tmp/uwsgi /tmp/scgi
/usr/sbin/php-fpm8.3 -F -y /app/php-fpm.conf &
exec /usr/sbin/nginx -c /app/nginx.conf -p /tmp -g 'daemon off;'
START
      ;;
    web-static)
      cat > "$staging/app/nginx.conf" <<'CONFIG'
worker_processes 1;
pid /tmp/nginx.pid;
error_log stderr;
events { worker_connections 256; }
http { access_log /dev/stdout; client_body_temp_path /tmp/client-body; server { listen 8080; root /data/app; index index.html; location / { try_files $uri $uri/ /index.html; } } }
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /tmp/client-body
exec /usr/sbin/nginx -c /app/nginx.conf -p /tmp -g 'daemon off;'
START
      ;;
    web-nodejs|web-nextjs)
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
cd /data/app
if [ -n "${CLOFIX_ENTRY:-}" ]; then exec node "$CLOFIX_ENTRY"; fi
if [ -f server.js ]; then exec node server.js; fi
if [ -f package.json ]; then exec npm start -- --hostname 0.0.0.0 --port 8080; fi
echo 'No server entrypoint supplied. Add server.js, package.json, or set an entrypoint.' >&2
exit 64
START
      ;;
    web-php|web-wordpress)
      cat > "$staging/app/nginx.conf" <<'CONFIG'
worker_processes 1;
pid /tmp/nginx.pid;
error_log stderr;
events { worker_connections 256; }
http { access_log /dev/stdout; client_body_temp_path /tmp/client-body; fastcgi_temp_path /tmp/fastcgi; server { listen 8080; root /data/app; index index.php index.html; location / { try_files $uri $uri/ /index.php?$query_string; } location ~ \.php$ { include /etc/nginx/fastcgi_params; fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name; fastcgi_pass 127.0.0.1:9000; } } }
CONFIG
      cat > "$staging/app/php-fpm.conf" <<'CONFIG'
[global]
daemonize = no
error_log = /proc/self/fd/2
[www]
listen = 127.0.0.1:9000
user = nobody
group = nogroup
pm = ondemand
pm.max_children = 8
catch_workers_output = yes
clear_env = no
php_admin_value[session.save_path] = /tmp
php_admin_value[upload_tmp_dir] = /tmp
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /tmp/client-body /tmp/fastcgi
if [ "${CLOFIX_WORDPRESS_BUNDLED:-}" = "1" ] && [ ! -f /data/app/index.php ]; then cp -R /usr/share/wordpress/. /data/app/; fi
      /usr/sbin/php-fpm8.3 -F -y /app/php-fpm.conf &
exec /usr/sbin/nginx -c /app/nginx.conf -p /tmp -g 'daemon off;'
START
      ;;
    jenkins)
      test -f "$CLOFIX_JENKINS_WAR"
      cp "$CLOFIX_JENKINS_WAR" "$staging/app/jenkins.war"
      chmod 0644 "$staging/app/jenkins.war"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
export JENKINS_HOME=${JENKINS_HOME:-/data}
mkdir -p "$JENKINS_HOME"
exec /usr/bin/java -Djava.awt.headless=true -jar /app/jenkins.war --httpPort=8080 --httpListenAddress=0.0.0.0
START
      ;;
    grafana)
      test -f "$CLOFIX_GRAFANA_ARCHIVE"
      mkdir -p "$staging/app/grafana"
      tar -xzf "$CLOFIX_GRAFANA_ARCHIVE" -C "$staging/app/grafana" --strip-components=1
      rm -f "$CLOFIX_GRAFANA_ARCHIVE"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
export GF_SERVER_HTTP_PORT=${GF_SERVER_HTTP_PORT:-8080}
export GF_SERVER_HTTP_ADDR=${GF_SERVER_HTTP_ADDR:-0.0.0.0}
export GF_PATHS_DATA=${GF_PATHS_DATA:-/data}
export GF_PATHS_LOGS=${GF_PATHS_LOGS:-/data/log}
export GF_PATHS_PLUGINS=${GF_PATHS_PLUGINS:-/data/plugins}
export GF_PATHS_PROVISIONING=${GF_PATHS_PROVISIONING:-/data/provisioning}
mkdir -p "$GF_PATHS_DATA" "$GF_PATHS_LOGS" "$GF_PATHS_PLUGINS" "$GF_PATHS_PROVISIONING"
cd /app/grafana
if [ -x ./bin/grafana ]; then exec ./bin/grafana server --homepath=/app/grafana; fi
exec ./bin/grafana-server --homepath=/app/grafana
START
      ;;
    prometheus)
      test -f "$CLOFIX_PROMETHEUS_ARCHIVE"
      mkdir -p "$staging/app/prometheus"
      tar -xzf "$CLOFIX_PROMETHEUS_ARCHIVE" -C "$staging/app/prometheus" --strip-components=1
      rm -f "$CLOFIX_PROMETHEUS_ARCHIVE"
      cat > "$staging/app/prometheus.yml" <<'CONFIG'
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['127.0.0.1:8080']
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /data
exec /app/prometheus/prometheus \
  --config.file=/app/prometheus.yml \
  --storage.tsdb.path=/data \
  --web.listen-address=0.0.0.0:8080 \
  --web.enable-lifecycle
START
      ;;
    loki)
      test -f "$CLOFIX_LOKI_ARCHIVE"
      if command -v unzip >/dev/null 2>&1; then
        unzip -qo "$CLOFIX_LOKI_ARCHIVE" -d "$staging/app"
      elif command -v python3 >/dev/null 2>&1; then
        python3 - "$CLOFIX_LOKI_ARCHIVE" "$staging/app" <<'PY'
import sys, zipfile
zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])
PY
      else
        echo 'Install unzip or python3 to extract the Loki archive.' >&2
        exit 1
      fi
      rm -f "$CLOFIX_LOKI_ARCHIVE"
      if [ -f "$staging/app/loki-linux-${BINARCH}" ]; then mv "$staging/app/loki-linux-${BINARCH}" "$staging/app/loki"; fi
      chmod 0755 "$staging/app/loki"
      cat > "$staging/app/loki-config.yml" <<'CONFIG'
auth_enabled: false
server:
  http_listen_address: 0.0.0.0
  http_listen_port: 8080
  grpc_listen_port: 9096
common:
  instance_addr: 127.0.0.1
  path_prefix: /data/loki
  storage:
    filesystem:
      chunks_directory: /data/loki/chunks
      rules_directory: /data/loki/rules
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
schema_config:
  configs:
    - from: 2020-10-24
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h
limits_config:
  reject_old_samples: true
  reject_old_samples_max_age: 168h
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /data/loki/chunks /data/loki/rules
exec /app/loki -config.file=/app/loki-config.yml
START
      ;;
    jaeger)
      test -f "$CLOFIX_JAEGER_ARCHIVE"
      mkdir -p "$staging/app/jaeger"
      tar -xzf "$CLOFIX_JAEGER_ARCHIVE" -C "$staging/app/jaeger" --strip-components=1
      rm -f "$CLOFIX_JAEGER_ARCHIVE"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /data
bin=/app/jaeger/jaeger-all-in-one
if [ ! -x "$bin" ]; then bin=/app/jaeger/jaeger; fi
exec "$bin" \
  --query.http-server.host-port=:8080 \
  --collector.otlp.enabled \
  --span-storage.type=memory
START
      ;;
    otel-collector)
      test -f "$CLOFIX_OTEL_ARCHIVE"
      tar -xzf "$CLOFIX_OTEL_ARCHIVE" -C "$staging/app"
      rm -f "$CLOFIX_OTEL_ARCHIVE"
      chmod 0755 "$staging/app/otelcol-contrib"
      cat > "$staging/app/otel-config.yaml" <<'CONFIG'
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:8080
processors:
  batch: {}
exporters:
  debug:
    verbosity: basic
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
CONFIG
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /data
exec /app/otelcol-contrib --config=/app/otel-config.yaml
START
      ;;
  esac
  chmod 0755 "$staging/app/start"
  chmod 1777 "$staging/tmp"
  chown -R root:root "$staging"
  mv "$staging" "$target"
  staging=
  trap - EXIT HUP INT TERM
  echo "Prepared managed template: $app"
done
