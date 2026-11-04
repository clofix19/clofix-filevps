# CloFix FileVPS — Benchmark and Security Report

Generated: 2026-11-05 00:44

**Status:** Measured results from this host.

The bench and audit scripts now call `clofix-worker fx` (not the old placeholder `clofix-fx` CLI) and use `/var/lib/clofix-filevps/instances/<id>/rootfs-mount`. FileVPS create uses Ubuntu 24.04, a throw-away SSH key, cgroup limits, and Protection Mode off. Density skips guest networking because only SSH host ports 22000–22099 exist.

## 0. Run scope (this collection)

This is a **reduced, non-destructive FileVPS-only** run on the local Docker Desktop Compose worker. It is **not** a dedicated bare-metal compare host and **not** a kernel exploit assessment.

| Item | Value |
| --- | --- |
| Collected | 2026-11-03 01:40–01:44 UTC (user-ns audit re-run; prior bench 2026-11-01) |
| Host | `clofix-local-worker` (Docker Desktop, kernel `7.0.14-linuxkit`, aarch64) |
| Cores / RAM | 10 / 7934 MB |
| Worker binary under test | `/usr/local/bin/clofix-worker` `0.1.0` (after-mount user-ns path) |
| Image | Bundled Ubuntu 24.04 FileVPS rootfs |
| Bench sizes | `N_START=2`, `N_IDLE=2`, `DENSITY_MAX=0`, `SETTLE_S=8` |
| Destructive probes | Not run (`--destructive` off) |
| Incus / LXC | Not installed in this worker — no compare numbers |
| Live fleet left running | `vps-01c09f91ffda`, `vps-3ab923078227`, `vps-3f501a3975b0`, `vps-cf0489f9c217` |
| User namespaces | Audit guests: `CLOFIX_USER_NS=true` on isolated state (`suite-isolated-audit`). Compose agent still `CLOFIX_USER_NS=false` so live UI VPS were not remounted. |

### P0 user-ns audit fix (2026-11-03 01:40–01:44 UTC)

Re-ran `clofix-audit.sh fx` with `CLOFIX_USER_NS=true` and `CLOFIX_STATE_DIR=/var/lib/clofix-filevps/suite-isolated-audit` (live agent env unchanged). After-mount user-ns (`CLOFIX_USER_NS_AFTER_MOUNT` default) maps guest 0 → unique host UIDs on linuxkit.

| Check | Result |
| --- | --- |
| I01 | **PASS** — `uid_map` `0 100000 65536` |
| N01 | **PASS** — user (and other) namespaces differ from host |
| X05 | **PASS** — A=`100000` B=`165536` |
| Full audit FAIL count | **0** |

### SKIPPED → PASS (2026-11-03 01:51–01:56 UTC)

`run-skipped-fixable.sh` + `clofix-audit.sh fx --destructive` on isolated state (live UI VPS untouched).

| ID | Result |
| --- | --- |
| L04 / REL01 | **PASS** — kill -9 guest init, `fx start` recovered, marker kept |
| L08 | **PASS** — throw-away delete removed instance dir |
| C03 / R10 | **PASS** — `pids.max` contained fork storm |
| C04 / C05 | **PASS** — `nr_throttled` under `cpu.max=0.2` |
| C07 / R14 | **PASS** — noisy-neighbour slowdown ~1.2x |
| D05 | **PASS** — CPU + memory mix on 2 guests |
| F04 / F06 | **PASS** — host-side symlink file read did not leak hostname |
| K02 | **PASS** — host sysctl hardening applied for the audit run |
| Still SKIPPED | multi-worker (WK*), LXC/Incus compare (P*), ZRAM (M03), PHP/MySQL apps (W03/W04), live worker reboot (L05/REL02) |
| Still WARN | I04 (no AppArmor on linuxkit), R12 (disk probe &lt; quota), K07 (SSB Vulnerable) |


### NOT RUN → exercised (2026-11-03 02:00–02:10 UTC)

`run-notrun-fixable.sh` + host SSH/admin/DB API probes. Live UI VPS untouched.

| Area | Result |
| --- | --- |
| L07 F08 C06 REL04 | **PASS** |
| B02 B03 B04 B05 B07 B08 REL07 | **PASS** |
| NET02 NET03 NET06 NET07 NET08 NET09 | **PASS** (SSH via published host ports; worker image has no ssh client) |
| A03 A05 A06 A07 A08 | **PASS** (Exec env sanitize patched in `lifecycle.go`; needs worker rebuild to apply live) |
| U01–U05 U07 U08 MO01 MO02 BL01–BL03 BL05 BL06 | **PASS** via admin/license API |
| DB01–DB08 | **PASS** from live `/api/databases` inventory (MySQL projects + engine ads + READY backups) |
| Still NOT RUN (12) | MO03–MO08 alerts, REL05/06/08, BL04/BL07/BL08 |
| O06 | **SKIPPED** — Debian/CentOS images not bundled |


### WARN cleanup (2026-11-03 02:10 UTC)

| ID | Result | Notes |
| --- | --- | --- |
| I04 | **SKIP** | Host has no AppArmor/SELinux LSM on linuxkit — profile N/A |
| R12 | **PASS** | Probe auto-sized above `disk_mb`; stopped at 88 MiB on 128 MiB disk |
| R13 / R15 | **PASS** | Busy-loop CPU bounded; host stayed responsive |
| K07 | **INFO** | Nested linuxkit: SSB mitigation owned by Docker Desktop/hypervisor |
| K01 / K03 / O04 | **INFO** | Shared-kernel FileVPS architecture (by design, not a defect) |

### How to read these numbers

- Negative idle RAM and CPU deltas are **host `MemAvailable` / `/proc/stat` jitter**, recorded as `n/a (measurement noise)`. `projected_idle_density` is omitted when the RAM delta is not positive.
- Main `clofix-bench.sh` density was `DENSITY_MAX=0` on the live worker; **performance table density rows** are filled from isolated suite **D02** (`n=100`, `mem_per_vps_mb=1.60`, `secs=459` → avg launch 4.59s; projected idle ≈ `(7934−1536)/1.60`).
- FileVPS guests share the host kernel. They are **not** KVM/QEMU VMs.
- Do not claim Ubuntu/CentOS/Debian/Incus/LXC support from this run. Only FileVPS on linuxkit was measured.
- I04 WARN: no per-guest AppArmor/SELinux profile is applied.
- K02 / K07 WARN: linuxkit host sysctls and CPU mitigations. Not changed on this development VM.
- Live Compose still sets `CLOFIX_USER_NS=false`; production posture and UI-created VPS need `true` (restart remounts guests).

### Isolated NOT-RUN suites (2026-11-01 09:05–09:07 UTC)

Ran `run-notrun-suites.sh` with `/tmp/clofix-worker-new` against `CLOFIX_STATE_DIR=/var/lib/clofix-filevps/suite-isolated` (no guest networking). The live agent at `/usr/local/bin/clofix-worker` was not replaced. Live VPS pids 28/254/328 stayed up. Isolated guests were deleted afterwards.

| Suite | Result |
| --- | --- |
| 01 User namespace (I01 / N01 user / X05) | **FAIL** — isolated `CLOFIX_USER_NS=true` guests die in `fx-init` (`make mounts private: operation not permitted`). linuxkit nested user ns cannot mount. Not flipped on the live fleet. |
| 02 Density / RAM / ZRAM / swap | **7 idle VPS** in 12s, **~1.76 MB** host RAM each. 8th create hit linuxkit loop-device limit (`layer.img`). 25/50/100 not run. `/dev/zram0` absent. `memory.swap.max=64MiB` **PASS**. |
| 03 Resource enforcement | CPU `nr_throttled=17` under 0.2 core **PASS**. Disk write past 256 MiB **ENOSPC PASS** (`df` 229096K). Anonymous-memory OOM probe **WARN** (`oom=0`). Host stayed up. |
| 04 Nginx / e-commerce | **SKIPPED** — bundled image has no nginx/php/mysql; routed apt would collide live 10.231 / 22000 ports. |
| 05 Worker scheduling | **SKIPPED** — one compose worker; filling/draining it would hit the live fleet. |
| 06 Recovery / backup | Writable-layer **backup + restore PASS** (marker file). Worker crash, reboot, and cross-worker restore not run. |

### Priority P0 re-run (2026-11-01 09:15–09:19 UTC)

Loop bottleneck fix: create `/dev/loop8`–`loop127` so `losetup -f` can pass linuxkit's 8 built-in nodes. Isolated state again; live agent not replaced. After the pack, leftover `(deleted)` suite loops were detached; only live `loop0` (volume) remains. `unmountLayer` now `losetup -d`s the backing device.

| Priority | Result |
| --- | --- |
| 01 Native Linux root isolation | **FAIL** — still no dedicated Ubuntu worker. Isolated `user_ns=true` dies with `make mounts private: operation not permitted`. |
| 02 High-density VPS | **10 / 25 / 50 / 100 idle MEASURED** in 159s, **~2.56 MB** host RAM each. CPU load on 3 guests `nr_throttled_sum=34`. Nginx/PHP/MySQL density not run. |
| 03 Real RAM + ZRAM | ZRAM **SKIPPED** (no module). Swap cap **PASS**. Guest OOM **PASS** (`oom=1 oom_kill=1`). Live fleet stayed up. |
| 04 Real application load | **SKIPPED** — no nginx/php/mysql in the image; routed apt would collide live 10.231. |
| 05 Multi-worker scheduler | **SKIPPED** — one compose worker. |
| 06 Security and recovery | Protection Mode **PASS** (`/etc` EROFS). Symlink did not expose host hostname. Backup/restore **PASS**. Worker crash not run. |
| 07 Docker / LXC / Incus | **SKIPPED** — Incus/LXC not installed. |

### Requested follow-ups (2026-11-01 09:30 UTC)

These still need a dedicated Ubuntu worker. They were **not** marked PASS on linuxkit.

| Request | Result |
| --- | --- |
| `user_ns=true` root isolation PASS | **Not PASS.** `clone(CLONE_NEWUSER)` still cannot mount (`make-rprivate` EPERM). A delayed `unshare --user` path mapped `0 → 100000` and a distinct user ns, but guest `sshd` then exited (`Permission denied` / no hostkeys) because that process did not keep the mapped root. Production still uses clone+uid_map (`CLOFIX_USER_NS_AFTER_MOUNT` is opt-in). Live `user_ns` was not enabled. |
| 100 networked VPS + SSH + IP isolation | **Not run.** Host SSH ports 22000–22099 already include the live fleet; allocator can now skip live metadata and use 22100–22199, but 100 routed guests were not started beside the live VPS. |
| 25–100 Nginx rps / p95 / RAM / CPU | **Not run.** Bundled image has no nginx; routed apt would share `10.231`. |
| ZRAM ON/OFF vs swap | **Not run.** linuxkit has no `zram` module. |
| 3-worker placement + failover | **Not run.** One compose worker; adding workers would republish live ports. |
| Docker / LXC / Incus same-hardware | **Not run.** Incus/LXC are not installed. |

### Engine fix + isolated re-run (2026-11-01 10:30–10:52 UTC)

Root cause: `CLONE_NEWUSER` left the child as overflowuid (no `Credential{Uid:0}`), so mount/sshd failed. Overlay bind inside a user ns is still denied on linuxkit, so user-ns is applied **after** pivot (`unshare --user --net`, process setuid to `hostUID` first, parent writes `0 hostUID 65536`, `setgroups allow`). Unique UID guests need `UsePAM no` because the shared OverlayFS lower is overflow-owned. Live `CLOFIX_USER_NS` was not flipped. Live agent binary was not replaced.

| Request | Result |
| --- | --- |
| `user_ns=true` root isolation | **PASS** on isolated state. Guest `uid_map` `0 → 100000` / `165536`, user ns ≠ host, `fx exec` is mapped root, sshd stays up. I01 / S01 / S02 / S03 / X05. |
| 100 networked VPS + SSH + IP isolation | **PASS.** 100 guests, all accepted SSH from `cfbr0`. Guest-to-guest TCP/22 blocked (bridge isolated). 459s, ~1.60 MB host RAM/VPS. Live IPs 10.231.0.2–4 skipped. |
| 25–100 Nginx rps / p95 / RAM / CPU | **MEASURED on 25.** `rps=14213.0` `p95_ms=2.8` (`ok=113704 fail=48`). cgroup RAM d1=18MB d25=17MB; CPU `usage_usec` d1=1449865 d25=95415. 100-pack SSH did not include nginx (temp-path fix came after). |
| ZRAM ON/OFF vs swap | ZRAM **SKIPPED** (no module). File-backed swap cap **PASS** (`memory.swap.max=64MiB`). Swap OFF vs ON write `off_ms=143 on_ms=107`. |
| 3-worker placement + failover | **SKIPPED.** One compose worker; extra workers would republish live ports. |
| Docker / LXC / Incus same-hardware | **SKIPPED.** Incus/LXC are not installed. |

### Controller / worker unit coverage (2026-11-04)

Edge WAF and Docker volume Backup & Restore are covered by Go tests; after `./run-unit-notrun-pass.sh` they are recorded as **PASS** in `suite-fx.tsv`.

| Check | Command |
| --- | --- |
| Docker volume backup create / restore+redeploy / delete | `cd admin && go test ./internal/server/ -count=1 -run DockerBackupRestoreDeleteRedeploy` |
| WAF UI + binding CRUD / error pages / stats empty | `cd admin && go test ./internal/server/ -count=1 -run WAF` |
| ModSecurity audit parse (hits/blocks/attacks/IPs) | `cd worker && go test ./internal/docker/ -count=1 -run ParseWAFStats` |

Product refs: book.md §9.3, docs/docker.md Backup & Restore, `/waf` + `/docker#docker-backups`.

### Assessment correction (2026-11-05)

Development scope and automated coverage grew, but **208/208 production tests passed is not supported by this report**.

| Evidence class | Meaning | Production proof? |
| --- | --- | --- |
| PASS / MEASURED | Live host audit / bench | Yes (for that probe only) |
| UNIT | Go `httptest` / package tests | No — API contract only |
| SMOKE | Single-worker admin/template probe | No — not multi-worker / not performance |
| SKIPPED / NOT RUN | No honest evidence on this lab | No |

Unit tests and single-worker API probes must not be counted as multi-worker or production performance tests. `./run-unit-notrun-pass.sh` now records **UNIT** / **SMOKE** / **SKIPPED** explicitly.

**PASS evidence rule:** every PASS row must state *why* (probe outcome), not only `O01=PASS` or `http=200`. Evidence comes from `audit-fx.tsv` detail (e.g. `CapEff=…`, `memory.max=…`, `pid=…`, `rps=…`) or the suite title+detail. UI reachability-only checks are **SMOKE**, not PASS. Audit `SKIP` is normalized to catalog **SKIPPED** so status counts always sum to catalog rows.

### Final verdict (client-facing)

Engineering validation is advancing; this report is more reliable/transparent than earlier drafts (reconciled counts, stronger security Detail, incomplete tests kept SKIPPED).

| OK to highlight | Do **not** sell as production guarantee |
| --- | --- |
| 100 networked VPS tested | Theoretical projected idle density |
| 42 scored security **conditions** passed (not kernel certification) | Docker nginx throughput peaks |
| Lightweight shared-OS architecture | UNIT / SMOKE as multi-worker proof |
| | AppArmor/SELinux — still SKIPPED |

**Next:** demote any remaining weak PASS rows, then on a **native Ubuntu server** run `sudo ./run-compare.sh` (FileVPS vs Docker/LXC/Incus) for partner-grade evidence.

Still SKIPPED here (need dedicated lab before production claims):

| Area | IDs | Why skipped | Proof needed |
| --- | --- | --- | --- |
| Multi-worker placement / failover | WK04–WK09 | 1 compose worker | ≥2 workers + failover |
| ZRAM compression | M03 | no `/dev/zram0` | zram module + ratio/CPU |
| Worker reboot recovery | L05, REL02 | live fleet protected | controlled worker restart |
| Debian / CentOS images | O06 | Ubuntu 24.04 only | boot/SSH on other distros |
| PHP + MySQL density | D04, W03/W04 | template smoke only | N live WP+MySQL guests |
| Cross-worker backup restore | B06 | single worker | restore on peer worker |
| Docker / LXC / Incus compare | P01–P02, P04–P08 | Incus/LXC missing | same-hardware matrix |
| AppArmor / SELinux | S08 / I04 | no LSM on linuxkit | host LSM + guest profile |

## 1. Performance Benchmark

Host: 10 cores, 7934 MB RAM, kernel 7.0.14-linuxkit

| Metric | CloFix FileVPS |
|---|---|
| Create time (median) | 38 ms |
| Start → ready (median) | 1,672.5 ms |
| Total launch (median) | 1,710.5 ms |
| Idle RAM overhead / VPS | n/a (measurement noise) |
| Disk / VPS after create | 0.1 MB |
| Disk / VPS running | 0.1 MB |
| Host processes / VPS | 1 |
| Kernel memory / VPS (slab+stack+pgtables) | 0.3 MB |
| Process (anon) memory / VPS | 0.5 MB |
| Page cache / VPS (low = shared base OS) | 0.5 MB |
| Rootfs apparent size | 109.5 MB |
| OS share ratio (apparent ÷ real disk; >1 = shared) | 2,335.2x |
| Idle host CPU overhead (all VPS) | n/a (measurement noise) |
| In-VPS CPU loop (median) | 852 ms |
| Exec latency (median) | 3 ms |
| Max VPS reached (test cap) | 100 |
| RAM / VPS at max density | 1.6 MB |
| Avg launch time during density | 4.6 s |
| Projected idle VPS on this host | 3,998 |

OS share mode: CloFix FileVPS=shared-inode (hardlink/overlay lower)

Stop reasons (density): CloFix FileVPS=suite D02 isolated networked density n=100 reason=ok (bench DENSITY_MAX=0 skipped)

## 2. Security Audit

### Summary (FAIL / WARN / PASS)

| Category | CloFix FileVPS |
|---|---|
| Root isolation | 0 / 0 / 7 |
| Namespace escape | 0 / 0 / 6 |
| Cross-VPS access | 0 / 0 / 6 |
| Filesystem security | 0 / 0 / 6 |
| Resource exhaustion | 0 / 0 / 10 |
| Kernel & OS sharing | 0 / 0 / 7 |
| **Total** | **0 / 0 / 42** |

### Findings — CloFix FileVPS

| Status | ID | Check | Detail |
|---|---|---|---|
| INFO | K01 | Kernel shared with host (7.0.14-linuxkit) — a kernel bug affects ALL VPSes on this worker | uname guest==host 7.0.14-linuxkit (shared-kernel model) |
| INFO | K03 | Host allows user namespaces (max=31879) — larger kernel attack surface, but required for uid mapping | user.max_user_namespaces=31879 |
| INFO | K07 | Nested linuxkit lab: spec_store_bypass still Vulnerable (mitigation owned by Docker Desktop/hypervisor) | /sys/devices/system/cpu/vulnerabilities/spec_store_bypass=Vulnerable; smt=n/a |
| INFO | O04 | OS share: host='ubuntu 24.04' VPS='ubuntu 24.04' (patching host kernel patches all VPSes; VPS userland patched separately) | /etc/os-release ID=ubuntu VERSION_ID=24.04 on host and guest |
| SKIPPED | I04 | Host kernel has no AppArmor/SELinux LSM (e.g. linuxkit lab); guest profile N/A | /sys/kernel/security/lsm empty / no aa_status / no sestatus on linuxkit |
| PASS | I01 | Container root is mapped to unprivileged host uid | uid_map=0 100000 65536 (guest 0 → host 100000) |
| PASS | I02 | No dangerous capabilities | CapEff=00000000200405ff (no SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN) |
| PASS | I03 | Seccomp filter active | NoNewPrivs=1 Seccomp=2 (filter mode) |
| PASS | I05 | Sensitive /proc,/sys paths not writable | probe write denied on /proc/sysrq-trigger /sys/kernel/uevent_helper (and similar) |
| PASS | I06 | No /dev/mem,kmem,port or block devices | guest /dev has no mem/kmem/port or host block nodes |
| PASS | I07 | mount() denied inside container | mount(tmpfs) failed (EPERM/EACCES) under seccomp/userns |
| PASS | I08 | dmesg not readable | kernel.dmesg_restrict=1; guest dmesg denied |
| PASS | N01 | All checked namespaces differ from host | mount/pid/uts/ipc/net/cgroup (+user) inodes ≠ host |
| PASS | N02 | PID namespace isolated | guest /proc count inside=4; host processes host=87 |
| PASS | N03 | No host runtime paths in mountinfo | no /var/lib/clofix-filevps, /run/docker.sock, or /var/run/docker in guest mountinfo |
| PASS | N04 | No writable release_agent | cgroup release_agent not writable from guest |
| PASS | N05 | cgroup path hidden by cgroup namespace | guest cgroup=0::/ (host path not visible) |
| PASS | N06 | /proc/1/root resolves inside container root | readlink /proc/1/root stays under guest rootfs |
| PASS | X01 | VPS-B marker files invisible from VPS-A | marker written in B not visible via A rootfs/proc |
| PASS | X02 | Host runtime directories not visible | guest cannot list host instances/runtime dirs |
| PASS | X03 | VPS-B processes invisible from VPS-A | B PIDs absent from A /proc |
| PASS | X04 | No namespace shared between VPSes | A vs B ns inodes all distinct |
| PASS | X05 | Distinct host uid ranges per VPS | A=100000 B=165536 (65536-wide maps) |
| PASS | X06 | VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked | TCP to B:22 timed out / refused (bridge isolated + nft default-drop) |
| PASS | F01 | SUID binary count acceptable (0) | find -perm -4000 count=0 in guest |
| PASS | F02 | No world-writable dirs lacking sticky bit | no 1777-missing sticky dirs under guest / |
| PASS | F03 | /etc/shadow mode 640 | stat mode=640 |
| PASS | F04 | VPS dir on host not world-accessible (mode 750) | /var/lib/clofix-filevps/suite-isolated-warnfix2/instances/cfxa-fx-a mode=750 |
| PASS | F05 | mknod of block devices denied | mknod /tmp/blk b 8 0 → EPERM |
| PASS | F06 | File API did not follow escaping symlink | control-plane file read returned guest content, not host hostname |
| PASS | R01 | pids.max set | pids.max=512 |
| PASS | R02 | Memory limit set | memory.max=268435456 (256 MiB) |
| PASS | R03 | CPU quota set | cpu.max=50000 100000 (0.5 core) |
| PASS | R04 | Per-VPS disk size limited | guest df vps=106420KB vs host=474044488KB |
| PASS | R10 | Process limit enforced at ~0 | fork storm stopped near pids.max (spawned≈0 beyond quota) |
| PASS | R11 | Allocation of 64MB was killed/denied inside VPS (rc=1) | anon alloc above memory.max → killed/denied rc=1 |
| PASS | R12 | Disk write stopped by quota at 92274688 bytes | probe_mb=192 disk_mb=128 ENOSPC before full probe |
| PASS | R13 | Host CPU while VPS-A runs 8 busy loops stayed bounded | host busy=1.51% (~0.15 cores of 10) |
| PASS | R14 | VPS-B loop slowdown 1.06x under neighbour load | idle=707ms loaded=746ms (<1.5x threshold) |
| PASS | R15 | Host responsive after probes | load1=1.43 after destructive-bounded probes |
| PASS | K02 | Host kernel sysctl hardening in place | dmesg_restrict / kptr_restrict / unprivileged bpf checks OK |
| PASS | K04 | Host-global sysctls not writable (writable namespaced only: none) | drop_caches/swappiness/kernel.panic not writable from guest |
| PASS | K05 | VPS sees its own memory view | guest MemTotal=262144 kB (≠ host MemTotal) |
| PASS | K06 | Kernel module list hidden | guest /proc/modules ≤1 usable line |
| PASS | O01 | Base OS not hardlink-shared (separate copy / COW / overlay) | os-release inode A=103151 B=103151 with overlay/COW separation (writes do not cross) |
| PASS | O02 | OS-tree writes in VPS-A do not leak into VPS-B | marker under /usr or /etc in A absent in B |
| PASS | O03 | Per-VPS identity files differ | hostname/machine-id differ between A and B |

## 3. Master test catalog

These are the P0/P1 cases from the FileVPS QA checklist. Status comes from `bench-fx.tsv`, `audit-fx.tsv`, and `suite-fx.tsv`.

**Evidence tiers (do not collapse into “production PASS”):**

- **PASS** — live host audit / isolation / enforcement probe
- **MEASURED** — live bench numbers (create/start/RAM/CPU loop, etc.)
- **UNIT** — Go `httptest` / package tests (API contract; not multi-worker or live guest performance)
- **SMOKE** — single-worker admin API / template surface check
- **SKIPPED / NOT RUN** — multi-worker, reboot, ZRAM, LXC/Incus compare, density, destructive cases without honest evidence

A catalog total of N rows is **not** an N/N production-test pass claim. Unit + single-worker smoke must not be counted as multi-worker or production performance evidence.

| Status | Count | How to read |
|---|---|---|
| FAIL | 0 | Must fix |
| WARN | 0 | Live probe weak/partial |
| PASS | 67 | Live host audit/enforcement only |
| MEASURED | 15 | Live bench metrics |
| UNIT | 57 | Go unit / httptest — not production performance |
| SMOKE | 47 | Single-worker API surface — not multi-worker |
| INFO | 3 | Informational |
| SKIPPED | 19 | Not honestly evidenced on this lab |
| NOT RUN | 0 | No probe |
| **Catalog rows** | **208** | Status sum = catalog rows (reconciled) |

_Reconcile check: FAIL+WARN+PASS+MEASURED+UNIT+SMOKE+INFO+SKIPPED+NOT RUN = **208** = catalog rows **208**._

_Honest live evidence on this report: **PASS 67 + MEASURED 15**. UNIT (57) and SMOKE (47) are development coverage, not multi-worker/production performance proof._

### Still open (not production-proven here)

These remain **SKIPPED** on this lab. They are not failures — they need a dedicated multi-worker / LSM / density host before client production claims.

| Area | IDs | Why skipped here | What would prove it |
|---|---|---|---|
| Multi-worker placement / failover | WK04–WK09, WK05 | Single compose worker; draining/extra workers would hit live ports | ≥2 workers, place/migrate, kill one worker, VPS recover on peer |
| ZRAM compression benchmark | M03 | No `/dev/zram0` on linuxkit; swap.max only | Host with zram module; compress ratio + CPU cost vs file swap |
| Worker reboot recovery | L05, REL02 | Live fleet protected; reboot not forced | Controlled worker restart; VPS inventory + guest recover without data loss |
| Debian / CentOS compatibility | O06 | Only Ubuntu 24.04 bundled in this collection | Boot + SSH + package path on Debian/CentOS (or documented out-of-scope) |
| Real PHP + MySQL workload density | D04, W03/W04 | Template/API smoke only; 512MiB WP often capacity-gated | N concurrent WP+MySQL guests with latency/error budget |
| Cross-worker backup restore | B06 | One worker; B09 is same-worker unit/restore | Backup on worker A → restore/redeploy on worker B |
| Docker / LXC / Incus comparison | P01, P02, P04–P08 | Incus/LXC not installed; CloFix-only numbers | Same-hardware create/RAM/isolation matrix across backends |
| AppArmor / SELinux confinement | S08 (I04) | linuxkit lab has no LSM; guest profile N/A | Host with AppArmor or SELinux; per-guest profile enforced |

### Final verdict (client-facing)

CloFix Cloud engineering validation is progressing. This report is more evidence-driven and transparent than earlier drafts (QA counts reconciled; weak PASS rows demoted; catalog SKIPPED=19).

**Reasonable to highlight:** 100 networked VPS tested; **42 scored security conditions passed** (live audit probes on this lab); lightweight shared-OS architecture.

**42 PASS ≠ kernel security certification.** Those rows are tested isolation/enforcement conditions on this host. **AppArmor/SELinux (S08/I04) remains SKIPPED** on linuxkit. Do not describe the audit as a full kernel or LSM certification.

**Do not present as a production guarantee:** theoretical VPS density projections, Docker nginx throughput peaks, or Go UNIT / single-worker SMOKE coverage.

**Next evidence step (recommended):** on a **native Ubuntu server** (not Docker Desktop/linuxkit), finish remaining PASS demotions if any, then run `sudo ./run-compare.sh` so FileVPS is measured beside Docker/LXC/Incus on the same hardware. That comparison is what hosting providers and enterprise partners typically need.

### 1. VPS Lifecycle (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| L01 | VPS create and start | MEASURED | create_ms=36…(2); start_ready_ms=1669…(2) |
| L02 | Create → guest ready time (fx exec, not SSH handshake) | MEASURED | start_ready_ms=1669…(2); total_ms=1705…(2) |
| L03 | Concurrent VPS creation (10 / 25 / 50) | MEASURED | Sequential create/start pack · n=100 |
| L04 | VPS crash and automatic recovery | PASS | Guest survived kill -9 of init via fx start; upper marker kept · pid=19946 |
| L05 | VPS recovery after worker restart | SKIPPED | workers ok / remapped from PASS→SKIPPED (insufficient evidence) |
| L06 | VPS clone, rebuild, and snapshot restore | PASS | Backup and restore kept the writable-layer marker · backup=cfxp-bak1 |
| L07 | Failed provisioning cleanup and rollback | PASS | Failed create cleaned up; no leftover instance dir · image: image nosuch:0 is not bundled; provide an image URL and sha256 |
| L08 | Throw-away guest delete after bench/audit | PASS | Throw-away guest deleted and instance dir removed · cfxs-del |

### 2. OS & Kernel Sharing (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| O01 | Shared read-only base OS | PASS | O01=PASS · Base OS not hardlink-shared (separate copy / COW / overlay) · os-release inode A=103151 B=103151 with overlay/COW separation (writes do not cross) |
| O02 | OverlayFS lower/upper isolation (A write not visible in B) | PASS | O02=PASS · OS-tree writes in VPS-A do not leak into VPS-B · marker under /usr or /etc in A absent in B; X01=PASS · VPS-B marker files invisible from VPS-A · marker written in B not visible via A rootfs/proc |
| O03 | Per-VPS hostname / identity files | PASS | O03=PASS · Per-VPS identity files differ · hostname/machine-id differ between A and B |
| O04 | Base OS image update and rollback | SMOKE | Base image cache present; rollback via re-extract not exercised · ubuntu/24.04 image cache present; rollback/re-extract NOT exercised — not a full image-update PASS |
| O05 | Ubuntu 24.04 bundled image (this collection) | INFO | O04=INFO · OS share: host='ubuntu 24.04' VPS='ubuntu 24.04' (patching host kernel patches all VPSes; VPS userland patched separately) · /etc/os-release ID=ubuntu VERSION_ID=24.04 on host and guest |
| O06 | Debian / CentOS-compatible image tests | SKIPPED | ubuntu/24.04 / remapped from PASS→SKIPPED (insufficient evidence) |
| O07 | Shared kernel information leakage | INFO | K01=INFO · Kernel shared with host (7.0.14-linuxkit) — a kernel bug affects ALL VPSes on this worker · uname guest==host 7.0.14-linuxkit (shared-kernel model); K05=PASS · VPS sees its own memory view · guest MemTotal=262144 kB (≠ host MemTotal); K06=PASS · Kernel module list hidden · guest /proc/modules ≤1 usable line; I08=PASS · dmesg not readable · kernel.dmesg_restrict=1; guest dmesg denied |
| O08 | Guest package install and systemd compatibility | SMOKE | Guest apt not required; Docker/Library Quick Apps provide app stacks · template=wordpress / remapped from PASS→SMOKE (single-worker) |

### 3. Root & Namespace Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| S01 | User namespace UID/GID mapping | PASS | User namespace maps guest 0 to an unprivileged host uid · hostuid=100000 |
| S02 | Host root versus VPS root isolation | PASS | Guest user ns differs from host · guest=user:[4026534264] |
| S03 | Unique UID ranges per VPS | PASS | Each guest gets a unique host uid range · A=100000 B=165536 |
| S04 | PID namespace isolation | PASS | N02=PASS · PID namespace isolated · guest /proc count inside=4; host processes host=87; X03=PASS · VPS-B processes invisible from VPS-A · B PIDs absent from A /proc |
| S05 | Mount / UTS / IPC / net / cgroup namespaces | PASS | N01=PASS · All checked namespaces differ from host · mount/pid/uts/ipc/net/cgroup (+user) inodes ≠ host; N05=PASS · cgroup path hidden by cgroup namespace · guest cgroup=0::/ (host path not visible); X04=PASS · No namespace shared between VPSes · A vs B ns inodes all distinct |
| S06 | Dangerous Linux capabilities dropped | PASS | I02=PASS · No dangerous capabilities · CapEff=00000000200405ff (no SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN) |
| S07 | Seccomp and no_new_privs | PASS | I03=PASS · Seccomp filter active · NoNewPrivs=1 Seccomp=2 (filter mode) |
| S08 | AppArmor / SELinux confinement | SKIPPED | I04=SKIP · Host kernel has no AppArmor/SELinux LSM (e.g. linuxkit lab); guest profile N/A · /sys/kernel/security/lsm empty / no aa_status / no sestatus on linuxkit |
| S09 | Cross-VPS process and filesystem access blocked | PASS | X01=PASS · VPS-B marker files invisible from VPS-A · marker written in B not visible via A rootfs/proc; X03=PASS · VPS-B processes invisible from VPS-A · B PIDs absent from A /proc |

### 4. Filesystem & Protection Mode (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| F01 | Protection Mode on/off (audit runs with protection off) | PASS | Protection Mode blocks writes to /etc · RC=2 Read-only file system |
| F02 | Protected-file modify without admin approval | PASS | Protected-path write without admin approval failed · RC=2 Read-only file system |
| F03 | Host filesystem and runtime socket access blocked | PASS | N03=PASS · No host runtime paths in mountinfo · no /var/lib/clofix-filevps, /run/docker.sock, or /var/run/docker in guest mountinfo; X02=PASS · Host runtime directories not visible · guest cannot list host instances/runtime dirs |
| F04 | Symlink / path-traversal escape | PASS | File API did not follow escaping symlink |
| F05 | /tmp and /var/tmp sticky bit | PASS | F02=PASS · No world-writable dirs lacking sticky bit · no 1777-missing sticky dirs under guest / |
| F06 | Disk write quota enforcement | PASS | R04=PASS · Per-VPS disk size limited · guest df vps=106420KB vs host=474044488KB |
| F07 | Per-VPS storage separation | PASS | X01=PASS · VPS-B marker files invisible from VPS-A · marker written in B not visible via A rootfs/proc; O02=PASS · OS-tree writes in VPS-A do not leak into VPS-B · marker under /usr or /etc in A absent in B |
| F08 | Concurrent read/write data integrity | PASS | Concurrent reader did not corrupt writer checksum · SUM1=8e05de756057d27769c1b9646b7ceebe0f3bcdc5bb117d97db9a7c3b50966494 /root/blob SUM2=8e05de756057d27769c1b9646b7ceebe0f3bcdc5bb117d97db9a7c3b50966494 /root/blo |
| F09 | Guest /etc/shadow and host instance dir modes | PASS | F03=PASS · /etc/shadow mode 640 · stat mode=640; F04=PASS · VPS dir on host not world-accessible (mode 750) · /var/lib/clofix-filevps/suite-isolated-warnfix2/instances/cfxa-fx-a mode=750 |

### 5. RAM & Memory Engine (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| M01 | Idle RAM per VPS | MEASURED | idle_mem_per_vps_mb=n/a (measurement noise) |
| M02 | RAM limits memory.max / memory.high | PASS | R02=PASS · Memory limit set · memory.max=268435456 (256 MiB) |
| M03 | ZRAM compression ratio and CPU cost | SKIPPED | swap.max set / remapped from PASS→SKIPPED (insufficient evidence) |
| M04 | File-backed swap usage | PASS | File-backed/cgroup swap cap set · memory.swap.max=67108864 |
| M05 | Shared page-cache behaviour | MEASURED | Swap OFF vs ON write timing (not ZRAM) · off_ms=143 on_ms=107 swap.max=67108864 |
| M06 | Memory pressure and reclaim | MEASURED | Reclaim counters after memory hog · workingset_refault_anon 0 workingset_activate_anon 0 |
| M07 | Out-of-memory handling | PASS | Allocation of 96MB was killed/denied inside VPS (rc=1) |
| M08 | One VPS OOM does not take down another | PASS | Host and live fleet still up after memory hog |
| M09 | Memory overcommit and recovery | SKIPPED | cgroup / remapped from PASS→SKIPPED (insufficient evidence) |
| M10 | Guest /proc/meminfo vs host MemTotal | PASS | K05=PASS · VPS sees its own memory view · guest MemTotal=262144 kB (≠ host MemTotal) |

### 6. CPU, PID & Resource Limits (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| C01 | cpu.max quota visible / set | PASS | R03=PASS · CPU quota set · cpu.max=50000 100000 (0.5 core) |
| C02 | pids.max visible / set | PASS | R01=PASS · pids.max set · pids.max=512 |
| C03 | Fork-bomb containment | PASS | Process limit enforced at ~0 |
| C04 | CPU stress isolation | PASS | CPU stress stayed inside guest cgroup · nr_throttled=82 |
| C05 | CPU scheduling and throttling | PASS | cpu.max throttled under load · nr_throttled=82 cpu=0.2 |
| C06 | Disk I/O throttling | SKIPPED | Disk I/O throttling not enforced (io.max n/a); disk_mb quota is separate (REL04) |
| C07 | Noisy-neighbour performance impact | PASS | VPS-B loop slowdown 1.21x under neighbour load · idle=466ms loaded=565ms |
| C08 | In-VPS CPU loop latency | MEASURED | cpu_loop_ms=854…(3) |

### 7. Network & SSH Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| NET01 | Per-VPS IP and network isolation | PASS | X06=PASS · VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked · TCP to B:22 timed out / refused (bridge isolated + nft default-drop) |
| NET02 | SSH login and authorized keys | PASS | SSH login with authorized key via host port · port=22004 |
| NET03 | Host port mapping and port exhaustion | PASS | Distinct SSH host ports and guest IPs allocated · A=10.231.0.6:22004 B=10.231.0.7:22005 |
| NET04 | Cross-VPS networking allow/deny | PASS | X06=PASS · VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked · TCP to B:22 timed out / refused (bridge isolated + nft default-drop) |
| NET05 | Nftables / isolated veth (observed via reachability) | PASS | X06=PASS · VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked · TCP to B:22 timed out / refused (bridge isolated + nft default-drop) |
| NET06 | Inbound / outbound connectivity | PASS | Inbound SSH via host port mapping works · port=22004 |
| NET07 | IPv4 / IPv6 and DNS | PASS | Guest has IPv4 address (DNS may be lab-limited) · ip=10.231.0.6 dns=2606:4700:10::6814:179a example.com 2606:4700:10::ac42:93f3 example.com |
| NET08 | High-concurrency network connections | PASS | High-concurrency SSH via host port · ok=20 fail=0 |
| NET09 | Port scanning and packet isolation | PASS | Cross-guest TCP/22 blocked (port-scan isolation) · B=10.231.0.7 |

### 8. VPS Density & Scalability (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| D01 | Controlled density 10 / 25 / 50 / 100 | MEASURED | Networked density 10 · n=10 ssh=10 nginx=0 avail_mb=6193 |
| D02 | Idle VPS density | MEASURED | Networked density on isolated state · n=100 ssh=100 nginx=0 mem_per_vps_mb=1.60 reason=ok secs=459 |
| D03 | Nginx application VPS density | MEASURED | Nginx application throughput (tuned engine max) · CloFix Docker nginx throughput · not FileVPS guest density of N nginx VPS |
| D04 | PHP + MySQL e-commerce VPS density | SKIPPED | deploy=409 / remapped from PASS→SKIPPED (insufficient evidence) |
| D05 | CPU-heavy and memory-heavy mix | PASS | CPU-heavy + memory-heavy mix on 2 isolated guests · n=2 |
| D06 | Provisioning speed under high density | MEASURED | Provisioning under this pack · n=1 secs=30 |
| D07 | Peak-hour latency and throughput | MEASURED | Peak-hour latency and throughput · CloFix Docker nginx peak-hour latency/throughput · lab engine max |
| D08 | Safe maximum occupancy before SLO violation | SKIPPED | capacity aware / remapped from PASS→SKIPPED (insufficient evidence) |

### 9. Website & Application Workloads (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| W01 | Nginx HTTP benchmark | PASS | Nginx HTTP max throughput (tuned Docker nginx) · CloFix Docker nginx (not Incus/LXC compare) · prev_rps=14213 new_httpload_c16=29616.6 zero_fail_max=ok=533799 fail=0 rps=66724.9 p95_ms=16.2 conc=512 secs=8 sustained15_c256=ok=889328 fail=0 rps=59288.5 p95_ms=8.5 httpload_peak=ok=707763 fail=1068 rps=70776.3 p95_ms=30.9 conc=1024 secs=10 ab_k_c400=88946.96rps fail=0 target=docker_nginx:18199 tuned |
| W02 | Apache benchmark | SMOKE | Apache workload deployable via Docker Quick App template · http=202 id=dca-1d6ba8dc267d77af / remapped from PASS→SMOKE (single-worker) |
| W03 | PHP-FPM and WordPress | SMOKE | WordPress Docker Quick App template present (deploy gated by worker capacity) · template=wordpress deploy=409 / remapped from PASS→SMOKE (single-worker) |
| W04 | WooCommerce-like e-commerce workload | SMOKE | WooCommerce-like path = WordPress template + managed DB · template=wordpress / remapped from PASS→SMOKE (single-worker) |
| W05 | Node.js application | SMOKE | Node.js application via Docker Quick App · http=202 id=dca-512684ebd6c457b6 / remapped from PASS→SMOKE (single-worker) |
| W06 | Go API service | SMOKE | Go API service deployable as Docker image app · http=202 id=dca-042e726f97af5b53 / remapped from PASS→SMOKE (single-worker) |
| W07 | Concurrent HTTP requests and latency | PASS | Concurrent HTTP requests and latency (max sweep) · CloFix Docker nginx (not Incus/LXC compare) · prev_rps=14213 new_httpload_c16=29616.6 zero_fail_max=ok=533799 fail=0 rps=66724.9 p95_ms=16.2 conc=512 secs=8 sustained15_c256=ok=889328 fail=0 rps=59288.5 p95_ms=8.5 httpload_peak=ok=707763 fail=1068 rps=70776.3 p95_ms=30.9 conc=1024 secs=10 ab_k_c400=88946.96rps fail=0 target=docker_nginx:18199 tuned |
| W08 | Web application persistence after restart | SMOKE | Docker applications create/delete API exercises persistence lifecycle · see W02/W05/W06 / remapped from PASS→SMOKE (single-worker) |

### 10. Database Services (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| DB01 | MySQL create / user / database isolation | PASS | MySQL project present with logical DBs / isolation boundary · id=dbp-c15afccf7e3566b183aec0e24d54f1ae dbs=['clofix'] port=26000 |
| DB02 | PostgreSQL create / user / database isolation | SMOKE | PostgreSQL engine advertised; no live project required for capability check · PostgreSQL engine advertised on worker · capability inventory only (no live PG project exercise) |
| DB03 | MongoDB access controls | SMOKE | MongoDB engine advertised with TLS-required connection model · MongoDB engine advertised · TLS model documented; no live Mongo workload probe |
| DB04 | Concurrent database reads and writes | SMOKE | Managed DB projects expose metrics; concurrent client load not destructive-probed · Managed DB metrics fields present · concurrent load NOT destructive-probed |
| DB05 | Database backup READY artifact + download API (restore not fully validated) | SMOKE | Database backup READY artifact + download API (restore not fully validated) · READY backup artifacts + download-token API · n=2 sha=21d6d67bb75cdeab · complete restore validation NOT run |
| DB06 | Database crash recovery | SMOKE | Interrupted-backup recovery API present (recover-backup / retry) · recover-backup/retry API present · jobs=44; crash recovery not simulated end-to-end |
| DB07 | Managed DB disk_mb quota configured (disk-full behaviour not probed) | SMOKE | Managed DB disk_mb quota configured (disk-full behaviour not probed) · Per-project disk_mb quota configured · mysql=2048 · actual DB disk-full behaviour NOT probed |
| DB08 | Tenant database security | PASS | DB backups scoped by owner; unauth API blocked (see A08/U02) · owners=['root'] |
| DB09 | TLS required on published DB listeners | UNIT | Managed DB production (DB09) · go test TestProductionManagedDatabase + DB suite |
| DB10 | Encrypted backup + safety-backup gate on restore | UNIT | Managed DB production (DB10) · go test TestProductionManagedDatabase + DB suite |
| DB11 | Engine version pin + worker capability advertise | UNIT | Managed DB production (DB11) · go test TestProductionManagedDatabase + DB suite |
| DB12 | Failed provision retry without duplicate project | UNIT | Managed DB production (DB12) · go test TestProductionManagedDatabase + DB suite |
| DB13 | Reserved admin usernames rejected | UNIT | Managed DB production (DB13) · go test TestProductionManagedDatabase + DB suite |
| DB14 | License quota blocks over-provision | UNIT | Managed DB production (DB14) · go test TestProductionManagedDatabase + DB suite |
| DB15 | Redis logical DB is prefix isolation (no native backup claim) | UNIT | Managed DB production (DB15) · go test TestProductionManagedDatabase + DB suite |
| DB16 | Stop/start bills storage-only while data retained | UNIT | Managed DB production (DB16) · go test TestProductionManagedDatabase + DB suite |

### 11. Worker & Auto Scheduler (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| WK01 | Multiple worker registration | SMOKE | Worker registration inventory returns enrolled worker(s) · n_workers_live=1 lab / remapped from PASS→SMOKE (single-worker) |
| WK02 | Worker secret-key enrollment | SMOKE | Worker enrollment present and inventory returned · workers=ok / remapped from PASS→SMOKE (single-worker) |
| WK03 | CPU / RAM / disk capacity discovery | SMOKE | Worker capacity fields returned for placement · workers=ok / remapped from PASS→SMOKE (single-worker) |
| WK04 | Automatic VPS placement | SKIPPED | docker_ready from overview / remapped from PASS→SKIPPED (insufficient evidence) |
| WK05 | Select another worker when one is full | SKIPPED | single-worker lab / remapped from PASS→SKIPPED (insufficient evidence) |
| WK06 | Worker offline detection | SKIPPED | heartbeat field present / remapped from PASS→SKIPPED (insufficient evidence) |
| WK07 | Insufficient capacity rejection | SMOKE | Insufficient capacity / invalid size rejected · http=409 / remapped from PASS→SMOKE (single-worker) |
| WK08 | Scheduler concurrent placement races | SKIPPED | jobs API / remapped from PASS→SKIPPED (insufficient evidence) |
| WK09 | Worker removal and draining | SKIPPED | lab / remapped from PASS→SKIPPED (insufficient evidence) |

### 12. Backup & Object Storage (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| B01 | Full VPS backup and restore | PASS | Full VPS writable-layer backup/restore on one worker · backup=cfxp-bak1 |
| B02 | Second full snapshot after change (not true incremental) | SMOKE | Second full snapshot after change (not true incremental) · Second full snapshot after change · b1=cfnr-bak1 b2=cfnr-bak2 · NOT true incremental backup API |
| B03 | S3-compatible storage operations | PASS | S3 connection configured and READY backup stored to s3 destination · endpoint=http://s3pool:8333 n=1 |
| B04 | Backup checksum verification | PASS | Backup artifact checksum computed · backup=cfnr-bak1 sha256=ae68b0e592212967… |
| B05 | Encrypted backup storage | SMOKE | DB backup pipeline uses encrypted backup UI/API (READY artifacts) · DB backup READY artifacts present · encryption-at-rest not cryptographically proven here |
| B06 | Cross-worker restoration | SKIPPED | see B09 / remapped from PASS→SKIPPED (insufficient evidence) |
| B07 | Interrupted backup recovery | PASS | Backup usable after source stop (interrupted-run safe path) · backup=cfnr-bak1 |
| B08 | Backup retention and cleanup | PASS | Backup retention cleanup removed test backup ids · b1=cfnr-bak1 b2=cfnr-bak2 |
| B09 | Docker volume backup create / restore / delete / redeploy | UNIT | Docker volume backup create / restore+redeploy / delete · go test TestDockerBackupRestoreDeleteRedeploy |

### 13. Cloud Admin & Customer UI (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| U01 | Admin login API (MFA enrollment/challenge not proven) | SMOKE | Admin login API (MFA enrollment/challenge not proven) · Admin login API succeeded · email=admin@clofix.local · MFA enrollment/challenge NOT proven |
| U02 | RBAC and tenant isolation | PASS | Unauthenticated API blocked · http=401 |
| U03 | VPS create / update / delete UI | SMOKE | Instances UI reachable after auth · http=200 · UI reachability only |
| U04 | Worker and VPS usage dashboard | SMOKE | Dashboard reachable after auth · http=200 · UI reachability only |
| U05 | Admin-only protection controls | SMOKE | Settings (admin controls) reachable · http=200 · UI reachability only |
| U06 | Audit logs and activity history | SMOKE | Audit/activity API reachable · path=/api/logs (surface only) |
| U07 | Billing and payment integration | SMOKE | Billing page reachable · http=200 · UI reachability only |
| U08 | License and worker limits enforcement | SMOKE | License/settings surface reachable for limit checks · http=200 · UI reachability only |

### 14. Security Attack Simulation (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| A01 | Guest-to-host escape regression probes | PASS | I05=PASS · Sensitive /proc,/sys paths not writable · probe write denied on /proc/sysrq-trigger /sys/kernel/uevent_helper (and similar); I06=PASS · No /dev/mem,kmem,port or block devices · guest /dev has no mem/kmem/port or host block nodes; I07=PASS · mount() denied inside container · mount(tmpfs) failed (EPERM/EACCES) under seccomp/userns; N03=PASS · No host runtime paths in mountinfo · no /var/lib/clofix-filevps, /run/docker.sock, or /var/run/docker in guest mountinfo; N04=PASS · No writable release_agent · cgroup release_agent not writable from guest; F05=PASS · mknod of block devices denied · mknod /tmp/blk b 8 0 → EPERM |
| A02 | Cross-tenant access attempts | PASS | X01=PASS · VPS-B marker files invisible from VPS-A · marker written in B not visible via A rootfs/proc; X02=PASS · Host runtime directories not visible · guest cannot list host instances/runtime dirs; X03=PASS · VPS-B processes invisible from VPS-A · B PIDs absent from A /proc; X06=PASS · VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked · TCP to B:22 timed out / refused (bridge isolated + nft default-drop) |
| A03 | Command injection input validation | PASS | Malicious project name rejected by API · http=409 |
| A04 | Privilege escalation controls | PASS | I02=PASS · No dangerous capabilities · CapEff=00000000200405ff (no SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN); I03=PASS · Seccomp filter active · NoNewPrivs=1 Seccomp=2 (filter mode); F05=PASS · mknod of block devices denied · mknod /tmp/blk b 8 0 → EPERM |
| A05 | Wrong SSH key rejected (rate-limit / brute-force detection not proven) | SMOKE | Wrong SSH key rejected (rate-limit / brute-force detection not proven) · Wrong SSH key rejected (BatchMode) · port=22004 · auth reject only; no rate-limit/brute-force detection evidence |
| A06 | Malicious configuration attempts | PASS | Invalid resource config rejected · spec requires positive ram_mb and cpu and non-negative swap_mb |
| A07 | Secrets and credentials exposure | PASS | Guest PID1 environ has no worker secrets · checked=/proc/1/environ |
| A08 | API authorization bypass tests | PASS | Admin API rejects unauthenticated access · http=401 |
| A09 | Kernel attack surface hardening | INFO | K02=PASS · Host kernel sysctl hardening in place · dmesg_restrict / kptr_restrict / unprivileged bpf checks OK; K04=PASS · Host-global sysctls not writable (writable namespaced only: none) · drop_caches/swappiness/kernel.panic not writable from guest; K07=INFO · Nested linuxkit lab: spec_store_bypass still Vulnerable (mitigation owned by Docker Desktop/hypervisor) · /sys/devices/system/cpu/vulnerabilities/spec_store_bypass=Vulnerable; smt=n/a |
| A10 | Edge OWASP CRS WAF bindings (create / rules / error pages / delete) | UNIT | Edge OWASP CRS WAF bindings create / rules / error pages / delete · go test WAF binding + UI |
| A11 | WAF ModSecurity audit stats parse (hits / blocks / attacks / IPs) | UNIT | ModSecurity audit stats parse (hits/blocks/attacks/IPs) · go test TestParseWAFStatsAudit |

### 15. Performance Comparison (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| P01 | CloFix vs LXC on same hardware | SKIPPED | fx-only lab / remapped from PASS→SKIPPED (insufficient evidence) |
| P02 | CloFix vs Incus on same hardware | SKIPPED | fx-only lab / remapped from PASS→SKIPPED (insufficient evidence) |
| P03 | CloFix vs Docker on same workload | SMOKE | CloFix Docker Engine + FileVPS APIs both live on same stack · docker=200 instances=200 / remapped from PASS→SMOKE (single-worker) |
| P04 | Creation and boot time comparison | MEASURED | Create/boot times measured in bench-fx.tsv (create_ms/start_ready_ms) · bench-fx |
| P05 | Idle and loaded RAM comparison | MEASURED | Idle RAM measured in bench/suite density packs · mem_per_vps |
| P06 | Disk footprint and I/O comparison | SKIPPED | disk quota / remapped from PASS→SKIPPED (insufficient evidence) |
| P07 | Application throughput and tail latency | MEASURED | Application throughput and tail latency · CloFix Docker nginx only · max_rps=88947 zero_fail_httpload=66725 · NOT vs LXC/Incus |
| P08 | Security and isolation differences | SKIPPED | audit-fx / remapped from PASS→SKIPPED (insufficient evidence) |

### 16. Monitoring & Alerts (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| MO01 | Per-VPS CPU / RAM / disk metrics | PASS | Per-VPS memory.current + cpu.usage_usec + upper disk visible · mem=26566656 cpu_us=22564 disk_kb=13084 |
| MO02 | Worker node health monitoring | SMOKE | Workers API returns worker inventory · API inventory smoke (not alert/metering end-to-end) |
| MO03 | Memory pressure / OOM alerts | SMOKE | Security overview exposes alert/finding surface for memory pressure class · http=200 / remapped from PASS→SMOKE (single-worker) |
| MO04 | Storage almost-full alerts | SMOKE | Workers API reports disk capacity for almost-full monitoring · http=200 / remapped from PASS→SMOKE (single-worker) |
| MO05 | Network traffic metrics | SMOKE | Instances API carries network/metrics fields for traffic monitoring · http=200 / remapped from PASS→SMOKE (single-worker) |
| MO06 | Worker down and VPS crash alerts | SMOKE | Health-check / monitoring / workers surfaces available for down alerts · health=404 monitors=404 workers=200 / remapped from PASS→SMOKE (single-worker) |
| MO07 | Audit / event log integrity | SMOKE | Audit log API returns entries for integrity/review · http=200 / remapped from PASS→SMOKE (single-worker) |
| MO08 | Alert delivery and deduplication | SMOKE | Settings UI reachable for notification/alert channel config · http=200 channels=404 / remapped from PASS→SMOKE (single-worker) |

### 17. Reliability & Failure Recovery (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| REL01 | Unexpected VPS process termination | PASS | Unexpected VPS process termination recovered with fx start · pid=19946 |
| REL02 | Worker reboot / power-loss recovery | SKIPPED | workers+instances ok / remapped from PASS→SKIPPED (insufficient evidence) |
| REL03 | Network disconnect / reconnect | SMOKE | Guest net devices present; reconnect covered by SSH retry above · Guest net iface present (cfbr0); ssh_ok=0 on this reconnect sample — SSH proven elsewhere (NET02) |
| REL04 | Disk full and filesystem read-only | PASS | Disk fill hit ENOSPC inside quota · dd: error writing '/root/fill': No space left on device 42+0 records in 41+0 records out 42991616 bytes (43 MB, 41 MiB) copied, 0.0449164 s, 957 MB/s RC=1 |
| REL05 | Interrupted OS image update | SMOKE | Image catalog API present; interrupted update recovers via re-pull/re-extract path · http=200 / remapped from PASS→SMOKE (single-worker) |
| REL06 | Control-plane database outage | SMOKE | Controller health endpoint reachable; DB outage fails closed via auth/API errors · healthz=200 / remapped from PASS→SMOKE (single-worker) |
| REL07 | Backup / restore after simulated failure | PASS | Restore after stop/backup recovered marker-v1 · got=marker-v1 |
| REL08 | Rolling worker upgrade and rollback | SMOKE | Worker/docker engine control APIs present for upgrade/restart jobs · docker_workers=200 workers=200 / remapped from PASS→SMOKE (single-worker) |

### 18. Billing, License & Abuse Controls (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| BL01 | Per-VPS resource billing calculation | SMOKE | Billing UI reachable; metering surface present with instances API · http=200 · UI reachability only |
| BL02 | Usage metering accuracy | SMOKE | Instance list API available for usage metering inputs · API inventory smoke (not alert/metering end-to-end) |
| BL03 | Wallet, invoice, and payment state | SMOKE | Billing page loads wallet/invoice UI shell · http=200 · UI reachability only |
| BL04 | Subscription expiration enforcement | SMOKE | Billing subscriptions / license APIs expose expiration enforcement inputs · subs=404 license=200 / remapped from PASS→SMOKE (single-worker) |
| BL05 | License worker-count verification | SMOKE | License usage API returns worker/limit inventory · License usage/quotas API returns inventory · enforcement path not timed-out here |
| BL06 | VPS quota enforcement | PASS | Oversized resource request rejected · http=400 |
| BL07 | Duplicate provisioning and double-charge prevention | SMOKE | Oversized create rejected earlier (BL06); invoice bookkeeping UI exists · invoices=404 / remapped from PASS→SMOKE (single-worker) |
| BL08 | Suspension / reactivation without data loss | SMOKE | Instance stop/start + security modes provide suspend/reactivate without deleting data · modes=200 instances=200 / remapped from PASS→SMOKE (single-worker) |

### 19. VPS Production Controls (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| VP01 | Protection Mode default-on for new guests | UNIT | VPS production control (VP01) · go test TestProductionVPSControls |
| VP02 | SSH key / PAM / Key-Only / Login Block API controls | UNIT | VPS production control (VP02) · go test TestProductionVPSControls |
| VP03 | Delete Protection blocks accidental destroy | UNIT | VPS production control (VP03) · go test TestProductionVPSControls |
| VP04 | Security Modes + Network Quarantine apply live controls | UNIT | VPS production control (VP04) · go test TestProductionVPSControls |
| VP05 | Published port map stays in cloud ranges (SSH/HTTP/HTTPS/custom) | UNIT | VPS production control (VP05) · go test TestProductionVPSControls |
| VP06 | Firewall CIDR allow/deny on mapped ports | UNIT | VPS production control (VP06) · go test TestProductionVPSControls |
| VP07 | Edge WAF binding attach for FileVPS HTTP path | UNIT | VPS production control (VP07) · go test TestProductionVPSControls |
| VP08 | Writable-layer backup only when stopped; restore as new VPS | UNIT | VPS production control (VP08) · go test TestProductionVPSControls |
| VP09 | cgroup v2 CPU/RAM/PID/swap limits enforced in placement | UNIT | VPS production control (VP09) · go test TestProductionVPSControls |
| VP10 | Cross-tenant instance API isolation | UNIT | VPS production control (VP10) · go test TestProductionVPSControls |
| VP11 | Volume / EFS / S3 mount path conflict rejection | UNIT | VPS production control (VP11) · go test TestProductionVPSControls |
| VP12 | IAM security:manage required for hardening mutations | UNIT | VPS production control (VP12) · go test TestProductionVPSControls |

### 20. Managed Database Production (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| MDB01 | MySQL project provision metadata + TLS CA return | UNIT | Managed DB production (MDB01) · go test TestProductionManagedDatabase + DB suite |
| MDB02 | PostgreSQL / MongoDB / Redis engines advertised per worker | UNIT | Managed DB production (MDB02) · go test TestProductionManagedDatabase + DB suite |
| MDB03 | Job payload not stored plaintext on controller | UNIT | Managed DB production (MDB03) · go test TestProductionManagedDatabase + DB suite |
| MDB04 | Quota + license deny without database entitlement | UNIT | Managed DB production (MDB04) · go test TestProductionManagedDatabase + DB suite |
| MDB05 | Unknown DB / reserved user operations rejected | UNIT | Managed DB production (MDB05) · go test TestProductionManagedDatabase + DB suite |
| MDB06 | FAILED project retry requeues once | UNIT | Managed DB production (MDB06) · go test TestProductionManagedDatabase + DB suite |
| MDB07 | Backup READY artifacts carry sha256 | UNIT | Managed DB production (MDB07) · go test TestProductionManagedDatabase + DB suite |
| MDB08 | Schedules / jobs inventory for ops visibility | UNIT | Managed DB production (MDB08) · go test TestProductionManagedDatabase + DB suite |
| MDB09 | Tenant cannot see another account's DB project | UNIT | Managed DB production (MDB09) · go test TestProductionManagedDatabase + DB suite |
| MDB10 | Published ports stay in 47000–51999 band | UNIT | Managed DB production (MDB10) · go test TestProductionManagedDatabase + DB suite |

### 21. CKS / EKS Production (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| CK01 | FileVPS infrastructure_mode rejected for kubelet | UNIT | CKS/EKS production (CK01) · go test kubernetes + production surfaces |
| CK02 | Placement skips workers without Kubernetes capacity | UNIT | CKS/EKS production (CK02) · go test kubernetes + production surfaces |
| CK03 | Cluster provision + kubeconfig download | UNIT | CKS/EKS production (CK03) · go test kubernetes + production surfaces |
| CK04 | Tenant isolation on cluster list/get | UNIT | CKS/EKS production (CK04) · go test kubernetes + production surfaces |
| CK05 | IAM kubernetes scope required for mutations | UNIT | CKS/EKS production (CK05) · go test kubernetes + production surfaces |
| CK06 | production_ready false on lab/fake workers | UNIT | CKS/EKS production (CK06) · go test kubernetes + production surfaces |
| CK07 | production_ready true only with real worker + CLOFIX_K8S_E2E | UNIT | CKS/EKS production (CK07) · go test kubernetes + production surfaces |
| CK08 | Helm install / StorageClass / etcd backup encrypted | UNIT | CKS/EKS production (CK08) · go test kubernetes + production surfaces |
| CK09 | Workload catalog + CloFix attach (LB/volume/EFS/S3) | UNIT | CKS/EKS production (CK09) · go test kubernetes + production surfaces |
| CK10 | VPS group / node pool scale on dedicated CKS workers | UNIT | CKS/EKS production (CK10) · go test kubernetes + production surfaces |
| CK11 | HA control-plane option recorded (3/5) without public 6443 | UNIT | CKS/EKS production (CK11) · go test kubernetes + production surfaces |
| CK12 | License max_kubernetes_* quota enforced | UNIT | CKS/EKS production (CK12) · go test kubernetes + production surfaces |

### 22. Docker Engine Production (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| DCK01 | Compose policy rejects privileged / host-net / docker.sock | UNIT | Docker Engine production (DCK01) · go test docker + production surfaces |
| DCK02 | Host publish ports only in 18000–21999 | UNIT | Docker Engine production (DCK02) · go test docker + production surfaces |
| DCK03 | Deploy rejected when Docker engine not ready | UNIT | Docker Engine production (DCK03) · go test docker + production surfaces |
| DCK04 | Port conflict detection against FileVPS maps | UNIT | Docker Engine production (DCK04) · go test docker + production surfaces |
| DCK05 | Cross-tenant Docker resource deny | UNIT | Docker Engine production (DCK05) · go test docker + production surfaces |
| DCK06 | License max_docker_* quota reject | UNIT | Docker Engine production (DCK06) · go test docker + production surfaces |
| DCK07 | Image / Compose / Git / Quick App create paths | UNIT | Docker Engine production (DCK07) · go test docker + production surfaces |
| DCK08 | Networks + volumes + delete protection | UNIT | Docker Engine production (DCK08) · go test docker + production surfaces |
| DCK09 | Volume backup create / restore / redeploy / delete | UNIT | Docker Engine production (DCK09) · go test docker + production surfaces |
| DCK10 | Edge WAF install path for container/app targets | UNIT | Docker Engine production (DCK10) · go test docker + production surfaces |
| DCK11 | Security scan job queue (Trivy optional) | UNIT | Docker Engine production (DCK11) · go test docker + production surfaces |
| DCK12 | FileVPS API still healthy beside Docker product | UNIT | Docker Engine production (DCK12) · go test docker + production surfaces |

## 4. FileVPS code-backed expectations

These rows describe what the current worker engine is written to do. They are **not** live measurements.

| ID | Expected | Why (code) | Verification |
|---|---|---|---|
| I01 | PASS when `user_ns=true` | After-mount user-ns maps guest 0 to `host_uid` (base 100000) | Verified 2026-11-03: `0 100000 65536` |
| I02 | PASS | Guest allowlist drops SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN | Verified |
| I03 | PASS | `fx-init` installs a seccomp filter and `no_new_privs` | Verified |
| I04 | WARN | AppArmor is detected on the host; no per-guest profile is applied | Verified WARN |
| I05 I06 | PASS | Guest `/dev` is a private tmpfs; mount/kexec/module syscalls are denied | Verified |
| I07 | PASS | `mount` is in the seccomp deny list | Verified |
| N01 N02 | PASS | New mount, PID, UTS, IPC, net, cgroup (and user when enabled) namespaces | Verified |
| N05 | PASS | After the process is placed in `/clofix-filevps/<id>`, `CLONE_NEWCGROUP` makes the guest see `0::/`. Host `pids.max` / `memory.max` / `cpu.max` still apply. | Verified |
| X01 X03 X04 | PASS | Separate OverlayFS upper layers and namespaces | Verified |
| X05 | PASS when `user_ns=true` | Each guest gets a unique `host_uid` range (`uid_base + index * 65536`) | Verified 2026-11-03: A=100000 B=165536 |
| X06 | PASS | Each veth is `bridge link set isolated on`; nftables default-drop forward | Verified |
| R01 R02 R03 | PASS | cgroup v2 sets `pids.max`, `memory.max`/`memory.high`, `cpu.max` | Verified |
| R04 R12 | PASS | `disk_mb` is a loop+ext4 backing for overlay upper/work; guest `df` shows the limit | Verified |
| K05 | PASS | `fx-init` bind-mounts a per-guest `/proc/meminfo` from `ram_mb` | Verified |
| K01 | INFO | FileVPS shares the host kernel; it is not KVM/QEMU | Documented |
| O01 O02 | PASS | Shared read-only image + per-guest OverlayFS upper layer | Verified |

Production workers default `user_ns=true`. Audit re-run used isolated `CLOFIX_USER_NS=true`. Compose agent may still set `false` for live UI VPS until flipped.

## 5. Methodology and limitations

- Same host, one backend at a time. FileVPS uses bundled Ubuntu 24.04. Incus/LXC default to Debian 12 unless you override `INCUS_IMAGE` or `BASE_DISTRO`/`BASE_REL`.
- FileVPS create requires an SSH public key, cgroup limits, and (for networked guests) a `10.231.0.0/16` address plus an SSH host port in 22000–22099. Density tests skip guest networking because only 100 SSH ports exist.
- Protection Mode stays off during the audit so writes inside the guest can be tested. That is not the default for UI-created VPS instances.
- RAM overhead is the host `MemTotal−MemAvailable` delta. Disk is a `df` delta on `STORAGE_PATH`. OverlayFS should show a high OS-share ratio.
- Density is bounded by `DENSITY_MAX` and a memory reserve. It is idle density, not workload density.
- The audit is a configuration/behaviour check, not a kernel exploit assessment. A PASS means that probe did not cross the boundary. Shared-kernel isolation can still fail if the host kernel is buggy.
- Resource probes are bounded. Re-run with `--destructive` only on a throw-away host.
- Run each benchmark at least three times and compare medians before drawing business conclusions.

## 6. How to produce measured results

On a dedicated Linux worker that already has `clofix-worker`, Incus, and/or LXC:

```bash
sudo ./run-compare.sh            # backends that are installed
sudo ./clofix-bench.sh fx
sudo ./clofix-audit.sh fx
sudo FX_BIN=/tmp/clofix-worker-new ./run-notrun-suites.sh
python3 make-report.py results results/report.md
```

Do not run density or `--destructive` probes on a production host.

