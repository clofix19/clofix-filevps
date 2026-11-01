#!/bin/sh
# Prepare trusted root-owned native Quick App templates on a Linux worker.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root on the Linux worker.' >&2; exit 1; }
command -v debootstrap >/dev/null || { echo 'Install debootstrap first.' >&2; exit 1; }
STATE=${CLOFIX_STATE_DIR:-/var/lib/clofix-filevps}
ROOT="$STATE/managed-templates"
ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64) MIRROR=http://archive.ubuntu.com/ubuntu; NODE_ARCH=x64 ;; arm64) MIRROR=http://ports.ubuntu.com/ubuntu-ports; NODE_ARCH=arm64 ;; *) MIRROR=http://ports.ubuntu.com/ubuntu-ports; NODE_ARCH=$ARCH ;; esac
mkdir -p "$ROOT"
fetch() {
  dest=$1 url=$2
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$dest" "$url"
  else echo 'Install curl or wget to download official Quick App archives.' >&2; return 1
  fi
}
prepare_official_node() {
  sums=$(mktemp)
  archive=$(mktemp)
  fetch "$sums" https://nodejs.org/dist/latest-v22.x/SHASUMS256.txt || { rm -f "$sums" "$archive"; return 1; }
  name=$(awk -v arch="$NODE_ARCH" '$2 ~ ("^node-v.+-linux-" arch "[.]tar[.]xz$") { print $2; exit }' "$sums")
  hash=$(awk -v file="$name" '$2 == file { print $1; exit }' "$sums")
  [ -n "$name" ] && [ -n "$hash" ] || { rm -f "$sums" "$archive"; return 1; }
  fetch "$archive" "https://nodejs.org/dist/latest-v22.x/$name" || { rm -f "$sums" "$archive"; return 1; }
  command -v xz >/dev/null 2>&1 || { echo 'Install xz-utils to unpack official Node.js archives.' >&2; rm -f "$sums" "$archive"; return 1; }
  echo "$hash  $archive" | sha256sum -c - >/dev/null || { rm -f "$sums" "$archive"; return 1; }
  CLOFIX_NODE_ARCHIVE=$archive
  CLOFIX_NODE_SHA256=$hash
  rm -f "$sums"
}
prepare_uptime_kuma() {
  if [ -n "${CLOFIX_UPTIME_KUMA_DIR:-}" ]; then
    [ -f "$CLOFIX_UPTIME_KUMA_DIR/server/server.js" ] || return 1
    if [ -z "${CLOFIX_NODE_ARCHIVE:-}" ] || [ -z "${CLOFIX_NODE_SHA256:-}" ]; then
      prepare_official_node || return 1
    fi
    return 0
  fi
  version=${CLOFIX_UPTIME_KUMA_VERSION:-2.5.8}
  hash=${CLOFIX_UPTIME_KUMA_SHA256:-04752491d5b9e726c392df5fca3a89b01f77cc8100bc13d17e2bc0f798d77e92}
  archive=$(mktemp)
  fetch "$archive" "https://github.com/louislam/uptime-kuma/releases/download/${version}/dist.tar.gz" || { rm -f "$archive"; return 1; }
  echo "$hash  $archive" | sha256sum -c - >/dev/null || { rm -f "$archive"; return 1; }
  work=$(mktemp -d)
  tar -xf "$archive" -C "$work"
  rm -f "$archive"
  if [ -f "$work/server/server.js" ]; then CLOFIX_UPTIME_KUMA_DIR=$work
  elif [ -f "$work/dist/server/server.js" ]; then CLOFIX_UPTIME_KUMA_DIR=$work/dist
  else
    inner=$(find "$work" -mindepth 1 -maxdepth 2 -type f -name server.js -path '*/server/server.js' -print -quit)
    [ -n "$inner" ] || { rm -rf "$work"; return 1; }
    CLOFIX_UPTIME_KUMA_DIR=$(dirname "$(dirname "$inner")")
  fi
  prepare_official_node || { rm -rf "$work"; return 1; }
}
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
for app in ${CLOFIX_MANAGED_TEMPLATES:-adminer uptime-kuma jenkins web-static web-php web-nodejs web-nextjs web-wordpress}; do
  case "$app" in
    adminer) packages=ca-certificates,nginx,php-fpm,php-cli,php-mysql,php-pgsql,php-sqlite3,adminer ;;
    uptime-kuma)
      prepare_uptime_kuma || { echo 'Skipping uptime-kuma: could not download the official dist archive and verified Node.js Linux runtime.'; continue; }
      packages=ca-certificates,python3,build-essential ;;
    jenkins)
      prepare_jenkins_war || { echo 'Skipping jenkins: could not download the official LTS war and SHA-256.'; continue; }
      packages=ca-certificates,default-jre-headless,fontconfig,fonts-dejavu-core ;;
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
  trap 'if [ -n "${staging:-}" ] && [ -d "$staging" ]; then rm -rf "$staging"; fi' EXIT HUP INT TERM
  debootstrap --components=main,universe --variant=minbase --include=ca-certificates noble "$staging" "$MIRROR"
  printf '#!/bin/sh\nexit 101\n' > "$staging/usr/sbin/policy-rc.d"
  chmod 0755 "$staging/usr/sbin/policy-rc.d"
  printf 'deb %s noble main universe\ndeb %s noble-updates main universe\ndeb %s noble-security main universe\n' "$MIRROR" "$MIRROR" "$MIRROR" > "$staging/etc/apt/sources.list"
  chroot "$staging" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update
  saved_ifs=$IFS
  IFS=,
  set -- $packages
  IFS=$saved_ifs
  chroot "$staging" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
  chroot "$staging" apt-get clean
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
    uptime-kuma)
      test -f "$CLOFIX_UPTIME_KUMA_DIR/server/server.js"
      printf '%s  %s\n' "$CLOFIX_NODE_SHA256" "$CLOFIX_NODE_ARCHIVE" | sha256sum -c -
      tar -xf "$CLOFIX_NODE_ARCHIVE" --strip-components=1 -C "$staging/usr/local"
      cp -R "$CLOFIX_UPTIME_KUMA_DIR/." "$staging/app/"
      if [ ! -d "$staging/app/node_modules" ] && [ -f "$staging/app/package.json" ]; then
        chroot "$staging" /usr/bin/env PATH=/usr/local/bin:/usr/bin:/bin npm ci --omit=dev
      fi
      test -f "$staging/app/server/server.js"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
cd /app
exec /usr/local/bin/node server/server.js
START
      ;;
    jenkins)
      test -f "$CLOFIX_JENKINS_WAR"
      cp "$CLOFIX_JENKINS_WAR" "$staging/app/jenkins.war"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
export JENKINS_HOME=${JENKINS_HOME:-/data}
mkdir -p "$JENKINS_HOME"
exec /usr/bin/java -Djava.awt.headless=true -jar /app/jenkins.war --httpPort=8080 --httpListenAddress=0.0.0.0
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
