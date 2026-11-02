#!/bin/sh
# Install production CKS dependencies on an Ubuntu 22.04/24.04 CloFix worker host.
# Never set CLOFIX_KUBERNETES_FAKE on production workers.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root on the Linux worker.' >&2; exit 1; }
. /etc/os-release
case "${VERSION_CODENAME:-}" in
  jammy|noble) ;;
  *) echo "Unsupported Ubuntu release: ${VERSION_CODENAME:-unknown} (need jammy or noble)." >&2; exit 1 ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gnupg apt-transport-https

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
chmod 0644 /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.31/deb/ /' > /etc/apt/sources.list.d/kubernetes.list

curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod 0644 /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list

apt-get update
apt-get install -y --no-install-recommends containerd.io kubelet kubeadm kubectl etcd-client
apt-mark hold kubelet kubeadm kubectl

mkdir -p /etc/containerd
containerd config default > /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
systemctl enable --now containerd
systemctl enable --now kubelet

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

cat <<'EOF'
CKS worker packages installed.
Next:
  1. Ensure CLOFIX_KUBERNETES_FAKE is unset (never =1 on production).
  2. Enroll/restart clofix-worker so it advertises KubernetesReady.
  3. On the controller, after HA + etcd backup/restore verification, set CLOFIX_K8S_E2E=1.
EOF
