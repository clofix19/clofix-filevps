#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop clofix-worker || true
  systemctl disable clofix-worker || true
fi
