# CloFix Linux 1.0.0 — Implementation tasks

**Security Engine:** CloFix Guard · **Base (this artifact):** Ubuntu **noble** / arm64 · https://clofix.com

Status: `PASS` · `PARTIAL` · `FAIL` · `UNSUPPORTED` · `TODO`

## Phase 1 — RootFS inspection — PASS
Inventory: `docs/ROOTFS-INVENTORY.md`. Path traversal check OK. Shared-kernel FileVPS rootfs (no systemd PID1 / empty boot).

## Phase 2 — Branding — PASS
`ID=clofix`, `VERSION_ID=1.0`, `UBUNTU_CODENAME` preserved, `clofix-release` owns branding files.

## Phase 3 — APT policy — PASS
Ubuntu noble repos; smoke `apt update` + `curl` install; foreign suite scan; release-upgrade prompts disabled. Non-absolute lock documented in `docs/APT-POLICY.md`.

## Phase 4 — Security baseline
| Module | Status |
|--------|--------|
| SSH Guard | PASS (config + brute indicators; no auto lockout) |
| Firewall Guard | PARTIAL / UNSUPPORTED without nft+CAP |
| FIM | PASS (+ unit test) |
| Process Guard | PASS (no name-based kill) |
| Privilege Guard | PASS |
| Malware Guard | PARTIAL (hash DB only; empty DB = inactive) |
| Rootkit Guard | PARTIAL (guest); kernel checks → host |
| Network Guard | PASS |
| Vulnerability Guard | PARTIAL (apt sim + optional OSV; backport-aware disclaimer) |
| Hardening | PASS (`harden-rootfs.sh`) |

## Phase 5 — Modes — PASS (policy layer)
Immutable/Zero Trust **enforcement** = host-worker (`PARTIAL` API, not wired to FileVPS worker yet).

## Phase 6 — Shared-kernel — PARTIAL
Docs + HMAC host API skeleton. Worker integration TODO.

## Phase 7–9 — Agent / score / CLI — PASS
`cmd/clofix`, `cmd/clofix-guard`, scoring unknowns ≠ pass (unit test). Smoke: `GUARD_SMOKE_OK`, score.json evidence.

## Phase 10 — Default policy — PASS
Safe baseline on; disruptive controls opt-in.

## Phase 11–13 — Structure / tests / deliverables — PASS
Tree under `OS/clofix-linux/1.0/`; unit tests; rootfs 40M; reports in `.build/reports/`; matrix `SECURITY-MATRIX.md`.

### Not done / next
- Wire `host-worker` into FileVPS `worker` execution engine
- Populate operational malware/KEV feeds
- Install nftables in optional “strict host” profile
- Broader integration tests (SSH brute live, firewall apply/rollback on privileged VM)
