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
