#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root"
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) test_arch=arm64 ;;
  x86_64|amd64) test_arch=amd64 ;;
  *) echo 'Unsupported Docker architecture' >&2; exit 1 ;;
esac
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/clofix-database-test.XXXXXXXX")
test_container="clofix-database-test-${RANDOM}-${RANDOM}"
cleanup() {
  docker rm -f -v "$test_container" >/dev/null 2>&1 || true
  rm -rf "$build_dir"
}
trap cleanup EXIT INT TERM
GOOS=linux GOARCH="$test_arch" CGO_ENABLED=0 go test -c -o "$build_dir/database.test" ./worker/internal/database
cp scripts/database-integration/Dockerfile "$build_dir/Dockerfile"
docker build -t clofix-database-integration:local "$build_dir"
# A dedicated nested daemon: no host socket, host data binds or published ports.
docker run -d --privileged --name "$test_container" --label clofix.purpose=database-integration \
  -e CLOFIX_DATABASE_INTEGRATION=1 --entrypoint dockerd \
  clofix-database-integration:local --host=unix:///var/run/docker.sock --storage-driver=overlay2 >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$test_container" docker info >/dev/null 2>&1; then break; fi
  if [ "$attempt" = 60 ]; then docker logs --tail 30 "$test_container"; exit 1; fi
  sleep 1
done
docker exec "$test_container" database.test -test.v -test.timeout=40m "$@"
