# CloFix Cloud - Client Feature Guide

This document describes every major CloFix Cloud Console feature and its sub-features for customers (root accounts and IAM users). For each area you will find:

- **About** - what the feature is  
- **What you can do** - practical capabilities  
- **Benefits** - why it matters  

> **Note:** Some platform-only areas (Workers, License, pricing catalogs, NLB IP pool) are operated by the CloFix admin/provider. They are summarized at the end so you understand how capacity and billing work.

---

## Table of contents

1. [Getting started](#1-getting-started)
2. [Dashboard](#2-dashboard)
3. [FileVPS](#3-filevps)
4. [CloFix Library](#4-clofix-library)
5. [Jobs & Functions](#5-jobs--functions)
6. [Docker](#6-docker)
7. [WAF](#7-waf)
8. [CKS (CloFix Kubernetes Service)](#8-cks-clofix-kubernetes-service)
9. [Redis Cluster](#9-redis-cluster)
10. [Volumes](#10-volumes)
11. [EFS](#11-efs)
12. [Object Storage (S3)](#12-object-storage-s3)
13. [Backups](#13-backups)
14. [Managed Databases](#14-managed-databases)
15. [Load Balancers](#15-load-balancers)
16. [VPC](#16-vpc)
17. [DNS Service](#17-dns-service)
18. [Health Checks](#18-health-checks)
19. [Firewall](#19-firewall)
20. [Security Center](#20-security-center)
21. [Secrets Manager](#21-secrets-manager)
22. [Audit Trail](#22-audit-trail)
23. [IAM](#23-iam)
24. [My Profile](#24-my-profile)
25. [Log Service](#25-log-service)
26. [Billing & Payments](#26-billing--payments)
27. [Settings (client defaults)](#27-settings-client-defaults)
28. [Platform appendix (provider)](#28-platform-appendix-provider)

---

## 1. Getting started

### About

CloFix Cloud is a console to create and operate Linux VPS (FileVPS), containers, Kubernetes, databases, storage, networking, and security from one place—without logging into hypervisors or worker hosts.

### What you can do

- Register a **root** organization account (self-service)
- Sign in with password; complete MFA when enabled
- Use global **service search** (`/` shortcut or the search bar) to jump to any product—empty focus shows the full catalog; typing filters by name, group, and keywords
- Open the **hamburger service menu** for every console service, grouped by Overview, Compute, Storage, Networking, Security, Account, and Platform
- Switch themes; open Billing and Settings from the header
- See license/quota warnings when the provider’s entitlement blocks creates

### Benefits

- One login for compute, storage, networking, and security  
- Discover every product from search or the menu—not only a short “core” list  
- Faster onboarding than piecing together separate tools  
- Clear account identity (Account ID, role, email) on every page  

---

## 2. Dashboard

### About

The home page is your fleet overview: usage charts, VPS summary, security snapshot, shortcuts, and customizable widgets.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Shortcuts | Role-based jumps (create VPS, open Security, etc.) |
| Widgets | Add, hide, or reset home tiles |
| Recently visited | Quick return to services you used |
| Resource usage charts | CPU, RAM, disk, network over time |
| Compute / host summary | Counts and capacity at a glance |
| VPS fleet table | Running, stopped, failed instances |
| Security snapshot | Score and posture highlights |
| Get started | Guidance when the account is empty |

### What you can do

- Launch FileVPS from home  
- Track guest and fleet metrics (1h–30d or custom range)  
- Customize which widgets you see  
- Jump into Security Center, Health Checks, or FileVPS list  

### Benefits

- Situational awareness without opening every product page  
- Personalized layout for ops vs. developer workflows  
- Clear empty-state path for new accounts  

---

## 3. FileVPS

### About

**FileVPS** is CloFix’s managed Linux VPS. Guests run on enrolled worker hosts with cgroup limits, SSH access, optional Protection Mode, and deep links to networking, storage, and security.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Overview | Fleet usage boards and trends |
| Instances | List FileVPS (and related CKS nodes) |
| Create FileVPS | Image, CPU/RAM/disk/swap, worker, VPC, SSH key / password |
| PEM keys | Save and reuse SSH public keys; one-time private PEM download |
| Instance detail · Overview | Live status, metadata, charts |
| Instance detail · Resources | Resize CPU/RAM/disk/swap |
| Instance detail · Access | Root password, SSH helpers, credentials |
| Instance detail · Services | Library software installed on this VPS |
| Instance detail · Backups | Snapshot when stopped; restore as new VPS |
| Instance detail · Security | Protection Mode, SSH block, delete protection, quarantine, modes |
| Instance detail · Networking | Published ports, firewall rules, WAF / EFS links |

### What you can do

- Create Linux VPS with bundled **Ubuntu 22.04**, **Ubuntu 24.04**, or **CloFix Linux 1.0** (or a custom rootfs URL)  
- Login as **root** only - bundled images do not include the stock `ubuntu` cloud user  
- Start, stop, restart, delete (respecting delete protection); retry failed creates  
- Manage PEM keys; download a private key once when generated  
- Resize resources; set root password; open console helpers  
- Publish TCP/UDP ports; attach volumes/EFS; enable Protection Mode  
- Create backups of a **stopped** VPS; restore as a **new** instance  
- Apply security modes / quarantine from Security Center or detail  
- Reach managed databases over the **VPC gateway** (`10.231.0.1` + project port) with TLS  

### Benefits

- Full VPS lifecycle without hypervisor access  
- CloFix Linux is a branded Ubuntu-based guest (`ID_LIKE=ubuntu`) so Library apt packages still work  
- Built-in metrics, logs, and security on the same page  
- Private-path access to managed DBs without exposing engines on the public internet  
- Safe defaults (no privileged guest escape paths exposed to tenants)  

---

## 4. CloFix Library

### About

The Library deploys **managed apps** on workers and installs **VPS software packages** into existing FileVPS instances.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Quick Apps | Template and ZIP-based managed deploys (Adminer, Jenkins, Grafana, WordPress, Node/PHP/static, etc.) |
| Your managed apps | Track, open, uninstall running apps |
| VPS software | Install nginx, Apache, PHP, MySQL, Docker CLI, kubectl, Terraform, Ansible, Helm, and more **into** a FileVPS |
| Deploy / install dialogs | Choose worker or VPS, attach DB/VPC where supported, follow live logs |

### What you can do

- Deploy Quick Apps with optional managed database attach  
- Upload custom ZIP stacks (static / PHP / Node / Next / WordPress patterns)  
- Install curated packages onto a selected FileVPS  
- View live service and error logs from the console  

### Benefits

- Faster time-to-value than manual SSH setup  
- Separates “platform-managed apps” from “software inside my VPS”  
- Integrated status and logging for operations  

---

## 5. Jobs & Functions

### About

Run scripts, ZIP projects, or binaries as **Jobs** (one-shot / cron) or **Functions** (HTTP) without provisioning a full VPS.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Applications list | All jobs and functions |
| Create / edit | Runtime, entrypoint, env, secrets, RAM/CPU/timeout |
| Run history | Past executions and status |
| Manual run | Trigger with optional stdin |
| Versions | Keep and select code versions |
| Webhook | HTTP invoke with shared secret |
| Output / logs | stdout, stderr, downloadable artifacts |

### What you can do

- Create Jobs or Functions (Python, Go, Node, PHP, Java, Ruby, Bun, Deno, …)  
- Upload a single file or ZIP; set encrypted secrets (or reference **Secrets Manager**)  
- Schedule cron Jobs; invoke Functions over HTTP  
- Review history and download outputs  

### Benefits

- Lightweight automation without VPS overhead  
- Secrets stay encrypted on the controller  
- One place for batch jobs and HTTP endpoints  

---

## 6. Docker

### About

Deploy containers and Compose stacks on the worker Docker Engine. Tenants do **not** receive `docker.sock`. Prefer **Git repository** deploy with a saved Git registration (and optional private registry) over one-off raw runs.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Overview | Capacity and activity |
| Containers | Lifecycle (start/stop/pause/logs/delete) |
| Applications | Image / Git / Quick App deployments |
| Quick Apps | Template images (nginx, WordPress, Redis, Postgres, MySQL, …) |
| Compose | Multi-service YAML stacks |
| Images & Registry | Pull, prune, private registry credentials |
| Git registration | Saved tokens for private GitHub/GitLab/Bitbucket repos |
| Networks | Bridge / internal networks |
| Volumes | Named CloFix Docker volumes + quotas |
| Deployments | Job history |
| Monitoring | Engine snapshot and inventory |
| Backup & Restore | Volume archives for Compose / image apps |
| Security | Hardening defaults, image scan, WAF-eligible targets |
| Engine settings | *(admin)* install/start/stop host engine |

### What you can do

- Deploy via image, Compose YAML, or **Git** (select Git registration → Docker Compose or Dockerfile → optional Docker registry)  
- Manage containers, Compose projects, networks, and volumes  
- Connect private registries and Git registrations (add/edit/delete)  
- Inject secrets with `sec:name:KEY` / Secrets Manager refs  
- Wire managed DB connection env via **Databases → Connect to… → Docker**  
- Backup and restore CloFix Docker volumes  
- Scan images; review hardening; bind WAF for published ports  

### Benefits

- Production-style containers with platform guardrails  
- Private repo and private image workflows without shell on the host  
- Credentials and DB wiring without baking secrets into images  
- Billing-aware Docker / Compose / volume / backup quotas  

---

## 7. WAF

### About

Shared **OWASP CRS** (ModSecurity) engines on workers. You create **bindings** for FileVPS HTTP or Docker apps—no per-guest engine reinstall.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Engines | Worker readiness and errors |
| Bindings | Activate WAF for a Docker or VPS target |
| Binding detail · Overview | Status and target info |
| Binding detail · Traffic & attacks | Hits, blocks, client IPs |
| Binding detail · Rule groups | Toggle CRS categories |
| Binding detail · Custom error pages | Branded block pages |

### What you can do

- See which workers have a ready WAF engine  
- Bind WAF to FileVPS or Docker applications/containers  
- Inspect attack analytics; tune rule groups; customize error pages  

### Benefits

- Central edge protection without changing guest images  
- Selective activation per app  
- Visibility into blocked and allowed traffic  

---

## 8. CKS (CloFix Kubernetes Service)

### About

Managed Kubernetes on dedicated workers (not inside FileVPS guests), with console resource editing, Helm, storage/ingress add-ons, and CloFix attachments (LB, volume, EFS, S3).

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Clusters | Create, list, upgrade, kubeconfig download |
| Resources | Namespaces, pods, services, secrets, and more |
| CloFix attach | Wire LB, FileVPS, volume, EFS, S3 into the cluster |
| VPS groups / node pools | Scale worker groups for the cluster |
| Helm | Install and upgrade charts |
| Storage & ingress | CSI / IngressClass style add-ons |
| HA & backups | etcd backup and restore flows |
| Events | Cluster event stream |

### What you can do

- Create and manage clusters; download kubeconfig  
- Apply Kubernetes objects from the console  
- Attach CloFix load balancers and storage products  
- Sync vault secrets or managed DB credentials into a CKS Secret  
- Run Helm and etcd backup/restore  

### Benefits

- Kubernetes without self-managing every control-plane detail on guests  
- Native hooks to CloFix networking, storage, and Secrets Manager  
- Single console for cluster ops and platform integrations  

---

## 9. Redis Cluster

### About

Multi–FileVPS Redis topologies: **master–master** cluster or **master–slave** replication on the private VPC, with local disk or EFS-backed persistence.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Inventory & stats | Ready / in-progress / failed clusters |
| Create cluster | Node count, CPU/RAM/disk, topology, persistence |
| Cluster details | Connection and member status |

### What you can do

- Create 2–9 node clusters with chosen topology  
- Use temporary local disk or persistent EFS for data dirs  
- Monitor cluster health from the console  

### Benefits

- HA Redis without manual cluster scripting on each node  
- Optional shared persistence via EFS  
- Fits private VPC addressing with other CloFix services  

---

## 10. Volumes

### About

Worker-local persistent directories you attach **1:1** to a FileVPS on the same worker (e.g. `/var/www/html`).

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Inventory | List volumes, quotas, protection |
| Volume Explorer | Browse, upload, download, edit files in-browser |
| Create / attach / move | Lifecycle and guest mount path |
| Rename / delete | Manage with data-loss awareness |

### What you can do

- Create volumes (optional size quota); attach/detach at guest paths  
- Manage files from the browser (within UI size limits)  
- Keep data durable when VPS root is rebuilt or Protection Mode is on  

### Benefits

- Data survives guest OS changes  
- No SFTP required for common file tasks  
- Complements Protection Mode on read-only roots  

---

## 11. EFS

### About

Named **shared** file stores backed by CloFix object storage. Mount the same share on many FileVPS instances—ideal for load-balanced web roots.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Inventory | Shares and attachments |
| Explorer | Upload once; all members see the same tree |
| Create / attach / rename | Lifecycle and mount path (default `/var/www/html`) |

### What you can do

- Create shares; attach multiple VPS at a path  
- Upload content once for horizontal scale-out  
- Detach all members before delete (object prefix retained as documented in UI)  

### Benefits

- Shared content for NLB/ALB backends  
- Simpler than syncing per-VPS volumes  
- Works with Load Balancer EFS settings  

---

## 12. Object Storage (S3)

### About

S3-compatible buckets: CloFix shared worker pool plus optional external AWS / GCS / Azure / custom endpoints. Mount buckets into FileVPS with s3fs when needed.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Shared pool | Platform capacity and online storage workers |
| Bucket browser | Upload, download, folders, move, edit objects |
| External connections | Register third-party S3-compatible endpoints |
| Mount in VPS | s3fs mounts (e.g. website root) |
| Current mounts | List and unmount |

### What you can do

- Browse and manage objects in CloFix or external buckets  
- Register encrypted credentials (not echoed back to the browser)  
- Mount buckets into FileVPS paths  

### Benefits

- One console for internal and external object storage  
- Classic web stacks can treat objects as filesystems  
- Credentials stay on the controller  

---

## 13. Backups

### About

Worker-local compressed snapshots of a **stopped** FileVPS writable root. This is separate from volume, EFS, S3, database, and Docker volume backups.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Backups list | Inventory of snapshots |
| Scope | What is included / excluded |
| Restore as new VPS | Non-destructive restore into a new instance |

### What you can do

- Create a backup from instance detail when the VPS is stopped  
- List snapshots and restore as a **new** FileVPS with fresh resources/SSH  

### Benefits

- Point-in-time recovery for guest OS state  
- Restore does not overwrite the original instance  
- Clear boundary vs. other storage products  

---

## 14. Managed Databases

### About

Managed **MySQL**, **PostgreSQL**, and **MongoDB** instances (isolated engine containers on database-capable workers) with logical databases, users, TLS-required gateways, Explorer, Properties, Connect-to targets, backups, and schedules. For multi-VPS Redis topologies use **Redis Cluster** under Compute—not this page.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Instances | Create/manage engines; logical DBs & users; live CPU/RAM/disk/connections |
| Explorer | phpMyAdmin-style browse: tables, Structure, Browse rows, SQL + `.sql` upload |
| Structure CRUD | Create tables; add / edit / delete / update columns; indexes; drop table |
| Browse & SQL | Paginated rows, insert/edit/delete rows, run statements (writes confirm) |
| Properties · Connection | Public host:port, **VPC IP:port** (`10.231.0.1` + project port), URI, TLS CA |
| Properties · Performance | Max connections, buffer / work_mem; apply recreates engine, keeps data |
| Connect to… | Inject env into Secrets Manager, FileVPS, Docker, Applications, or CKS Secret |
| Backup Manager | Encrypted central/S3 backups, upload, staged restore |
| Schedules | Automatic backup schedules |
| Jobs | Live progress; inspect after interrupted backup |
| TLS CA | Download per-instance CA for `VERIFY_CA` clients |

### What you can do

- Provision instances (engine, version, worker, VPC, CPU/RAM/disk)  
- Create databases and users (`REQUIRE SSL` where the engine enforces it)  
- Explore schema and data without installing a separate admin UI  
- Tune performance settings from Properties  
- Copy connection URI; download CA; connect apps via **Connect to…**  
- From a FileVPS on the same VPC: `mysql -h 10.231.0.1 -P <DB_VPC_PORT> -u USER -p --ssl-mode=VERIFY_CA --ssl-ca=./ca.pem` (use capital **`-P`** for port)  
- Run and schedule backups; track jobs; recover after interrupted work  

### Benefits

- Database tier without DIY installation on a VPS  
- In-console Explorer replaces jumping to Adminer/phpMyAdmin for day-to-day work  
- VPC private path + mandatory TLS for safer app wiring  
- One-click credential injection into VPS, Docker, apps, and CKS  
- Integrated backup/schedule operations and Security Center DB posture  

---

## 15. Load Balancers

### About

**NLB** (TCP) and **ALB** (HTTP/HTTPS) frontends with min/max FileVPS replica pools and health-aware distribution.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| List & detail | Balancers, backends, public URL |
| Metrics | Hits, CPU, latency charts |
| Create / modify | Algorithm, scaling, domains/slug (ALB) |
| EFS association | Shared web root for scale-out members |
| Floating IPs | Provider-managed public IPv4 pool *(Settings → NLB)* |

### What you can do

- Create NLB or ALB; set scaling rules and listeners  
- Watch replica health and traffic  
- Attach EFS so new replicas share content  
- Copy the public URL; manage backend members  

### Benefits

- Built-in horizontal scaling of FileVPS  
- ALB HTTP metrics vs. NLB TCP passthrough  
- Designed to work with EFS and DNS  

---

## 16. VPC

### About

Private IPv4 segmentation on the FileVPS network (`10.231.0.0/16`), with a default block plus optional named subnets. Managed database gateways listen on the VPC bridge (`10.231.0.1`) in the **47000+** port band.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Overview | Default VPC and usage |
| IP blocks | Extra non-overlapping subnets |

### What you can do

- Review the default VPC  
- Create named subnets; edit/delete when unused  
- Select VPC when creating FileVPS, Library apps, databases, Redis  

### Benefits

- Logical isolation between app and data tiers  
- Predictable private addressing for VPS ↔ managed DB  
- Safe defaults with room for custom blocks  

---

## 17. DNS Service

### About

DNS zones and records for FileVPS, load balancers, CKS, databases, managed apps, and workers—plus TLS certificates (ACME).

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Overview | Zone health and summary |
| Zone settings | Defaults and auto-record behavior |
| Records | Manual A/AAAA/CNAME/TXT/… management |
| Certificates | Let’s Encrypt / ACME TLS |
| Zone file | View / export |

### What you can do

- Manage zones and records in one place  
- Align hostnames with CloFix resources  
- Request and renew TLS certificates from the console  

### Benefits

- Less manual DNS bookkeeping  
- HTTPS workflow next to DNS  
- Consistent naming across products  

---

## 18. Health Checks

### About

Automatic worker/LB health plus custom HTTP, TCP, or ICMP monitors with alerting through notification channels.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Current health | Built-in worker and LB checks |
| Endpoints | Custom monitors and thresholds |
| Alert history | Down / recovery events |
| Add / edit endpoint | HTTP, TCP, ICMP configuration |

### What you can do

- View automatic health (≈30s interval, multi-failure threshold)  
- Add custom endpoints with your own thresholds  
- Review alert history; wire channels in Settings  

### Benefits

- Proactive outage detection  
- Shared alerting path with Log Service  
- Grace handling for new load balancers  

---

## 19. Firewall

### About

CIDR allow rules for CloFix-published ports (security groups). Complements port mapping; does not replace Security Center policy modes.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Worker host firewall | Restrict access to host-published ports *(often via Workers)* |
| VPS security group | Per-instance inbound CIDR rules |
| Add rule | Protocol, port, source CIDR |

### What you can do

- Limit which source networks can reach exposed ports  
- Manage rules from Firewall page or instance Networking  
- Layer with WAF and Protection Mode  

### Benefits

- Defense in depth beyond “open port = open world”  
- Instance detail shows effective rules  
- Clear separation from application WAF rules  

---

## 20. Security Center

### About

Fleet security posture: live **score**, explainable **findings**, **Security Modes**, **Advanced Protection** catalog, detection/FIM, AutoPilot, and per-VPS controls.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Score | 0–100 posture with coverage stats |
| Findings | Ledger of score-impacting items |
| Security Modes | Policy templates (dry-run / apply) |
| Advanced Protection | Catalog unlock, quarantine, approvals |
| Detection & FIM | Runtime detections and file integrity |
| Vulns / DB / CKS panels | Product-specific posture |
| AutoPilot | Monitor → harden → emergency styles |
| Security audit panel | Security Center–scoped audit view |
| Guide | Control map with deep links |
| Per-VPS controls | Bulk Protection Mode, SSH block, delete protection |

### What you can do

- Track score and findings; apply modes to VPS sets  
- Unlock advanced protections with TTL; quarantine risky guests  
- Review detections, FIM baselines, and security audit events  
- Set AutoPilot mode; export compliance mapping where offered  
- Manage fleet port-forward inventory  

### Benefits

- Transparent scoring—not a black box  
- Maps controls to Firewall, WAF, Logs, and instance detail  
- Governance (approvals, audit, AutoPilot) for production fleets  

---

## 21. Secrets Manager

### About

Encrypted **vault** for application credentials. Store a secret once, then inject it into Jobs & Functions, Docker, FileVPS apps, managed database Connect flows, and CKS Kubernetes Secrets—without putting plaintext in images or git.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Vault | Create, list, reveal (on demand), copy, delete |
| Usage guide | FileVPS runtime fetch, Docker `sec:` / `{{secret:…}}` refs, CKS sync |
| Managed DB Connect | Write DB env (`DB_*`, `DATABASE_URL`, `DB_CA`, …) into a secret |
| IAM gates | `secrets:read` for runtime fetch; `secrets:manage` for console create/sync |

### What you can do

- Create named secrets; values encrypted at rest (list APIs never return plaintext)  
- Reveal/copy when needed; dismiss after use  
- Reference secrets from Jobs, Docker env, and Application env templates  
- Sync a secret into a CKS namespace as a Kubernetes Secret  
- From Databases → Properties → **Connect to… → Secrets Manager / FileVPS / Docker / Application / CKS**  

### Benefits

- One vault instead of scattered passwords in tickets and `.env` commits  
- Least-privilege IAM for automation vs. console operators  
- Same secret can feed VPS apps, containers, and Kubernetes  
- Complements managed DB Connect without storing the DB root password on the instance  

---

## 22. Audit Trail

### About

Account activity log for **who did what**: root and IAM users under your organization. Clients retain **90 days** (platform admin can view longer). Older rows are deleted automatically.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Activity table | Actor, action, target, result, time |
| Filters | Search, action pattern (e.g. `iam.*`), actor, date range |
| Detail | Expand a row for request context (JSON where available) |
| Scope | Your account’s root + IAM users (admin can filter across accounts) |

### What you can do

- Review login, IAM, instance, billing, and other mutations  
- Filter by action prefix or actor email/username  
- Export/review for compliance investigations alongside Security Center  

### Benefits

- Accountability without hunting worker syslog  
- Clear trail for team and API key activity  
- Complements Security Center’s security-scoped audit panel  

---

## 23. IAM

### About

Identity and Access Management for a **root** client account: sub-users, groups, policies, and API access keys. *(Hidden for partner logins.)*

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Users | IAM User, Read Only, Full Administrator roles |
| Groups | Bundle users for shared policy |
| Policies | Templates or granular permission checklists |
| Access keys | Up to two keys per user; one-time secret / bearer |

### What you can do

- Create IAM users and assign groups/policies  
- Generate API keys (e.g. bearer for Secrets Manager runtime fetch) without sharing the root password  
- Scope automation to least privilege (`secrets:read`, compute, billing, …)  

### Benefits

- Team and CI access without root credentials  
- Clear separation from provider **Users** (partners/clients directory)  
- Fits console + API workflows  

---

## 24. My Profile

### About

Self-service account hardening for any signed-in role.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Profile details | Name, username, email, password |
| Login IP rules | Allow / deny lists (deny wins) |
| Google Authenticator MFA | TOTP setup with QR |

### What you can do

- Update identity and password  
- Restrict login by IP  
- Enable or disable MFA  

### Benefits

- Stronger account security without support tickets  
- MFA and IP rules for compliance-minded teams  

---

## 25. Log Service

### About

Bounded live log tailing across VPS, LB, database, Library apps, workers, and WAF—with analysis and keyword/level alerts.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Sources | Auto-discovered and custom absolute paths |
| Live tail | Filter, pause, download (periodic refresh) |
| Analysis | Error / warn / keyword counts |
| Alerts | Threshold + cooldown → notification channels |
| Alert history | Past firings |

### What you can do

- Tail logs without SSH  
- Add custom source paths on resources  
- Define alerts to Slack, Teams, Discord, email, webhooks, etc.  

### Benefits

- Ops visibility from the console  
- Bridges to Security Center alert hub  
- Reuses Settings notification channels  

---

## 26. Billing & Payments

### About

Customer financial operations: live billable resources, due bill, invoices, wallet, payments, subscriptions, refunds/credits, and reports. Catalog unit prices are configured by the provider in Settings.

### Sub-features

| Sub-feature | Purpose |
|---|---|
| Overview | Financial summary |
| Live resources | Inventory with estimates (VPS, CKS, Redis, Docker, LB, DB, storage, …) |
| Due bill | Minute-granularity usage; generate invoice |
| Monthly analysis | Paid spend trends |
| Invoices | Issue and track |
| Wallet | Credits balance |
| Payments & transactions | Recorded payments |
| Subscriptions | Snapshot plan prices |
| Refunds & credits | Adjustments |
| Revenue reports | Summaries |

### What you can do

- See what is running and estimated cost  
- Review due bill and generate invoices  
- Track wallet, payments, subscriptions, and credits  

### Benefits

- Usage-aligned billing next to live inventory  
- Separates customer money (Billing) from catalog config (Settings)  
- Transparent minute-level metering for short-lived resources  

---

## 27. Settings (client defaults)

### About

Operators can save **browser defaults** for new FileVPS creations. Commercial catalogs, gateways, NLB pool, and license are provider/admin tools.

### Client-relevant sub-features

| Sub-feature | Purpose |
|---|---|
| General Settings | Default image, CPU, RAM, disk, swap, Protection Mode for new VPS |

### What you can do

- Save defaults applied to future creates in this browser  
- Reset defaults  

### Benefits

- Consistent VPS sizing for your team’s workflow  
- Optional Protection Mode on by default for safer new guests  

---

## 28. Platform appendix (provider)

These areas are typically **admin/provider** only. End customers consume the capacity they unlock.

### Workers

- Host capacity that runs FileVPS, Docker engine, WAF engines, databases, CKS  
- Enrollment tokens, drain, host charts, host firewall  
- Database-capable workers need a local database runtime + pinned engine images  

### License

- Controller ↔ CloFix license server  
- Status, plan, expiry, entitlements, live usage vs quotas, enforcement (create block / auto-stop after grace)  

### Pricing & commercial settings

- Product plans, resource unit prices (CPU/RAM/disk/CKS/Redis/Docker/LB/backup/volume/…)  
- Discounts, payment gateway credentials, currency/tax, billing notification flags  
- Notification channels used by Health Checks and Log Service  

### NLB network / Floating IPs

- Public IPv4 pool and listener helpers for floating IPs  

### Users (provider directory)

- Partner / client account directory (not the same as customer IAM)  

---

## Quick map: FileVPS and friends

| Need | Start here |
|---|---|
| Linux server + SSH | **FileVPS** |
| Install packages on a VPS | **CloFix Library → VPS software** |
| Managed web/app templates | **CloFix Library → Quick Apps** |
| Containers / Compose / private Git | **Docker** |
| Kubernetes | **CKS** |
| HA Redis across VPS | **Redis Cluster** |
| Disk attached to one VPS | **Volumes** |
| Shared web root across VPS | **EFS** |
| Buckets / external S3 | **Object Storage** |
| Guest OS snapshot | **Backups** |
| Managed MySQL / PostgreSQL / MongoDB | **Databases** |
| Browse / edit DB schema & rows | **Databases → Explorer** |
| Wire DB into VPS / Docker / CKS | **Databases → Properties → Connect to…** |
| Encrypted app credentials | **Secrets Manager** |
| Scale HTTP/TCP frontends | **Load Balancers** |
| Private addressing / DB VPC path | **VPC** |
| Hostnames & TLS | **DNS** |
| Uptime monitors | **Health Checks** |
| Port CIDR filters | **Firewall** |
| OWASP edge WAF | **WAF** |
| Fleet posture & modes | **Security Center** |
| Who did what | **Audit Trail** |
| Team / API access | **IAM** |
| Live logs & alerts | **Log Service** |
| Invoices & wallet | **Billing** |
| Find any service | **Search bar** or **☰ menu** |

---

*Document version: client feature guide for CloFix Cloud Console (updated for full service menu/search, Secrets Manager, Audit Trail, and Managed Databases Explorer / Properties / Connect / VPC TLS). Product names match the console navigation and service search.*
