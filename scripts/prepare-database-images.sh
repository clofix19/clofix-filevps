#!/bin/sh
set -eu

state_dir=${1:-/var/lib/clofix-filevps}
mysql_image=${CLOFIX_MYSQL_IMAGE:-mysql:8.4}
postgres_image=${CLOFIX_POSTGRES_IMAGE:-postgres:17-bookworm}
mongo_image=${CLOFIX_MONGO_IMAGE:-mongo:8.0}

if [ "$(docker info --format '{{.Name}}')" != "$(hostname)" ]; then
  echo "The database runtime must be the worker's local Docker daemon." >&2
  exit 1
fi
install -d -m 0700 "$state_dir"
temporary=$(mktemp "$state_dir/database-images.json.XXXXXXXX")
trap 'rm -f "$temporary"' EXIT INT TERM

resolve_image() {
  image=$1
  expected_user=$2
  docker pull "$image" >/dev/null
  digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "$image")
  case "$digest" in
    *@sha256:????????????????????????????????????????????????????????????????) ;;
    *) echo "Image $image did not resolve to an immutable repository digest." >&2; exit 1 ;;
  esac
  actual_user=$(docker run --rm --entrypoint sh "$digest" -c "id -u '$expected_user'")
  if [ "$actual_user" != 999 ]; then
    echo "Image $image uses unexpected $expected_user UID $actual_user; expected 999." >&2
    exit 1
  fi
  printf '%s' "$digest"
}

mysql_digest=$(resolve_image "$mysql_image" mysql)
postgres_digest=$(resolve_image "$postgres_image" postgres)
mongo_digest=$(resolve_image "$mongo_image" mongodb)

printf '{\n  "mysql":{"ref":"%s","version":"8.4"},\n  "postgresql":{"ref":"%s","version":"17"},\n  "mongodb":{"ref":"%s","version":"8.0"}\n}\n' \
  "$mysql_digest" "$postgres_digest" "$mongo_digest" >"$temporary"
chmod 0600 "$temporary"
mv "$temporary" "$state_dir/database-images.json"
trap - EXIT INT TERM
echo "Trusted database image manifest written. Restart clofix-worker to advertise engines."
