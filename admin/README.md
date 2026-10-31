# admin
Controller API + Admin UI (spec §2, §3, §10, §11).
- internal/api, auth (roles, MFA, sessions)
- internal/enrollment : one-time token, hash-only storage, atomic consume
- internal/licensing  : signed entitlements, graceful offline behavior
- internal/objectstore: S3/MinIO/Ceph/R2 config + bucket tests
- internal/audit, telemetry
- web/admin-ui        : CloFix-branded, responsive multi-page admin console. Each section and VPS detail has its own HTML route; shared layout, navigation, CSS, and browser code keep the pages consistent. Includes live CPU/RAM trend charts, per-VPS protection controls, Nginx OWASP CRS rule-group switches and error logs, port-forward review, curated Ubuntu service installs, application-service controls/live logs, usage, and instance logs.
# Identity and access management

Open **IAM** in the admin sidebar to create users, groups, permission policies, and API access keys. The `root` account is the Compose `ADMIN_TOKEN`; it is a permanent Full Administrator. IAM users can be assigned the `IAM User`, `Read Only`, or `Full Administrator` role. Custom policies grant scoped `read` or `manage` permissions for instances, workers, volumes, storage, security, and services. Attach policies directly to a user or through a group.

Each IAM user may have up to two active keys. CloFix displays the generated secret once and stores only its SHA-256 hash. To use a generated key with this API, send `Authorization: Bearer ACCESS_KEY_ID.SECRET_ACCESS_KEY`. Revoking a key immediately stops authentication. These are CloFix bearer keys; AWS Signature Version 4 signing, MFA, and password-based console login are not implemented.

IAM tables are created automatically by the Postgres store and are also defined in `db/migrations/0005_iam.up.sql`.

## VPS backups and login

The Backups page snapshots a stopped VPS writable root layer to compressed worker-local storage. It reports archive size and supports deletion. **Run as VPS** restores the snapshot as a new VPS on its source worker, where the original base image remains cached, and allows separate RAM, CPU, disk allocation, swap, and SSH public key settings. Persistent local volumes and S3 mounts are outside the backup. Cross-worker restore is not supported yet.

VPS Details includes **VPS Login Console**, which displays and copies the SSH command for the Mac Terminal app. It uses the VPS's assigned localhost SSH port and the private key corresponding to the configured public key. This is an SSH command helper, not an interactive browser terminal.
