FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -o /out/clofix-worker ./cmd/clofix-worker

FROM ubuntu:24.04 AS builtin-rootfs
RUN export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends tar openssh-server iproute2 \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/ssh/ssh_host_*_key* \
    && tar --numeric-owner -czf /tmp/rootfs.tar.gz \
       --exclude=./proc/* --exclude=./sys/* --exclude=./dev/* --exclude=./run/* --exclude=./tmp/* \
       -C / .

# DEV ONLY image. Production workers are installed as a signed package on Ubuntu.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates iproute2 nftables util-linux kmod s3fs && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/clofix-worker /usr/local/bin/clofix-worker
COPY --from=builtin-rootfs /tmp/rootfs.tar.gz /opt/clofix/builtin/ubuntu/24.04/rootfs.tar.gz
CMD ["clofix-worker", "run"]
