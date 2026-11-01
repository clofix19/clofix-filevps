# CloFix company license server

Standalone CloFix-hosted plan validation. Copy and deploy **this directory only**. It has its own Go module, admin console, Docker image, Compose project, company token and data volume. Customer controller and worker source are not required.

Customer FileVPS stacks only call `POST /api/validate`. Plans, keys and installation binding stay on CloFix infrastructure.

FileVPS guests are OS-level isolation (namespaces, cgroup v2, OverlayFS). They are not KVM/QEMU VMs. Do not describe a license as a hypervisor seat count.

## Deploy

```sh
cp .env.example .env
# Set LICENSE_ADMIN_TOKEN to a unique random company secret (minimum 16 chars).
docker compose up -d --build
```

Open `http://localhost:8090/` on the company host. By default only loopback is published. Put a TLS reverse proxy in front of port 8090 for the real CloFix license hostname. Restrict `/` and `/api/plans` / `/api/licenses` to company operators. Allow customer controllers to POST `/api/validate`. This service does not listen on port 53 and does not issue certificates.

The company admin token is independent of every customer `ADMIN_TOKEN`. There is no token fallback. `.env` is ignored by Git and Docker build context. The data volume is `clofix-license-server-data` by default. Keep `LICENSE_DATA_VOLUME` stable and back it up.

For development outside Docker:

```sh
# Export CLOFIX_LICENSE_ADMIN_TOKEN and CLOFIX_LICENSE_DATA_DIR first.
GOWORK=off go run ./cmd/clofix-license
GOWORK=off go test ./...
```

The repository-root Go workspace may include this module for local tests. Building the customer controller does not import or package this server.

## Company workflow

1. Create a plan with the FileVPS entitlements the customer is allowed to allocate.
2. Issue a license: customer name, controller Installation ID (`ctl-…` from Settings → License), and that key’s duration in days. Copy the `CFL-` key, or use **Copy CFL key** later.
3. Give the customer the key and the company validation base URL. They enter both under Settings → License.
4. Renew, modify, revoke or logically delete keys from this console. Installation binding cannot be released or reassigned.

Changing plan limits applies on the customer’s next successful validation. Each key owns its duration. Existing expiry stays fixed until renewal. Revoking rejects future validations. A key cannot move to another installation; issue a replacement.

## Current FileVPS entitlements

Required on every plan:

| Field | Meaning |
| --- | --- |
| `max_vps` | FileVPS instances, including load-balancer-owned guests |
| `max_workers` | Enrolled workers (revoked workers do not consume a slot) |
| `max_load_balancers` | NLB + ALB records |
| `max_activations` | Always `1` (one controller Installation ID per key) |

Optional limits (`-1` unlimited, `0` not included, omitted unspecified):

| Field | Customer resource |
| --- | --- |
| `max_database_projects` | Managed database instances (MySQL, PostgreSQL, MongoDB, Redis) |
| `max_databases` | Logical databases / Redis key-prefix DBs |
| `max_database_users` | Database users |
| `max_database_schedules` | Database backup schedules |
| `max_volumes` | Persistent volumes |
| `max_storage_gb` | Controller allocation: VPS disk, volumes, backups (not a guest or S3 hard cap) |
| `max_s3_connections` | Object-storage connections |
| `max_backups` | Retained VPS + database backups |
| `max_applications` | Jobs & Functions |
| `max_managed_apps` | Library / ZIP managed apps (Uptime Kuma and Jenkins stay unready until the worker template builder publishes them) |
| `max_dns_zones` | Controller DNS Service zones (not a public authoritative nameserver) |
| `max_dns_records` | DNS Service records (auto + manual, including SRV ports) |
| `max_health_checks` | Custom health-check endpoints (not derived worker/LB checks) |
| `max_notification_channels` | Alert channels |
| `max_iam_users` / `max_iam_groups` / `max_iam_policies` / `max_access_keys` | IAM |

Customer **Billing & Payments** and **Settings → Pricing & Plans** are controller features. They are not issued or charged by this server.

Unspecified optional limits are not allocation permission on the customer controller, except DNS: plans created before DNS Service keep unspecified DNS as unlimited so existing controllers are not blocked. Set `0` to exclude DNS, or a positive/`-1` value to cap it.

Missing optional fields stay unspecified on old plans; they are not rewritten to zero.

## API / scope

Bearer-authenticated company APIs: `GET/POST /api/plans`, `PUT/DELETE /api/plans/{id}`, `GET/POST /api/licenses`, `PUT/DELETE /api/licenses/{id}`, `POST /api/licenses/{id}/renew`, `POST /api/licenses/{id}/revoke`, `DELETE /api/licenses/{id}/activations/{installation}` (always 409).

Customer validation: `POST /api/validate` with `key` and `installation_id`. No company token. Validation checks expiry, revocation and the bound Installation ID. Keys have 192 random bits. Validation uses SHA-256 hashes; recoverable copies use AES-GCM. List responses omit raw keys and ciphertext. Validation is limited to 60 requests per remote address per minute.

`GET /healthz` is used by Compose. State is `/data/licenses.json` (0600 in a 0700 directory). Run one replica; the JSON store is not a multi-writer database. This server does not process card charges or issue offline signatures.

## Management console

Sidebar: **License Plans**, **Issue License**, **Issued Licenses**. Summary cards count total, active, expired, revoked and deleted keys from the live inventory.

The plan dialog is grouped like the customer Admin UI: Compute, Databases, Storage, Networking, Security, Platform. Edit uses the same dialog. Plans referenced by issued licenses cannot be deleted; disable them instead.

Issued licenses support Copy/Restore CFL key, Modify, Renew, Revoke and logical Delete. Delete keeps history and created counts. Customer status updates on the next validation.

## Per-license duration and binding

Plans contain entitlements only. **Issue License → License duration (days)** is required (1–36500). Renewal uses that key’s duration. Modify can change duration and the explicit UTC expiry independently.

Copy Installation ID from the customer Settings → License when issuing. Binding is permanent. Unactivated legacy keys bind on first successful validation. Grant responses include the bound ID.

On first load after older upgrades, keys without duration copy the former plan duration (backup: `/data/licenses.json.before-key-duration`). Multi-activation keys migrate to the first Installation ID (backup: `/data/licenses.json.before-single-installation`).

## Copying keys later

**Issued Licenses → Copy CFL key** is an admin-authenticated POST (`Cache-Control: no-store`). It does not regenerate the key. Hash-only older keys use **Restore CFL key**. Back up `/data/licenses.json` and `/data/license-key-encryption.key` together. Losing the encryption file prevents recovery when ciphertext exists.

## Local preview against a customer Compose stack

The company project can run on the same workstation. Its UI is `http://localhost:8090/`. A customer controller in Docker Desktop can reach it at `http://host.docker.internal:8090`. That is a local preview address, not a production company URL.
