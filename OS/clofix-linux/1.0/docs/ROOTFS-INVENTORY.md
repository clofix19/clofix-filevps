# CloFix Linux 1.0 - RootFS inventory (Phase 1)

**Source archive:** `OS/clofix-linux/1.0/rootfs.tar.gz`  
**Inspected:** 2026-11-17 (UTC)  
**Path traversal check:** PASS (no absolute paths, no `../` members)

## Detected base

| Field | Value | Evidence |
|-------|--------|----------|
| Product branding | CloFix Linux 1.0 | `/etc/os-release` |
| Package ID (pre-security pass) | `clofix-linux` | `/etc/os-release` |
| Ubuntu packaging codename | **noble** | `UBUNTU_CODENAME=noble` |
| Ubuntu release family | **24.04 LTS** | noble |
| `/etc/debian_version` | `trixie/sid` (unchanged; Docker/base-files quirk) | preserved for dpkg |
| Architecture | **aarch64** (`arm64`) | `usr/lib/aarch64-linux-gnu/` |
| Package manager | **APT / dpkg** | `usr/bin/apt-get`, `var/lib/dpkg/status` |
| Approx. installed packages | ~125 | `dpkg` status Package: count |
| Archive members | ~4327 | `tar -tzf` |
| Archive size | ~35 MiB compressed | filesystem |

## Boot / kernel model

| Question | Finding |
|----------|---------|
| Independent boot? | **No** - `boot/` present but empty of kernel/initramfs; no GRUB payload |
| systemd PID 1 binary? | **ABSENT** (`usr/lib/systemd/systemd` missing) - container/FileVPS rootfs |
| Shared host kernel? | **Yes** (CloFix FileVPS / Docker export target) |
| Mount requirements | Overlay lower rootfs; host provides `/proc` `/sys` `/dev` `/run` |

## Security facilities in archive

| Facility | Status |
|----------|--------|
| OpenSSH server/client | **PRESENT** |
| nftables | **ABSENT** (installable from Ubuntu noble) |
| iptables | ABSENT |
| AppArmor userspace | ABSENT |
| auditd | ABSENT |
| fail2ban | ABSENT |
| CloFix CLI (`/usr/bin/clofix`) | PRESENT (shell, pre-Guard) |

## APT

- Sources use Ubuntu **deb822** (`/etc/apt/sources.list.d/ubuntu.sources` pattern; stub note in `sources.list`).
- Codename pinned via Ubuntu noble mirrors (ports.ubuntu.com on this arm64 build).
- CloFix APT snippets already ship release-upgrade posture (`Prompt=never` policy files).

## Branding already present

`os-release`, `issue`, `issue.net`, `motd`, `update-motd.d/00-clofix-header`, `profile.d`, `skel`, `clofix` CLI, `/etc/clofix/*`.

## Compatibility notes

1. Never assume 22.04 vs 24.04 without reading `UBUNTU_CODENAME` from the archive under inspection.
2. Guest security features that need host kernel (immutable overlays, seccomp, cgroup kill, true rootkit kernel checks) are **host-worker** scope.
3. Guest kernel sysctl baseline: `/etc/sysctl.d/99-clofix-kernel-security.conf` (see `docs/SHARED-KERNEL.md`). Host-global keys may be no-ops inside shared-kernel VPS; apply `99-clofix-host-kernel-security.conf` on the worker host.
3. Preserve `UBUNTU_CODENAME` and APT GPG verification for all package operations.
4. Do not rewrite `/etc/debian_version` for branding.

## Dependency / compatibility report (summary)

| Consumer | Needs | Risk if broken |
|----------|-------|----------------|
| apt/dpkg | Ubuntu noble repos + keys | No updates/installs |
| OpenSSH | host keys generated at provision | Login failure |
| CloFix Guard | writable `/var/lib/clofix` | Agent cannot persist |
| FileVPS worker | tar extract + overlay | VPS create fails |
| Host isolation | worker namespaces/seccomp | Guest escape / fake security |

Full matrix: see `SECURITY-MATRIX.md` (filled as modules land).
