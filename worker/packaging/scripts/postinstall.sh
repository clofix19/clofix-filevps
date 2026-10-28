#!/bin/sh
set -e
mkdir -p /var/lib/clofix-filevps /var/log/clofix-filevps /etc/clofix-filevps
chmod 750 /var/lib/clofix-filevps /etc/clofix-filevps
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  echo "Enroll this worker with: clofix-worker enroll   (then: systemctl enable --now clofix-worker)"
fi
