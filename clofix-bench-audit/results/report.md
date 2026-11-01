# CloFix FileVPS — Benchmark and Security Report

Generated: 2026-11-01 16:52

**Status:** Measured results from this host.

The bench and audit scripts now call `clofix-worker fx` (not the old placeholder `clofix-fx` CLI) and use `/var/lib/clofix-filevps/instances/<id>/rootfs-mount`. FileVPS create uses Ubuntu 24.04, a throw-away SSH key, cgroup limits, and Protection Mode off. Density skips guest networking because only SSH host ports 22000–22099 exist.

## 0. Run scope (this collection)

This is a **reduced, non-destructive FileVPS-only** run on the local Docker Desktop Compose worker. It is **not** a dedicated bare-metal compare host and **not** a kernel exploit assessment.

| Item | Value |
| --- | --- |
| Collected | 2026-11-01 08:38–08:40 UTC (re-audit after isolation fixes) |
| Host | `clofix-local-worker` (Docker Desktop, kernel `7.0.14-linuxkit`, aarch64) |
| Cores / RAM | 10 / 7934 MB |
| Worker binary under test | `/tmp/clofix-worker-new` (engine fixes; on-disk agent stayed `0.1.0`) |
| Image | Bundled Ubuntu 24.04 FileVPS rootfs |
| Bench sizes | `N_START=2`, `N_IDLE=2`, `DENSITY_MAX=0`, `SETTLE_S=8` |
| Destructive probes | Not run (`--destructive` off) |
| Incus / LXC | Not installed in this worker — no compare numbers |
| Live fleet left running | `vps-01c09f91ffda`, `vps-1b7ce1e40adf`, `vps-3ab923078227` |
| User namespaces | `CLOFIX_USER_NS=false` (Compose development override). I01 / N01(user) / X05 FAIL are expected here. Production defaults `user_ns=true`. |

### How to read these numbers

- Negative idle RAM and CPU deltas are **host `MemAvailable` / `/proc/stat` jitter**, recorded as `n/a (measurement noise)`. `projected_idle_density` is omitted when the RAM delta is not positive.
- Density is `skipped` because `DENSITY_MAX=0` on a worker that already hosts customer VPS.
- FileVPS guests share the host kernel. They are **not** KVM/QEMU VMs.
- Do not claim Ubuntu/CentOS/Debian/Incus/LXC support from this run. Only FileVPS on linuxkit was measured.
- I04 WARN: no per-guest AppArmor/SELinux profile is applied.
- K02 / K07 WARN: linuxkit host sysctls and CPU mitigations. Not changed on this development VM.
- Remaining live-audit FAILs I01 / N01(user) / X05 need `CLOFIX_USER_NS=true` on a throw-away **Linux** worker. Enabling that on this Compose stack would remount live customer VPS and was not done.

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
| Root isolation | 1 / 1 / 6 |
| Namespace escape | 1 / 0 / 5 |
| Cross-VPS access | 1 / 0 / 5 |
| Filesystem security | 0 / 0 / 5 |
| Resource exhaustion | 0 / 0 / 4 |
| Kernel & OS sharing | 0 / 2 / 6 |
| **Total** | **3 / 3 / 31** |

### Findings — CloFix FileVPS

| Status | ID | Check | Detail |
|---|---|---|---|
| FAIL | I01 | Container root == host root (identity uid_map, no user namespace) |          0          0 4294967295 |
| FAIL | N01 | Namespaces shared with host: user |  |
| FAIL | X05 | Identity uid mapping — root in A equals root in B on host |  |
| WARN | I04 | No AppArmor/SELinux confinement |  |
| WARN | K02 | Host kernel hardening gaps (shared-kernel attack surface): kptr_restrict=0(want 2) unprivileged_bpf_disabled=0(want 2) io_uring_disabled=0(want 2) |  |
| WARN | K07 | CPU side-channel mitigations missing for: spec_store_bypass  (shared kernel/CPU) | smt= |
| INFO | K01 | Kernel shared with host (7.0.14-linuxkit) — a kernel bug affects ALL VPSes on this worker |  |
| INFO | K03 | Host allows user namespaces (max=31879) — larger kernel attack surface, but required for uid mapping |  |
| INFO | O04 | OS share: host='ubuntu 24.04' VPS='ubuntu 24.04' (patching host kernel patches all VPSes; VPS userland patched separately) |  |
| SKIP | F06 | Symlink-escape test needs FX_FILE_READ template (control-plane file read) |  |
| SKIP | R10 | Active exhaustion probes skipped (use --destructive) |  |
| PASS | I02 | No dangerous capabilities | CapEff=00000000200405ff |
| PASS | I03 | Seccomp filter active | NoNewPrivs: 1 Seccomp: 2  |
| PASS | I05 | Sensitive /proc,/sys paths not writable |  |
| PASS | I06 | No /dev/mem,kmem,port or block devices |  |
| PASS | I07 | mount() denied inside container |  |
| PASS | I08 | dmesg not readable | restrict=1 |
| PASS | N02 | PID namespace isolated | inside=4 host=56 |
| PASS | N03 | No host runtime paths in mountinfo |  |
| PASS | N04 | No writable release_agent |  |
| PASS | N05 | cgroup path hidden by cgroup namespace | 0::/ |
| PASS | N06 | /proc/1/root resolves inside container root |  |
| PASS | X01 | VPS-B marker files invisible from VPS-A |  |
| PASS | X02 | Host runtime directories not visible |  |
| PASS | X03 | VPS-B processes invisible from VPS-A |  |
| PASS | X04 | No namespace shared between VPSes |  |
| PASS | X06 | VPS-A cannot reach VPS-B (10.231.0.6) — inter-VPS traffic blocked |  |
| PASS | F01 | SUID binary count acceptable (0) |  |
| PASS | F02 | No world-writable dirs lacking sticky bit |  |
| PASS | F03 | /etc/shadow mode 640 |  |
| PASS | F04 | VPS dir on host not world-accessible (mode 750) | /var/lib/clofix-filevps/instances/cfxa-fx-a |
| PASS | F05 | mknod of block devices denied |  |
| PASS | R01 | pids.max set | 256 |
| PASS | R02 | Memory limit set | 268435456 |
| PASS | R03 | CPU quota set | 50000100000 |
| PASS | R04 | Per-VPS disk size limited | vps=1992552KB host=474044488KB |
| PASS | K04 | Host-global sysctls not writable (writable namespaced only: none) |  |
| PASS | K05 | VPS sees its own memory view | MemTotal=262144 kB |
| PASS | K06 | Kernel module list hidden |  |
| PASS | O01 | Base OS not hardlink-shared (separate copy / COW / overlay) | A=5120986 B=5120986 |
| PASS | O02 | OS-tree writes in VPS-A do not leak into VPS-B |  |
| PASS | O03 | Per-VPS identity files differ |  |

## 3. Master test catalog

These are the P0/P1 cases from the FileVPS QA checklist. Status comes from `bench-fx.tsv`, `audit-fx.tsv`, and `suite-fx.tsv` (isolated NOT-RUN suite runner). Later engine fixes are not invented as a pass. Density above 10, worker crash, and multi-worker cases stay skipped on this live fleet host.

| Status | Count |
|---|---|
| FAIL | 1 |
| WARN | 2 |
| PASS | 34 |
| MEASURED | 13 |
| INFO | 2 |
| SKIPPED | 38 |
| NOT RUN | 61 |
| **Total** | **151** |

### 1. VPS Lifecycle (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| L01 | VPS create and start | MEASURED | create_ms=36…(2); start_ready_ms=1669…(2) |
| L02 | Create → guest ready time (fx exec, not SSH handshake) | MEASURED | start_ready_ms=1669…(2); total_ms=1705…(2) |
| L03 | Concurrent VPS creation (10 / 25 / 50) | MEASURED | n=100 |
| L04 | VPS crash and automatic recovery | SKIPPED |  |
| L05 | VPS recovery after worker restart | SKIPPED |  |
| L06 | VPS clone, rebuild, and snapshot restore | PASS | backup=cfxp-bak1 |
| L07 | Failed provisioning cleanup and rollback | NOT RUN | No probe in this collection |
| L08 | Throw-away guest delete after bench/audit | NOT RUN | No probe in this collection |

### 2. OS & Kernel Sharing (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| O01 | Shared read-only base OS | PASS | O01=PASS |
| O02 | OverlayFS lower/upper isolation (A write not visible in B) | PASS | O02=PASS; X01=PASS |
| O03 | Per-VPS hostname / identity files | PASS | O03=PASS |
| O04 | Base OS image update and rollback | NOT RUN | No probe in this collection |
| O05 | Ubuntu 24.04 bundled image (this collection) | INFO | O04=INFO |
| O06 | Debian / CentOS-compatible image tests | NOT RUN | No probe in this collection |
| O07 | Shared kernel information leakage | INFO | K01=INFO; K05=PASS; K06=PASS; I08=PASS |
| O08 | Guest package install and systemd compatibility | SKIPPED |  |

### 3. Root & Namespace Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| S01 | User namespace UID/GID mapping | PASS | hostuid=100000 |
| S02 | Host root versus VPS root isolation | PASS | guest=user:[4026534264] |
| S03 | Unique UID ranges per VPS | PASS | A=100000 B=165536 |
| S04 | PID namespace isolation | PASS | N02=PASS; X03=PASS |
| S05 | Mount / UTS / IPC / net / cgroup namespaces | FAIL | N01=FAIL; N05=PASS; X04=PASS |
| S06 | Dangerous Linux capabilities dropped | PASS | I02=PASS |
| S07 | Seccomp and no_new_privs | PASS | I03=PASS |
| S08 | AppArmor / SELinux confinement | WARN | I04=WARN |
| S09 | Cross-VPS process and filesystem access blocked | PASS | X01=PASS; X03=PASS |

### 4. Filesystem & Protection Mode (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| F01 | Protection Mode on/off (audit runs with protection off) | PASS | RC=2 Read-only file system |
| F02 | Protected-file modify without admin approval | PASS | RC=2 Read-only file system |
| F03 | Host filesystem and runtime socket access blocked | PASS | N03=PASS; X02=PASS |
| F04 | Symlink / path-traversal escape | PASS | got=cfxp-poff |
| F05 | /tmp and /var/tmp sticky bit | PASS | F02=PASS |
| F06 | Disk write quota enforcement | PASS | R04=PASS |
| F07 | Per-VPS storage separation | PASS | X01=PASS; O02=PASS |
| F08 | Concurrent read/write data integrity | NOT RUN | No probe in this collection |
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
| M07 | Out-of-memory handling | PASS | oom=1 oom_kill=1 |
| M08 | One VPS OOM does not take down another | PASS |  |
| M09 | Memory overcommit and recovery | SKIPPED |  |
| M10 | Guest /proc/meminfo vs host MemTotal | PASS | K05=PASS |

### 6. CPU, PID & Resource Limits (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| C01 | cpu.max quota visible / set | PASS | R03=PASS |
| C02 | pids.max visible / set | PASS | R01=PASS |
| C03 | Fork-bomb containment | SKIPPED | --destructive not run; live fleet must stay up |
| C04 | CPU stress isolation | SKIPPED | --destructive not run; live fleet must stay up |
| C05 | CPU scheduling and throttling | SKIPPED | --destructive not run; live fleet must stay up |
| C06 | Disk I/O throttling | NOT RUN | No probe in this collection |
| C07 | Noisy-neighbour performance impact | SKIPPED | --destructive not run; live fleet must stay up |
| C08 | In-VPS CPU loop latency | MEASURED | cpu_loop_ms=854…(3) |

### 7. Network & SSH Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| NET01 | Per-VPS IP and network isolation | PASS | X06=PASS |
| NET02 | SSH login and authorized keys | NOT RUN | No probe in this collection |
| NET03 | Host port mapping and port exhaustion | NOT RUN | No probe in this collection |
| NET04 | Cross-VPS networking allow/deny | PASS | X06=PASS |
| NET05 | Nftables / isolated veth (observed via reachability) | PASS | X06=PASS |
| NET06 | Inbound / outbound connectivity | NOT RUN | No probe in this collection |
| NET07 | IPv4 / IPv6 and DNS | NOT RUN | No probe in this collection |
| NET08 | High-concurrency network connections | NOT RUN | No probe in this collection |
| NET09 | Port scanning and packet isolation | NOT RUN | No probe in this collection |

### 8. VPS Density & Scalability (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| D01 | Controlled density 10 / 25 / 50 / 100 | MEASURED | n=10 ssh=10 nginx=0 avail_mb=6193 |
| D02 | Idle VPS density | MEASURED | n=100 ssh=100 nginx=0 mem_per_vps_mb=1.60 reason=ok secs=459 |
| D03 | Nginx application VPS density | MEASURED | n=25 ok=113704 fail=48 rps=14213.0 p95_ms=2.8  d1=18MB,cpu_us=1449865 d25=17MB,cpu_us=95415 |
| D04 | PHP + MySQL e-commerce VPS density | SKIPPED |  |
| D05 | CPU-heavy and memory-heavy mix | SKIPPED | n=1 |
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
| DB01 | MySQL create / user / database isolation | NOT RUN | No probe in this collection |
| DB02 | PostgreSQL create / user / database isolation | NOT RUN | No probe in this collection |
| DB03 | MongoDB access controls | NOT RUN | No probe in this collection |
| DB04 | Concurrent database reads and writes | NOT RUN | No probe in this collection |
| DB05 | Database backup / download / upload / restore | NOT RUN | No probe in this collection |
| DB06 | Database crash recovery | NOT RUN | No probe in this collection |
| DB07 | Storage exhaustion response | NOT RUN | No probe in this collection |
| DB08 | Tenant database security | NOT RUN | No probe in this collection |

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
| B02 | Incremental backup | NOT RUN | No probe in this collection |
| B03 | S3-compatible storage operations | NOT RUN | No probe in this collection |
| B04 | Backup checksum verification | NOT RUN | No probe in this collection |
| B05 | Encrypted backup storage | NOT RUN | No probe in this collection |
| B06 | Cross-worker restoration | SKIPPED |  |
| B07 | Interrupted backup recovery | NOT RUN | No probe in this collection |
| B08 | Backup retention and cleanup | NOT RUN | No probe in this collection |

### 13. Cloud Admin & Customer UI (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| U01 | User registration / login / MFA | NOT RUN | No probe in this collection |
| U02 | RBAC and tenant isolation | NOT RUN | No probe in this collection |
| U03 | VPS create / update / delete UI | NOT RUN | No probe in this collection |
| U04 | Worker and VPS usage dashboard | NOT RUN | No probe in this collection |
| U05 | Admin-only protection controls | NOT RUN | No probe in this collection |
| U06 | Audit logs and activity history | NOT RUN | No probe in this collection |
| U07 | Billing and payment integration | NOT RUN | No probe in this collection |
| U08 | License and worker limits enforcement | NOT RUN | No probe in this collection |

### 14. Security Attack Simulation (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| A01 | Guest-to-host escape regression probes | PASS | I05=PASS; I06=PASS; I07=PASS; N03=PASS; N04=PASS; F05=PASS |
| A02 | Cross-tenant access attempts | PASS | X01=PASS; X02=PASS; X03=PASS; X06=PASS |
| A03 | Command injection input validation | NOT RUN | No probe in this collection |
| A04 | Privilege escalation controls | PASS | I02=PASS; I03=PASS; F05=PASS |
| A05 | SSH brute-force protection | NOT RUN | No probe in this collection |
| A06 | Malicious configuration attempts | NOT RUN | No probe in this collection |
| A07 | Secrets and credentials exposure | NOT RUN | No probe in this collection |
| A08 | API authorization bypass tests | NOT RUN | No probe in this collection |
| A09 | Kernel attack surface hardening | WARN | K02=WARN; K04=PASS; K07=WARN |

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
| MO01 | Per-VPS CPU / RAM / disk metrics | NOT RUN | No probe in this collection |
| MO02 | Worker node health monitoring | NOT RUN | No probe in this collection |
| MO03 | Memory pressure / OOM alerts | NOT RUN | No probe in this collection |
| MO04 | Storage almost-full alerts | NOT RUN | No probe in this collection |
| MO05 | Network traffic metrics | NOT RUN | No probe in this collection |
| MO06 | Worker down and VPS crash alerts | NOT RUN | No probe in this collection |
| MO07 | Audit / event log integrity | NOT RUN | No probe in this collection |
| MO08 | Alert delivery and deduplication | NOT RUN | No probe in this collection |

### 17. Reliability & Failure Recovery (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| REL01 | Unexpected VPS process termination | SKIPPED |  |
| REL02 | Worker reboot / power-loss recovery | SKIPPED |  |
| REL03 | Network disconnect / reconnect | NOT RUN | No probe in this collection |
| REL04 | Disk full and filesystem read-only | NOT RUN | No probe in this collection |
| REL05 | Interrupted OS image update | NOT RUN | No probe in this collection |
| REL06 | Control-plane database outage | NOT RUN | No probe in this collection |
| REL07 | Backup / restore after simulated failure | NOT RUN | No probe in this collection |
| REL08 | Rolling worker upgrade and rollback | NOT RUN | No probe in this collection |

### 18. Billing, License & Abuse Controls (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| BL01 | Per-VPS resource billing calculation | NOT RUN | No probe in this collection |
| BL02 | Usage metering accuracy | NOT RUN | No probe in this collection |
| BL03 | Wallet, invoice, and payment state | NOT RUN | No probe in this collection |
| BL04 | Subscription expiration enforcement | NOT RUN | No probe in this collection |
| BL05 | License worker-count verification | NOT RUN | No probe in this collection |
| BL06 | VPS quota enforcement | NOT RUN | No probe in this collection |
| BL07 | Duplicate provisioning and double-charge prevention | NOT RUN | No probe in this collection |
| BL08 | Suspension / reactivation without data loss | NOT RUN | No probe in this collection |

## 4. FileVPS code-backed expectations

These rows describe what the current worker engine is written to do. They are **not** live measurements.

| ID | Expected | Why (code) | Verification |
|---|---|---|---|
| I01 | PASS when `user_ns=true` | `CLONE_NEWUSER` maps guest 0 to `uid_base` (default 100000) | Isolated try on this linuxkit host: `fx-init` EPERM on make-rprivate |
| I02 | PASS | Guest allowlist drops SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN | Unverified |
| I03 | PASS | `fx-init` installs a seccomp filter and `no_new_privs` | Unverified |
| I04 | WARN | AppArmor is detected on the host; no per-guest profile is applied | Unverified |
| I05 I06 | PASS | Guest `/dev` is a private tmpfs; mount/kexec/module syscalls are denied | Unverified |
| I07 | PASS | `mount` is in the seccomp deny list | Unverified |
| N01 N02 | PASS | New mount, PID, UTS, IPC, net, cgroup (and user when enabled) namespaces | Unverified |
| N05 | PASS | After the process is placed in `/clofix-filevps/<id>`, `CLONE_NEWCGROUP` makes the guest see `0::/`. Host `pids.max` / `memory.max` / `cpu.max` still apply. | Unverified |
| X01 X03 X04 | PASS | Separate OverlayFS upper layers and namespaces | Unverified |
| X05 | PASS when `user_ns=true` | Each guest gets a unique `host_uid` range (`uid_base + index * 65536`) | Isolated user-ns guests never reached `uid_map` on linuxkit |
| X06 | PASS | Each veth is `bridge link set isolated on`; nftables default-drop forward | Unverified |
| R01 R02 R03 | PASS | cgroup v2 sets `pids.max`, `memory.max`/`memory.high`, `cpu.max` | Unverified |
| R04 R12 | PASS | `disk_mb` is a loop+ext4 backing for overlay upper/work; guest `df` shows the limit | Unverified |
| K05 | PASS | `fx-init` bind-mounts a per-guest `/proc/meminfo` from `ram_mb` | Unverified |
| K01 | INFO | FileVPS shares the host kernel; it is not KVM/QEMU | Documented |
| O01 O02 | PASS | Shared read-only image + per-guest OverlayFS upper layer | Unverified |

Production workers default `user_ns=true`. Local Docker Desktop Compose sets `CLOFIX_USER_NS=false`, which makes I01 FAIL and is development-only.

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

