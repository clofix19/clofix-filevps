FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -o /out/clofix-worker ./cmd/clofix-worker

FROM chrislusf/seaweedfs:4.51 AS seaweed
FROM docker:27-cli AS dockercli

FROM ubuntu:24.04 AS builtin-rootfs
RUN export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends tar openssh-server iproute2 \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/ssh/ssh_host_*_key* \
    && mkdir -p /tmp /var/tmp \
    && chmod 1777 /tmp /var/tmp \
    && tar --numeric-owner -czf /tmp/rootfs.tar.gz \
       --exclude=./proc/* --exclude=./sys/* --exclude=./dev/* --exclude=./run/* --exclude=./tmp/* \
       -C / .

# DEV ONLY image. Production workers are installed as a signed package on Ubuntu.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tar iproute2 nftables util-linux e2fsprogs kmod s3fs openssl curl wget unzip debootstrap && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/clofix-worker /usr/local/bin/clofix-worker
COPY --from=seaweed /usr/bin/weed /usr/local/bin/weed
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=dockercli /usr/local/bin/docker-compose /usr/local/bin/docker-compose
COPY --from=dockercli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY --from=builtin-rootfs /tmp/rootfs.tar.gz /opt/clofix/builtin/ubuntu/24.04/rootfs.tar.gz
COPY scripts/build-managed-templates.sh /usr/local/lib/clofix/build-managed-templates.sh
RUN mkdir -p /usr/local/lib/clofix /usr/local/lib/docker/cli-plugins \
 && ln -sf /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/lib/docker/cli-plugins/docker-compose \
 && chmod 0755 /usr/local/lib/clofix/build-managed-templates.sh /usr/local/bin/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
CMD ["clofix-worker", "run"]
