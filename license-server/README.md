# CloFix company license server

This is a standalone company-side project. Copy/deploy **this directory only** on CloFix infrastructure. It has its own Go module, admin UI, Docker image, Compose project, credentials and data volume. No customer controller/worker source or customer database is required.

## Deploy

```sh
cp .env.example .env
# Set LICENSE_ADMIN_TOKEN to a unique random company secret (minimum 16 chars).
docker compose up -d --build
```

Open `http://localhost:8090/` on the company host. By default only loopback is published. Put a TLS reverse proxy in front of port 8090 to expose the real CloFix license hostname. Restrict management access on `/` and `/api/plans` / `/api/licenses`; allow customer controllers to POST `/api/validate`. No DNS or certificate is created automatically.

The company admin token is independent of every customer `ADMIN_TOKEN`; it must never be shipped to customers. There is no token fallback. `.env` is ignored by Git and Docker build context. The data volume is `clofix-license-server-data` by default. `LICENSE_DATA_VOLUME` can choose a stable company-managed volume name; keep it stable across updates and back it up.

For development outside Docker:

```sh
# Export CLOFIX_LICENSE_ADMIN_TOKEN and CLOFIX_LICENSE_DATA_DIR first.
GOWORK=off go run ./cmd/clofix-license
GOWORK=off go test ./...
```

The optional repository-root Go workspace includes this module for local tests only. Building the customer controller does not import or package this server.

## Company workflow

1. Create a plan with resource entitlements; one controller per key.
2. Issue a license for a customer, enter the client’s Installation ID, and choose that key’s duration in days. Copy the issued key, or use Copy CFL key later from Issued Licenses. The server keeps its validation hash and an encrypted copy.
3. Give the customer the license key and company validation server base URL. Customers enter these in their controller's Settings → License.
4. Manage renewal or revoke licenses from this console. Installation binding cannot be released or reassigned.

Changing plan resource limits affects subsequent validation. Each license key owns its duration; plans do not define duration. Existing expiry stays fixed until renewal. Revoking rejects future validations. An existing key cannot move to a different installation; issue a replacement key for another controller. Disabling plans blocks issuance/renewal but does not revoke existing licenses.

## API / scope

Bearer-authenticated company APIs: `GET/POST /api/plans`, `PUT/DELETE /api/plans/{id}`, `GET/POST /api/licenses`, `PUT/DELETE /api/licenses/{id}`, `POST /api/licenses/{id}/renew`, `POST /api/licenses/{id}/revoke`, `DELETE /api/licenses/{id}/activations/{installation}`.

Customer validation: `POST /api/validate` with `key` and `installation_id`. No company management token is sent by customers. Validation checks expiry, revocation and the permanently bound Installation ID. Keys have 192 random bits. Validation uses SHA-256 hashes; recoverable copies use AES-GCM encryption. Raw keys and ciphertext are excluded from list responses. Validation is rate limited to 60 requests per remote address per minute.

`GET /healthz` is used by Compose. State is persisted in `/data/licenses.json` (0600 in a 0700 directory). Run one service replica; the JSON store is not a multi-writer database. Billing and offline signatures are not implemented. The customer controller enforces resource quotas and requires a valid installation-matched license for resource mutations.

## Extended plan entitlements

Plans include custom health-check endpoint counts, retained backups, volumes, total storage entitlement in GiB, S3 connections, jobs/functions, managed apps, notification channels, IAM users/groups/policies and IAM access keys, in addition to VPS/workers/load balancers and controller activations.

For these optional limits, `-1` means unlimited, `0` means not included and blank/null/omitted means not specified. Existing plans with no such fields remain unspecified, not zero. The validation API returns the exact values, including zero. Plan limits can be edited and are delivered on subsequent customer validation. Health-check counts refer to custom endpoints, not the automatically derived worker/LB checks. Counts are per controller; they are entitlements rather than live usage measurements. Storage is a total plan entitlement in GiB.

These fields describe licensed entitlements only. The customer controller enforces these quotas. Missing optional entitlements are not allocation permissions; explicit -1 grants unlimited usage. Adding/editing a limit does not remove or stop existing resources.

## Management navigation and license counts

The company console sidebar separates **License Plans**, **Issue License** and **Issued Licenses**. Summary cards show total created licenses and mutually exclusive active, expired, revoked and deleted counts. Active means unexpired and not revoked; a revoked expired record is counted under revoked only; deleted records are counted under deleted. These counts come from the authenticated license inventory, not sample values.

Each plan row shows its issued-license count. Clicking the count opens Issued Licenses filtered to that plan. The issuance form also shows the selected plan's total/active count. The license list includes creation/expiry dates, renew/revoke/activation actions and plan/status/customer-ID filters. New issuance refreshes the counts immediately; use Refresh to reload changes made from another console.


**Create license plan** opens a dialog instead of an inline form. Edit uses the same dialog; unreferenced plans can be deleted there or from the list. Plans used by existing licenses cannot be deleted; disable them instead.

Issued licenses also offer Modify (customer, plan and UTC expiry) and Delete. Modifications preserve the key and activations; a plan change is rejected if its activation limit is lower than the current activation count. Delete is a logical deletion: the key can no longer validate or renew, while the record remains in history and total created counts. Revoked licenses remain revoked after modification. Deleted licenses cannot be modified. Customer license status updates on subsequent validation.


## Per-license duration

Plans contain resource entitlements only; the activation count is fixed at one. **Issue License → License duration (days)** is required (1–36500) for each key. The issuance API accepts `duration_days` and mandatory `installation_id` alongside `plan_id` and `customer`; creation/expiry timestamps use this duration. Two keys on the same plan can have different durations.

Renewal uses the key's own `duration_days`, starting from the later of now and current expiry. Modify License can change the key's renewal duration; the explicit expiry field controls current expiry independently. A plan change does not change a key's duration. Validation returns duration at the top level of the license grant, and customer License status shows it there.

On first load after upgrading, keys without duration copy the former plan duration. If that legacy value is missing, duration is inferred from creation/expiry (rounded up to days, bounded to the allowed range). Key hashes, activations, creation dates and expiration dates are unchanged. The pre-migration JSON is saved as `/data/licenses.json.before-key-duration` before persisting migrated records.


## Copying license keys later

**Issued Licenses → Copy CFL key** retrieves the existing key through an admin-authenticated POST and copies it to the clipboard. It does not regenerate the key or change activations/expiry. If clipboard permission is denied, a dialog allows a second copy attempt or manual selection; the dialog clears its key when closed. Deleted licenses cannot reveal their keys.

The service stores AES-GCM encrypted copies in `licenses.json`, authenticated with each license ID. The 32-byte encryption key is stored at `/data/license-key-encryption.key` (0600), alongside the database. Back up both files together and protect the data volume: possession of both permits decryption. Losing the encryption file prevents recovery of encrypted keys; startup refuses to replace a missing key when ciphertext exists.

Older hash-only keys are not mathematically recoverable. Their **Restore CFL key** action accepts the original key and verifies its hash before storing an encrypted copy. If the original key is truly lost, issue a replacement license and revoke/delete the old one as appropriate. The customer's encrypted key storage remains separate.

Admin-only endpoints: `POST /api/licenses/{id}/key` retrieves a key with `Cache-Control: no-store`; `PUT /api/licenses/{id}/key` restores a known original key. The list API reports `key_available` without exposing raw key, ciphertext or hash.


## Permanent installation binding

Copy Installation ID from the customer's Settings → License and enter it when issuing a key. `installation_id` is mandatory for new licenses. The license server always permits exactly one Installation ID; `max_activations` must be 1. The ID cannot be changed by modification, renewal, key restoration or activation release. The old activation-release endpoint returns 409. Existing activated keys migrate to their first activation and save a pre-migration backup at `/data/licenses.json.before-single-installation`. Additional legacy activations are no longer valid. Unactivated legacy keys bind permanently on their first successful validation. Grant responses include the bound ID, which the client also checks.

The controller identity is persisted before license setup. Independent controllers must not share a cloned controller data volume: ID binding identifies an installation, not multiple processes intentionally impersonating the same cloned identity. Moving to a new installation requires revoking the old key and issuing a replacement bound to the new ID.
