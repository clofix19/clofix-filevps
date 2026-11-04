# CloFix FileVPS

OS-level isolation on customer Linux hosts: namespaces, cgroup v2, OverlayFS, veth/nftables and seccomp. FileVPS guests are containers that share the worker kernel. They are not hardware VMs and do not use KVM/QEMU.

The stack is three parts:

- `admin/` — customer controller (CA, enrollment, mTLS worker API, admin API, placement) plus the Admin UI and `admin/db/` migrations
- `worker/` — one binary `clofix-worker` = agent + clofix-fx engine + scheduler (`worker/pkg/scheduler`)
- `license-server/` — CloFix-hosted plan validation; the customer stack only calls its API

## Run locally (Docker)

```sh
cp .env.example .env            # set DB_PASSWORD, CLOFIX_ADMIN_EMAIL, CLOFIX_ADMIN_PASSWORD
docker compose up --build
open http://localhost:8080/login
```

Local Compose publishes admin UI/API on `8080`, worker mTLS on `8443`, and a loopback NLB test port on `127.0.0.1:18080`. UI files are volume-mounted (`./admin/web:/app/web:ro`); HTML/JS/CSS changes do not require an image rebuild. Go API changes do.

The Compose worker defaults to `CLOFIX_KUBERNETES_FAKE=1` (lab CKS without host kubeadm — never on production workers) and `CLOFIX_USER_NS=false` (Docker Desktop). Do **not** set the worker’s DNS to public resolvers only (`8.8.8.8` / `1.1.1.1`); that breaks Docker service discovery (`admin`, `s3pool`) and stalls heartbeats / CKS readiness.

The Compose worker bundles an Ubuntu 24.04 userspace for new VPSs, so the default Ubuntu image needs no URL or checksum. VPSs using the same image share its read-only base rootfs layer; each VPS has its own writable OverlayFS layer, so its file changes stay separate. Removing a VPS removes its writable layer, not the shared base image. VPSs also share the worker's Linux kernel and physical CPU and RAM. CPU, memory, swap, and process limits are enforced per VPS with cgroup v2; these are limits on shared host resources, not dedicated cores or RAM. Custom images still need a URL and SHA-256. Production workers need a matching bundled rootfs installed or a custom image URL and SHA-256.

## Admin UI

Open `http://localhost:8080/login` with the bootstrap admin email and password from `.env`. Public **Register** creates a root client account. The search catalog is the live product map:

| Section | Pages |
|---|---|
| Overview | Dashboard — VPS/worker counts, guest and host CPU/RAM/disk/swap, VPS and worker network IN/OUT/bandwidth charts |
| Compute | FileVPS, Library (Quick Apps + VPS software), Jobs & Functions, CKS (`/kubernetes` — dedicated workers; FileVPS kubelet unavailable) |
| Storage | Volumes, EFS, Object Storage, Backups, Databases |
| Networking | Load Balancers, VPC, DNS Service, Health Checks, Firewall |
| Security | **WAF** (`/waf` — shared OWASP CRS engine + bindings), Security Center (score, modes, Protection), Users (partner / client / admin cards + create/edit/delete), IAM |
| Platform | Workers, Billing & Payments, Settings (General, Pricing & Plans, NLB pool, notifications, License) |

IAM scopes match those resources (`instances`, `databases`, `kubernetes`, `load-balancers`, `dns`, `pricing`, `billing`, and the rest). Admin is the CloFix owner; partners manage their clients; root and IAM users stay inside one client account. **Users** (`/users`) shows partner / client / admin counts and a directory with Create, Edit, and Delete (guards block deleting your own session and unsafe last-admin cases).

## FileVPS

New VPSs start automatically with Protection Mode enabled. The guest root filesystem is read-only; `/run`, `/tmp`, `/var/log`, and APT indexes/cache use temporary storage and are cleared on restart. `apt update` can refresh temporary indexes, but installing packages requires disabling protection. An admin can toggle the mode from **VPS Details → Security**; changing it restarts a running VPS. Protection Mode does not stop processes or network activity, does **not** inspect HTTP payloads (use OWASP CRS WAF for SQLi/XSS/RCE), and is not a substitute for VM isolation.

**Details → Security** also covers SSH Login Block, SSH Key-Only, Delete Protection, **Network Quarantine** (Protection + SSH block + delete lock + inbound restrict), and **Security Modes** (Standard, Hardened, Lockdown, Maintenance, Web Block). Modes apply those live controls together; Quarantine is a separate incident action, not a mode card. Fleet dry-run/apply lives on Security Center → Modes.

**WAF** (`/waf`) is a shared **OWASP CRS** edge engine per worker (ModSecurity + CRS image / `clofix-waf` network). You do **not** reinstall the engine per target: create a **binding** for a FileVPS, Docker application, or Docker container, then activate it. Open a binding at `/waf/bindings/{id}` for KPI sparklines (hits / blocks / attacks / unique IPs), traffic tables (top IPs, URIs, attack types, recent events), CRS rule-group toggles, and custom HTML error pages (403/404/406/429/500). Stats are pulled from the edge ModSecurity audit (`waf-stats`); Refresh queues a sample without jumping the page scroll. FileVPS traffic is fronted by the worker gateway — the WAF is **not** installed inside the guest. Legacy Library catalog `owasp-waf` guest installs are obsolete for new bindings; uninstall them before attaching edge WAF. The edge inspects HTTP/HTTPS on the bound target only — not SSH or raw TCP forwards. See [book.md](book.md) §9.3.

Paste an SSH public key in the create form; on macOS, create one with `ssh-keygen -t ed25519` and copy `cat ~/.ssh/id_ed25519.pub`. VPS Details shows CPU, RAM, swap, and guest network IN/OUT. The Workers page shows host CPU, RAM, disk, swap, ZRAM and network. New VPSs default to a 512 MB swap limit, adjustable during creation (0 disables it). Host-wide ZRAM is optional and configured with `zram_mb` in the worker config; local Docker Desktop may not expose ZRAM devices.

The UI shows each private worker IP and local SSH, HTTP, and HTTPS access. Compose publishes cloud-scale ranges: SSH `22000–26999`, HTTP `27000–31999` (guest 80 = SSH+5000), HTTPS `32000–36999` (guest 443 = SSH+10000), custom maps `37000–41999`, managed apps `42000–46999`, databases `47000–51999`. For example, SSH `22000` is HTTP at `http://localhost:27000`. The `10.231.0.x` addresses are private to the worker and are not directly reachable from the Mac. VPS Details includes an SSH command helper for Terminal; it is not an in-browser shell.

The Backups page snapshots a stopped VPS writable root layer to compressed worker-local storage. **Run as VPS** restores it as a new VPS on the same worker. Persistent volumes and S3 mounts are outside the backup. Cross-worker restore is not available.

## CKS (CloFix Kubernetes Service)

`/kubernetes` (also `/cks`) manages EKS-style clusters on **dedicated CKS workers**, not FileVPS guests. From the UI: create/select clusters, Open / Resources / Kubeconfig / Edit, VPS groups (node pools) with optional autoscaling, workloads (Deployments, StatefulSets, Services, Ingress, Secrets, ConfigMaps, PVC, Jobs, HPA, NetworkPolicy, pod logs), Helm, CloFix attach (LB / FileVPS / volume / EFS / S3), add-ons, and etcd backup/upgrade. Local Compose uses the lab fake executor — see [docs/cks.md](docs/cks.md) and [book.md](book.md) §6.4.

## Shared S3 storage pool

Compose starts a SeaweedFS master, filer, and authenticated S3 gateway at `http://localhost:8333` (also reachable by the admin container at `http://s3pool:8333`). Set `S3_ACCESS_KEY` and `S3_SECRET_KEY` in `.env`, then create an S3 connection in the UI using endpoint `http://s3pool:8333`, bucket name, and those credentials. The master stores metadata in the persistent `s3masterdata` volume. Each enrolled Linux worker starts a SeaweedFS volume server automatically and contributes `/var/lib/clofix-filevps/s3-volume`; the Storage page reports online worker count and aggregate backing-filesystem size/free space.

The pool uses replication placement `000` (one copy) to maximize capacity. If a worker disk fails, objects on that worker can be lost. The S3 directory shares the worker filesystem with its OS, VPS layers, and backups; the UI reports backing filesystem capacity, not a reserved quota, and SeaweedFS leaves 5% free as a safety margin. For workers outside the local Compose network, configure the controller's `CLOFIX_S3_MASTER_ADDRESS` to a private `host:9333`, bind the Compose master ports to a private interface with `S3_MASTER_BIND_IP`, and allow TCP 9333/19333 to the master plus TCP 8080/18080 between Linux workers and the master on that private network. Do not expose these cluster ports publicly. The S3 gateway is loopback-only in the local Compose setup; production S3 access needs TLS and firewall restrictions. Docker Desktop on macOS cannot provide a production Linux storage node; use Linux workers for pool capacity.

## Library and managed apps

`/services` has Quick Apps (persistent sandboxes) and VPS software installs. Quick Apps include Adminer, Jenkins, and ZIP web deployments: static HTML, PHP, Node.js/Next.js and WordPress. A deploy can attach an existing managed database. Apps use CloFix namespaces and cgroup v2, not a customer VPS. The worker template builder downloads the official Jenkins LTS war and verifies its SHA-256 checksum. See [managed apps](docs/managed-apps.md).

Jobs & Functions (`/applications`) run scheduled or invoked workloads on workers that advertise serverless runtimes. See [serverless](docs/serverless.md).

## Docker Engine

`/docker` manages containers on the **worker-host Docker Engine** (admin opt-in install/detect). Image, Compose, Git build/webhook/rollback and Docker Quick App templates are separate from Library native Quick Apps. Tenants never receive `docker.sock`; database DinD is unchanged. Host ports 18000–21999 with FileVPS conflict checks. IAM `docker:read`/`docker:manage`; optional license quotas `max_docker_*`.

**Backup & Restore** (`/docker#docker-backups`) archives managed Docker volumes on the worker: create, restore into the same volume, restore+redeploy apps that mount it, or delete the archive. This is separate from FileVPS writable-layer backups on `/backups`. See [docker](docs/docker.md).

## Managed databases

`/databases` manages owner-scoped MySQL, PostgreSQL, MongoDB and Redis projects: users, schema, encrypted backups (where implemented), uploads, restores, schedules and jobs. Redis uses key-prefix logical databases; Redis backup/restore is not implemented. Database capability is opt-in per Linux worker (pinned image manifest + local Docker runtime). Failed provision can be retried from the UI. See [managed databases](docs/managed-databases.md).

## Load balancers

`/load-balancers` creates NLB (TCP, unique IP from Settings → NLB pool) or ALB (HTTP/HTTPS domain routing or a generated `/lb/<id>/` URL). The balancer owns backend VPS instances restored from a ready backup and scales them by health and target CPU. ALB custom domains must be pointed at the controller; this stack does not issue certificates. See [load balancers](docs/load-balancers.md).

## DNS Service

`/dns` stores a controller zone (default `clofix.internal`). Creating a VPS, load balancer, database, managed app or worker adds A/CNAME records automatically (`{id}.vps.`, `{id}.lb.` / `{id}.alb.`, `{id}.db.`, `{id}.app.`, `{id}.worker.`). Automatic records include known ports (VPS SSH, LB listen, database/app listener).

On **Records**, select FileVPS, load balancer, database, app or worker, set a hostname and optional port, and add a record. Ports are exported as SRV lines in the BIND-style zone file. The controller does **not** listen on port 53 and does not publish names on the public Internet. Point an external nameserver at the exported zone, or set a zone publish address for ALBs that share the controller entrypoint.

## Health Checks

`/health-checks` monitors HTTP, TCP and ICMP targets plus worker/LB availability. Alerts go to channels configured in Settings. See [monitoring](docs/monitoring.md).

## Pricing and billing

**Settings → Pricing & Plans** is the catalog: plans, resource unit rates, discounts, gateway fields, currency/tax and notification flags. Resource Pricing has Standard and Low cost monthly/hourly rates and a default metering tier. A price edit requires `effective_from`. Active subscriptions keep their snapshotted rates; later catalog edits do not rewrite them.

**Billing & Payments** is a separate menu and IAM resource: invoices, wallets, manual payments, subscriptions, refunds, credits and revenue totals. **Live resources** scans VPS, databases, load balancers, applications, volumes, backups, S3 and EFS and shows name, ID, worker and the current USD/BDT estimate. A stopped VPS or database is billed for storage only. **Due bill** meters hourly Standard or Low cost usage every minute. A 5 or 10 minute run is billed for those minutes, and deleting the resource or port still adds the usage to Due bill until it is invoiced. Each invoice lists the billed resource with USD and BDT amounts using a bookkeeping FX rate from Settings → Currency. **Download PDF** writes a CloFix-branded invoice (logo, bill-to, service period, resource line items, tax, USD/BDT total). The Monthly analysis view has spend-by-month and spend-by-resource bar charts. Draft invoices stay marked DRAFT. Card charges are not processed; gateway secrets are stored and never returned by the API.

## Company-side license service

The independent [license-server project](license-server/README.md) is deployed on CloFix infrastructure. The customer Compose stack only connects to its validation API through Settings → License; company plan management and credentials stay outside the customer stack. See [licensing](docs/licensing.md).

## Enroll the dev worker

```sh
curl -s http://localhost:8080/api/ca.pem -o /tmp/ca.pem
# UI: "New enrollment token" (shown once), then:
docker compose cp /tmp/ca.pem worker:/tmp/ca.pem
docker compose exec -it worker clofix-worker enroll --controller https://admin:8443 --ca /tmp/ca.pem
```

No enrollment secret lives in Compose files. The local worker hostname is `clofix-local-worker`.

## Build

- worker: `cd worker && ./build.sh [bin|deb|rpm|all]`
- admin + db: `./scripts/build-admin-db.sh [build|up|down|logs]`
- controller without Docker: `cd admin && go build ./cmd/clofix-controller` (in-memory store; add `-tags postgres` for PostgreSQL)

## More documentation

- [book.md](book.md) — client book (edge WAF bindings, Security Modes, CKS, Users, storage, billing)
- [admin/README.md](admin/README.md) — IAM, backups, VPS login helper
- [worker/README.md](worker/README.md) — worker package and fx engine
- [docs/cks.md](docs/cks.md) — CloFix Kubernetes Service (node pools, Helm, HA, backups; FileVPS kubelet unavailable)
- [docs/library-java-services.md](docs/library-java-services.md) — WildFly/Tomcat library services
- [clofix-bench-audit/README.md](clofix-bench-audit/README.md) — bench/audit scripts
