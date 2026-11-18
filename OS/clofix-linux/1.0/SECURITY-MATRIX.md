# CloFix Linux 1.0.0 — Security implementation matrix

Evidence from unit tests + `scripts/build-rootfs.sh` reports under `.build/reports/`.

| Area | Status | Paths | Evidence |
|------|--------|-------|----------|
| RootFS inspection | PASS | `docs/ROOTFS-INVENTORY.md`, `scripts/inspect-rootfs.sh` | Inventory generated; path-safety OK |
| Branding ID=clofix | PASS | `branding/os-release`, `scripts/brand-rootfs.sh` | validate-rootfs checks |
| APT same-codename | PASS | `config/apt/*`, agent apt_codename | APT smoke in build |
| Release-upgrade prompts off | PASS | apt conf + upgrade-policy | Documented non-absolute lock |
| SSH Guard | PASS | `internal/sshguard` | Assesses sshd_config + auth failures |
| Firewall Guard | PARTIAL | `internal/firewall` | Works when nft+CAP; UNSUPPORTED in unprivileged guest |
| FIM | PASS | `internal/fim` | Unit test modify detection |
| Process Guard | PASS | `internal/process` | Snapshot + indicators; no auto-kill |
| Privilege Guard | PASS | `internal/privilege` | passwd/sudoers/SUID inventory |
| Malware Guard | PARTIAL | `internal/malware` | Hash DB matcher only (honest claim) |
| Rootkit Guard | PARTIAL | `internal/rootkit` | Guest checks; kernel → host |
| Network Guard | PASS | `internal/network` | Listeners + new-port diff |
| Vulnerability Guard | PARTIAL | `internal/vulnerability` | apt simulation; OSV optional + backport caveat |
| Hardening | PASS | `scripts/harden-rootfs.sh`, sysctl | Dry-run/backup supported |
| Modes / AutoPilot | PASS | `internal/policy`, `internal/response` | High-risk requires approval |
| Security score | PASS | `internal/scoring` | Unknown ≠ pass (unit test) |
| clofix CLI | PASS | `cmd/clofix` | Security subcommands implemented |
| clofix-guard agent | PASS | `cmd/clofix-guard`, `internal/agent` | `-once` scan + daemon loop |
| Host-worker API | PARTIAL | `host-worker/api.go`, `internal/hostclient` | Authz/replay/HMAC; not wired into FileVPS worker yet |
| Immutable / Zero Trust host enforce | UNSUPPORTED (guest) | host-worker actions | Requires worker integration |
| Resource bounds | PARTIAL | systemd unit CPU/Memory limits | Benchmark after rootfs rebuild |

## Explicit honesty rules

- Empty malware hash DB ⇒ scanner inactive (not a fake AV).
- nft missing / no CAP_NET_ADMIN ⇒ firewall status `unknown`/`UNSUPPORTED`, never reported as pass.
- Shared-kernel isolation score stays `unknown` until host-worker verifies.
