#!/bin/sh
# Started by systemd clofix-s3pool.service (EnvironmentFile=/etc/clofix/s3pool.env).
set -eu
: "${S3_ACCESS_KEY:?}"
: "${S3_SECRET_KEY:?}"
: "${S3_DEFAULT_REPLICATION:=000}"
: "${S3_DATA_DIR:=/var/lib/clofix-s3pool}"
: "${S3_CONFIG:=/etc/clofix/seaweed-s3.json}"
: "${S3_WITH_LOCAL_VOLUME:=1}"
: "${S3_MASTER_PORT:=9333}"
: "${S3_MASTER_GRPC_PORT:=19333}"
: "${S3_GATEWAY_PORT:=8333}"

mkdir -p "$S3_DATA_DIR" "$(dirname "$S3_CONFIG")"
umask 077
printf '{"identities":[{"name":"clofix","credentials":[{"accessKey":"%s","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' \
  "$S3_ACCESS_KEY" "$S3_SECRET_KEY" > "$S3_CONFIG"

WEED="${WEED_BIN:-/usr/local/bin/weed}"
set -- "$WEED" server \
  -dir="$S3_DATA_DIR" \
  -master.defaultReplication="$S3_DEFAULT_REPLICATION" \
  -master.port="$S3_MASTER_PORT" \
  -master.port.grpc="$S3_MASTER_GRPC_PORT" \
  -filer \
  -s3 \
  -s3.port="$S3_GATEWAY_PORT" \
  -s3.config="$S3_CONFIG"

# Match Compose (volume=false) when workers supply volumes; local volume for standalone admin.
if [ "$S3_WITH_LOCAL_VOLUME" = "0" ]; then
  set -- "$@" -volume=false
fi

exec "$@"
