# Changelog

## 1.0.0 - CloFix Guard

- Phase 1 inventory for existing `rootfs.tar.gz` (Ubuntu noble / arm64 FileVPS rootfs).
- Branding aligned to `ID=clofix`, website https://clofix.com.
- Go `clofix` CLI + `clofix-guard` security agent with real FIM, SSH, process, network, privilege, vuln, scoring, policy, AutoPilot categories.
- Host-worker security API skeleton (HMAC, nonce/replay, audit).
- `harden-rootfs.sh` baseline (sysctl, SSH recommendations, Guard configs).
- Strong guest/host kernel security sysctl (`config/sysctl/99-clofix-kernel-security.conf`, host variant).
- Admin DB schema parity migration `0011_schema_parity.up.sql` for existing deployments (`deploy/admin/migrate-db.sh`).
- Unit tests for FIM + scoring; build pipeline compiles linux/amd64 + arm64 binaries.
