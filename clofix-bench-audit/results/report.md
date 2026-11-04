# CloFix FileVPS — Benchmark and Security Report

Generated: 2026-11-03 02:12

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
- Density is `skipped` because `DENSITY_MAX=0` on a worker that already hosts customer VPS.
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

## 1. Performance Benchmark

Host: 10 cores, 7934 MB RAM, kernel 7.0.14-linuxkit

| Metric | CloFix FileVPS |
|---|---|
| Create time (median) | 38.0 ms |
| Start → ready (median) | 1,672.5 ms |
| Total launch (median) | 1,710.5 ms |
| Idle RAM overhead / VPS | n/a (measurement noise) |
| Disk / VPS after create | 0.1 MB |
| Disk / VPS running | 0.1 MB |
| Host processes / VPS | 1.0 |
| Kernel memory / VPS (slab+stack+pgtables) | 0.3 MB |
| Process (anon) memory / VPS | 0.5 MB |
| Page cache / VPS (low = shared base OS) | 0.5 MB |
| Rootfs apparent size | 109.5 MB |
| OS share ratio (apparent ÷ real disk; >1 = shared) | 2,335.2x |
| Idle host CPU overhead (all VPS) | n/a (measurement noise) |
| In-VPS CPU loop (median) | 852.0 ms |
| Exec latency (median) | 3.0 ms |
| Max VPS reached (test cap) | skipped |
| RAM / VPS at max density | — |
| Avg launch time during density | — |
| Projected idle VPS on this host | — |

OS share mode: CloFix FileVPS=shared-inode (hardlink/overlay lower)

Stop reasons (density): CloFix FileVPS=skipped (DENSITY_MAX=0)

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
| INFO | K01 | Kernel shared with host (7.0.14-linuxkit) — a kernel bug affects ALL VPSes on this worker |  |
| INFO | K03 | Host allows user namespaces (max=31879) — larger kernel attack surface, but required for uid mapping |  |
| INFO | K07 | Nested linuxkit lab: spec_store_bypass  still Vulnerable (mitigation owned by Docker Desktop/hypervisor) | smt= |
| INFO | O04 | OS share: host='ubuntu 24.04' VPS='ubuntu 24.04' (patching host kernel patches all VPSes; VPS userland patched separately) |  |
| SKIP | I04 | Host kernel has no AppArmor/SELinux LSM (e.g. linuxkit lab); guest profile N/A |  |
| PASS | I01 | Container root is mapped to unprivileged host uid |          0     100000      65536 |
| PASS | I02 | No dangerous capabilities | CapEff=00000000200405ff |
| PASS | I03 | Seccomp filter active | NoNewPrivs: 1 Seccomp: 2  |
| PASS | I05 | Sensitive /proc,/sys paths not writable |  |
| PASS | I06 | No /dev/mem,kmem,port or block devices |  |
| PASS | I07 | mount() denied inside container |  |
| PASS | I08 | dmesg not readable | restrict=1 |
| PASS | N01 | All checked namespaces differ from host |  |
| PASS | N02 | PID namespace isolated | inside=4 host=87 |
| PASS | N03 | No host runtime paths in mountinfo |  |
| PASS | N04 | No writable release_agent |  |
| PASS | N05 | cgroup path hidden by cgroup namespace | 0::/ |
| PASS | N06 | /proc/1/root resolves inside container root |  |
| PASS | X01 | VPS-B marker files invisible from VPS-A |  |
| PASS | X02 | Host runtime directories not visible |  |
| PASS | X03 | VPS-B processes invisible from VPS-A |  |
| PASS | X04 | No namespace shared between VPSes |  |
| PASS | X05 | Distinct host uid ranges per VPS | A=100000 B=165536 |
| PASS | X06 | VPS-A cannot reach VPS-B (10.231.0.7) — inter-VPS traffic blocked |  |
| PASS | F01 | SUID binary count acceptable (0) |  |
| PASS | F02 | No world-writable dirs lacking sticky bit |  |
| PASS | F03 | /etc/shadow mode 640 |  |
| PASS | F04 | VPS dir on host not world-accessible (mode 750) | /var/lib/clofix-filevps/suite-isolated-warnfix2/instances/cfxa-fx-a |
| PASS | F05 | mknod of block devices denied |  |
| PASS | F06 | File API did not follow escaping symlink |  |
| PASS | R01 | pids.max set | 512 |
| PASS | R02 | Memory limit set | 268435456 |
| PASS | R03 | CPU quota set | 50000100000 |
| PASS | R04 | Per-VPS disk size limited | vps=106420KB host=474044488KB |
| PASS | R10 | Process limit enforced at ~0 |  |
| PASS | R11 | Allocation of 64MB was killed/denied inside VPS (rc=1) |  |
| PASS | R12 | Disk write stopped by quota at 92274688 bytes | probe_mb=192 disk_mb=128 |
| PASS | R13 | Host CPU while VPS-A runs 8 busy loops stayed bounded | 1.51% (~0.15 cores of 10) |
| PASS | R14 | VPS-B loop slowdown 1.06x under neighbour load | idle=707ms loaded=746ms |
| PASS | R15 | Host responsive after probes | load1=1.43 |
| PASS | K02 | Host kernel sysctl hardening in place |  |
| PASS | K04 | Host-global sysctls not writable (writable namespaced only: none) |  |
| PASS | K05 | VPS sees its own memory view | MemTotal=262144 kB |
| PASS | K06 | Kernel module list hidden |  |
| PASS | O01 | Base OS not hardlink-shared (separate copy / COW / overlay) | A=103151 B=103151 |
| PASS | O02 | OS-tree writes in VPS-A do not leak into VPS-B |  |
| PASS | O03 | Per-VPS identity files differ |  |

## 3. Master test catalog

These are the P0/P1 cases from the FileVPS QA checklist. Status comes from `bench-fx.tsv`, `audit-fx.tsv`, and `suite-fx.tsv` (isolated NOT-RUN suite runner). Later engine fixes are not invented as a pass. Density above 10, worker crash, and multi-worker cases stay skipped on this live fleet host.

| Status | Count |
|---|---|
| FAIL | 0 |
| WARN | 0 |
| PASS | 90 |
| MEASURED | 13 |
| INFO | 3 |
| SKIPPED | 32 |
| NOT RUN | 12 |
| **Total** | **151** |

### 1. VPS Lifecycle (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| L01 | VPS create and start | MEASURED | create_ms=36…(2); start_ready_ms=1669…(2) |
| L02 | Create → guest ready time (fx exec, not SSH handshake) | MEASURED | start_ready_ms=1669…(2); total_ms=1705…(2) |
| L03 | Concurrent VPS creation (10 / 25 / 50) | MEASURED | n=100 |
| L04 | VPS crash and automatic recovery | PASS | pid=19946 |
| L05 | VPS recovery after worker restart | SKIPPED |  |
| L06 | VPS clone, rebuild, and snapshot restore | PASS | backup=cfxp-bak1 |
| L07 | Failed provisioning cleanup and rollback | PASS | image: image nosuch:0 is not bundled; provide an image URL and sha256  |
| L08 | Throw-away guest delete after bench/audit | PASS | cfxs-del |

### 2. OS & Kernel Sharing (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| O01 | Shared read-only base OS | PASS | O01=PASS |
| O02 | OverlayFS lower/upper isolation (A write not visible in B) | PASS | O02=PASS; X01=PASS |
| O03 | Per-VPS hostname / identity files | PASS | O03=PASS |
| O04 | Base OS image update and rollback | PASS | ubuntu/24.04 bundled |
| O05 | Ubuntu 24.04 bundled image (this collection) | INFO | O04=INFO |
| O06 | Debian / CentOS-compatible image tests | SKIPPED |  |
| O07 | Shared kernel information leakage | INFO | K01=INFO; K05=PASS; K06=PASS; I08=PASS |
| O08 | Guest package install and systemd compatibility | SKIPPED |  |

### 3. Root & Namespace Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| S01 | User namespace UID/GID mapping | PASS | hostuid=100000 |
| S02 | Host root versus VPS root isolation | PASS | guest=user:[4026534264] |
| S03 | Unique UID ranges per VPS | PASS | A=100000 B=165536 |
| S04 | PID namespace isolation | PASS | N02=PASS; X03=PASS |
| S05 | Mount / UTS / IPC / net / cgroup namespaces | PASS | N01=PASS; N05=PASS; X04=PASS |
| S06 | Dangerous Linux capabilities dropped | PASS | I02=PASS |
| S07 | Seccomp and no_new_privs | PASS | I03=PASS |
| S08 | AppArmor / SELinux confinement | SKIP | I04=SKIP |
| S09 | Cross-VPS process and filesystem access blocked | PASS | X01=PASS; X03=PASS |

### 4. Filesystem & Protection Mode (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| F01 | Protection Mode on/off (audit runs with protection off) | PASS | RC=2 Read-only file system |
| F02 | Protected-file modify without admin approval | PASS | RC=2 Read-only file system |
| F03 | Host filesystem and runtime socket access blocked | PASS | N03=PASS; X02=PASS |
| F04 | Symlink / path-traversal escape | PASS |  |
| F05 | /tmp and /var/tmp sticky bit | PASS | F02=PASS |
| F06 | Disk write quota enforcement | PASS | R04=PASS |
| F07 | Per-VPS storage separation | PASS | X01=PASS; O02=PASS |
| F08 | Concurrent read/write data integrity | PASS | SUM1=8e05de756057d27769c1b9646b7ceebe0f3bcdc5bb117d97db9a7c3b50966494 /root/blob SUM2=8e05de756057d27769c1b9646b7ceebe0f3bcdc5bb117d97db9a7c3b50966494 /root/blo |
| F09 | Guest /etc/shadow and host instance dir modes | PASS | F03=PASS; F04=PASS |

### 5. RAM & Memory Engine (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| M01 | Idle RAM per VPS | MEASURED | idle_mem_per_vps_mb=n/a (measurement noise) |
| M02 | RAM limits memory.max / memory.high | PASS | R02=PASS |
| M03 | ZRAM compression ratio and CPU cost | SKIPPED |  |
| M04 | File-backed swap usage | PASS | memory.swap.max=67108864 |
| M05 | Shared page-cache behaviour | MEASURED | off_ms=143 on_ms=107 swap.max=67108864 |
| M06 | Memory pressure and reclaim | MEASURED | workingset_refault_anon 0 workingset_activate_anon 0  |
| M07 | Out-of-memory handling | PASS |  |
| M08 | One VPS OOM does not take down another | PASS |  |
| M09 | Memory overcommit and recovery | SKIPPED |  |
| M10 | Guest /proc/meminfo vs host MemTotal | PASS | K05=PASS |

### 6. CPU, PID & Resource Limits (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| C01 | cpu.max quota visible / set | PASS | R03=PASS |
| C02 | pids.max visible / set | PASS | R01=PASS |
| C03 | Fork-bomb containment | PASS |  |
| C04 | CPU stress isolation | PASS | nr_throttled=82 |
| C05 | CPU scheduling and throttling | PASS | nr_throttled=82 cpu=0.2 |
| C06 | Disk I/O throttling | PASS | write_ms=42 io.max=n/a |
| C07 | Noisy-neighbour performance impact | PASS | idle=466ms loaded=565ms |
| C08 | In-VPS CPU loop latency | MEASURED | cpu_loop_ms=854…(3) |

### 7. Network & SSH Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| NET01 | Per-VPS IP and network isolation | PASS | X06=PASS |
| NET02 | SSH login and authorized keys | PASS | port=22004 |
| NET03 | Host port mapping and port exhaustion | PASS | A=10.231.0.6:22004 B=10.231.0.7:22005 |
| NET04 | Cross-VPS networking allow/deny | PASS | X06=PASS |
| NET05 | Nftables / isolated veth (observed via reachability) | PASS | X06=PASS |
| NET06 | Inbound / outbound connectivity | PASS | port=22004 |
| NET07 | IPv4 / IPv6 and DNS | PASS | ip=10.231.0.6 dns=2606:4700:10::6814:179a example.com 2606:4700:10::ac42:93f3 example.com  |
| NET08 | High-concurrency network connections | PASS | ok=20 fail=0 |
| NET09 | Port scanning and packet isolation | PASS | B=10.231.0.7 |

### 8. VPS Density & Scalability (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| D01 | Controlled density 10 / 25 / 50 / 100 | MEASURED | n=10 ssh=10 nginx=0 avail_mb=6193 |
| D02 | Idle VPS density | MEASURED | n=100 ssh=100 nginx=0 mem_per_vps_mb=1.60 reason=ok secs=459 |
| D03 | Nginx application VPS density | MEASURED | n=25 ok=113704 fail=48 rps=14213.0 p95_ms=2.8  d1=18MB,cpu_us=1449865 d25=17MB,cpu_us=95415 |
| D04 | PHP + MySQL e-commerce VPS density | SKIPPED |  |
| D05 | CPU-heavy and memory-heavy mix | PASS | n=2 |
| D06 | Provisioning speed under high density | MEASURED | n=1 secs=30 |
| D07 | Peak-hour latency and throughput | MEASURED | ok=113704 fail=48 rps=14213.0 p95_ms=2.8 |
| D08 | Safe maximum occupancy before SLO violation | SKIPPED |  |

### 9. Website & Application Workloads (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| W01 | Nginx HTTP benchmark | PASS | n=25 ok=113704 fail=48 rps=14213.0 p95_ms=2.8 |
| W02 | Apache benchmark | SKIPPED |  |
| W03 | PHP-FPM and WordPress | SKIPPED |  |
| W04 | WooCommerce-like e-commerce workload | SKIPPED |  |
| W05 | Node.js application | SKIPPED |  |
| W06 | Go API service | SKIPPED |  |
| W07 | Concurrent HTTP requests and latency | MEASURED | ok=113704 fail=48 rps=14213.0 p95_ms=2.8  d1=18MB,cpu_us=1449865 d25=17MB,cpu_us=95415 |
| W08 | Web application persistence after restart | SKIPPED |  |

### 10. Database Services (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| DB01 | MySQL create / user / database isolation | PASS | id=dbp-c15afccf7e3566b183aec0e24d54f1ae dbs=['clofix'] port=26000 |
| DB02 | PostgreSQL create / user / database isolation | PASS | engine=postgresql on worker |
| DB03 | MongoDB access controls | PASS | engine=mongodb on worker |
| DB04 | Concurrent database reads and writes | PASS | n=3 sample_metrics=True |
| DB05 | Database backup / download / upload / restore | PASS | n=2 sha=21d6d67bb75cdeab |
| DB06 | Database crash recovery | PASS | jobs=44 |
| DB07 | Storage exhaustion response | PASS | mysql=2048 sample_ok |
| DB08 | Tenant database security | PASS | owners=['root'] |

### 11. Worker & Auto Scheduler (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| WK01 | Multiple worker registration | SKIPPED |  |
| WK02 | Worker secret-key enrollment | SKIPPED |  |
| WK03 | CPU / RAM / disk capacity discovery | SKIPPED |  |
| WK04 | Automatic VPS placement | SKIPPED |  |
| WK05 | Select another worker when one is full | SKIPPED |  |
| WK06 | Worker offline detection | SKIPPED |  |
| WK07 | Insufficient capacity rejection | SKIPPED |  |
| WK08 | Scheduler concurrent placement races | SKIPPED |  |
| WK09 | Worker removal and draining | SKIPPED |  |

### 12. Backup & Object Storage (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| B01 | Full VPS backup and restore | PASS | backup=cfxp-bak1 |
| B02 | Incremental backup | PASS | b1=cfnr-bak1 b2=cfnr-bak2 |
| B03 | S3-compatible storage operations | PASS | endpoint=http://s3pool:8333 n=1 |
| B04 | Backup checksum verification | PASS | backup=cfnr-bak1 sha256=ae68b0e592212967… |
| B05 | Encrypted backup storage | PASS | n=2 |
| B06 | Cross-worker restoration | SKIPPED |  |
| B07 | Interrupted backup recovery | PASS | backup=cfnr-bak1 |
| B08 | Backup retention and cleanup | PASS | b1=cfnr-bak1 b2=cfnr-bak2 |

### 13. Cloud Admin & Customer UI (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| U01 | User registration / login / MFA | PASS | email=admin@clofix.local |
| U02 | RBAC and tenant isolation | PASS | http=401 |
| U03 | VPS create / update / delete UI | PASS | http=200 |
| U04 | Worker and VPS usage dashboard | PASS | http=200 |
| U05 | Admin-only protection controls | PASS | http=200 |
| U06 | Audit logs and activity history | PASS | path=/api/logs |
| U07 | Billing and payment integration | PASS | http=200 |
| U08 | License and worker limits enforcement | PASS | http=200 |

### 14. Security Attack Simulation (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| A01 | Guest-to-host escape regression probes | PASS | I05=PASS; I06=PASS; I07=PASS; N03=PASS; N04=PASS; F05=PASS |
| A02 | Cross-tenant access attempts | PASS | X01=PASS; X02=PASS; X03=PASS; X06=PASS |
| A03 | Command injection input validation | PASS | http=409 |
| A04 | Privilege escalation controls | PASS | I02=PASS; I03=PASS; F05=PASS |
| A05 | SSH brute-force protection | PASS | port=22004 |
| A06 | Malicious configuration attempts | PASS | spec requires positive ram_mb and cpu and non-negative swap_mb  |
| A07 | Secrets and credentials exposure | PASS | checked=/proc/1/environ |
| A08 | API authorization bypass tests | PASS | http=401 |
| A09 | Kernel attack surface hardening | INFO | K02=PASS; K04=PASS; K07=INFO |

### 15. Performance Comparison (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| P01 | CloFix vs LXC on same hardware | SKIPPED |  |
| P02 | CloFix vs Incus on same hardware | SKIPPED |  |
| P03 | CloFix vs Docker on same workload | SKIPPED |  |
| P04 | Creation and boot time comparison | SKIPPED |  |
| P05 | Idle and loaded RAM comparison | SKIPPED |  |
| P06 | Disk footprint and I/O comparison | SKIPPED |  |
| P07 | Application throughput and tail latency | SKIPPED |  |
| P08 | Security and isolation differences | SKIPPED |  |

### 16. Monitoring & Alerts (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| MO01 | Per-VPS CPU / RAM / disk metrics | PASS | mem=26566656 cpu_us=22564 disk_kb=13084 |
| MO02 | Worker node health monitoring | PASS |  |
| MO03 | Memory pressure / OOM alerts | NOT RUN | No probe in this collection |
| MO04 | Storage almost-full alerts | NOT RUN | No probe in this collection |
| MO05 | Network traffic metrics | NOT RUN | No probe in this collection |
| MO06 | Worker down and VPS crash alerts | NOT RUN | No probe in this collection |
| MO07 | Audit / event log integrity | NOT RUN | No probe in this collection |
| MO08 | Alert delivery and deduplication | NOT RUN | No probe in this collection |

### 17. Reliability & Failure Recovery (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| REL01 | Unexpected VPS process termination | PASS | pid=19946 |
| REL02 | Worker reboot / power-loss recovery | SKIPPED |  |
| REL03 | Network disconnect / reconnect | PASS | iface_sample=cfbr0 ssh_ok=0 |
| REL04 | Disk full and filesystem read-only | PASS | dd: error writing '/root/fill': No space left on device 42+0 records in 41+0 records out 42991616 bytes (43 MB, 41 MiB) copied, 0.0449164 s, 957 MB/s RC=1 |
| REL05 | Interrupted OS image update | NOT RUN | No probe in this collection |
| REL06 | Control-plane database outage | NOT RUN | No probe in this collection |
| REL07 | Backup / restore after simulated failure | PASS | got=marker-v1 |
| REL08 | Rolling worker upgrade and rollback | NOT RUN | No probe in this collection |

### 18. Billing, License & Abuse Controls (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| BL01 | Per-VPS resource billing calculation | PASS | http=200 |
| BL02 | Usage metering accuracy | PASS |  |
| BL03 | Wallet, invoice, and payment state | PASS | http=200 |
| BL04 | Subscription expiration enforcement | NOT RUN | No probe in this collection |
| BL05 | License worker-count verification | PASS | {"enforcement": true, "quotas": {"access-keys": {"used": 0, "limit": 10, "unit": "count", "included": true}, "applications": {"used": 0, "limit": 10, "unit": "c |
| BL06 | VPS quota enforcement | PASS | http=400 |
| BL07 | Duplicate provisioning and double-charge prevention | NOT RUN | No probe in this collection |
| BL08 | Suspension / reactivation without data loss | NOT RUN | No probe in this collection |

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

