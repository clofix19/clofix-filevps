#!/bin/sh
# Provision native CloFix runtime roots. Run on the Linux worker as root.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root on the Linux worker.' >&2; exit 1; }
command -v debootstrap >/dev/null || { echo 'Install debootstrap first.' >&2; exit 1; }
RUNTIME_ROOT=${CLOFIX_RUNTIME_DIR:-${CLOFIX_STATE_DIR:-/var/lib/clofix-filevps}/runtimes}
SUITE=${CLOFIX_RUNTIME_SUITE:-noble}
ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64) DEFAULT_MIRROR=http://archive.ubuntu.com/ubuntu ;; *) DEFAULT_MIRROR=http://ports.ubuntu.com/ubuntu-ports ;; esac
MIRROR=${CLOFIX_RUNTIME_MIRROR:-$DEFAULT_MIRROR}
mkdir -p "$RUNTIME_ROOT"
for runtime in ${CLOFIX_RUNTIMES:-python go nodejs php java ruby bun deno}; do
  binary=
  case "$runtime" in
    python) packages=ca-certificates,python3,python3-pip,python3-venv ;;
    go) packages=ca-certificates ;;
    nodejs) packages=ca-certificates,nodejs,npm ;;
    php) packages=ca-certificates,php-cli ;;
    java) packages=ca-certificates,default-jre-headless ;;
    ruby) packages=ca-certificates,ruby ;;
    bun) packages=ca-certificates; binary=${CLOFIX_BUN_BIN:-} ;;
    deno) packages=ca-certificates; binary=${CLOFIX_DENO_BIN:-} ;;
    *) echo "Unknown runtime: $runtime" >&2; exit 1 ;;
  esac
  case "$runtime" in bun|deno)
    if [ -z "$binary" ]; then echo "Skipping $runtime: set CLOFIX_$(echo "$runtime" | tr '[:lower:]' '[:upper:]')_BIN to a verified Linux executable for $ARCH."; continue; fi
    [ -f "$binary" ] || { echo "Missing binary: $binary" >&2; exit 1; } ;;
  esac
  target="$RUNTIME_ROOT/$runtime"
  if [ -e "$target" ]; then echo "Keeping existing runtime: $target"; continue; fi
  debootstrap --components=main,universe --variant=minbase --include="$packages" "$SUITE" "$target" "$MIRROR"
  mkdir -p "$target/proc" "$target/dev" "$target/app" "$target/output" "$target/tmp" "$target/run" "$target/var/log/nginx" "$target/var/cache/nginx" "$target/var/lib/apt/lists" "$target/var/cache/apt" "$target/run/modsecurity" "$target/usr/local/bin"
  if [ -n "$binary" ]; then install -m 0755 "$binary" "$target/usr/local/bin/$runtime"; fi
  chmod 1777 "$target/tmp"
  chown -R root:root "$target"
  chmod 0755 "$target"
done
echo "Native CloFix runtimes prepared at $RUNTIME_ROOT"
