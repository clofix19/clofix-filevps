package lifecycle

// Fixed upstream CLI installs for tools that are not reliable in Ubuntu apt alone.
// Checksums are pinned per architecture; package names are never taken from the API.

const devopsKubectlSetup = `set -eu
arch=$(uname -m)
case "$arch" in
  x86_64) goarch=amd64; sum=298e19e9c6c17199011404278f0ff8168a7eca4217edad9097af577023a5620f ;;
  aarch64|arm64) goarch=arm64; sum=b97e93c20e3be4b8c8fa1235a41b4d77d4f2022ed3d899230dbbbbd43d26f872 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
tmpdir=$(mktemp -d /tmp/clofix-kubectl.XXXXXX)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 -fL --retry 3 -o "$tmpdir/kubectl" "https://dl.k8s.io/release/v1.31.4/bin/linux/${goarch}/kubectl"
printf '%s  %s\n' "$sum" "$tmpdir/kubectl" | sha256sum -c -
install -m 0755 "$tmpdir/kubectl" /usr/local/bin/kubectl
`

const devopsHelmSetup = `set -eu
arch=$(uname -m)
case "$arch" in
  x86_64) goarch=amd64; sum=f5355c79190951eed23c5432a3b920e071f4c00a64f75e077de0dd4cb7b294ea ;;
  aarch64|arm64) goarch=arm64; sum=5bd34ed774df6914b323ff84a0a156ea6ff2ba1eaf0113962fa773f3f9def798 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
tmpdir=$(mktemp -d /tmp/clofix-helm.XXXXXX)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 -fL --retry 3 -o "$tmpdir/helm.tgz" "https://get.helm.sh/helm-v3.16.3-linux-${goarch}.tar.gz"
printf '%s  %s\n' "$sum" "$tmpdir/helm.tgz" | sha256sum -c -
tar -xzf "$tmpdir/helm.tgz" -C "$tmpdir"
install -m 0755 "$tmpdir/linux-${goarch}/helm" /usr/local/bin/helm
`

const devopsTerraformSetup = `set -eu
arch=$(uname -m)
case "$arch" in
  x86_64) goarch=amd64; sum=186e0145f5e5f2eb97cbd785bc78f21bae4ef15119349f6ad4fa535b83b10df8 ;;
  aarch64|arm64) goarch=arm64; sum=f85868798834558239f6148834884008f2722548f84034c9b0f62934b2d73ebb ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
tmpdir=$(mktemp -d /tmp/clofix-terraform.XXXXXX)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 -fL --retry 3 -o "$tmpdir/terraform.zip" "https://releases.hashicorp.com/terraform/1.9.8/terraform_1.9.8_linux_${goarch}.zip"
printf '%s  %s\n' "$sum" "$tmpdir/terraform.zip" | sha256sum -c -
# unzip may be missing on minimal guests; use python as a fallback.
if command -v unzip >/dev/null 2>&1; then
  unzip -o -d "$tmpdir" "$tmpdir/terraform.zip"
else
  python3 - <<'PY' "$tmpdir/terraform.zip" "$tmpdir"
import sys, zipfile
zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])
PY
fi
install -m 0755 "$tmpdir/terraform" /usr/local/bin/terraform
`

func devopsToolSetup(serviceID string) string {
	switch serviceID {
	case "kubectl":
		return devopsKubectlSetup
	case "helm":
		return devopsHelmSetup
	case "terraform":
		return devopsTerraformSetup
	default:
		return ""
	}
}

func devopsToolCleanup(serviceID string) string {
	switch serviceID {
	case "kubectl":
		return "set -eu; rm -f /usr/local/bin/kubectl"
	case "helm":
		return "set -eu; rm -f /usr/local/bin/helm"
	case "terraform":
		return "set -eu; rm -f /usr/local/bin/terraform"
	default:
		return ""
	}
}
