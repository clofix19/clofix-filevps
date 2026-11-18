# Shared-kernel VPS security split

## Guest rootfs (this tree)

- SSH / PAM / user privilege assessment
- Local FIM, malware hash matching, package vulnerability signals
- Security score from guest-visible evidence
- CloFix Guard agent + CLI

## Trusted host worker (FileVPS)

Must enforce (never trust guest for these):

- User/mount/PID/network namespaces
- cgroups v2, capabilities, seccomp, AppArmor
- Filesystem mount policy + immutable lower/overlays
- Snapshots / rollback
- Host network isolation + resource abuse containment

API: `host-worker/` — HMAC-signed requests, tenant isolation, nonce replay protection, audit log.

Do not grant guests broad `CAP_SYS_ADMIN` / kernel module load merely to “enable” a security feature.
