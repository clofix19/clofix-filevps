#!/usr/bin/env bash
# Run bench + audit for every backend that is installed, then write the report.
# Usage: sudo ./run-compare.sh [fx] [incus] [lxc]
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
need_root

if [ "$#" -gt 0 ]; then
  BACKENDS=("$@")
else
  BACKENDS=()
  command -v "${FX_BIN:-clofix-worker}" >/dev/null && BACKENDS+=(fx)
  command -v incus >/dev/null && BACKENDS+=(incus)
  command -v lxc-create >/dev/null && BACKENDS+=(lxc)
fi

if [ "${#BACKENDS[@]}" -eq 0 ]; then
  echo "no backends found (need clofix-worker, incus, and/or lxc-create)"
  exit 1
fi

echo "backends: ${BACKENDS[*]}"
failed=0
for b in "${BACKENDS[@]}"; do
  echo "======== $b bench ========"
  if ! "$HERE/clofix-bench.sh" "$b"; then
    echo "bench $b failed"
    failed=1
  fi
  echo "======== $b audit ========"
  if ! "$HERE/clofix-audit.sh" "$b"; then
    echo "audit $b failed"
    failed=1
  fi
done

python3 "$HERE/make-report.py" "$HERE/results" "$HERE/results/report.md"
exit "$failed"
