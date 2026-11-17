# CloFix deploy (one component at a time)

Deploy in this order — each folder has its own `.env` and remote SSH settings.

| Step | Folder | What | Command |
| ---: | --- | --- | --- |
| 1 | `license/` | Company license server | `./deploy/license/deploy.sh` |
| 2 | `admin/` | Customer controller + DB + S3 master | `./deploy/admin/deploy.sh` |
| 3 | `worker/` | Linux FileVPS worker (`.deb` + enroll) | `./build-worker.sh deb` then `./push-join-worker.sh` |

```text
deploy/
  license/   .env  deploy.sh
  admin/     .env  deploy.sh  docker-compose.yml
  worker/    .env  build-worker.sh  push-join-worker.sh  install-join-worker.sh
  _lib/      ssh.sh
```

## 1. License

```sh
cp deploy/license/.env.example deploy/license/.env
# set CLOFIX_REMOTE_IP, USER, PASSWORD, LICENSE_ADMIN_USER, LICENSE_ADMIN_PASSWORD
./deploy/license/deploy.sh
```

## 2. Admin

```sh
cp deploy/admin/.env.example deploy/admin/.env
# set remote SSH + DB_PASSWORD + admin email/password
# set CLOFIX_LICENSE_SERVER_URL=http://LICENSE_IP:8090
./deploy/admin/deploy.sh
```

UI: `http://ADMIN_IP:8080/` · workers mTLS: `https://ADMIN_IP:8443/`

For remote workers, prefer advertising S3 as `ADMIN_IP:9333` in worker `.env` (`CLOFIX_S3_MASTER_ADDRESS`). Inside the admin Compose network the controller can keep `s3pool:9333`.

## 3. Worker

```sh
cp deploy/worker/.env.example deploy/worker/.env
# set remote SSH + CLOFIX_CONTROLLER + CLOFIX_ADMIN_HTTP + CLOFIX_TOKEN
# set CLOFIX_INSTALLATION_ID=ctl-…  (Admin → Settings → License; or leave blank to fetch via CLOFIX_ADMIN_HTTP)
# set CLOFIX_S3_MASTER_ADDRESS=ADMIN_IP:9333
cd deploy/worker
./build-worker.sh deb
./push-join-worker.sh
```

Create the token in Admin UI → **Workers → Enroll worker** before push.

Workers are **bound to the controller Installation ID** at build/enroll time. A worker built for another admin (`ctl-…`) is refused on enroll and heartbeat.

Password SSH needs `sshpass`. Each remote host can be different (fill that folder’s `.env`).

## Redis Cluster

Private multi-VPS Redis (`/redis-services`) needs **both**:

1. **Admin** — `./deploy/admin/deploy.sh` rebuilds the controller Go API (`/api/cluster-services`). UI files are volume-mounted from `admin/web`.
2. **Worker** — `./deploy/worker/build-worker.sh deb` then `./push-join-worker.sh` so the agent includes `configure-cluster-node` / `join-cluster`. Install enables host `ip_forward` + bridge helpers for same-VPC peer traffic.

Redis itself is installed **inside** member VPS guests (not on the worker host). Endpoints are VPC-private only (`10.231.x.x:6379`); do not publish 6379 to the internet.

After upgrading Redis Cluster code: redeploy admin, rebuild/push the worker `.deb`, then create or recreate clusters from the UI.
