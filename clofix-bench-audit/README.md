# CloFix FileVPS benchmark and isolation audit

Compare CloFix FileVPS with Incus and LXC on one dedicated Linux host. These scripts create throw-away guests, measure idle overhead, and probe isolation. They are not a kernel exploit assessment.

## What was updated

The original scripts called a placeholder `clofix-fx` CLI and looked for `/var/lib/clofix-fx`. They now use the real worker:

```text
clofix-worker fx create|start|exec|delete
```

Guests are stored under `/var/lib/clofix-filevps/instances/<id>/rootfs-mount`. Create uses the bundled Ubuntu 24.04 image, an SSH public key, cgroup limits, and Protection Mode off so the audit can write inside the guest.

**Catalog additions (Nov 2026):** `B09` (Docker volume backup), `A10`/`A11` (edge WAF). Re-run product probes and clear NOT RUN / SKIPPED:

```bash
./run-unit-notrun-pass.sh   # Go UNIT + single-worker SMOKE + honest SKIPPED (not “all PASS”)
```

See product docs: [book.md](../book.md) §9.3 (edge WAF), [docs/docker.md](../docs/docker.md) (volume Backup & Restore).

## Host requirements

- Root on a throw-away Linux worker (not this development Mac, not production).
- `clofix-worker` installed, with the Ubuntu 24.04 rootfs already cached.
- Optional: Incus and/or LXC for comparison.
- Stop `clofix-worker run` while the local `fx` CLI creates guests, so two processes do not share the same state directory.

Do not claim Ubuntu, CentOS, Debian, Incus, or LXC support until that distro has been installed and these scripts have been run there.

## Run

```bash
# FileVPS only (lab / compose worker OK for isolation probes)
sudo ./clofix-bench.sh fx
sudo ./clofix-audit.sh fx
sudo ./clofix-audit.sh fx --destructive   # bounded pids/memory/disk/CPU probes

# Product catalog honesty (UNIT / SMOKE / SKIPPED — not “all PASS”)
./run-unit-notrun-pass.sh
python3 make-report.py results results/report.md
```

### Native Ubuntu comparison (recommended for client/partner evidence)

Run on a **throw-away bare-metal or VM Ubuntu host** (not Docker Desktop / linuxkit). Install CloFix worker, then optional LXC and/or Incus. Stop `clofix-worker run` while the local `fx` CLI owns the state dir.

```bash
# Ubuntu 22.04/24.04 example
sudo apt-get update
# install clofix-worker + cached Ubuntu 24.04 rootfs per product docs
sudo apt-get install -y lxc lxd   # or Incus per upstream docs
sudo ./run-compare.sh            # backends that are installed
python3 make-report.py results results/report.md
```

Until that compare run exists, **P01/P02 and cross-runtime rows stay SKIPPED**. Do not paste linuxkit CloFix-only numbers as a Docker/LXC/Incus bake-off.

**Security wording:** audit **42 PASS** means 42 tested conditions on that host, **not** kernel/LSM certification. AppArmor/SELinux stays SKIPPED when the host has no LSM.

The generated report includes a master catalog of the P0/P1 QA checklist. Statuses are FAIL/WARN/PASS/MEASURED/UNIT/SMOKE/SKIPPED/NOT RUN. Counts are reconciled (`status sum == catalog rows`).

Outputs:

- `results/bench-<backend>.tsv`
- `results/audit-<backend>.tsv`
- `results/report.md` and `results/report.html`

Density tests skip FileVPS guest networking because the engine only has SSH host ports 22000–22099. Idle and audit guests still get a `10.231.0.0/16` address.

## Fairness notes

- FileVPS guests share the host kernel. Do not describe them as KVM/QEMU VMs.
- FileVPS always applies CPU, memory, swap, and PID cgroup limits. Incus/LXC defaults may not.
- `disk_mb` on FileVPS is placement accounting; per-guest disk quota is not enforced yet.
- Override `INCUS_IMAGE` or `BASE_DISTRO`/`BASE_REL` if you want Incus/LXC to use Ubuntu 24.04 as well.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `FX_BIN` | `clofix-worker` | Worker binary |
| `FX_RAM_MB` / `FX_CPU` / `FX_PIDS` | `256` / `0.5` / `256` | FileVPS limits |
| `FX_SWAP_MB` | `0` | FileVPS swap cap |
| `FX_NETWORK` | `1` | Assign FileVPS IP + SSH port |
| `N_START` / `N_IDLE` / `DENSITY_MAX` | `5` / `10` / `200` | Bench sizes |
| `OUT` | `./results` | Result directory |
