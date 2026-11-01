# CloFix FileVPS — Benchmark and Security Report

Generated: 2026-11-01 14:41

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
- Remaining FAILs I01 / N01(user) / X05 need `CLOFIX_USER_NS=true` on a throw-away worker. Enabling that on this Compose stack would remount live customer VPS and was not done.

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

These are the P0/P1 cases from the FileVPS QA checklist. Status comes from the last `bench-fx.tsv` / `audit-fx.tsv` on this host. Cases without a probe stay **NOT RUN**. Skipped items were not executed on purpose (density cap, no `--destructive`, no compare backends). Later engine fixes are not treated as a new measurement.

| Status | Count |
|---|---|
| FAIL | 4 |
| WARN | 2 |
| PASS | 22 |
| MEASURED | 5 |
| INFO | 2 |
| SKIPPED | 16 |
| NOT RUN | 99 |
| **Total** | **151** |

### 1. VPS Lifecycle (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| L01 | VPS create and start | MEASURED | create_ms=36…(2); start_ready_ms=1669…(2) |
| L02 | Create → guest ready time (fx exec, not SSH handshake) | MEASURED | start_ready_ms=1669…(2); total_ms=1705…(2) |
| L03 | Concurrent VPS creation (10 / 25 / 50) | NOT RUN | No probe in this collection |
| L04 | VPS crash and automatic recovery | NOT RUN | No probe in this collection |
| L05 | VPS recovery after worker restart | NOT RUN | No probe in this collection |
| L06 | VPS clone, rebuild, and snapshot restore | NOT RUN | No probe in this collection |
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
| O08 | Guest package install and systemd compatibility | NOT RUN | No probe in this collection |

### 3. Root & Namespace Security (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| S01 | User namespace UID/GID mapping | FAIL | I01=FAIL |
| S02 | Host root versus VPS root isolation | FAIL | I01=FAIL; X05=FAIL |
| S03 | Unique UID ranges per VPS | FAIL | X05=FAIL |
| S04 | PID namespace isolation | PASS | N02=PASS; X03=PASS |
| S05 | Mount / UTS / IPC / net / cgroup namespaces | FAIL | N01=FAIL; N05=PASS; X04=PASS |
| S06 | Dangerous Linux capabilities dropped | PASS | I02=PASS |
| S07 | Seccomp and no_new_privs | PASS | I03=PASS |
| S08 | AppArmor / SELinux confinement | WARN | I04=WARN |
| S09 | Cross-VPS process and filesystem access blocked | PASS | X01=PASS; X03=PASS |

### 4. Filesystem & Protection Mode (P0)

| ID | Case | Status | Evidence |
|---|---|---|---|
| F01 | Protection Mode on/off (audit runs with protection off) | NOT RUN | No probe in this collection |
| F02 | Protected-file modify without admin approval | NOT RUN | No probe in this collection |
| F03 | Host filesystem and runtime socket access blocked | PASS | N03=PASS; X02=PASS |
| F04 | Symlink / path-traversal escape | SKIP | F06=SKIP |
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
| M03 | ZRAM compression ratio and CPU cost | NOT RUN | No probe in this collection |
| M04 | File-backed swap usage | NOT RUN | No probe in this collection |
| M05 | Shared page-cache behaviour | MEASURED | page_cache_per_vps_mb=0.48; os_share_ratio=2335.2 |
| M06 | Memory pressure and reclaim | SKIPPED | --destructive not run; live fleet must stay up |
| M07 | Out-of-memory handling | SKIPPED | --destructive not run; live fleet must stay up |
| M08 | One VPS OOM does not take down another | SKIPPED | --destructive not run; live fleet must stay up |
| M09 | Memory overcommit and recovery | NOT RUN | No probe in this collection |
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
| D01 | Controlled density 10 / 25 / 50 / 100 | SKIPPED | DENSITY_MAX=0 on a worker that already has customer VPS |
| D02 | Idle VPS density | SKIPPED | DENSITY_MAX=0 on a worker that already has customer VPS |
| D03 | Nginx application VPS density | NOT RUN | No probe in this collection |
| D04 | PHP + MySQL e-commerce VPS density | NOT RUN | No probe in this collection |
| D05 | CPU-heavy and memory-heavy mix | NOT RUN | No probe in this collection |
| D06 | Provisioning speed under high density | SKIPPED | DENSITY_MAX=0 on a worker that already has customer VPS |
| D07 | Peak-hour latency and throughput | NOT RUN | No probe in this collection |
| D08 | Safe maximum occupancy before SLO violation | NOT RUN | No probe in this collection |

### 9. Website & Application Workloads (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| W01 | Nginx HTTP benchmark | NOT RUN | No probe in this collection |
| W02 | Apache benchmark | NOT RUN | No probe in this collection |
| W03 | PHP-FPM and WordPress | NOT RUN | No probe in this collection |
| W04 | WooCommerce-like e-commerce workload | NOT RUN | No probe in this collection |
| W05 | Node.js application | NOT RUN | No probe in this collection |
| W06 | Go API service | NOT RUN | No probe in this collection |
| W07 | Concurrent HTTP requests and latency | NOT RUN | No probe in this collection |
| W08 | Web application persistence after restart | NOT RUN | No probe in this collection |

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
| WK01 | Multiple worker registration | NOT RUN | No probe in this collection |
| WK02 | Worker secret-key enrollment | NOT RUN | No probe in this collection |
| WK03 | CPU / RAM / disk capacity discovery | NOT RUN | No probe in this collection |
| WK04 | Automatic VPS placement | NOT RUN | No probe in this collection |
| WK05 | Select another worker when one is full | NOT RUN | No probe in this collection |
| WK06 | Worker offline detection | NOT RUN | No probe in this collection |
| WK07 | Insufficient capacity rejection | NOT RUN | No probe in this collection |
| WK08 | Scheduler concurrent placement races | NOT RUN | No probe in this collection |
| WK09 | Worker removal and draining | NOT RUN | No probe in this collection |

### 12. Backup & Object Storage (P1)

| ID | Case | Status | Evidence |
|---|---|---|---|
| B01 | Full VPS backup and restore | NOT RUN | No probe in this collection |
| B02 | Incremental backup | NOT RUN | No probe in this collection |
| B03 | S3-compatible storage operations | NOT RUN | No probe in this collection |
| B04 | Backup checksum verification | NOT RUN | No probe in this collection |
| B05 | Encrypted backup storage | NOT RUN | No probe in this collection |
| B06 | Cross-worker restoration | NOT RUN | No probe in this collection |
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
| P01 | CloFix vs LXC on same hardware | SKIPPED | Incus/LXC/Docker were not measured on this host |
| P02 | CloFix vs Incus on same hardware | SKIPPED | Incus/LXC/Docker were not measured on this host |
| P03 | CloFix vs Docker on same workload | NOT RUN | No probe in this collection |
| P04 | Creation and boot time comparison | SKIPPED | Incus/LXC/Docker were not measured on this host |
| P05 | Idle and loaded RAM comparison | SKIPPED | Incus/LXC/Docker were not measured on this host |
| P06 | Disk footprint and I/O comparison | SKIPPED | Incus/LXC/Docker were not measured on this host |
| P07 | Application throughput and tail latency | NOT RUN | No probe in this collection |
| P08 | Security and isolation differences | SKIPPED | Incus/LXC/Docker were not measured on this host |

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
| REL01 | Unexpected VPS process termination | NOT RUN | No probe in this collection |
| REL02 | Worker reboot / power-loss recovery | NOT RUN | No probe in this collection |
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
| I01 | PASS when `user_ns=true` | `CLONE_NEWUSER` maps guest 0 to `uid_base` (default 100000) | Unverified on this Mac workspace |
| I02 | PASS | Guest allowlist drops SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN | Unverified |
| I03 | PASS | `fx-init` installs a seccomp filter and `no_new_privs` | Unverified |
| I04 | WARN | AppArmor is detected on the host; no per-guest profile is applied | Unverified |
| I05 I06 | PASS | Guest `/dev` is a private tmpfs; mount/kexec/module syscalls are denied | Unverified |
| I07 | PASS | `mount` is in the seccomp deny list | Unverified |
| N01 N02 | PASS | New mount, PID, UTS, IPC, net, cgroup (and user when enabled) namespaces | Unverified |
| N05 | PASS | After the process is placed in `/clofix-filevps/<id>`, `CLONE_NEWCGROUP` makes the guest see `0::/`. Host `pids.max` / `memory.max` / `cpu.max` still apply. | Unverified |
| X01 X03 X04 | PASS | Separate OverlayFS upper layers and namespaces | Unverified |
| X05 | PASS when `user_ns=true` | Each guest gets a unique `host_uid` range (`uid_base + index * 65536`) | Unverified |
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
python3 make-report.py results results/report.md
```

Do not run density or `--destructive` probes on a production host.

