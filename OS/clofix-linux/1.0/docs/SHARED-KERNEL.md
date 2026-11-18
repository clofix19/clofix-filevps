# Shared-kernel VPS security split

## Guest rootfs (this tree)

- SSH / PAM / user privilege assessment
- Local FIM, malware hash matching, package vulnerability signals
- Security score from guest-visible evidence
- CloFix Guard agent + CLI
- **Kernel security sysctl** - `/etc/sysctl.d/99-clofix-kernel-security.conf`
  - Source: `config/sysctl/99-clofix-kernel-security.conf`
  - Hardens ASLR, ptrace, dmesg/kptr leaks, symlink/FIFO/hardlink protection,
    BPF restrictions, and IPv4/IPv6 spoofing / redirect / martian / SYN-cookie policy
  - `net.*` / many `fs.*` knobs apply inside the VPS namespace
  - Some `kernel.*` keys are host-global; guests may ignore them (expected)

## Trusted host worker (FileVPS)

Must enforce (never trust guest for these):

- User/mount/PID/network namespaces
- cgroups v2, capabilities, seccomp, AppArmor
- Filesystem mount policy + immutable lower/overlays
- Snapshots / rollback
- Host network isolation + resource abuse containment
- **Host kernel sysctl** - `config/sysctl/99-clofix-host-kernel-security.conf`
  - Install on the worker host (not inside guest rootfs)
  - `sudo cp …/99-clofix-host-kernel-security.conf /etc/sysctl.d/ && sudo sysctl --system`

API: `host-worker/` - HMAC-signed requests, tenant isolation, nonce replay protection, audit log.

Do not grant guests broad `CAP_SYS_ADMIN` / kernel module load merely to “enable” a security feature.

## Apply guest baseline on a running VPS

```bash
sysctl --system
# or:
sysctl -p /etc/sysctl.d/99-clofix-kernel-security.conf
```
