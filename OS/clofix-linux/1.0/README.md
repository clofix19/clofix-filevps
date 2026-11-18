# CloFix Linux 1.0.0

Ubuntu LTS–based, CloFix-branded distribution with **CloFix Guard** security engine.

- Website: https://clofix.com  
- Current inspected artifact base: **Ubuntu noble (24.04)** — always re-detect from the archive under build.

## Quick build (does not build FileVPS worker)

```bash
./scripts/build-binaries.sh
./scripts/build-rootfs.sh
```

Outputs: `rootfs.tar.gz`, `dist/`, `.build/reports/`.

## Commands

```bash
clofix version|info|status
clofix security scan|score|events
clofix security ssh|firewall|fim|process status
clofix security mode list|enable standard|strict|immutable
clofix security autopilot status|enable
clofix security rollback
clofix update|update check
clofix-guard -once          # single scan
clofix-guard                # daemon
```

## Layout

See `TASKS.md`, `SECURITY-MATRIX.md`, `docs/ROOTFS-INVENTORY.md`, `docs/APT-POLICY.md`, `docs/RECOVERY.md`.

## Guest vs host

Guest rootfs: SSH/FIM/vuln/malware-hash/local scoring.  
Trusted host-worker: namespaces, seccomp, cgroups, immutable overlays, snapshots (`host-worker/`).
