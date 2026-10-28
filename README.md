# CloFix FileVPS

- admin/  : controller (CA, enrollment, mTLS worker API, admin API, placement) + UI + db/ (migrations)
- worker/ : one binary `clofix-worker` = agent + clofix-fx engine + scheduler (shared in worker/pkg/scheduler)

## Run locally (Docker)
    cp .env.example .env            # set DB_PASSWORD and ADMIN_TOKEN
    docker compose up --build
    open http://localhost:8080      # paste ADMIN_TOKEN

The Compose worker bundles an Ubuntu 24.04 userspace for new VPSs, so the default Ubuntu image needs no URL or checksum. VPSs using the same image share its read-only base rootfs layer; each VPS has its own writable OverlayFS layer, so its file changes stay separate. Removing a VPS removes its writable layer, not the shared base image. VPSs also share the worker's Linux kernel and physical CPU and RAM. CPU, memory, swap, and process limits are enforced per VPS with cgroup v2; these are limits on shared host resources, not dedicated cores or RAM. FileVPS guests are containers, not full virtual machines with separate kernels. Custom images still need a URL and SHA-256. Production workers need a matching bundled rootfs installed or a custom image URL and SHA-256.

New VPSs start automatically with Protection Mode enabled. The guest root filesystem is read-only; `/run`, `/tmp`, `/var/log`, and APT indexes/cache use temporary storage and are cleared on restart. `apt update` can refresh temporary indexes, but installing packages requires disabling protection. An admin can toggle the mode from VPS Details; changing it restarts a running VPS. Protection Mode does not stop processes or network activity and is not a substitute for VM isolation. Paste an SSH public key in the create form; on macOS, create one with `ssh-keygen -t ed25519` and copy `cat ~/.ssh/id_ed25519.pub`. VPS Details shows RAM and swap usage/limits; the Workers page shows host swap and ZRAM metrics. New VPSs default to a 512 MB swap limit, adjustable during creation (0 disables it). Host-wide ZRAM is optional and configured with `zram_mb` in the worker config; local Docker Desktop may not expose ZRAM devices. The UI shows each private worker IP and local SSH, HTTP, and HTTPS access. Compose forwards SSH ports 22000–22099, HTTP ports 23000–23099 to guest port 80, and HTTPS ports 23100–23199 to guest port 443. Each VPS detail page shows CPU/RAM usage, metadata, logs, and manual TCP/UDP port mappings. Manual mappings use unique local ports 24000–24099. For example, the first VPS is available at `http://localhost:23000`. The `10.231.0.x` addresses are private to the worker and are not directly reachable from the Mac.

## Enroll the dev worker
    curl -s http://localhost:8080/api/ca.pem -o /tmp/ca.pem
    # UI: "New enrollment token" (shown once), then:
    docker compose cp /tmp/ca.pem worker:/tmp/ca.pem
    docker compose exec -it worker clofix-worker enroll --controller https://admin:8443 --ca /tmp/ca.pem

## Build
- worker: `cd worker && ./build.sh [bin|deb|rpm|all]`
- admin + db: `./scripts/build-admin-db.sh [build|up|down|logs]`
- controller without Docker: `cd admin && go build ./cmd/clofix-controller` (in-memory store; add `-tags postgres` for PostgreSQL)
