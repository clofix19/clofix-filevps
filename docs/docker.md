# CloFix Docker Engine

Docker is a **separate Compute product** beside FileVPS, CloFix Library (native Quick Apps), Jobs & Functions, and managed databases. It runs containers on the **worker-host Docker Engine** after an administrator authorizes detect/install. Tenants never receive `docker.sock`. Database DinD used by the Compose lab stays unchanged and is not the customer Docker product.

## Console

Open **Compute → Docker** (`/docker`): Overview, Workers/Engine, Containers, Applications (image / Compose / Git / Quick App), Images, Networks, Volumes, Deployments, Registries, **Backup & Restore**, Security.

IAM scopes: `docker:read`, `docker:manage`. License optional quotas: `max_docker_containers`, `max_docker_applications`, `max_docker_workers` (omit = unlimited for existing plans; `0` excludes the product).

### Backup & Restore (`/docker#docker-backups`)

Worker-local **Docker volume** archives (not FileVPS `/backups`). From the panel:

| Action | Behavior |
|--------|----------|
| **Create backup** | Pick a managed volume → queues `volume-backup` → state `RUNNING` then `READY` / `FAILED` |
| **Restore** | Writes the `.tar.gz` back into the same volume (`volume-restore`) |
| **Redeploy** | Restore, then recreate containers/apps that mount that volume |
| **Delete** | Removes the archive on the worker and the backup record (`volume-backup-delete`) |

Archives live under the worker Docker product state dir (`…/docker-product/backups/<id>.tar.gz`). Restore does not copy volumes across workers.

## Ops channel

1. Controller queues a job in `CLOFIX_DATA_DIR/docker.json` and enqueues heartbeat action `docker-job` / `cancel-docker` with `DockerJobID`.
2. Worker fetches the operation over mTLS: `GET /v1/docker/jobs/{id}`.
3. Long pulls/builds run asynchronously so FileVPS serial work is not blocked.
4. Workers advertise `DockerReady`, `DockerVersion`, and disk usage on heartbeat; placement uses `scheduler.Place` and rejects when Docker is not ready or resources are over committed.

## Security defaults

- No host network, no privileged containers, no `docker.sock` mounts, no-new-privileges where applied by the worker runner
- Host publish ports only in **18000–21999** (below managed-app / DB bands)
- Compose YAML rejected if it requests privileged, host network, host PID, or docker.sock
- Registry passwords stored like other application secrets (not returned on list APIs); never logged
- Mutations audited via `Store.Audit`

## APIs (authenticated)

| Area | Routes |
|------|--------|
| Overview / workers | `GET /api/docker/overview`, `GET /api/docker/workers`, `POST /api/docker/workers/{id}/engine/{install\|start\|stop\|restart\|upgrade\|status}` |
| Containers | CRUD + `/start\|/stop\|/restart\|/pause\|/unpause`, logs |
| Images | list, pull, remove, prune |
| Applications | create (image/compose/git/quickapp), redeploy, rollback, delete; webhook `POST /hooks/docker/{id}` |
| Networks / volumes | create/list/delete (volume delete protection) |
| Deployments / metrics | job timeline, container metrics snapshot |
| Security | posture + optional Trivy scan |
| Backups | `GET/POST /api/docker/backups`, `POST /api/docker/backups/{id}/restore` (`redeploy?: bool`), `DELETE /api/docker/backups/{id}` |
| Registries | create/list/delete (password write-only) |

## Production limitations (honest)

- Shared kernel with the worker host — not a hypervisor VM
- No automatic cross-worker live migration of containers
- Zero-downtime requires multi-replica + routed front door; single-container restart is disruptive
- Vulnerability scanning requires a configured scanner binary (e.g. Trivy) on the worker; the API reports unavailable when missing
- Nested Docker / Docker-in-FileVPS is out of v1
- Engine install/upgrade is **admin-authorized** package management on Ubuntu/Debian workers only

## Coexistence

FileVPS, Library Quick Apps, Serverless jobs, CKS, and database DinD continue as today. Docker Quick App templates (Nginx, WordPress, Redis, Postgres, …) are **not** the Library native installers.
