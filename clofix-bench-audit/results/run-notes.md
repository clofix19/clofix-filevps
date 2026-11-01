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
