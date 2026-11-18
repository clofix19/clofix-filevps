# Build context: repository root (see docker-compose.yml worker.build.context).
FROM golang:1.23-bookworm AS build
ARG CLOFIX_INSTALLATION_ID=
WORKDIR /src
COPY worker/go.mod ./
COPY worker/cmd ./cmd
COPY worker/internal ./internal
COPY worker/pkg ./pkg
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/clofix/filevps/worker/internal/config.BakedInstallationID=${CLOFIX_INSTALLATION_ID}" -o /out/clofix-worker ./cmd/clofix-worker

FROM chrislusf/seaweedfs:4.51 AS seaweed
FROM docker:27-cli AS dockercli

# Pack rootfs with empty mountpoints (tar --exclude=./proc drops the dirs themselves).
# Ubuntu 24.04 - root-only (strip stock ubuntu user)
FROM ubuntu:24.04 AS builtin-ubuntu-2404
RUN export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends tar openssh-server iproute2 systemd \

    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/ssh/ssh_host_*_key* \
    && (userdel -r ubuntu 2>/dev/null || true) \
    && rm -rf /home/ubuntu \
    && mkdir -p /tmp /var/tmp /opt/rootfs-mps/proc /opt/rootfs-mps/sys /opt/rootfs-mps/dev /opt/rootfs-mps/run /opt/rootfs-mps/tmp \
    && chmod 1777 /tmp /var/tmp /opt/rootfs-mps/tmp \
    && tar --numeric-owner -cf /tmp/rootfs.tar \
       --exclude=./proc --exclude=./sys --exclude=./dev --exclude=./run --exclude=./tmp \
       --exclude=./opt/rootfs-mps \
       -C / . \
    && tar --numeric-owner -rf /tmp/rootfs.tar -C /opt/rootfs-mps proc sys dev run tmp \
    && gzip -f /tmp/rootfs.tar

# Ubuntu 22.04 - root-only
FROM ubuntu:22.04 AS builtin-ubuntu-2204
RUN export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends tar openssh-server iproute2 systemd \

    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/ssh/ssh_host_*_key* \
    && (userdel -r ubuntu 2>/dev/null || true) \
    && rm -rf /home/ubuntu \
    && mkdir -p /tmp /var/tmp /opt/rootfs-mps/proc /opt/rootfs-mps/sys /opt/rootfs-mps/dev /opt/rootfs-mps/run /opt/rootfs-mps/tmp \
    && chmod 1777 /tmp /var/tmp /opt/rootfs-mps/tmp \
    && tar --numeric-owner -cf /tmp/rootfs.tar \
       --exclude=./proc --exclude=./sys --exclude=./dev --exclude=./run --exclude=./tmp \
       --exclude=./opt/rootfs-mps \
       -C / . \
    && tar --numeric-owner -rf /tmp/rootfs.tar -C /opt/rootfs-mps proc sys dev run tmp \
    && gzip -f /tmp/rootfs.tar

# CloFix Guard / CLI binaries for CloFix Linux rootfs
FROM golang:1.23-bookworm AS clofix-linux-bins
WORKDIR /src
COPY OS/clofix-linux/1.0/go.mod ./
COPY OS/clofix-linux/1.0/cmd ./cmd
COPY OS/clofix-linux/1.0/internal ./internal
COPY OS/clofix-linux/1.0/host-worker ./host-worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /out/clofix ./cmd/clofix \
 && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /out/clofix-guard ./cmd/clofix-guard

# CloFix Linux 1.0 - Ubuntu 24.04 + brand + harden + Guard (root-only)
FROM ubuntu:24.04 AS builtin-clofix-linux
COPY OS/clofix-linux/1.0 /opt/clofix-linux-1.0
COPY --from=clofix-linux-bins /out/clofix /opt/clofix-linux-1.0/dist/clofix
COPY --from=clofix-linux-bins /out/clofix-guard /opt/clofix-linux-1.0/dist/clofix-guard
RUN export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends tar openssh-server iproute2 ca-certificates systemd \

    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/ssh/ssh_host_*_key* \
    && chmod +x /opt/clofix-linux-1.0/scripts/*.sh \
                 /opt/clofix-linux-1.0/packages/clofix-release/usr/bin/clofix \
                 /opt/clofix-linux-1.0/dist/clofix \
                 /opt/clofix-linux-1.0/dist/clofix-guard \
    && /opt/clofix-linux-1.0/scripts/brand-rootfs.sh / --no-backup \
    && /opt/clofix-linux-1.0/scripts/harden-rootfs.sh / --no-backup \
    && /opt/clofix-linux-1.0/scripts/validate-rootfs.sh / \
    && rm -rf /opt/clofix-linux-1.0 \
    && mkdir -p /tmp /var/tmp /opt/rootfs-mps/proc /opt/rootfs-mps/sys /opt/rootfs-mps/dev /opt/rootfs-mps/run /opt/rootfs-mps/tmp \
    && chmod 1777 /tmp /var/tmp /opt/rootfs-mps/tmp \
    && tar --numeric-owner -cf /tmp/rootfs.tar \
       --exclude=./proc --exclude=./sys --exclude=./dev --exclude=./run --exclude=./tmp \
       --exclude=./opt/rootfs-mps \
       -C / . \
    && tar --numeric-owner -rf /tmp/rootfs.tar -C /opt/rootfs-mps proc sys dev run tmp \
    && gzip -f /tmp/rootfs.tar

# DEV ONLY image. Production workers are installed as a signed package on Ubuntu.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tar iproute2 nftables util-linux e2fsprogs kmod s3fs openssl curl wget unzip debootstrap && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/clofix-worker /usr/local/bin/clofix-worker
COPY --from=seaweed /usr/bin/weed /usr/local/bin/weed
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=dockercli /usr/local/bin/docker-compose /usr/local/bin/docker-compose
COPY --from=dockercli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY --from=builtin-ubuntu-2404 /tmp/rootfs.tar.gz /opt/clofix/builtin/ubuntu/24.04/rootfs.tar.gz
COPY --from=builtin-ubuntu-2204 /tmp/rootfs.tar.gz /opt/clofix/builtin/ubuntu/22.04/rootfs.tar.gz
COPY --from=builtin-clofix-linux /tmp/rootfs.tar.gz /opt/clofix/builtin/clofix-linux/1.0/rootfs.tar.gz
COPY worker/scripts/build-managed-templates.sh /usr/local/lib/clofix/build-managed-templates.sh
RUN mkdir -p /usr/local/lib/clofix /usr/local/lib/docker/cli-plugins \
 && ln -sf /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/lib/docker/cli-plugins/docker-compose \
 && chmod 0755 /usr/local/lib/clofix/build-managed-templates.sh /usr/local/bin/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
CMD ["clofix-worker", "run"]
