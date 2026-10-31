# Managed database implementation status

This module is integrated with the controller and client dashboard. Database workers remain opt-in: a worker advertises capability only after an operator installs a trusted immutable image manifest and a local database runtime. Existing VPS and load-balancer workflows remain separate.

## Integration design

Reuse the enrolled worker certificate, capacity scheduler, principal/IAM ownership, license mutation gates, encrypted application-secret key, notification delivery and cron parser. Database jobs need a separate persistent state machine and authenticated worker transfer endpoints because database restores can run for hours and must not block heartbeats or serial VPS operations.

The implementation currently includes:

- Portable project/job/backup protocol models and allowlisted SQL schema generation in `worker/pkg/database`.
- Chunked AES-256-GCM backup streams, authenticated end markers, object-bound integrity and a seekable decrypting reader for future streaming/range endpoints.
- Upload filename, native signature, UTF-8 SQL, gzip checksum and expansion-limit validation. Native parsing still runs only in staging under an unprivileged database role.
- Linux worker adapters for MySQL, PostgreSQL and MongoDB: dedicated project containers, private networks, CPU/RAM/PID limits, preallocated ext4 volumes, encrypted administrator credentials, TLS certificates and native CLI administration.
- Database/user/table/collection/index operations and native encrypted backup/staged restore execution.
- Worker job journaling, cancellation and per-project serialization; interrupted work is fenced for inspection rather than replayed. Failed cutover keeps the application gateway paused.
- Controller metadata models with file/PostgreSQL snapshot persistence and migration `0006_managed_databases`, owner-scoped REST APIs, encrypted job payloads, mTLS transfers and durable dispatch.
- Central and S3-compatible encrypted storage, resumable uploads, range-capable downloads, schedules, retention and transfer retries.
- Database license quotas, IAM permissions, audit records, failure alerts and a dashboard for instances, users, schema, backups, restores and jobs.
- An isolated real-engine integration harness.

## Worker runtime boundary

Database runtime containers run on a Linux worker with its **own local Docker daemon**. The readiness check rejects a remote daemon with a different hostname. The customer controller never receives a Docker socket. The existing development worker does not automatically gain database capability.

An operator-managed `database-images.json` in the worker state directory maps `mysql`, `postgresql` and `mongodb` to `ref` (an immutable `repository@sha256:digest`) and `version` (the supported version prefix). Customer input cannot choose arbitrary images. Native server versions are checked against the manifest. Official Debian-based images with database UID/GID 999 are assumed; operator packaging must validate this before enabling an image.

On a production Linux worker with a local Docker daemon, prepare and pin the supported official images, then restart the worker:

```sh
sudo scripts/prepare-database-images.sh /var/lib/clofix-filevps
sudo systemctl restart clofix-worker
```

The preparation script rejects remote daemons, unresolved tags and images whose database account is not UID 999.

Worker `databases/credentials.key` is required to decrypt persistent administrative passwords and TLS private keys. Back it up with project metadata. A missing key with existing metadata fails closed. No root password is returned in job results.

Database ports are reserved in the 26000–26099 range. The passthrough gateway bounds connections to 128 per project and closes application connections during cutover. PostgreSQL explicitly rejects unencrypted connections; MySQL requires secure transport; MongoDB requires TLS. Public routing/firewall integration is still pending.

## Backup and restore constraints

MySQL uses `mysqldump --single-transaction` and requires InnoDB tables. Concurrent schema changes must be prevented during a backup. Trigger/routine/event backup coverage is not complete. PostgreSQL uses custom-format `pg_dump`; MongoDB uses compressed native archives and pauses writes with `fsyncLock` during backup. Durable recovery of a MongoDB write lock after worker failure remains required before deployment.

All encrypted content, checksums, format and decompression limits are checked before staging is created. Restore roles have access only to staging, with narrowly expanded destination rights for selected PostgreSQL restore. PostgreSQL client meta-commands are conservatively filtered for uploaded SQL; uploaded SQL is never passed to a shell. This filter can reject otherwise valid dumps with unsupported escapes.

Existing destinations require a completed safety-backup receipt less than ten minutes old and a matching encrypted-file checksum. Full MySQL/MongoDB replacement and selected-table cutover can be destructive and are not atomic rollback operations. A failure after cutover begins needs recovery from the retained safety backup. The controller chains safety backups before restore, protects them during the workflow and fences interrupted cutovers for inspection.

Exact engine version prefixes must match during restore. Tested upgrade and physical-backup adapters are not implemented. MongoDB BSON-directory archives are not yet supported.

## Running isolated integration tests

Requirements: Go, Docker Engine/Desktop, internet access for official engine images, and enough space for images plus a 2 GiB preallocated database disk. Tests run engines sequentially.

```sh
scripts/database-integration/run.sh
```

The harness builds a Linux test binary, creates a dedicated Docker-in-Docker test daemon, runs real database operations, then removes only its own test container and anonymous volumes. It does not mount the host socket, mount customer data, expose ports, or start root Compose services. The privileged test container is needed for nested namespaces and loop-mounted ext4 quota disks.

Portable security tests:

```sh
go test -race ./worker/pkg/database ./admin/internal/database
GOOS=linux GOARCH=amd64 go build ./worker/...
```

## Validation so far

Real MySQL 8.4, PostgreSQL 17 and MongoDB 8.0 integration runs exercise provisioning, TLS configuration, multiple databases, users with read/write grants, revocation, read-only write rejection, schema/index creation, native encrypted backup, restoration to a new database, and rejection of overwrite without a safety backup. Portable tests cover encrypted range reads, truncation/tampering, gzip expansion, invalid formats and state persistence. These are not the complete failure/concurrency/end-to-end suite requested for production.

## Operational limitations

1. MySQL full logical backups currently require InnoDB. Trigger, routine and event coverage needs a separate adapter before those objects can be promised.
2. Full MySQL/MongoDB replacement and selected-object cutover are controlled but non-atomic. A failed cutover is fenced and needs recovery from the retained safety backup.
3. MongoDB BSON-directory tar uploads and physical backup adapters are not included; native archive uploads are supported.
4. Database image preparation remains an explicit Linux worker operation. The controller never installs arbitrary customer-selected images or receives a Docker socket.
5. Complete disaster, storage-exhaustion and long-running concurrency qualification on the target production Linux distribution before enabling database images for customers.
