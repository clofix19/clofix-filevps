# Managed Quick Apps

The Library at `/services` has two sections: Quick Apps deploy persistent managed
sandboxes; VPS software installs packages into an existing VPS.

Quick Apps currently includes Adminer, VS Code Web (code-server), and Uptime Kuma.
The client chooses an app, names it, and presses Deploy. CloFix selects a prepared
worker, generates login credentials, creates persistent storage and publishes an
application URL. No client VPS or SSH account is created.

## Native lifecycle

Managed Apps use CloFix's own user/mount/PID/network namespaces, cgroup v2 limits
and seccomp engine. Applications run as UID/GID 65534, with a read-only template,
a private network namespace, a temporary root overlay and a separate persistent
`/data` filesystem. Docker is only used by the optional local development stack,
not as the managed application runtime.

Each app has a sparse 2 GiB ext4 data image mounted with `nosuid,nodev`. The size is
a hard filesystem limit; CPU and RAM are cgroup-enforced. Stopped apps retain data
and their gateway port. Deleting an app removes its persistent data after processes
and mounts are stopped. Worker/host restart reconciliation resumes apps whose
desired state is running; crashed apps retry at most once every 30 seconds.

The client controls Open app, Login, Start/Stop, Restart, Logs, Data export and
Delete from the Library. Login credentials are encrypted on the controller and
only their SHA-256 hash is kept in the worker's gateway metadata. Apps may still
have their own first-time setup, such as Uptime Kuma's administrator account or
Adminer's database login.

## Prepare worker templates

On a supported Linux worker, install `debootstrap`, then run:

```sh
sudo ./scripts/build-managed-templates.sh
```

This prepares Adminer from Ubuntu packages. It uses Nginx and PHP-FPM, not PHP's
development HTTP server. Templates are published atomically after preparation;
existing templates are preserved. Package daemons are prevented from starting
inside the provisioning chroot.

For code-server, obtain a verified Linux standalone `.tar.gz` for the worker's
architecture and its SHA-256 checksum:

```sh
sudo env CLOFIX_MANAGED_TEMPLATES=code-server \
  CLOFIX_CODE_SERVER_ARCHIVE=/path/to/code-server-linux-arm64.tar.gz \
  CLOFIX_CODE_SERVER_SHA256=YOUR_VERIFIED_SHA256 \
  ./scripts/build-managed-templates.sh
```

For Uptime Kuma, supply a compatible, prepared application directory containing
`server/server.js`, production `node_modules`, and built `dist/index.html`. Native
Node modules must match the worker architecture and guest OS. Also supply a
compatible Linux Node.js runtime archive and verified SHA-256:

```sh
sudo env CLOFIX_MANAGED_TEMPLATES=uptime-kuma \
  CLOFIX_UPTIME_KUMA_DIR=/path/to/prepared/uptime-kuma \
  CLOFIX_NODE_ARCHIVE=/path/to/node-linux-arm64.tar.xz \
  CLOFIX_NODE_SHA256=YOUR_VERIFIED_SHA256 \
  ./scripts/build-managed-templates.sh
```

Missing code-server/Uptime Kuma inputs cause those templates to be skipped, not
advertised as ready. Preparation happens once per worker, not per client deploy.
The default state location is `/var/lib/clofix-filevps`; `CLOFIX_STATE_DIR` must be
consistent between provisioning and the worker service. Templates live under
`managed-templates/`, while app data/metadata lives under `managed-apps/`.

Requires Linux user namespaces, cgroup v2, overlayfs, loop devices, ext4 tools,
`iproute2`, bridge isolation, `nftables` and root privileges for sandbox setup.

## URLs and network boundaries

Development gateways use ports 25000–25099, mapped only to `127.0.0.1` by local
Compose. A worker advertises its external hostname using `CLOFIX_APP_PUBLIC_HOST`;
the controller also supports that setting as a fallback, defaulting to `localhost`.

Gateways require the generated `client` username/password on every request. They
support HTTP and WebSocket proxying and strip gateway credentials before forwarding
to the app. Internal application port 8080 and management filesystem paths are not
exposed directly. There are at most 64 simultaneous gateway requests per app.

For production, configure an HTTPS ingress to the gateways and per-app domains.
Set `CLOFIX_APP_PUBLIC_SCHEME=https` on the controller only when TLS termination is
configured for the published addresses/ports. Automatic DNS, ACME certificates,
custom domain routing and direct Internet publication are not implemented.

Sandbox networking currently allows public IPv4 destinations and DNS through
1.1.1.1/8.8.8.8. Host access, RFC1918/link-local/metadata ranges and cross-sandbox
private traffic are blocked. IPv6 is disabled inside these application namespaces.
Adminer cannot currently connect to private databases, and Uptime Kuma cannot
monitor private network targets without an operator-designed connectivity policy.

## Data exports and logs

Stop an app before Data export. The authenticated gateway's
`/__clofix/backup` endpoint exports regular files in `/data` as ZIP. Export is
limited to 10000 files / 1 GiB; symlinks and special files are skipped. Start,
restart and delete operations are blocked while export is active. This exports
application files, not remote database contents or a full worker image. Import /
restore and scheduled backup retention are not implemented.

Worker logs rotate at 1 MiB per app; the latest 64 KiB is reported through worker
heartbeats. Logs update on the Library page while its log dialog is open.

## IAM and persistence

`managed-apps:read` grants inventory/template/log access for owned apps.
`managed-apps:manage` adds deploy, credentials, lifecycle and delete operations.
The root token can manage all apps. The controller persists metadata and encrypted
credentials in the same data store file/key used by Applications; workers persist
their own desired state and gateway password hashes on their state volume.

The first version supports 100 managed app records and up to 100 gateway slots per
worker. Apps remain pinned to the selected worker because their data is local.
Automatic cross-worker migration, horizontal scaling, billing and template updates
are not implemented.

Validation includes API authorization/credential tests and a native Linux smoke
test for gateway authentication, unprivileged UID, host-network blocking, daemon
state recovery, data persistence, stopped-data ZIP export and deletion. The smoke
test uses a small HTTP application; it does not validate every third-party app.
