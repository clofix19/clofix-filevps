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
