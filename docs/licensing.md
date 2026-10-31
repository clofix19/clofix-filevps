# Customer license configuration

The license server belongs to CloFix company infrastructure, not the customer FileVPS stack. Its complete standalone project lives in [`license-server/`](../license-server/README.md). Deploy that folder independently with its own Compose configuration, company admin token and persistent data volume.

The repository-root `docker-compose.yml` runs the customer controller, workers, storage and database only. It does not build/run the license server, mount its database or receive the company admin token. The controller contains only the license validation client and wire types; it has no dependency on the license-server Go module.

## Customer setup

In **Settings → License** enter the actual CloFix license server base URL and the customer license key, then click Save & validate. Saving a configured key triggers online validation immediately. Use the `CFL-...` key shown at issuance, not the `lic-...` record ID. The license ID is shown in the client status after successful validation. Alternatively set `CLOFIX_LICENSE_SERVER_URL` as the initial URL in the customer's environment. Use the actual deployed address supplied by CloFix. The customer UI does not link to company plan or license management.

Customers receive a license key, not the company's management token. Validation sends only the key and persisted controller installation ID to `/api/validate`. It rejects redirects and uses a five-second timeout. Manual validation and a five-minute background check display validity, customer, plan entitlements and expiry. Connection failures are reported. Customer license keys are encrypted in `/data/license.json` with the existing AES-GCM application secret key. The read API returns neither key nor ciphertext.

Blank key input preserves the current key; Remove saved license key clears it. Preserve the controller data volume to retain its activation identity. Do not clone one installation ID across independent controllers.

Controller APIs: `GET/PUT /api/license`, `POST /api/license/validate`, authenticated with customer controller credentials and IAM `license:read` / `license:manage` scopes.

Resource quotas are enforced in controller APIs. Plans, key issuance, renewal and revocation belong to the company server. Billing is not implemented.

## Separate local development preview

The company Compose project can be run on the same workstation for development, independently of the customer Compose project. Its UI is `http://localhost:8090/`. A customer controller running in Docker Desktop can reach it using `http://host.docker.internal:8090`; this is a local preview address, not a production company URL. Production customers should connect to the company HTTPS endpoint.

When migrating from the former combined stack, stop the old license container before binding the same port, copy its data into the company project's volume, and keep the old volume as a backup. Never delete a license volume as part of a stack migration.

License status also displays the extended plan entitlements for health checks, backups, volumes/storage, S3 connections, jobs/functions, managed apps, notification channels and IAM resources. Optional limits retain the company API semantics: unlimited (-1), not included (0), or not specified (missing/null). Missing optional limits do not grant allocation permission; -1 is explicit unlimited.

License duration belongs to the individual key, not its plan. The company chooses it when issuing each key. Client status shows the duration returned at the top level of validation. Renewal uses that key’s duration.


## Installation binding and enforcement

The Settings Admin access section has been removed. Customer setup and usage are under License. Copy the persisted Installation ID and provide it to CloFix when obtaining a key. Each key is permanently bound to that ID. It cannot be released or reused by another ID. Customer validation checks the bound ID returned by the company server.

A valid, non-expired, matching license with a successful validation within the last 10 minutes is required for resource changes. Background validation runs at startup and every five minutes; server rejection or connection failure immediately marks the cached grant invalid. License configuration/validation, read-only inventory, resource DELETE, stop/cancel/detach and worker draining/revocation remain available for recovery and cleanup. Missing, stale or expired licenses do not automatically delete data or stop already-running VPSs. Existing LB data-plane traffic is not forcibly interrupted solely because a license expires; recreating listeners or allocating new backends requires a valid license.

Controller mutation admission is serialized to prevent concurrent create requests exceeding quotas. It counts all stored resource records, including pending/deleting resources until removed. Revoked workers do not consume worker slots; pending/draining workers do. Quotas cover VPSs (including LB-owned VPSs), workers, load balancers, custom health endpoints, retained/pending backups, volumes, S3 connections, jobs/functions, managed apps, notification channels and IAM users/groups/policies/access keys. Editing a record at its limit does not consume a new slot. Existing over-quota resource operations/growth are rejected; cleanup remains possible. Scheduled/webhook application execution and automatic LB provisioning also check the license. Background custom checks beyond the license limit are paused; alert delivery checks notification-channel entitlement.

Storage quota admission counts VPS disk allocation (or observed usage if greater), bounded volume reservation/usage, and backup storage/creating-backup reservations. Finite storage licenses cannot create unbounded volumes. Allocation growth and controller uploads while over storage quota are rejected. This is controller allocation accounting, not a new filesystem or remote-object-store hard cap: direct guest writes and S3 access outside the controller are not physically limited by this code. Managed-app host disk and remote S3 bucket usage are not measured in this total. Existing technical limits on the platform still apply even if a license says unlimited.

`GET /api/license/usage` shows current counts and limits and requires license read permission. The UI displays quota usage and a license/quota warning. Lowering company plan limits does not delete existing resources; the new entitlements take effect on subsequent validation.
