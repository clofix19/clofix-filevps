#!/usr/bin/env bash
# Wrapper — full build lives in scripts/build-rootfs.sh (you run; does not build the worker).
exec "$(cd "$(dirname "$0")" && pwd)/scripts/build-rootfs.sh" "$@"
