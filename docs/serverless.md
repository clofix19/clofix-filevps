# CloFix Applications: Jobs and HTTP Functions

CloFix runs applications with its own Linux namespace/cgroup/seccomp engine.
No Docker runtime or VPS instance is created for an application. Every invocation
uses a fresh overlay filesystem, an unprivileged UID, a read-only project, resource
limits and a private network namespace with no external connectivity.

## Runtime support

| Runtime | Upload / entrypoint |
| --- | --- |
| Python | Python script, or ZIP with `main.py` and project modules |
| Go | Static Linux executable built for the worker architecture |
| Node.js | JavaScript file, or ZIP with an entry such as `index.js` |
| PHP | PHP CLI script |
| Java | Executable JAR, or ZIP containing the JAR |
| Ruby | Ruby script |
| Bun | JavaScript / TypeScript project |
| Deno | JavaScript / TypeScript, with local/vendored imports |

The page shows ready worker counts per runtime. Creating an application does not
install its runtime on a worker. Missing runtimes are not advertised as ready.

## Provision a Linux worker

Deploy the updated controller and worker binaries. Install `debootstrap` on the
worker host, then run from the repository as root:

```sh
sudo ./scripts/build-function-runtimes.sh
```

This prepares Python, Go, Node.js, PHP, Java and Ruby Ubuntu runtime roots under
`${CLOFIX_STATE_DIR:-/var/lib/clofix-filevps}/runtimes`. Existing runtime directories
are preserved. To provision selected runtimes:

```sh
sudo env CLOFIX_RUNTIMES='nodejs php java ruby' ./scripts/build-function-runtimes.sh
```

For Bun and Deno, first obtain and verify Linux executables for the worker's CPU
architecture from their official distributions. Supply the extracted binaries:

```sh
sudo env CLOFIX_RUNTIMES='bun deno' \
  CLOFIX_BUN_BIN=/path/to/verified/linux/bun \
  CLOFIX_DENO_BIN=/path/to/verified/linux/deno \
  ./scripts/build-function-runtimes.sh
```

The script skips Bun/Deno when their binaries are not supplied. `CLOFIX_RUNTIME_DIR`
overrides the roots directory; set it consistently in provisioning and the worker
service environment. Keep all runtime roots root-owned and patch/rebuild them as
part of host maintenance. Execution scratch directories are on the worker state
filesystem, which must support overlayfs. Linux user namespaces, cgroup v2 and root
mount privileges are required. The local Compose stack is for development; Docker
is not used as the application execution runtime.

## Uploads, arguments and dependencies

The upload limit is 4 MiB. ZIP projects allow up to 4096 entries and 32 MiB expanded
content. Entrypoints are relative to the ZIP root, such as `src/main.py`; there is no
automatic removal of a top-level project folder. Symlinks, special files, traversal
paths, duplicate entries and reserved `.clofix*` root paths are rejected.

Single-file uploads may leave Entry blank (`main` is used internally). Arguments
are a JSON array of strings and are passed directly, without shell interpolation.
Use a static Go executable matching the worker architecture:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o application ./cmd/application
```

**Dependencies are offline and bundled with the upload.** No automatic downloads
from PyPI/npm or arbitrary network access are enabled:

- Python: include `requirements.txt` and a `wheels/` directory containing compatible
  wheels. Enable the dependency option. CloFix installs with `--no-index`,
  `--only-binary=:all:` and `--find-links=/app/wheels` into `/tmp/deps`. Include all
  transitive dependencies. Local project imports are available through `/app`.
- Node.js: include `package.json`, `package-lock.json` and the npm cache in `.npm/`.
  Enable the dependency option. A writable project copy in `/tmp/project` is used
  with a writable cache and `npm ci --offline --ignore-scripts`; lifecycle scripts are disabled. Native
  dependencies must already be compatible with the worker runtime.
- Other runtimes: bundle dependencies, such as `node_modules`, local Deno vendored
  modules, PHP `vendor/`, or a self-contained JAR. The install checkbox is restricted
  to Python/Node.js ZIPs. Go artifacts are built before upload.

Prepare wheels or an npm cache on a compatible trusted build environment before
zipping the project. Runtime setup and dependency preparation are separate steps.
Dependencies must also fit in the invocation's RAM and temporary filesystem limits.

## Environment variables and secrets

Environment and Secrets are separate JSON objects with string values. Secrets are
AES-GCM encrypted at rest, omitted from API responses, and decrypted only for the
worker invocation. Blank Secrets during an edit preserves the current secrets;
enter `{}` to clear them. The encryption key is stored in
`CLOFIX_DATA_DIR/application-secrets.key` with mode 0600. Back up that key together
with application metadata. Old versions retain encrypted snapshots of secrets.

At most 64 variables/secrets are allowed, with values up to 4096 bytes. `PATH`,
`HOME`, `LD_*` and `CLOFIX_*` are reserved. Literal secret/environment values are
redacted from captured stdout/stderr as a best-effort measure; transformed or
encoded values cannot be reliably redacted. Code given a secret can deliberately
export it, including in output files. Only give applications the secrets they need.

Input arrives on stdin; write the response to stdout. `OUTPUT_DIR=/output` is a
writable, bounded directory for output files. The application filesystem is read
only; temporary work goes in `/tmp`.

## Jobs and HTTP Functions

Run Jobs from the UI or through:

```http
POST /api/applications/APP_ID/run
Authorization: Bearer YOUR_TOKEN
Content-Type: application/json

{"input":"{\"name\":\"CloFix\"}"}
```

Invoke a Function through:

```http
POST /api/functions/APP_ID/invoke
Authorization: Bearer YOUR_TOKEN
Content-Type: application/json

{"input":"{\"name\":\"CloFix\"}"}
```

A Function is a request-triggered process, not a persistent HTTP server. The
endpoint waits up to 25 seconds and returns the run record, including stdout/stderr.
Longer invocations return 202 and can be followed using
`GET /api/application-runs/RUN_ID`. Failed completed invocations return 502.
Closing the HTTP request does not cancel the process. **Stop** sends cancellation
through the worker heartbeat.

## Live logs and output files

Live stdout/stderr snapshots arrive through worker heartbeats; UI dialogs poll
these snapshots. This is not a direct continuous streaming connection. Each stream
is bounded at 128 KiB and reports truncation. Completed logs are persisted with the
run. **Download logs** exports a JSON run record.

Write regular files under `/output` to export them. After process cleanup CloFix
collects at most 128 files / 2 MiB into a ZIP. Output symlinks and special files are
rejected. The writable output mount is a separate 4 MiB tmpfs. Use **Download output
files**, or `GET /api/application-runs/RUN_ID/output` with bearer authentication.
ZIPs are stored under `CLOFIX_DATA_DIR/application-output/`, outside metadata JSON.
Deleting a completed run also deletes its output ZIP.

## Schedules and webhooks

Jobs support five-field **UTC** cron: `minute hour day-of-month month weekday`.
Examples: `*/15 * * * *`, `0 2 * * *`, `0 9 * * 1-5`. Fields support `*`, numbers,
ranges, comma lists and `/step`. Weekdays use 0–6, Sunday=0. Restricted day-of-month
and weekday fields use OR semantics. Leave Schedule blank to disable it.

The controller checks every ten seconds, persists the last handled minute and
attempts one run per matching minute. Missed schedules are not backfilled. Lack of
capacity or revoked owner permission is recorded as a schedule error; no automatic
same-minute retry occurs. There is one scheduler per controller process.

Choose **Webhook** to create/rotate a one-time-displayed secret. Send:

```http
POST /hooks/applications/APP_ID
X-CloFix-Webhook-Key: YOUR_WEBHOOK_SECRET
Content-Type: application/json

{"event":"deploy"}
```

The raw body becomes stdin and execution is queued with 202. Both Jobs and Functions
can have webhook triggers. Rotation invalidates the old key; Disable removes access.
Only the key hash is stored. The owner must remain active and retain
`applications:manage` permission. These are generic header-authenticated webhooks,
not GitHub-specific HMAC signature verification or URL-fetching triggers.

## Versions and rollback

**Edit** creates a new numbered version with the execution configuration, artifact
and encrypted secret snapshot. An unchanged upload can be reused by leaving File
blank. **Versions → Restore** selects a previous version for future runs; already
queued/running invocations retain their original version. Numbers are never reused
after rollback. Up to ten versions are retained per application. Name, job/function
type, schedule and webhook settings belong to the application, not to a version.

## Limits and persistence

CPU: 0.1–2 cores; RAM: 32–2048 MiB; timeout: 1–120 seconds; stdin: 64 KiB.
Temporary storage is memory-backed and counts toward memory limits. Queued work
expires after five minutes; stale runs are marked expired after eight minutes.
Only one outstanding serverless execution is placed on a worker at a time and
admission is rechecked locally.

Applications, versions and completed logs are atomically persisted in
`CLOFIX_DATA_DIR/serverless.json`. Limits are 100 applications and 1000 retained run
records; completed records can be deleted. Active runs become `INTERRUPTED` after
controller restart and are not automatically rerun. This remains a single-controller
implementation, not a distributed durable queue. Storage is controller-local.

IAM uses `applications:read` / `applications:manage`, scoped to the creating
principal; the root token can manage all applications. Function invocation requires
manage permission; anonymous invocation requires an enabled webhook credential.

Validation includes Linux amd64/arm64 cross-builds, controller/API tests, ZIP and
output safety tests, and a native Linux Go smoke test covering UID isolation,
secret redaction, output collection and timeout enforcement. Python dependency
installation and the additional language runtimes still require end-to-end checks
on provisioned roots; the Go smoke test does not validate those runtimes.
