# Load balancers

Open **Networking → Load Balancers** (`/load-balancers`) to create, modify or delete a balancer. Select a ready VPS backup from the image dropdown, then set the SSH public key, resources per VPS, minimum and maximum VPS counts, target CPU and health check path. The dropdown uses the same backups as the Backups page and pre-fills the resources from the selected backup. Configuration and membership are saved in `$CLOFIX_DATA_DIR/load-balancers.json` with restricted file permissions.

The selected backup must serve the application on HTTP port 80. Backups are worker-local: all restored backend VPS instances are pinned to the backup’s source worker. Keep the backup available for future scale-out and ensure its worker has enough capacity. The default Ubuntu image is a base OS; it does not automatically contain your application. Use an application image available to every worker. Custom download URLs must remain valid for future autoscaling operations. Changing the selected backup or resources affects future instances; existing instances keep their original specification.

Click a balancer name to open `/load-balancers/<balancer-id>` with its application URL, settings, backend VPS links and per-backend health results. Set **Application URL name** when creating or modifying a balancer for a readable URL such as `/lb/my-app/`. Names must be unique and use lowercase letters, numbers and hyphens; `lb-` is reserved. The ID endpoint remains available.

The default application endpoint is `/lb/<balancer-id>/` on the controller origin. It is public; protect private applications in the application itself. The prefix is stripped before forwarding, and the query string is preserved. Applications that generate absolute redirects or asset URLs must support a configured base path. TLS can terminate at the controller's existing ingress. This feature balances HTTP application traffic; it does not expose a separate TCP/UDP listener or implement TLS passthrough.

## Algorithms

- Round robin: rotate requests across healthy backends.
- Least connections: choose the backend with the fewest active proxied requests.
- Least response time: choose the backend with the lowest smoothed request duration, starting with unmeasured backends.
- Client IP hash: stable selection using the direct connection's client IP. Behind an ingress, clients sharing that ingress address share the hash.
- URI hash: stable selection using the request path.
- Random: choose a random healthy backend.
- Power of two choices: sample two backends and choose the one with fewer active requests.

Hash affinity can change when the healthy backend set changes. Algorithms use controller-local request counters, so run a single controller for this configuration store.

## Health checks and scaling

The controller reconciles every 15 seconds, checks application health with a two-second timeout, and routes only to running, healthy instances on active workers with a recent heartbeat. HTTP 2xx and 3xx responses count as healthy; health check redirects are not followed. A proxy connection failure removes the backend until a later successful check.

Workers must be reachable from the controller using their registered hostname, or `ManagedPublicHost` when supplied, on their published HTTP ports (`SSHPort + 1000`). Existing worker networking publishes these ports. For the local single-worker Docker Compose stack, `CLOFIX_LB_WORKER_HOST=worker` selects the stable Docker service address instead of a container hostname. Leave this override unset for multi-worker deployments; each worker must advertise its own reachable address. Ensure the hostname resolves to a reachable worker address; hostnames that only resolve on the worker itself will not work.

The controller provisions one instance per pass until the minimum is reached. With current worker metrics for every running backend, CPU usage above the target adds one VPS up to maximum. CPU below half the target removes one VPS down to minimum when all backends are healthy. CPU is normalized by each backend's allocated CPU cores. A two-minute cooldown between CPU-driven scaling operations reduces oscillation. Missing metrics, stale workers and transitional instances prevent CPU-driven scaling. Scheduler capacity rules still apply; capacity errors appear in the UI and are retried on later passes.

Failed backend instances are deleted and replaced. Reducing maximum removes excess owned instances. CPU-driven scale-in waits until the chosen backend has no active requests; explicit deletion and reducing maximum may interrupt traffic. Persistent volumes and delete protection block deletion through the existing VPS lifecycle rules and are reported in the UI. Only instances created by the balancer are managed.

Deleting a balancer disables new traffic immediately and queues deletion of its owned VPS instances, including local data. The record remains in a deleting state until the worker confirms those instances are gone.

Use persistent controller data and the PostgreSQL instance store for restart-safe VPS inventory. The development memory store loses instance records on restart. Membership persistence cannot recover worker instances missing from that inventory. Provisioning operations and the balancer JSON file do not share a database transaction; a controller crash between provisioning and saving membership requires checking the VPS inventory for an orphan.

## API and IAM

- `GET /api/load-balancers`
- `POST /api/load-balancers`
- `PUT /api/load-balancers/{id}`
- `DELETE /api/load-balancers/{id}`

Management uses bearer authentication and the `load-balancers:read` / `load-balancers:manage` IAM permissions. Manage grants creation and deletion of the balancer's backend VPS instances. Like the VPS inventory, balancer inventory is shared within the controller's IAM resource scope.

Rebuild and restart the controller to load the new UI, routes and scheduler. No worker protocol change is required.

## NLB allocated IP pool and TCP listeners

Under **Settings → NLB network settings**, save your provider-allocated IPv4 addresses, one entry per line:

- A single IPv4 address.
- A CIDR prefix (network and broadcast excluded for /30 and larger networks).
- An inclusive `start-end` range.

The controller accepts at most 4096 unique addresses. For local host testing only, a loopback range such as `127.0.0.2-127.0.0.10` can be used. Do not substitute a documentation/example range for real allocated public IPs.

Create or modify a balancer and choose **NLB · TCP · unique IP**. Set its TCP listener port (1–65535) and backend guest port (22, 80 or 443, using the worker's existing published mappings). The controller reserves one unique address per NLB, persists it with the pool configuration, and starts the TCP listener on the next scheduler pass. A full or missing pool rejects NLB creation with an explicit error. Existing HTTP balancers keep their mode until explicitly changed.

TCP streams are forwarded without HTTP header rewriting or TLS termination. TLS certificates remain on backend applications. Supported algorithms are round robin, least connections, client IP hash, random and power of two choices. URI hashing and response-time HTTP balancing are unavailable in NLB mode. NLB health checks use TCP connect, and do not validate application HTTP status or TLS certificates. Client source IP is not preserved by this userspace proxy. UDP, PROXY protocol and arbitrary guest ports are not implemented. Idle reads/writes time out after five minutes.

Each IP must already be allocated by your hosting provider, routed to the controller host, and configured on its network interface. The controller does not provision provider IPs or change OS interfaces, routing or firewalls. It binds the allocated address directly, with no fallback to a wildcard address. OS bind errors appear in the NLB details and are retried. Low ports such as 80/443 may require service capabilities/permissions. Firewall rules must allow the listener ports. In a container deployment, the IP must be available in that network namespace; listing a host IP alone does not make it available inside a container. For the local Compose deployment, port 18080 is published on host 127.0.0.1 and an explicit `CLOFIX_NLB_LISTEN_MAP` maps `127.0.0.1:18080` to `0.0.0.0:18080` inside the controller container. Only listed endpoints use NAT mapping; other allocated IPs still bind directly. A public deployment needs its own assigned/routed addresses and matching network setup.

Saving a changed pool preserves existing assignments that still belong to the pool. It reassigns other NLBs deterministically from the free addresses in the new pool. If the new pool cannot hold every existing NLB, the entire update is rejected and old assignments remain. Listener reconciliation applies accepted changes within the scheduler interval. IP changes may interrupt traffic and require updating DNS **A records** and client allowlists. A CNAME cannot point directly to an IP.

Settings API: `GET` / `PUT /api/load-balancers/settings`, using `load-balancers:read` / `load-balancers:manage`. PUT body: `{"ip_ranges":["<allocated-IP-or-range>"]}`. NLB creation/update additionally accepts `"mode":"nlb"`, `"listen_port":80`, `"backend_port":80`; `public_ip` is assigned by the controller and cannot be overridden by the client. The HTTP `/lb/.../` proxy is not the NLB traffic endpoint; connect directly to the allocated IP and listener port shown in details.

From Settings, choose the existing load balancer and click **Apply NLB settings** to convert it to TCP mode without replacing its backend VPS instances. The local development endpoint is `http://127.0.0.1:18080/` after saving pool `127.0.0.1` and applying listener port `18080`, backend port `80`. This is loopback-only, not a public allocation.

## ALB · application load balancer

Choose **ALB · HTTP/HTTPS · domain routing** when creating or modifying a balancer. Existing HTTP mode is the ALB implementation; the API accepts `mode: "alb"` as an alias and persists `http` for compatibility. Current NLBs remain NLBs unless explicitly modified.

ALB supports the seven HTTP algorithms above, HTTP status health checks, generated `/lb/<id-or-slug>/` endpoints, and up to 20 exact custom domain names per balancer. Put one hostname per line in **ALB custom domains**, without schemes, ports, paths or wildcards. Domains are normalized to lowercase and cannot belong to two balancers. Custom-domain traffic preserves the original path, query and Host header. The first configured domain is used as the HTTP health check Host header. Unmatched hosts retain normal controller routing.

Point the domain's DNS at the controller/ingress address. TLS must terminate at an HTTPS ingress or reverse proxy with a certificate; this feature does not automatically provision certificates, create DNS records or provide a direct HTTPS listener. Ingress must preserve the original Host header. ALB uses HTTP port 80 on backend VPS instances. Generated endpoints still work without DNS. ALBs share the controller entrypoint and do not reserve an NLB IP.
