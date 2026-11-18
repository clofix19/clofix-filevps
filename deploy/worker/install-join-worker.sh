#!/bin/sh
# Prepare a remote Ubuntu worker and join it to the CloFix controller.
#
# From the repo (build host):
#   cp deploy/worker/.env.example deploy/worker/.env   # fill CLOFIX_* + TOKEN
#   ./deploy/worker/build-worker.sh deb
#   ./deploy/worker/push-join-worker.sh
# Or copy deploy/worker/ to the remote host, then:
#   sudo ./install-join-worker.sh
#
# Values load from deploy/worker/.env (or env vars). Package defaults to newest ./dist/*.deb.
# Redis Cluster needs this worker binary (configure-cluster-node / join-cluster) plus
# host bridge east-west forwarding (applied below via ensure_vps_networking).
#
# CLOFIX_CONTROLLER must be reachable from this host on TCP 8443 (worker mTLS).
# Do not use docker-compose names like https://admin:8443 on a real remote host.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

if [ -f "$SCRIPT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/.env"
  set +a
fi

[ "$(id -u)" = 0 ] || { echo 'Run as root on the Linux worker host.' >&2; exit 1; }

. /etc/os-release
case "${ID:-}-${VERSION_CODENAME:-}" in
  ubuntu-jammy|ubuntu-noble) ;;
  *)
    echo "Unsupported OS: ${ID:-unknown} ${VERSION_CODENAME:-unknown} (need Ubuntu 22.04 jammy or 24.04 noble)." >&2
    exit 1
    ;;
esac

if [ ! -f /sys/fs/cgroup/cgroup.controllers ] && [ ! -f /sys/fs/cgroup/cgroup.subtree_control ]; then
  echo 'cgroup v2 is required. Boot with systemd unified hierarchy (default on Ubuntu 22.04+).' >&2
  exit 1
fi

CONTROLLER="${CLOFIX_CONTROLLER:-}"
ADMIN_HTTP="${CLOFIX_ADMIN_HTTP:-}"
TOKEN="${CLOFIX_TOKEN:-}"
INSTALLATION_ID="${CLOFIX_INSTALLATION_ID:-}"
WORKER_PKG="${CLOFIX_WORKER_PKG:-}"
WORKER_DEB_URL="${CLOFIX_WORKER_DEB_URL:-}"
CA_FILE="${CLOFIX_CA_FILE:-}"
WITH_DOCKER="${WITH_DOCKER:-1}"
WITH_CKS="${WITH_CKS:-1}"
WITH_HOST_DEPS="${WITH_HOST_DEPS:-1}"
WITH_MANAGED_TEMPLATES="${WITH_MANAGED_TEMPLATES:-1}"
WITH_S3_POOL="${WITH_S3_POOL:-1}"
WITH_S3_FIREWALL="${WITH_S3_FIREWALL:-1}"
SKIP_ENROLL="${SKIP_ENROLL:-0}"
ARCH="$(dpkg --print-architecture)"
BUILDER_DST=/usr/local/lib/clofix/build-managed-templates.sh
S3_MASTER="${CLOFIX_S3_MASTER_ADDRESS:-}"
S3_ADVERTISE="${CLOFIX_S3_ADVERTISE_ADDRESS:-${CLOFIX_REMOTE_IP:-}}"
S3_DATA_DIR="${CLOFIX_S3_DATA_DIR:-/var/lib/clofix-filevps/s3-volume}"

# Resolve relative package paths against deploy/
if [ -n "$WORKER_PKG" ] && [ "${WORKER_PKG#/}" = "$WORKER_PKG" ]; then
  WORKER_PKG="$SCRIPT_DIR/${WORKER_PKG#./}"
fi
if [ -z "$WORKER_PKG" ] && [ -z "$WORKER_DEB_URL" ]; then
  newest=""
  for f in "$SCRIPT_DIR"/dist/clofix-worker*.deb "$SCRIPT_DIR"/dist/*.deb; do
    [ -f "$f" ] || continue
    if [ -z "$newest" ] || [ "$f" -nt "$newest" ]; then
      newest="$f"
    fi
  done
  WORKER_PKG="$newest"
fi

if [ -z "$CONTROLLER" ]; then
  echo 'Set CLOFIX_CONTROLLER in deploy/worker/.env (or env), e.g. https://controller.example:8443' >&2
  exit 2
fi
case "$CONTROLLER" in
  https://admin:*|http://admin:*|https://admin|http://admin)
    echo "CLOFIX_CONTROLLER='$CONTROLLER' is a Compose service name; use the public controller hostname/IP from this host." >&2
    exit 2
    ;;
esac

resolve_installation_id() {
  if [ -n "$INSTALLATION_ID" ]; then
    return 0
  fi
  base="${ADMIN_HTTP:-}"
  [ -n "$base" ] || return 1
  base="${base%/}"
  echo "== Fetching Installation ID from $base/api/installation-id =="
  json="$(curl -fsSL --max-time 15 "$base/api/installation-id")" || return 1
  INSTALLATION_ID="$(printf '%s' "$json" | sed -n 's/.*"installation_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ -n "$INSTALLATION_ID" ] || return 1
}

# Before connect/enroll: refuse when admin license worker slots are full.
check_admin_worker_limit() {
  base="${ADMIN_HTTP:-}"
  [ -n "$base" ] || return 0
  base="${base%/}"
  echo "== Checking admin worker license limit ($base/api/installation-id) =="
  json="$(curl -fsSL --max-time 15 "$base/api/installation-id")" || {
    echo "WARNING: could not reach admin worker capacity endpoint; enroll will re-check via controller." >&2
    return 0
  }
  admit="$(printf '%s' "$json" | python3 -c 'import json,sys; d=json.load(sys.stdin); w=d.get("workers") or {}; print("true" if w.get("admit") else "false")' 2>/dev/null || true)"
  used="$(printf '%s' "$json" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("workers") or {}).get("used",""))' 2>/dev/null || true)"
  limit="$(printf '%s' "$json" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("workers") or {}).get("limit",""))' 2>/dev/null || true)"
  reason="$(printf '%s' "$json" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("workers") or {}).get("reason",""))' 2>/dev/null || true)"
  if [ "$admit" = "true" ]; then
    echo "Worker capacity OK (used=${used:-?} limit=${limit:-?})"
    return 0
  fi
  if [ -n "$reason" ]; then
    echo "Cannot connect worker: $reason" >&2
  else
    echo "Cannot connect worker: license worker limit reached (used=${used:-?} limit=${limit:-?}). Raise Max Workers in Admin → Settings → License." >&2
  fi
  return 1
}

if ! resolve_installation_id; then
  echo 'Set CLOFIX_INSTALLATION_ID=ctl-… (Admin → Settings → License) or CLOFIX_ADMIN_HTTP so this worker binds to the controller.' >&2
  exit 2
fi
case "$INSTALLATION_ID" in
  ctl-*) ;;
  *)
    echo "CLOFIX_INSTALLATION_ID must look like ctl-… (got: $INSTALLATION_ID)" >&2
    exit 2
    ;;
esac
export CLOFIX_INSTALLATION_ID="$INSTALLATION_ID"
if ! check_admin_worker_limit; then
  exit 2
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq

install_host_deps() {
  echo '== Host packages (FileVPS / storage / templates / Redis Cluster VPC) =='
  apt-get install -y --no-install-recommends \
    ca-certificates curl wget gnupg apt-transport-https \
    tar iproute2 nftables util-linux e2fsprogs kmod \
    s3fs openssl unzip fuse3 debootstrap python3 netcat-openbsd
  # bridge CLI (port isolation / AllowVPCPeers) ships with iproute2 on jammy/noble.
  command -v bridge >/dev/null 2>&1 || {
    echo 'bridge CLI missing after iproute2 install' >&2
    exit 1
  }
}

# FileVPS + Redis Cluster peers need forwarding even when CKS packages are skipped.
ensure_vps_networking() {
  echo '== FileVPS / Redis Cluster host networking =='
  modprobe bridge || true
  modprobe br_netfilter || true
  cat > /etc/sysctl.d/99-clofix-vps.conf <<'SYSCTL'
net.ipv4.ip_forward = 1
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
SYSCTL
  sysctl --system >/dev/null || true
  if [ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo 0)" != "1" ]; then
    echo 'WARNING: net.ipv4.ip_forward is not 1 - VPS↔VPS / Redis Cluster may fail' >&2
  fi
}

install_docker() {
  if command -v docker >/dev/null 2>&1; then
    echo '== Docker already present =='
    return 0
  fi
  echo '== Docker Engine =='
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod 0644 /etc/apt/keyrings/docker.gpg
  echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y --no-install-recommends \
    docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
}

install_cks() {
  if command -v kubelet >/dev/null 2>&1 && command -v kubeadm >/dev/null 2>&1; then
    echo '== CKS packages already present =='
  else
    echo '== CKS host packages (containerd / kubelet / helm) =='
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key \
      | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    chmod 0644 /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.31/deb/ /' \
      > /etc/apt/sources.list.d/kubernetes.list
    if [ ! -f /etc/apt/keyrings/docker.gpg ]; then
      curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
      chmod 0644 /etc/apt/keyrings/docker.gpg
    fi
    if [ ! -f /etc/apt/sources.list.d/docker.list ]; then
      echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
        > /etc/apt/sources.list.d/docker.list
    fi
    apt-get update -qq
    apt-get install -y --no-install-recommends containerd.io kubelet kubeadm kubectl etcd-client
    apt-mark hold kubelet kubeadm kubectl
  fi

  mkdir -p /etc/containerd
  if [ ! -f /etc/containerd/config.toml ]; then
    containerd config default > /etc/containerd/config.toml
  fi
  sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
  systemctl enable --now containerd
  systemctl enable --now kubelet || true

  if ! command -v helm >/dev/null 2>&1; then
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi

  swapoff -a || true
  sed -i.bak '/\sswap\s/s/^/#/' /etc/fstab || true
  modprobe br_netfilter || true
  modprobe overlay || true
  cat > /etc/sysctl.d/99-clofix-cks.conf <<'SYSCTL'
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward = 1
SYSCTL
  sysctl --system >/dev/null || true
}

install_worker_package() {
  echo '== CloFix worker package =='
  pkg="$WORKER_PKG"
  tmp=""
  if [ -z "$pkg" ] && [ -n "$WORKER_DEB_URL" ]; then
    tmp="$(mktemp /tmp/clofix-worker.XXXXXX.deb)"
    curl -fsSL "$WORKER_DEB_URL" -o "$tmp"
    pkg="$tmp"
  fi
  if [ -z "$pkg" ]; then
    echo 'No worker package found. Set CLOFIX_WORKER_PKG / CLOFIX_WORKER_DEB_URL, or run ./build-worker.sh deb first.' >&2
    exit 2
  fi
  case "$pkg" in
    *.deb)
      apt-get install -y "$pkg"
      ;;
    *.rpm)
      if command -v rpm >/dev/null 2>&1; then
        rpm -Uvh "$pkg"
      else
        echo "RPM package given but rpm is not available on this host: $pkg" >&2
        exit 2
      fi
      ;;
    *)
      echo "Unsupported package type (need .deb or .rpm): $pkg" >&2
      exit 2
      ;;
  esac
  [ -n "$tmp" ] && rm -f "$tmp"
  command -v clofix-worker >/dev/null 2>&1 || { echo 'clofix-worker missing after package install' >&2; exit 1; }
  mkdir -p /var/lib/clofix-filevps /var/log/clofix-filevps /etc/clofix-filevps /usr/local/lib/clofix
  chmod 750 /var/lib/clofix-filevps /etc/clofix-filevps
}

resolve_templates_script() {
  if [ -n "${CLOFIX_TEMPLATES_SCRIPT:-}" ] && [ -f "${CLOFIX_TEMPLATES_SCRIPT}" ]; then
    echo "$CLOFIX_TEMPLATES_SCRIPT"
    return 0
  fi
  for cand in \
    "$SCRIPT_DIR/build-managed-templates.sh" \
    "$SCRIPT_DIR/../../worker/scripts/build-managed-templates.sh" \
    "$BUILDER_DST"
  do
    if [ -f "$cand" ]; then
      echo "$cand"
      return 0
    fi
  done
  return 1
}

install_managed_templates() {
  echo '== Managed Quick App templates =='
  if ! src="$(resolve_templates_script)"; then
    echo 'build-managed-templates.sh not found. Copy it beside this script, or set CLOFIX_TEMPLATES_SCRIPT.' >&2
    exit 2
  fi
  install -m 0755 -d /usr/local/lib/clofix
  install -m 0755 "$src" "$BUILDER_DST"
  command -v debootstrap >/dev/null 2>&1 || apt-get install -y --no-install-recommends debootstrap
  echo "Building templates into /var/lib/clofix-filevps/managed-templates (can take several minutes)…"
  # shellcheck disable=SC2086
  env CLOFIX_STATE_DIR=/var/lib/clofix-filevps \
    CLOFIX_MANAGED_TEMPLATES="${CLOFIX_MANAGED_TEMPLATES:-adminer jenkins grafana prometheus loki jaeger otel-collector web-static web-php web-nodejs web-nextjs web-wordpress}" \
    /bin/sh "$BUILDER_DST"
}

fetch_ca() {
  if [ -n "$CA_FILE" ] && [ -f "$CA_FILE" ]; then
    echo "$CA_FILE"
    return 0
  fi
  if [ -z "$ADMIN_HTTP" ]; then
    # Derive admin HTTP from worker URL host when possible (8443 → 8080).
    hostport="$(printf '%s' "$CONTROLLER" | sed -E 's#^https?://##; s#/.*##')"
    host="$(printf '%s' "$hostport" | sed -E 's/:.*//')"
    ADMIN_HTTP="http://${host}:8080"
    echo "CLOFIX_ADMIN_HTTP not set; trying ${ADMIN_HTTP}/api/ca.pem" >&2
  fi
  out="/etc/clofix-filevps/controller-ca.pem"
  curl -fsSL "${ADMIN_HTTP%/}/api/ca.pem" -o "$out"
  chmod 0644 "$out"
  echo "$out"
}

detect_advertise_ip() {
  if [ -n "$S3_ADVERTISE" ]; then
    echo "$S3_ADVERTISE"
    return 0
  fi
  # Prefer the interface used toward the controller host.
  hostport="$(printf '%s' "$CONTROLLER" | sed -E 's#^https?://##; s#/.*##')"
  host="$(printf '%s' "$hostport" | sed -E 's/:.*//')"
  ip="$(ip -4 route get "$host" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)"
  if [ -n "$ip" ]; then
    echo "$ip"
    return 0
  fi
  ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  echo "$ip"
}

configure_s3_pool() {
  [ "$WITH_S3_POOL" = 1 ] || { echo '== S3 pool skipped (WITH_S3_POOL=0) =='; return 0; }
  echo '== S3 shared pool (SeaweedFS volume) =='
  if ! command -v weed >/dev/null 2>&1; then
    echo 'weed binary missing - reinstall the worker .deb (includes SeaweedFS weed).' >&2
    exit 1
  fi
  mkdir -p "$S3_DATA_DIR"
  chmod 700 "$S3_DATA_DIR"

  adv="$(detect_advertise_ip)"
  [ -n "$adv" ] || {
    echo 'Could not determine s3_advertise_address. Set CLOFIX_S3_ADVERTISE_ADDRESS or CLOFIX_REMOTE_IP.' >&2
    exit 2
  }

  cfg=/etc/clofix-filevps/worker.json
  mkdir -p /etc/clofix-filevps
  [ -f "$cfg" ] || echo '{}' >"$cfg"
  python3 - "$cfg" "$adv" "$S3_DATA_DIR" "$S3_MASTER" "$INSTALLATION_ID" <<'PY'
import json, sys
path, adv, data_dir, master, install_id = sys.argv[1:6]
with open(path) as f:
    cfg = json.load(f)
cfg["s3_advertise_address"] = adv
cfg["s3_data_dir"] = data_dir
cfg["installation_id"] = install_id
if master:
    cfg["s3_master_address"] = master
with open(path, "w") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
print(f"worker.json installation_id={install_id} s3_advertise_address={adv} s3_data_dir={data_dir}" + (f" s3_master_address={master}" if master else ""))
PY

  # Optional local firewall openings for volume ↔ master traffic.
  if [ "$WITH_S3_FIREWALL" = 1 ] && command -v ufw >/dev/null 2>&1; then
    if ufw status 2>/dev/null | grep -qi 'Status: active'; then
      ufw allow 8080/tcp comment 'CloFix S3 volume' || true
      ufw allow 18080/tcp comment 'CloFix S3 volume grpc' || true
      echo 'ufw: allowed 8080/18080 for S3 volume'
    fi
  fi

  master_probe="$S3_MASTER"
  if [ -z "$master_probe" ] && [ -f "$cfg" ]; then
    master_probe="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("s3_master_address",""))' "$cfg" 2>/dev/null || true)"
  fi
  if [ -n "$master_probe" ]; then
    mhost="${master_probe%%:*}"
    mport="${master_probe##*:}"
    [ "$mport" = "$master_probe" ] && mport=9333
    if command -v nc >/dev/null 2>&1; then
      if nc -z -w 3 "$mhost" "$mport" 2>/dev/null; then
        echo "S3 master reachable: $mhost:$mport"
      else
        echo "WARNING: cannot reach S3 master $mhost:$mport - set controller CLOFIX_S3_MASTER_ADDRESS to an address this host can reach (not s3pool:9333)." >&2
      fi
    fi
  else
    echo "NOTE: s3_master_address empty until controller heartbeats CLOFIX_S3_MASTER_ADDRESS. For remote workers that value must be a reachable host:9333 (not Compose DNS)."
  fi
}

enroll_and_start() {
  ca="$1"
  if [ "$SKIP_ENROLL" = 1 ]; then
    echo "SKIP_ENROLL=1 - package installed. Enroll manually:"
    echo "  printf '%s\\n' \"\$CLOFIX_TOKEN\" | clofix-worker enroll --controller '$CONTROLLER' --ca '$ca' --installation-id '$INSTALLATION_ID'"
    echo "  systemctl enable --now clofix-worker"
    configure_s3_pool || true
    return 0
  fi
  if [ -z "$TOKEN" ]; then
    echo 'Paste the one-time enrollment token from Workers → Enroll worker (input hidden if tty):'
    clofix-worker enroll --controller "$CONTROLLER" --ca "$ca" --installation-id "$INSTALLATION_ID"
  else
    printf '%s\n' "$TOKEN" | clofix-worker enroll --controller "$CONTROLLER" --ca "$ca" --installation-id "$INSTALLATION_ID"
  fi
  configure_s3_pool
  systemctl daemon-reload
  systemctl enable --now clofix-worker
  systemctl --no-pager --full status clofix-worker || true
}

[ "$WITH_HOST_DEPS" = 1 ] && install_host_deps
ensure_vps_networking
[ "$WITH_DOCKER" = 1 ] && install_docker
[ "$WITH_CKS" = 1 ] && install_cks
install_worker_package
[ "$WITH_MANAGED_TEMPLATES" = 1 ] && install_managed_templates
CA_PATH="$(fetch_ca)"
enroll_and_start "$CA_PATH"

cat <<EOF

Worker join complete.
- Agent: systemctl status clofix-worker
- Installation ID: ${INSTALLATION_ID} (must match Admin → Settings → License)
- UI: Workers page should show this host after the next heartbeat (~10s)
- Redis Cluster: UI → Compute → Redis Cluster (/redis-services); redis-server installs inside member VPS (same VPC only)
- Templates: /var/lib/clofix-filevps/managed-templates (Library Quick Apps)
- S3 pool: ${S3_DATA_DIR:-/var/lib/clofix-filevps/s3-volume} (weed volume; needs reachable CLOFIX_S3_MASTER_ADDRESS on controller)
- Never set CLOFIX_KUBERNETES_FAKE=1 on production workers
- Controller must allow this host to reach ${CONTROLLER}
EOF
