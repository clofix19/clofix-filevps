# CloFix FileVPS Worker

The worker is the host-side agent that provisions and runs FileVPS guests. The `clofix-worker` binary combines worker enrollment, the mTLS control agent, capacity reporting, and the `clofix-fx` guest lifecycle engine. Guests use the worker's Linux kernel and share its physical CPU and RAM. Guests using the same image share its read-only base rootfs layer, while each gets a separate writable OverlayFS layer; one guest's file changes do not alter another's layer. Removing a guest deletes its writable layer but retains the cached base image. Each guest has separate namespaces, and cgroup v2 enforces per-guest CPU, memory, swap, and PID limits. These limits do not provide dedicated CPU cores or RAM, and guests are containers rather than full hardware virtual machines.

## Features

- **Guest lifecycle:** create, start, stop, delete, and reconcile guests after a worker restart.
- **Guest service startup:** the default guest boot command starts an installed Nginx service before SSH, so Nginx comes back after a VPS restart. A custom guest `init` command remains responsible for starting its own services.
- **Service inventory, resource use, and controls:** worker heartbeats report enabled, non-system application services from each running guest's systemd/SysV startup links, with process-based CPU, proportional set size (PSS) RAM estimates, guest swap estimates, and bounded log tails. PSS apportions shared process pages; older kernels fall back to RSS. Service RAM values are estimates and do not necessarily sum to the VPS cgroup total, which also includes cache and kernel memory. From VPS Details, admins can start, stop, or restart discovered services and view recent service logs. Commands run inside the guest's namespaces and cgroup. OS services such as SSH, cron, and systemd are filtered. Inventory, resource samples, and logs refresh on worker heartbeats; the inventory is scanned while the VPS is running. ZRAM use is reported at worker/host level and cannot be attributed to an individual service.
- **Service catalog installs:** the admin Services page can install Nginx, Apache, OWASP CRS WAF for Nginx, WordPress, Joomla, MySQL, MariaDB, PostgreSQL, Redis, PHP, Node.js, and Python in a running Ubuntu guest. Install requests are fixed catalog IDs (not arbitrary shell/package input), execute in the guest namespaces and cgroup, and report installation state in the UI. Protection Mode must be disabled first. WordPress and Joomla packages still require their browser-based site/database setup; Joomla files are placed at `/var/www/html/joomla`.
- **OWASP CRS WAF:** installs Ubuntu's Nginx ModSecurity connector and ModSecurity CRS packages, enables request and response inspection in blocking mode, and applies selected CRS attack groups to Nginx HTTP/HTTPS traffic. VPS Details can control SQLi, XSS, RCE, LFI/RFI, PHP/generic attacks, protocol and method enforcement, scanner detection, IP reputation, Layer 7 DoS rules, multipart checks, response disclosure, and session fixation. Category switches remove CRS tags or rule-ID ranges; each change validates with `nginx -t` before reloading. The WAF error-log viewer shows the Nginx error log, where ModSecurity events are recorded. IP reputation state is temporary and resets when the VPS restarts. This only protects HTTP traffic handled by this Nginx instance. It does not protect direct SSH/database traffic, other guest ports, authorization/business-logic flaws, or requests sent through a different web server. CRS rules may need application-specific tuning to avoid false positives.
- **Protection Mode:** enabled by default for each VPS. The guest root is mounted read-only; `/run`, `/tmp`, `/var/log`, and APT indexes/cache use temporary writable storage that is cleared on restart. `apt update` can refresh temporary package indexes, but package installation and persistent changes require disabling protection. Admins can switch the mode in VPS Details; changing it restarts a running VPS.
- **Linux isolation:** mount, PID, UTS, IPC, and network namespaces; private root filesystems built from OverlayFS; seccomp syscall filtering.
- **Resource limits:** per-guest cgroup v2 CPU, memory, swap, and PID limits.
- **Swap and ZRAM:** each VPS can be assigned a swap limit (shown with current swap use). Worker heartbeats report host swap and active ZRAM capacity/usage. Set `zram_mb` in `worker.json` or `CLOFIX_ZRAM_MB` (Compose: `ZRAM_MB` in `.env`) to configure host-wide ZRAM where the Linux host exposes `/dev/zram0`; zero leaves host swap unchanged. ZRAM is not counted as physical RAM for placement.
- **Image handling:** bundled Ubuntu 24.04 rootfs support, image cache, archive extraction safeguards, and SHA-256 verification for custom images.
- **S3/FUSE mounts:** an admin can attach the configured S3-compatible bucket to a VPS below `/mnt/`. The worker stores per-instance credentials in a mode-0600 file, runs `s3fs` on the Linux host, and bind-mounts the FUSE filesystem into the guest. A running VPS restarts when the mount changes. Workers need `s3fs` (s3fs-fuse), `/dev/fuse`, and permission to create FUSE mounts; Docker Desktop on macOS may not expose `/dev/fuse`, so this feature requires a Linux worker. S3 object writes go directly to the bucket and are not made read-only by guest Protection Mode. The UI bucket browser and upload controls are not implemented yet.
- **Networking:** private `10.231.0.0/16` bridge, guest egress NAT, metadata-service blocking, SSH forwarding, and TCP/UDP guest port mappings.
- **Control plane:** mTLS enrollment and heartbeats, worker capacity and health reporting, queued instance operations, and controller outage recovery.
- **Usage reporting:** worker CPU, memory availability, PSI pressure, and per-guest CPU/RAM usage are reported to the admin UI.
- **Worker security:** enrollment tokens are entered interactively and are not stored in the worker configuration. Production defaults enable a separate UID/GID range for guest root.

## Local Docker Desktop networking

In the local Compose setup, guest IPs such as `10.231.0.3` are private to the worker container and cannot be reached directly from macOS. Compose binds these host ports to `127.0.0.1`:

| Host port range | Guest port | Use |
| --- | ---: | --- |
| `22000–22099` | TCP 22 | SSH |
| `23000–23099` | TCP 80 | HTTP |
| `23100–23199` | TCP 443 | HTTPS |
| `24000–24099` | selected TCP/UDP port | Manual mappings created in the UI |

The UI assigns a localhost host port for each manual mapping and shows it on the guest details page. The worker applies mapping changes while the guest is running.

Compose sets `CLOFIX_USER_NS=false` for this local Docker Desktop environment because nested user namespaces are not supported reliably there. This runs guest root as worker-container root and is for local development only. Normal worker configuration defaults to user namespaces enabled.

## Commands

```text
clofix-worker run       Run the worker agent and guest engine
clofix-worker enroll    Enroll with a one-time token (prompted securely)
clofix-worker fx         Run the guest-engine CLI
```

## Main packages

- `internal/agent` — mTLS heartbeat loop and operation executor.
- `internal/capacity` — host resource, PSI, and storage capacity collection.
- `internal/engine/cgroups` — guest CPU, memory, swap, and PID limits and usage.
- `internal/engine/image` — image verification, caching, and safe rootfs extraction.
- `internal/engine/lifecycle` — guest create/start/stop/delete and recovery.
- `internal/engine/namespaces` — namespace init, root pivot, devices, and seccomp.
- `internal/engine/network` — bridge, veth, NAT, firewall, metadata blocking, and port mappings.
- `pkg/scheduler` — worker capacity scoring and placement.

The worker uses Linux kernel facilities directly; it does not depend on Docker, containerd, runc, or LXC to run guests. Host requirements are checked at startup; cgroup v2, namespaces, OverlayFS, and root privileges are required.

## Current limits

- `disk_mb` is used for placement and capacity accounting; per-guest disk quotas are not enforced yet.
- AppArmor availability is detected, but guest-specific AppArmor profiles are not applied.
- Guests share the host kernel. These are containers, not virtual machines with separate kernels.
- Protection Mode prevents persistent writes through the guest root while enabled, but it does not stop running processes or network activity. Treat local Docker Desktop mode with `CLOFIX_USER_NS=false` as development-only, not as a security boundary for hostile tenants.
