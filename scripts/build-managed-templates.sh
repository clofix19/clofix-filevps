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
for app in ${CLOFIX_MANAGED_TEMPLATES:-adminer code-server uptime-kuma}; do
  case "$app" in
    adminer) packages=ca-certificates,nginx,php-fpm,php-cli,php-mysql,php-pgsql,php-sqlite3,adminer ;;
    code-server)
      [ -n "${CLOFIX_CODE_SERVER_ARCHIVE:-}" ] && [ -n "${CLOFIX_CODE_SERVER_SHA256:-}" ] || { echo 'Skipping code-server: provide its verified Linux standalone archive and SHA256.'; continue; }
      packages=ca-certificates,git ;;
    uptime-kuma)
      [ -n "${CLOFIX_UPTIME_KUMA_DIR:-}" ] && [ -n "${CLOFIX_NODE_ARCHIVE:-}" ] && [ -n "${CLOFIX_NODE_SHA256:-}" ] || { echo 'Skipping uptime-kuma: provide a prepared application directory and verified compatible Node.js Linux archive/SHA256.'; continue; }
      packages=ca-certificates ;;
    *) echo "Unknown template: $app" >&2; exit 1 ;;
  esac
  target="$ROOT/$app"
  if [ -e "$target" ]; then echo "Keeping existing template: $target"; continue; fi
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
    code-server)
      printf '%s  %s\n' "$CLOFIX_CODE_SERVER_SHA256" "$CLOFIX_CODE_SERVER_ARCHIVE" | sha256sum -c -
      mkdir -p "$staging/opt/code-server"
      tar -xzf "$CLOFIX_CODE_SERVER_ARCHIVE" --strip-components=1 -C "$staging/opt/code-server"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
mkdir -p /data/user /data/extensions /data/workspace
exec /opt/code-server/bin/code-server --bind-addr 0.0.0.0:8080 --auth none --user-data-dir /data/user --extensions-dir /data/extensions /data/workspace
START
      ;;
    uptime-kuma)
      test -d "$CLOFIX_UPTIME_KUMA_DIR/node_modules"
      test -f "$CLOFIX_UPTIME_KUMA_DIR/server/server.js"
      test -f "$CLOFIX_UPTIME_KUMA_DIR/dist/index.html"
      printf '%s  %s\n' "$CLOFIX_NODE_SHA256" "$CLOFIX_NODE_ARCHIVE" | sha256sum -c -
      tar -xf "$CLOFIX_NODE_ARCHIVE" --strip-components=1 -C "$staging/usr/local"
      cp -R "$CLOFIX_UPTIME_KUMA_DIR/." "$staging/app/"
      cat > "$staging/app/start" <<'START'
#!/bin/sh
set -eu
cd /app
exec /usr/local/bin/node server/server.js
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
