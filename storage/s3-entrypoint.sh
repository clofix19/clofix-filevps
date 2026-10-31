#!/bin/sh
set -eu
umask 077

: "${S3_ACCESS_KEY:?Set S3_ACCESS_KEY in .env}"
: "${S3_SECRET_KEY:?Set S3_SECRET_KEY in .env}"
case "$S3_ACCESS_KEY$S3_SECRET_KEY" in
	*[!A-Za-z0-9_-]*) echo "S3_ACCESS_KEY and S3_SECRET_KEY may contain only letters, numbers, underscore, and hyphen" >&2; exit 1 ;;
esac

mkdir -p /etc/seaweedfs
printf '{"identities":[{"name":"clofix","credentials":[{"accessKey":"%s","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' \
	"$S3_ACCESS_KEY" "$S3_SECRET_KEY" > /etc/seaweedfs/s3.json
exec weed server -dir=/data -master.defaultReplication=000 -volume=false \
	-master.port=9333 -master.port.grpc=19333 -filer -s3 -s3.port=8333 \
	-s3.config=/etc/seaweedfs/s3.json
