# CloFix Cloud — Client Book

A practical guide for partners, root clients and IAM users: what CloFix Cloud is, every service in the console, why it is faster than a classic virtual machine, how FileVPS security works, and how storage, DNS, databases and billing work.

**Audience:** customers using the CloFix Cloud console  
**Product:** CloFix Cloud (FileVPS compute plus storage, networking, CKS, billing)  
**Measured numbers:** FileVPS bench/audit collection of 1 November 2026 (see chapter 4)

---

## Bangla summary / বাংলা সারাংশ

CloFix Cloud একটি **full cloud console**: FileVPS, library apps, databases, load balancer, DNS, firewall, IAM, billing, আর **CKS** (EKS-style Kubernetes)।

FileVPS সাধারণ KVM/QEMU VM নয়। এটা Linux **namespace + cgroup v2 + OverlayFS** — একই worker kernel share করে, তাই:

- নতুন VPS প্রায় **২ সেকেন্ডের** মধ্যে ready
- Create median **৩৮ ms**
- Idle host RAM প্রতি VPS প্রায় **১.৬ MB**
- ১০০টা networked VPS একই worker-এ উঠেছে
- Overlay base OS share — disk খরচ খুব কম

Traditional cloud VM সাধারণত boot করতে **১৫–৬০ সেকেন্ড** লাগে এবং প্রতি VM-এ **১০০–৫০০+ MB** hypervisor overhead থাকে। CloFix তাই boot ও density-তে অনেক দ্রুত। এটা **hardware isolation নয়** — kernel share হয়; Isolation namespaces, seccomp, cgroup আর Protection Mode দিয়ে হয়।

**VPS security (ক্লায়েন্ট কন্ট্রোল):** নতুন FileVPS-এ Protection Mode ডিফল্ট **on** (root read-only)। SSH key/password, SSH Login Block, SSH Key-Only, Delete Protection, **Security Modes** (Standard / Hardened / Lockdown / Maintenance / Web Block), Network Quarantine, **WAF** (`/waf` — worker-shared OWASP CRS edge + binding; FileVPS/Docker), port forward রিভিউ, আর Firewall-এ CIDR allow/deny। Protection Mode HTTP attack block করে না — SQLi/RCE-এর জন্য WAF binding লাগে। এক VPS থেকে অন্য VPS-এ SSH/TCP ডিফল্টে **block**। IAM / Users দিয়ে partner–client অ্যাকাউন্ট ম্যানেজ।

**Storage ও DNS:** Volume = এক worker-এ ১:১ disk। EFS = অনেক VPS/worker-এ shared web root (CloFix S3 prefix)। S3 = object bucket + VPS mount। Backup = বন্ধ VPS-এর writable layer; same worker-এ নতুন VPS। DB service = MySQL / PostgreSQL / MongoDB / Redis, TLS, encrypted backup। DNS = `clofix.internal` zone — VPS, LB, CKS Service/Ingress, DB, app auto record।

CKS হলো dedicated worker-এ kubeadm cluster (EKS-এর মতো console)। FileVPS guest-এ kubelet চলে না।

---

## Contents

1. [What CloFix Cloud is](#1-what-clofix-cloud-is)
2. [Who it is for](#2-who-it-is-for)
3. [Why clients choose it](#3-why-clients-choose-it)
4. [How much faster than other VMs](#4-how-much-faster-than-other-vms)
5. [Console map](#5-console-map)
6. [Compute](#6-compute)
7. [Storage (volumes, EFS, S3, backups, databases)](#7-storage-volumes-efs-s3-backups-databases)
8. [Networking (VPC, DNS, load balancers)](#8-networking-vpc-dns-load-balancers)
9. [Security and VPS security](#9-security-and-vps-security)
10. [Billing and plans](#10-billing-and-plans)
11. [What CloFix is not](#11-what-clofix-is-not)
12. [Getting started](#12-getting-started)
13. [Glossary](#13-glossary)

---

## 1. What CloFix Cloud is

CloFix Cloud is a **multi-tenant cloud control plane** plus Linux **workers** that run your workloads.

The console is one place for:

| Layer | What you get |
|---|---|
| Compute | FileVPS Linux guests, Library (Quick Apps + VPS software), Jobs & Functions, **Docker Engine** (host dockerd), CKS (EKS-style Kubernetes) |
| Storage | Volumes, EFS, S3 object storage, backups, managed databases |
| Networking | VPC, DNS, load balancers (ALB/NLB), health checks, firewall |
| Security | Protection Mode, SSH Login Block, SSH Key-Only, Delete Protection, Security Modes, Network Quarantine, OWASP CRS WAF, firewall CIDRs, IAM, Users (partner/client), SSH keys |
| Platform | Dashboard, logs, invoices, wallets, resource pricing |

Workers are physical (or dedicated) Linux hosts enrolled to the controller over mTLS. The controller places work using live CPU, RAM and disk capacity and **does not RAM-overcommit**.

FileVPS guests are **OS-level isolated Linux environments**. They share the worker kernel. They are **not** KVM/QEMU hardware VMs.

---

## 2. Who it is for

| Role | Sees |
|---|---|
| **Root client** | Everything in their account: VPS, CKS, storage, networking, IAM sub-users, due bill |
| **IAM user** | Only the permissions their policy allows (`instances`, `kubernetes`, `databases`, …) |
| **Partner** | The client accounts they manage, plus billing for those clients |
| **CloFix admin** | Platform: workers, catalog pricing, licenses, all accounts |

Register creates a root client account. Partners create clients. IAM lives under one account ID — Root and IAM users of the same account share that ID.

---

## 3. Why clients choose it

**Speed.** A FileVPS is created in tens of milliseconds and is typically ready in about two seconds. Classic hypervisor VMs often take tens of seconds to boot.

**Density.** Idle guests cost a few megabytes of host RAM, not hundreds. One worker can hold many small Linux environments because they share one read-only Ubuntu base layer (OverlayFS).

**Full product, one login.** VPS, volumes, EFS, S3, backups, databases, DNS, load balancers, WAF, Kubernetes-style clusters and invoices sit in the same console.

**Pay for what ran.** Resource Pricing has monthly and hourly Standard / Low-cost rates. Due bill meters by the minute. A stopped VPS or database is billed for storage only.

**Guardrails by default.** New FileVPS guests start with Protection Mode on (read-only root). CPU, RAM, swap and PIDs are cgroup v2 limits. SSH keys or PAM passwords are client-controlled. Per-VPS **Details → Security** covers Protection, SSH Login Block, Delete Protection, SSH Key-Only, Security Modes, Network Quarantine and port forwards. HTTP payload protection is on **WAF** (`/waf`): bind FileVPS or Docker targets to the shared OWASP CRS edge (rule groups, stats, custom error pages).

**Tenant isolation.** Resources are owned by `account_id`. Cross-account get/delete is not shown as another tenant’s object. Guest-to-guest TCP on the FileVPS bridge is blocked.

**CKS when you need Kubernetes.** EKS-style console: clusters, node pools / VPS groups, workloads, Helm, ingress, storage class, backups. Pods run on dedicated CKS workers, not inside FileVPS guests.

---

## 4. How much faster than other VMs

### 4.1 Architecture, in one picture

```text
Traditional cloud VM (KVM/QEMU)
  Hardware → hypervisor → guest kernel → guest OS → app
  Boot: often 15–60 s     RAM tax: often 100–500+ MB per VM

CloFix FileVPS
  Hardware → host kernel → namespaces + cgroup v2 + OverlayFS → guest OS → app
  Create: ~38 ms          Ready: ~1.7 s     Idle host RAM: ~1.6 MB per VPS
```

FileVPS **skips a second kernel**. That is why create, boot and idle RAM beat a hardware VM. It is the same class of idea as a system container (LXC/Incus), with CloFix’s own engine, scheduler, console and billing — not a generic LXC panel.

### 4.2 Measured FileVPS numbers

Collected 1 November 2026 on the CloFix FileVPS engine (Ubuntu 24.04 bundled rootfs). Isolated re-run after engine fixes. These are **measured**, not marketing estimates.

| Metric | CloFix FileVPS |
|---|---|
| Create time (median) | **38 ms** |
| Start → ready (median) | **1.67 s** |
| Total launch (median) | **1.71 s** |
| In-guest exec (median) | **3 ms** |
| Host processes per VPS | **1** |
| Disk after create / running | **~0.1 MB** extra (shared base OS) |
| Apparent rootfs vs real disk (share ratio) | **~2,335×** |
| 100 networked VPS + SSH | **PASS** — 459 s wall, **~1.60 MB** host RAM each |
| Guest-to-guest SSH on the bridge | **Blocked** (isolated) |
| 25-guest Nginx load | **14,213 req/s**, p95 **2.8 ms** |
| CPU quota under 0.2 core | Throttles as designed |
| Disk past guest image size | **ENOSPC** |
| Writable-layer backup + restore | **PASS** |
| Protection Mode (`/etc` read-only) | **PASS** |

Idle pack of 10 / 25 / 50 / 100 VPS was also measured (~2.56 MB host RAM each on that pass).

### 4.3 Versus a typical hypervisor VM

Same-hardware KVM / Incus / LXC bake-off was **not** installed on that collection host, so CloFix does **not** publish a fake “vs AWS / vs Proxmox” scoreboard. The comparison below uses **CloFix measurements** plus **typical industry ranges** for hardware VMs.

| What clients feel | Typical KVM / cloud VM | CloFix FileVPS (measured) | Practical gain |
|---|---|---|---|
| Time to a usable guest | Often **15–60 s** (boot + cloud-init) | **~1.7 s** ready | About **10–35×** faster to ready |
| Create API → guest exists | Seconds | **38 ms** | Near-instant place-and-start |
| Idle RAM per guest | Often **100–500+ MB** (guest kernel + hypervisor) | **~1.6–2.6 MB** host RAM | About **40–300×** denser idle |
| Extra disk per clone | Full guest disk | **~0.1 MB** + Overlay writes | Shared Ubuntu layer |
| Command inside guest | SSH handshake + guest kernel | **~3 ms** exec | Fast automation |
| 100 small Linux boxes | Heavy; many vCPUs/RAM | **100 SSH-ready guests** on one worker | High density |

**Read this honestly:**

- Faster boot and higher density come from **sharing the host kernel**.
- A hardware VM still wins if you need a **different kernel**, nested virtualization, or hypervisor-grade isolation.
- Nginx 14k rps was a **lab pack of 25** FileVPS guests, not a public CDN benchmark.
- Production workers should be Linux hosts with user namespaces on (`CLOFIX_USER_NS=true`). Local Docker Desktop is for development.

### 4.4 What “fast” means for a real app

| Job | CloFix path | Why it feels fast |
|---|---|---|
| New Ubuntu VPS | Create FileVPS | No guest kernel boot |
| Scale a website | ALB + backup restore extra FileVPS | Overlay clone, not full image copy |
| Run a script | Jobs & Functions | Fresh overlay, no VPS to provision |
| Deploy WordPress / Node | Library Quick App | Sandbox, not a VM install |
| Kubernetes workload | CKS on dedicated workers | kubeadm nodes, EKS-style console |

---

## 5. Console map

Open the console search box. This is the live product map.

| Group | Pages |
|---|---|
| Overview | Dashboard — VPS/worker counts, CPU/RAM/disk/swap, guest and host network charts |
| Compute | FileVPS, Library, Jobs & Functions, **Docker**, **CKS** |
| Storage | Volumes, EFS, Object Storage (S3), Backups, Databases |
| Networking | Load Balancers, VPC, DNS Service, Health Checks, Firewall |
| Security | **WAF** (`/waf` engines + bindings), Security Center (score, audit, modes, Protection), Firewall, IAM, **Users** (partner / client / admin cards, create / edit / delete) |
| Platform | Log Service, Workers (admin), Billing & Payments, Settings |

IAM scopes match those resources. Admin is the CloFix owner; partners manage clients; root and IAM stay inside one client account.

---

## 6. Compute

### 6.1 FileVPS

Linux guests on enrolled workers.

- Default image: bundled **Ubuntu 24.04**. Same-image VPS share a read-only base; each VPS has its own OverlayFS writable layer.
- CPU, RAM, swap and process limits: **cgroup v2**. These are limits on shared host resources, not dedicated physical cores.
- Placement uses live free CPU/RAM/disk. The scheduler does not RAM-overcommit.
- Login: SSH key (PEM), PAM password, or both. Generate a PEM at create time and download it once.
- Private address on worker bridge **10.231.0.0/16**. Local compose publishes SSH `22000–26999`, HTTP `27000–31999` (guest 80 = SSH+5000), HTTPS `32000–36999` (guest 443 = SSH+10000), extra maps `37000–41999`, managed apps `42000–46999`, databases `47000–51999`.
- Details: CPU, RAM, swap, guest network IN/OUT, port mappings, services, **Security** (modes / quarantine); HTTP WAF via `/waf` bindings; backups, volumes, S3 mounts.
- Instances list shows **FileVPS** and **CKS** nodes together (Kind column). CKS rows open the cluster; they are not FileVPS guests.

**Protection Mode** (on by default): guest root is read-only. `/run`, `/tmp`, `/var/log` and APT cache are temporary and clear on restart. `apt update` can refresh indexes; installing packages needs Protection Mode off. Toggling it restarts a running VPS. It does not stop processes or network traffic, does **not** inspect HTTP payloads (use OWASP CRS WAF for SQLi/RCE/XSS), and is **not** a substitute for a hardware VM.

### 6.2 Library (Quick Apps + VPS software)

**Quick Apps** deploy a managed sandbox — no client VPS and no SSH box:

- Adminer, Jenkins
- ZIP web: static HTML, PHP, Node.js, Next.js, WordPress
- Optional attach to an existing managed database
- Open / login / start / stop / logs / data export / delete from the Library

Apps use CloFix namespaces and cgroup v2, UID 65534, read-only template, private net, persistent `/data`.

**VPS software** installs packages into a running FileVPS (Protection Mode off), including Java services such as WildFly/Tomcat where catalogued.

### 6.3 Jobs & Functions

Run Python, Go, Node, PHP, Java, Ruby, Bun or Deno **without creating a VPS**. Each run gets a fresh overlay, unprivileged UID, limits and a private network. Workers advertise which runtimes they have.

### 6.3b Docker Engine (host dockerd)

Docker is a separate Compute product (`/docker`). Containers run on the **worker-host Docker Engine** after admin-authorized install/detect. Tenants never get `docker.sock`. Deploy via image wizard, Compose (policy-rejected privileged/host-net/sock), Git build + webhook + digest rollback, or Docker Quick App templates (distinct from Library native Quick Apps). Host ports use **18000–21999** with conflict checks against FileVPS mappings. IAM: `docker:read` / `docker:manage`. License: optional `max_docker_*` quotas.

**Backup & Restore** (`/docker#docker-backups`): create a worker-local archive of a **managed Docker volume**, restore into that volume, **Redeploy** (restore then recreate containers/apps that mount it), or **Delete** the archive. Distinct from FileVPS `/backups` (writable root snapshots). See [docs/docker.md](docs/docker.md) for APIs and production limits (no live migration, no fake “secure” without a configured scanner).

### 6.4 CKS — CloFix Kubernetes Service (EKS-style)

CKS is the Kubernetes product in the console (`/kubernetes`, also `/cks`).

**What you can do**

| Area | Client capability |
|---|---|
| Clusters | Create, open/select, rename, delete protection, kubeconfig download; inventory stays visible even if the worker briefly misses a heartbeat |
| Cluster row actions | **Open**, **Resources**, **Kubeconfig**, **Edit** |
| VPS groups / node pools | Sized like FileVPS (CPU, RAM, disk) on **dedicated CKS workers**; optional pool **autoscaling** (min/max) |
| Workloads | Namespaces, pods, Deployments (scale/restart), StatefulSets (scale/restart), ReplicaSets, DaemonSets, Services, Ingress, Secrets, ConfigMaps, PVC/PV, Jobs, HPA, NetworkPolicy, ServiceAccounts, pod logs |
| Helm | Install / upgrade / uninstall |
| CloFix attach | Bind existing LB, FileVPS, volume, EFS, S3 into the cluster |
| Add-ons | Local StorageClass, nginx IngressClass, RBAC / HPA YAML; StorageClasses listed with add-ons |
| Ops | etcd backup/restore, rolling upgrade, HA 3 or 5 control planes; production-gate note on HA & backups |
| DNS | Automatic `*.cks.` names for the cluster, Services and Ingresses |
| Billing | CKS control-plane unit + node CPU/RAM/disk; attached LBs stay on the LB row |

**Hard limits (read before you buy Kubernetes)**

- FileVPS guests **cannot** run kubelet (shared kernel, seccomp, no cgroup delegation). `infrastructure_mode=filevps` is rejected.
- CKS nodes are **dedicated worker machines**, not FileVPS.
- The Kubernetes API stays on the worker. CloFix does **not** publish `6443` on the public internet.
- `production_ready` stays **false** until live kubeadm HA is opted in on a real kubeadm host. Local Compose uses a **lab executor** (`CLOFIX_KUBERNETES_FAKE=1`) so the console can be demonstrated; that is not production Kubernetes. Do not point the Compose worker at public DNS only (`8.8.8.8`) — that breaks Docker service discovery (`admin`) and stalls heartbeats / CKS readiness.
- No pod exec/terminal, no public kube-apiserver, Flannel not Cilium.

Think of CKS as **“EKS-shaped ops on CloFix workers”**, not “Kubernetes inside every cheap VPS”.

---

## 7. Storage (volumes, EFS, S3, backups, databases)

Pick the right product first. They look similar in the menu; they are not interchangeable.

| Use this | When | Lives where | Attach to |
|---|---|---|---|
| **Volume** | One VPS needs a durable disk (`/mnt/data`, database files, site that must not vanish on VPS delete) | One worker directory / optional capped ext4 | **Same worker only**, 1:1 |
| **EFS** | Many VPS (including other workers / LB members) share one web root | CloFix S3 prefix, bind-mounted on each worker | **Any worker**, many VPS |
| **S3** | Object files, backups of objects, mount a bucket as a filesystem | CloFix worker pool **or** AWS / GCS / Azure gateway / custom | Mount with **s3fs** into a VPS |
| **VPS backup** | Clone / recover a guest OS writable layer | Compressed archive on the **same** worker | Restore = **new** FileVPS, same worker |
| **Database service** | Managed MySQL / PostgreSQL / MongoDB / Redis — not a package inside FileVPS | Isolated project on a database-capable worker | Connect over TLS; optional DNS name |

Volume, EFS and S3 mounts **must not overlap** the same guest path. A running VPS **restarts** when you attach or remount. Protection Mode keeps the guest **root** read-only; a mounted volume/EFS/S3 path stays writable.

### 7.1 Volumes (`/volumes`)

Persistent worker-backed storage, independent of the VPS OverlayFS layer.

| Client action | What happens |
|---|---|
| Create | Name + worker. Optional **hard size limit (MB)** makes a capped ext4 image (loop + ext4 on the worker). Blank = unlimited on that worker’s disk. |
| Attach | Only a VPS on **that** worker. Default path `/mnt/data`. Running VPS restarts. |
| Detach | Data stays; you can reattach later. |
| Delete | Permanently removes worker data. |
| Edit | Display name only. Size cap is fixed at create. |
| Explorer | Browse, upload files/folders (4 MB per file), new folder/text file, download folder, move. Text edit up to 2 MB. |

If you mount an **empty** volume at `/var/www/html`, it hides the image’s web files. Nginx may return 403 until you upload a readable `index.html` in Volume Explorer.

**Benefits:** survives VPS delete if you detach first; writable under Protection Mode; optional hard cap so one guest cannot fill the worker.

**Limits:** not shared across workers; not in a VPS backup; delete is permanent.

### 7.2 EFS (`/efs`)

One **named share** many VPS can mount, including backends on **other workers**. Files live on **CloFix S3**; each worker mounts the prefix once and bind-mounts it into attached guests.

Typical pattern: load-balanced website. Attach the same share to every member at `/var/www/html`. Creating a load balancer does **not** create EFS automatically — pick an existing share on the LB form (EFS share + guest path). Changing the path remounts existing and new members.

| Client action | What happens |
|---|---|
| Create share | Name only. Uses CloFix S3 by default. Attachable on any worker. |
| Attach VPS | Any worker. Default `/var/www/html`. Must not overlap a volume or S3 mount. Running VPS restarts. |
| Rename | Display name; attachments and S3 prefix stay. |
| Delete | Detach every VPS first. The S3 **prefix and objects are not deleted**. |
| Explorer | Upload once; every attached VPS sees it. 4 MB per file. Folders, text files, drag-drop, download folder, move, edit text ≤ 4 MB. |

**Benefits:** one upload, every VPS/LB replica sees the files; works across workers; good shared web root.

**Limits:** not a POSIX NFS appliance; backed by S3 (see 7.3 replication). Explorer file size cap. LB does not invent a share for you.

### 7.3 Object Storage / S3 (`/storage/s3`)

Two kinds of connection:

1. **CloFix S3 (default shared pool)** — each enrolled Linux worker contributes `/var/lib/clofix-filevps/s3-volume`. UI shows online storage workers, combined backing disk, and free capacity (SeaweedFS keeps ~5% free).
2. **External S3** — Amazon S3, GCS S3 interoperability, Azure S3-compatible gateway, or any custom endpoint. Credentials are **encrypted at rest and never returned** to the browser.

| Client action | What happens |
|---|---|
| Bucket files | Browse, search, upload files/folders, download folder, create folder/text file, move, delete. Text edit ≤ 2 MB. |
| Mount in a VPS | **s3fs** on the worker (`/dev/fuse`). Default guest path `/var/www/html`. Running VPS restarts. System paths blocked. |
| View mounts | Unmount before deleting a connection. |

**Benefits:** object API + optional filesystem in the guest; mix CloFix pool and AWS/GCS; EFS shares sit on this pool.

**Limits (honest):**

- Pool placement `000` = **one copy**. A worker disk failure can lose objects on that worker.
- Capacity is backing filesystem size, not a reserved quota.
- Local Compose S3 gateway is **loopback-only**. Production needs TLS and private networking (master `9333`/`19333`, volume `8080`/`18080` — do not publish those cluster ports).
- Docker Desktop on macOS is not a production storage node.

### 7.4 VPS backups (`/backups`)

Worker-local snapshots of a **stopped** FileVPS **writable root** (OverlayFS upper). Restore is **Run backup as a new VPS** with its own SSH key, CPU, RAM, disk and swap.

| Included | Not included |
|---|---|
| Compressed writable-layer archive | Persistent **volumes** |
| Guest packages/files written on the overlay | **S3** and **EFS** mounts |
| Same-worker restore using the cached base image | Cross-worker restore |
| Image source for **load balancer** scale-out | Live/running-VPS snapshot |

Snapshot size in the UI is the **compressed archive**. Disk allocation on restore is **placement accounting**, not a guest disk quota. Create backups only while stopped.

**Benefits:** fast clone of a prepared app image; LB members restore from the same backup; new SSH key on restore.

**Limits:** not crash-consistent multi-disk backup; not off-site unless you copy the archive yourself; original worker required.

### 7.5 Database service (`/databases`)

Managed **instances** (projects), not a MySQL package inside FileVPS.

| Engine | Client notes |
|---|---|
| **MySQL** | Logical DBs, users/grants, TLS required, `mysqldump --single-transaction` (InnoDB). Trigger/routine/event backup not complete. |
| **PostgreSQL** | TLS only (unencrypted rejected). Custom-format `pg_dump`. |
| **MongoDB** | TLS on published listener. Native compressed archives; `fsyncLock` during backup. |
| **Redis** | TLS on published listener. Logical DBs are **key prefixes** in a shared instance. **Backup/restore is not implemented.** |

Console: instances, logical databases, users, **encrypted** backups (central + S3, resumable upload), schedules, live jobs. Create form: name, engine, worker, VPC, version, CPU, RAM, disk. Download instance **TLS CA**. Failed provision can be retried.

Runtime (operator-enabled workers only): dedicated project containers, private net, CPU/RAM/PID limits, preallocated ext4, encrypted admin credentials, ports **47000–51999**, max **128** connections per project. Customer cannot pick arbitrary images. Controller never gets a Docker socket.

**Backup/restore:** AES-256-GCM streams. Restore to an existing destination needs a fresh **safety backup** (< 10 minutes, matching checksum). Full replace / selected-table cutover can be destructive and is **not** atomic rollback — recover from the safety backup if cutover fails. Engine version prefixes must match. Redis has no backup.

**Benefits:** isolated engine, TLS, IAM, encrypted backups, schedules, no SSH into a DB VPS.

**Limits:** worker must be database-capable; Redis backup missing; public routing of DB ports still pending (use Firewall + published TLS port); stopped instance bills **storage only**.

---

## 8. Networking (VPC, DNS, load balancers)

**VPC.** Private IPv4 on the FileVPS bridge `10.231.0.0/16`. Default block cannot be deleted. Extra blocks are non-overlapping subnets. New VPS, apps and databases take the default VPC unless you pick another.

### 8.1 DNS Service (`/dns`)

Controller zone (default **`clofix.internal`**). This is CloFix DNS, not a public anycast network. The controller answers the zone on `127.0.0.1:53` (and HTTP names on `127.0.0.1:80` in lab). Download a **BIND-style zone file** to point your real nameservers at it. Optional **publish address** fills ALB records that have no allocated IP.

**Automatic records** (toggles on Zone settings): FileVPS, load balancers, **CKS clusters**, databases, managed apps, workers. Names look like `{id}.{service}.{zone}` plus CKS Service/Ingress names. CKS-attached LBs also get `{service}.{cluster}.cks.{zone}` aliases.

| Source | Example name |
|---|---|
| FileVPS | `{id}.vps.clofix.internal` |
| Load balancer | `{id}.lb.` / `{id}.alb.clofix.internal` |
| Database | `{id}.db.clofix.internal` |
| Managed app | `{id}.app.clofix.internal` |
| Worker | `{id}.worker.clofix.internal` |
| CKS cluster | `test-cluster.cks.clofix.internal` |
| CKS Service | `nginx.svc.test-cluster.cks.clofix.internal` |
| CKS Ingress | `nginx.ing.test-cluster.cks.clofix.internal` |

**Manual records:** target FileVPS, LB, **CKS cluster / Kubernetes Service / Ingress**, database, app, worker, or a manual address. Types A / AAAA / CNAME / TXT. **Port** is stored and exported as **SRV**. CKS Service dropdown lists the cluster plus Services and Ingresses; Port lists mapped HTTP, HTTPS, lab, NodePort and guest ports (not only 80).

**Benefits:** one zone for VPS, LB, CKS workloads and databases; no per-name `/etc/hosts` in lab if you add a resolver (`nameserver 127.0.0.1` for `clofix.internal`); export to production DNS.

**Limits:** controller does not replace Route 53 / Cloudflare globally unless you publish the zone. Port 53 in Compose is local.

### 8.2 Load balancers

- **ALB** — HTTP/HTTPS, path `/lb/<name>/` or a custom domain pointed at the controller
- **NLB** — TCP, unique IP from Settings → NLB pool
- Owns backend FileVPS restored from a **ready VPS backup**; scales by health, CPU, optional hits
- Optional **EFS share** auto-mounted on every member (default `/var/www/html`)
- Algorithms: round robin, least connections, least response time, IP hash, URI hash, random, power of two

### 8.3 Health checks and firewall

**Health checks.** HTTP / TCP / ICMP plus worker and LB availability. Alerts go to Settings channels.

**Firewall.** Inbound rules for FileVPS DNAT, managed-app gateways and database TLS ports. See chapter 9.

---

## 9. Security and VPS security

CloFix security has two layers clients should treat separately:

1. **Engine isolation** — what the FileVPS runtime always does (namespaces, seccomp, cgroup, isolated bridge).
2. **VPS controls** — what you turn on per guest in **Security Center**, **VPS Details**, and **Firewall**.

Open **Security Center** (`/security`) for the fleet hub: transparent global score, findings ledger, audit trail (`GET /api/audit`), alerts summary, Security Modes (dry-run / apply), Network Quarantine, detection/FIM, DB/CKS panels, AutoPilot/incidents/compliance export, plus Protection Mode / SSH / Delete tables. Manage HTTP WAF on **WAF** (`/waf`). Open **VPS Details → Security** for per-guest switches, mode cards and Quarantine.

### 9.0a Advanced Protection System (roadmap)

Security Center → **Advanced Protection** maps the full protection feature list (#1–72 + management controls) as `live`, `satisfied_by_ro`, `partial`, or `planned`.

- **Protection Mode** (RO guest root) stays orthogonal to **Security Modes** profiles.
- Features #2–5/#8/#13–14 are **satisfied_by_ro** when Protection Mode is on (critical paths under read-only root).
- **Temporary unlock** (`POST /api/security/unlock`), **scheduler tick**, **approvals**, **rollback**, **quarantine**, **SSH key-only**, and **volume delete protection** are live management APIs.
- Egress firewall, AppArmor/SELinux, S3 object-lock/versioning remain **planned** until engine support exists. Docker Desktop keeps `CLOFIX_USER_NS=false` (lab).

Catalog: `GET /api/security/protection-catalog`.

### 9.0 Security Center score (deterministic)

`GET /api/security/overview` starts at **100** and subtracts capped deltas from **live inventory only**:

| Finding | Delta | Cap |
|---|---|---|
| Protection Mode off (per VPS) | −4 | −20 |
| Delete Protection off (per VPS) | −1 | −8 |
| HTTP/HTTPS without OWASP WAF (per VPS) | −5 | −20 |
| Extra port forward without WAF | −3 | −12 |
| Inactive worker | −10 | (uncapped per worker) |
| Detectable `user_ns=false` lab posture | −15 | −15 |
| Open log alert-event (last 24h) | −8 | −24 |

Response shape: `{ score, findings:[{id,scope,asset,delta,reason}], posture, alerts, deep_links, … }`. Tenant/VPS/worker score blocks are included for drill-down. **Protection Mode** (read-only rootfs) stays orthogonal to **Security Modes** (policy templates with dry-run/apply for supported controls).

### 9.1 FileVPS isolation (always on)

FileVPS is **OS-level isolation**, not a hypervisor. Guests share the worker kernel. Isolation is Linux namespaces + cgroup v2 + OverlayFS + veth/nftables + seccomp.

| Layer | What the guest gets |
|---|---|
| Mount / PID / UTS / IPC / net / cgroup namespaces | Own filesystem view, process tree, hostname, IPC, network, hidden cgroup tree (`0::/`) |
| User namespace (production default `CLOFIX_USER_NS=true`) | Guest root maps to a unique host UID range (default base `100000`), not host root |
| Seccomp + `no_new_privs` | Denies `mount`, `umount2`, `unshare`, `pivot_root`, `bpf` and related syscalls |
| Dropped capabilities | No dangerous host-admin caps (`CAP_SYS_ADMIN` / `CAP_NET_ADMIN` stay dropped) |
| Devices | No `/dev/mem`, `/dev/kmem`, `/dev/port` or block devices; `mknod` of block devices denied |
| OverlayFS | Shared read-only Ubuntu base; each VPS has its own writable layer |
| Network | Private `10.231.0.0/16` veth; each veth is **bridge-isolated**; nftables default-drop forward |
| Resources | CPU, RAM, swap and `pids.max` enforced per VPS — one guest cannot starve the host unbounded |

**Measured isolation (1 Nov 2026 FileVPS audit):** sibling VPS files and processes are invisible; inter-VPS TCP blocked; `/proc`/`sys` sensitive paths not writable; `dmesg` not readable; host runtime dirs not visible; Protection Mode `/etc` is EROFS. Production workers should keep user namespaces **on**. Local Compose may run `CLOFIX_USER_NS=false` for Docker Desktop — that is lab, not the production posture.

**Not claimed:** per-guest AppArmor/SELinux profiles are not applied. A host **kernel** bug can affect every FileVPS on that worker. Patch the worker kernel and every guest kernel is patched. This is not KVM hardware isolation.

### 9.2 Per-VPS security controls

These live on **VPS Details → Security** (`#detail-security`) and on **Security → VPS controls**. SSH policy changes restart a running VPS.

| Control | Default | What it does | Client tip |
|---|---|---|---|
| **Protection Mode** | **On** for new VPS | Persistent guest root is **read-only**. `/run`, `/tmp`, `/var/log` and APT cache are temporary and clear on restart. `apt update` can refresh indexes. Installing packages or writing `/etc` needs it **off**. Toggling restarts a running VPS. Processes and network stay up. **Does not** inspect HTTP (SQLi/XSS/RCE need WAF). | Leave on for production websites. Turn off only to install packages / WAF, then turn back on. |
| **SSH Login Block** | Off | Blocks **new** SSH logins. Existing sessions may stay until disconnected. | Use after deploy, or during an incident. |
| **SSH Key-Only** | Off | Disallows password (PAM) SSH; public-key authentication only. | Prefer on any guest that still allows SSH. |
| **VPS Delete Protection** | Off | Blocks delete through CloFix APIs and worker operations. A host admin with disk access can still remove files. | Enable on important guests and CKS clusters. |
| **Network Quarantine** | Off | One action that turns **Protection Mode**, **SSH Login Block**, and **Delete Protection** on, then tightens inbound firewall for that VPS. | Use during an incident. Prefer Security Modes for day-to-day templates. |
| **SSH keys / PAM password** | Client-chosen | Paste a public key at create time, or generate a PEM (private key shown **once**). Password login uses PAM when enabled. | Prefer keys. Never reuse the one-time PEM after download. |
| **Custom port forwards** | SSH / HTTP / HTTPS mapped by CloFix | Extra guest TCP/UDP ports on unique host ports (`37000–41999`). Each forward **exposes** that service. | Review **Security → Port forwards**. Remove unused maps. WAF does not cover raw TCP. |
| **Inbound security group** | Unrestricted until you restrict | Who may use **already-mapped** ports (CIDR allow/deny). Mapping still publishes the port. Established/related traffic stays allowed. | Set this on Firewall → VPS, also linked from VPS Details → Networking. |

#### Security Modes (per VPS + fleet)

**Security Modes** are named templates that apply several live controls together (Enable / Disable on the VPS card; dry-run / apply on Security Center → Modes). Quarantine is a separate button — not a mode card. Database Protection lives under Databases, not here.

| Mode | Live controls applied | Notes |
|---|---|---|
| **Standard** | Protection on; SSH allowed; delete unlocked | Everyday baseline |
| **Hardened** | Protection on; Delete Protection on; SSH allowed | Firewall CIDR / WAF marked planned — bind WAF on `/waf` and set CIDRs yourself |
| **Lockdown** | Protection on; Delete Protection on; SSH Login Block on | Incident lock without full Network Quarantine |
| **Maintenance** | Protection **off**; SSH allowed | Change window for packages / WAF install |
| **Web Block** | Protection on | Reminds you to create a **WAF binding** (`/waf`) for HTTP SQLi/XSS/RCE |

Matching mode is highlighted on Details → Security. Modes do not replace Firewall CIDRs or `/waf` rule toggles.

**Security Center posture cards** tell you how many VPS have Protection Mode off, SSH blocked, delete protection, missing WAF bindings, and extra forwards — a daily hygiene check alongside the global score.

### 9.3 OWASP CRS WAF (edge HTTP firewall)

**Console:** **Security → WAF** (`/waf`). Each enrolled worker prepares a shared OWASP CRS engine once (CRS/ModSecurity image + `clofix-waf` network). You attach targets with **bindings** — no per-target engine reinstall and **no install inside the FileVPS guest**.

| Page | What you do |
|---|---|
| `/waf#waf-engines` | Worker engine readiness / errors |
| `/waf#waf-bindings` | Create, open, activate/deactivate, delete bindings (FileVPS, Docker app, Docker container) |
| `/waf/bindings/{id}` | Binding detail — Overview KPIs + charts, Traffic, Rules, Error pages |

**Binding detail** (`/waf/bindings/{id}`):

- **Overview** — hits, blocks, attacks, unique IPs (sparklines + traffic / IP charts). **Refresh stats** pulls ModSecurity audit from the edge (`waf-stats`) without jumping page scroll.
- **Traffic** — top IPs, top URIs, attack types, recent deny events.
- **Rules** — CRS category toggles for that binding (protocol/scanners, SQLi/XSS/RCE/LFI, languages, leakage, …).
- **Error pages** — custom HTML for 403 / 404 / 406 / 429 / 500 on the edge proxy.

Toggle groups in the console (same CRS coverage as before):

| Group | Covers |
|---|---|
| HTTP protocol attacks / enforcement | Protocol and RFC checks (CRS 920xxx) |
| Allowed HTTP methods | CRS 911xxx |
| Scanners & hacking tools | CRS 913xxx |
| IP reputation | CRS 910xxx (edge state; may reset when the proxy is recreated) |
| Layer 7 DoS | CRS 912xxx |
| Multipart / uploads | CRS 922xxx |
| LFI / RFI | Local and remote file inclusion |
| RCE | OS / command injection (shell, python, go, …) |
| Shell / PowerShell language payloads | Language-shell / PowerShell CRS tags |
| PHP injection | PHP attack rules |
| Node.js / JavaScript server | CRS 934xxx |
| JavaScript language payloads | language-javascript |
| Java application attacks | CRS 944xxx |
| ASP.NET language payloads | language-aspnet |
| Generic application attacks | Generic CRS attack tags |
| XSS | Cross-site scripting |
| SQLi | SQL injection |
| Session fixation | Session fixation |
| Response data leakage | Generic + SQL / Java / PHP / IIS leakage (CRS 951–954xxx) |

**FileVPS:** the worker gateway sits in front of guest HTTP/HTTPS. Legacy Library catalog `owasp-waf` (guest Nginx + ModSecurity) is **not** used for new edge bindings — uninstall a leftover guest install before activating edge WAF on that VPS.

**Docker:** bind an application or container; the edge proxy shares the app network namespace. Docker Security still links here for OWASP CRS; managed engine + bindings live on `/waf`.

**Scope (important):** the WAF inspects **HTTP/HTTPS through the bound edge path only**. It does **not** protect SSH, database ports, other guest ports, app authorization logic, or traffic that bypasses the gateway (including a custom port forward straight to the app). CRS can block legitimate requests — review after enabling groups.

**Protection Mode ≠ WAF.** Protection Mode locks the guest disk (read-only root). WAF inspects HTTP bodies/headers on the edge. Use both for a public website.

### 9.4 Firewall and published ports

**Firewall** (`/firewall`) is packet filtering for CloFix published ports. It is not the HTTP WAF.

| Scope | What it filters | What it does not |
|---|---|---|
| **Workers** | FileVPS DNAT, managed-app gateways, database TLS ports | Host SSH 22 and admin ports |
| **VPS** | Already-mapped guest ports (security group) | Unpublished ports; you must still map a port to expose it |

Rules: allow or deny, TCP / UDP / ICMP / any, optional port or range, **IPv4 CIDR**. Shortcuts: `0.0.0.0/0` or same VPC. Default allow keeps localhost/Compose working until you tick **Restrict inbound**.

**Recommended web VPS:** map only 80/443 (CloFix already maps them), restrict SSH to your office/VPN CIDR, leave Protection Mode on, create a **WAF binding** for that FileVPS and activate it.

### 9.5 IAM, Users and platform

| Control | What it does |
|---|---|
| **Account ID** | Root and IAM users of one client share one tenant. Other accounts cannot see the object. |
| **Users** (`/users`) | Partner / Client / Admin **count cards**, directory table, **Create**, **Edit**, **Delete**. Admin creates partners; admin or partner creates root clients. Guards block deleting your own signed-in account and unsafe last-admin cases. |
| **IAM** | Users, groups, policies, access keys, bearer tokens under one account. `security:manage` covers Protection Mode, SSH block / key-only, delete protection, WAF bindings/rules and port forwarding. Distinct from `instances`, `kubernetes`, `billing`, … |
| **Cookie session** | Console login; IAM keys for API |
| **Worker mTLS** | One-time enrollment token + controller CA. No enrollment secret in Compose files. |
| **Encrypted secrets** | Kubeconfig, CKS jobs, database admin passwords, backup snapshots |
| **Library catalog installs** | Fixed service IDs (Nginx, WAF, WordPress, …), not arbitrary shell as package input |
| **Jobs & Functions** | Unprivileged UID, fresh overlay, private net, no extra VPS |
| **Managed apps** | UID/GID 65534, read-only template, seccomp, private net, persistent `/data` only |
| **CKS API** | Kubernetes API stays on the worker — CloFix does **not** publish `6443` |

### 9.6 Recommended VPS security posture

1. Create FileVPS with an **SSH public key**, Protection Mode **on**.
2. Download the PEM once if generated; store it offline.
3. Install guest packages with Protection Mode **off** (or apply **Maintenance** mode), then **turn Protection back on** (or apply **Standard** / **Hardened** / **Web Block**).
4. If the VPS or Docker app serves HTTP, open **WAF → Bindings**, create a binding, activate it, then enable SQLi/XSS/RCE/LFI first; add language/tool groups after testing. Use the binding page for stats and custom error pages.
5. On **Firewall → VPS**, restrict SSH to known CIDRs. Do not leave extra port forwards.
6. Enable **Delete Protection** (or **Hardened**) on production guests.
7. Prefer **SSH Key-Only**; use **SSH Login Block** when nobody should log in (app-only VPS).
8. Prefer Library Quick Apps or managed databases when you do not need a full guest OS.
9. Review **Security** overview weekly: Protection Mode off, WAF missing, extra forwards. Use **Network Quarantine** only for incidents.

### 9.7 Honest limits

- Shared kernel: not a hardware VM, not nested KVM.
- No per-guest AppArmor/SELinux profile in the current engine.
- WAF ≠ firewall ≠ Protection Mode. Use all three for a public website.
- Host administrators with disk access can still read worker storage; Delete Protection is an API/UI lock.
- Compose/`user_ns=false` is weaker than production `user_ns=true`.
- Isolation audit was FileVPS-on-linuxkit, not a kernel exploit assessment.

---

## 10. Billing and plans

**Settings → Pricing & Plans** (admin): catalog plans, resource unit rates, discounts, gateway, currency/tax.

**Resource Pricing:** Standard and Low-cost monthly/hourly rates for CPU, RAM, SSD, **CKS**, load balancer, backup, volume, EFS, S3, applications. CKS is the control-plane unit; node CPU/RAM/disk use the CPU/RAM/SSD rows. Attached LBs bill on the Load balancer row. Zero or disabled means that unit is not billed yet.

**Billing & Payments:** invoices, wallets, manual payments, subscriptions, refunds, credits.

- **Live resources** — VPS, CKS clusters, LBs, apps, volumes, backups, S3, EFS with USD/BDT estimate
- **Due bill** — hourly meter every minute; a 5 or 10 minute run is billed for those minutes even if you delete the resource afterwards, until invoiced
- Stopped VPS/database: **storage only**
- Invoice PDF: CloFix brand, bill-to, period, lines, tax, USD and BDT
- Card charges are **not** processed in this stack; gateway secrets are stored and never returned by the API

License quotas (company license server) can cap VPS, CKS clusters/nodes/pools, Docker containers/apps/workers, LBs, DNS, backups. Unspecified CKS and Docker limits stay unlimited so existing plans keep working.

---

## 11. What CloFix is not

Say this to technical buyers up front.

- FileVPS is **not** a KVM/QEMU virtual machine and **not** nested hardware virtualization.
- FileVPS is **not** a Kubernetes node. CKS needs dedicated workers with kubeadm/containerd.
- Customer **Docker Engine** uses host dockerd on workers (admin opt-in). It is not Docker-inside-FileVPS and not the database DinD socket.
- Local Docker Compose is a **lab**. Fake CKS on Compose is for UI/API rehearsal, not production HA Kubernetes.
- Backups are **worker-local** writable layers, not crash-consistent multi-disk enterprise backup.
- S3 default is **one replica**.
- CloFix does not issue public TLS certificates for ALB custom domains.
- DNS in the controller is a **CloFix zone**, not a public anycast DNS network, unless you export the zone to your nameservers.
- Density and rps figures are from a **specific lab host**. Your production worker’s cores, disks and NICs set the real ceiling.
- FileVPS security is **namespaces / seccomp / cgroup / Protection Mode / Security Modes / WAF / firewall**, not a hardware hypervisor. There is no per-guest AppArmor/SELinux profile yet.

---

## 12. Getting started

1. Sign in to the CloFix Cloud console (admin bootstrap or **Register** for a root client).
2. Open **FileVPS → Create FileVPS**. Keep Protection Mode on unless you must install packages.
3. Save the PEM or password when shown — the private key is one-time.
4. Open **VPS Details → Security**, **WAF** (`/waf`), and **Firewall**: confirm Protection Mode, Security Mode (or Quarantine if needed), WAF binding + rule groups, SSH Key-Only / Login Block, SSH CIDR, and unused port forwards.
5. Open **Instances**. FileVPS and CKS nodes appear together; search `cks` or `eks` to filter Kubernetes nodes.
6. Add **storage** as needed: Volume (one VPS, same worker), EFS (shared web root / LB), S3 (objects + optional s3fs mount).
7. Stop a golden FileVPS and create a **backup** before you scale an ALB from that image.
8. Create a **database instance** (MySQL/PostgreSQL/MongoDB/Redis) on a database-capable worker; download the TLS CA.
9. Open **DNS**: confirm auto records for the VPS, LB, CKS Service/Ingress and database; add a hostname if you want a friendly name.
10. Put HTTP in front of FileVPS with a **load balancer** from a ready backup (attach EFS if members share a web root).
11. For Kubernetes, open **CKS**, create a cluster on a dedicated worker, then **Add VPS group** for nodes that run pods. Use Open / Resources / Kubeconfig / Edit on the cluster row.
12. Partners/admins: open **Users** to create or edit partner and root-client accounts.
13. Watch **Billing → Due bill** and **Live resources** so rates match what you enabled in Resource Pricing.

Operators enroll Linux workers with a one-time token and the controller CA. Clients never handle worker enrollment.

---

## 13. Glossary

| Term | Meaning |
|---|---|
| **FileVPS** | CloFix OS-level Linux guest (namespaces, cgroup v2, OverlayFS) |
| **Worker** | Enrolled Linux host that runs FileVPS, apps, DB, Docker Engine, CKS |
| **Docker Engine** | Host dockerd product for containers/Compose — not Library Quick Apps, not DB DinD |
| **CKS** | CloFix Kubernetes Service — EKS-style clusters on dedicated workers |
| **VPS group** | CKS node pool: kubelet nodes sized like FileVPS, not FileVPS guests |
| **Protection Mode** | Read-only guest root filesystem (default on); not an HTTP WAF |
| **SSH Login Block** | Blocks new SSH logins to that VPS |
| **SSH Key-Only** | Password SSH disabled; public key only |
| **Security Modes** | Templates (Standard, Hardened, Lockdown, Maintenance, Web Block) that apply Protection / SSH / delete together |
| **Network Quarantine** | Incident action: Protection + SSH block + delete lock + inbound restrict |
| **OWASP CRS WAF** | Shared worker edge (ModSecurity + CRS); bindings for FileVPS / Docker; stats + custom error pages on `/waf/bindings/{id}` |
| **Volume** | Worker-local disk, 1:1 attach to a FileVPS on that worker |
| **EFS** | Shared S3-backed filesystem; many VPS / workers / LB members |
| **S3** | Object storage (CloFix pool or external); optional s3fs mount |
| **VPS backup** | Stopped writable-layer snapshot; restore as a new VPS on the same worker |
| **Database service** | Managed MySQL, PostgreSQL, MongoDB, Redis with TLS |
| **DNS Service** | Controller zone (`clofix.internal`); auto + manual records, BIND export |
| **VPS security group** | CIDR allow/deny on already-mapped ports |
| **ALB / NLB** | HTTP load balancer / TCP load balancer |
| **Due bill** | Minute-level hourly usage waiting to be invoiced |
| **Account ID** | Tenant key shared by Root and IAM users of one client |

---

*CloFix Cloud client book. Performance table from the FileVPS bench/audit collection (1 Nov 2026). Hypervisor VM boot/RAM ranges are typical industry figures, not a same-host bake-off. CKS is not claimed production-ready HA Kubernetes until live kubeadm e2e is enabled on a real worker. Revised for edge WAF (`/waf` bindings, stats, error pages), Security Modes / Quarantine / SSH Key-Only, CKS manage-from-UI, Docker volume backup/restore, and Users cards (Nov 2026).*
