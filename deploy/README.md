# CloFix deploy (one component at a time)

Each folder has its own `.env` with **remote SSH** (`CLOFIX_REMOTE_IP`, `USER`, `PASSWORD` or SSH key).

| Step | Folder | What | Native (no Docker) | Docker |
| ---: | --- | --- | --- | --- |
| 1 | `license/` | Company license server | `install-db.sh` → `deploy-native.sh` | `./deploy.sh` |
| 2 | `admin/` | Customer controller | `install-db.sh` → `deploy-native.sh` | `./deploy.sh` |
| 3 | `worker/` | FileVPS worker + admin join | `./deploy.sh` (`.deb` + enroll) | - |

```text
deploy/
  _lib/ssh.sh
  license/
    .env.example
    install-db.sh      # Postgres install + role/db
    migrate-db.sh      # SQL migrate only
    deploy-native.sh   # binary + systemd (no Docker)
    deploy.sh          # Docker Compose path
  admin/
    .env.example
    install-db.sh
    migrate-db.sh
    deploy-native.sh
    deploy.sh          # Docker Compose (db + S3 + admin)
  worker/
    .env.example
    build-worker.sh
    deploy.sh          # push + join admin (calls push-join-worker.sh)
    install-join-worker.sh
    push-join-worker.sh
```

Password SSH needs `sshpass` on the machine running these scripts.

---

## Native path (recommended for bare-metal / VM)

### 1. License server

```sh
cp deploy/license/.env.example deploy/license/.env
# set CLOFIX_REMOTE_IP, CLOFIX_REMOTE_USER, CLOFIX_REMOTE_PASSWORD
# set LICENSE_ADMIN_USER, LICENSE_ADMIN_PASSWORD, LICENSE_DB_PASSWORD

./deploy/license/install-db.sh      # Postgres + migrate
# ./deploy/license/migrate-db.sh    # migrate-only later
./deploy/license/deploy-native.sh   # build + systemd clofix-license
```

Health: `http://LICENSE_IP:8090/healthz`

### 2. Admin controller (+ DB runtime + S3 pool)

```sh
cp deploy/admin/.env.example deploy/admin/.env
# set remote SSH + CLOFIX_ADMIN_EMAIL/PASSWORD + DB_PASSWORD + S3_ACCESS_KEY/SECRET
# set CLOFIX_LICENSE_SERVER_URL=http://LICENSE_IP:8090

# One-shot (default WITH_DB_RUNTIME=1 WITH_S3_POOL=1):
./deploy/admin/deploy-native.sh

# Or step-by-step:
./deploy/admin/deploy-db-runtime.sh   # Postgres install + migrate + systemd
./deploy/admin/deploy-s3pool.sh       # SeaweedFS master/filer/S3 systemd
./deploy/admin/migrate-db.sh          # schema-only later
./deploy/admin/deploy-native.sh       # controller binary (skip runtimes: WITH_DB_RUNTIME=0 WITH_S3_POOL=0)
```

- UI: `http://ADMIN_IP:8080/`
- Workers mTLS: `https://ADMIN_IP:8443/`
- S3 master: `ADMIN_IP:9333` · S3 API: `ADMIN_IP:8333`

Optional dedicated DB host: set `DB_REMOTE_IP` / `DB_REMOTE_USER` / `DB_REMOTE_PASSWORD` in the same `.env`, then point `DB_HOST` (admin) or `LICENSE_DB_HOST` at that IP.

### 3. Worker (join admin)

```sh
cp deploy/worker/.env.example deploy/worker/.env
# set CLOFIX_REMOTE_IP, USER, PASSWORD
# set CLOFIX_CONTROLLER=https://ADMIN_IP:8443
# set CLOFIX_ADMIN_HTTP=http://ADMIN_IP:8080
# set CLOFIX_TOKEN=… (Admin UI → Workers → Enroll)
# set CLOFIX_INSTALLATION_ID=ctl-… (or leave blank to fetch)
# set CLOFIX_S3_MASTER_ADDRESS=ADMIN_IP:9333   # if using S3 pool

cd deploy/worker
./build-worker.sh deb    # once / after code changes
./deploy.sh              # or: ./deploy.sh --build

# Optional: isolated dockerd for managed databases (like Compose database-runtime)
WITH_DATABASE_RUNTIME=1 ./deploy.sh
# or later:
./deploy-database-runtime.sh
```

---

## Docker path (unchanged)

```sh
./deploy/license/deploy.sh
./deploy/admin/deploy.sh
cd deploy/worker && ./build-worker.sh deb && ./push-join-worker.sh
```

---

## Notes

- Native admin runtimes: `deploy-db-runtime.sh` (Postgres) + `deploy-s3pool.sh` (SeaweedFS). Both run from `deploy-native.sh` by default.
- Worker managed-DB isolation: `deploy/worker/deploy-database-runtime.sh` (dedicated `dockerd` on `/var/run/clofix-docker/docker.sock`).
- Admin SQL migrations live in `admin/db/migrations/*.up.sql` (also under `deploy/admin/sql/`).  
  `migrate-db.sh` baselines old installs then applies new files (e.g. `0011_schema_parity.up.sql`).  
  Controller `EnsureSchema()` remains a boot safety net.
- License schema is `deploy/license/sql/001_license_state.sql` (idempotent; also auto-migrates on first start with `CLOFIX_LICENSE_DATABASE_URL`).
- Update an existing admin DB only:
  ```sh
  ./deploy/admin/migrate-db.sh
  ```
- Workers are bound to the controller Installation ID (`ctl-…`) at build/enroll time.
