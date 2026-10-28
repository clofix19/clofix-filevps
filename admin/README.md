# admin
Controller API + Admin UI (spec §2, §3, §10, §11).
- internal/api, auth (roles, MFA, sessions)
- internal/enrollment : one-time token, hash-only storage, atomic consume
- internal/licensing  : signed entitlements, graceful offline behavior
- internal/objectstore: S3/MinIO/Ceph/R2 config + bucket tests
- internal/audit, telemetry
- web/admin-ui        : CloFix-branded, responsive multi-page admin console. Each section and VPS detail has its own HTML route; shared layout, navigation, CSS, and browser code keep the pages consistent. Includes live CPU/RAM trend charts, per-VPS protection controls, Nginx OWASP CRS rule-group switches and error logs, port-forward review, curated Ubuntu service installs, application-service controls/live logs, usage, and instance logs.
