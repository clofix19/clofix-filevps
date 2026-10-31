# Health checks and notification channels

Open **Health Checks** (`/health-checks`) from the sidebar. The controller automatically checks active workers (heartbeat no older than one minute) and load balancers (at least one healthy backend; NLB listener must also be available). Add custom HTTP/HTTPS or TCP endpoints to monitor applications or other services. HTTP 2xx/3xx counts as healthy; redirects are not followed. TCP uses a connection check. Each probe times out after three seconds and runs from the controller network, not your browser. In local Docker Compose, a worker endpoint uses `worker:<published-port>` rather than browser `localhost`.

Checks run every 30 seconds. Automatic resources require two consecutive failed checks before DOWN. Custom endpoints accept a threshold from 1 to 10. The first confirmed outage generates a DOWN event. A subsequent healthy check generates an UP/recovery event. Repeated failures during the same outage do not create repeated alerts. New load balancers have a two-minute provisioning grace period. Paused/deleted endpoints and inactive/deleted resources do not generate new down alerts. A stopped application can be monitored explicitly through its HTTP/TCP endpoint.

In **Settings → Notification channels**, add Slack or another JSON webhook destination. Enable the channel to receive all future DOWN and recovery events. Slack requires an official HTTPS incoming webhook URL; see [Slack's incoming webhook guide](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/). Slack receives `{"text":"CloFix health alert: ..."}`. Other JSON webhooks receive `text`, `event` (`DOWN`/`UP`), `event_id`, `monitor_id`, `name` and an RFC3339 `timestamp`. Destinations must accept this JSON format; arbitrary channel-specific formats, SMTP email, Telegram and Microsoft Teams cards are not implemented. Test a saved channel using **Send test**. A test explicitly sends a message, even when that channel is disabled for automatic alerts.

A blank webhook URL on channel modification preserves the existing URL. URLs are never returned by the read API. Notification response bodies and request URLs are excluded from delivery errors. Secrets are saved in `CLOFIX_DATA_DIR/monitoring.json` (directory mode 0700, file 0600); they are not encrypted in that file. Protect the controller data volume and its backups. HTTP destinations are supported for generic internal webhooks; use HTTPS for external destinations. Redirects are not followed for webhook delivery.

Alert history shows the latest 100 transitions and delivery results per channel. Webhook requests time out after five seconds. Failed deliveries retry up to five attempts, with exponential backoff (minimum 60 seconds after the first failure). Work is bounded to eight deliveries per pass. Delivery is at least once: a crash after sending but before persisting its result can produce a duplicate. Generic consumers can deduplicate using `event_id`. History is bounded; oldest transitions, including their retry state, are discarded past 100. No channels means events are recorded without recipients; adding a channel does not retroactively send old events.

Configuration, state, alert history and retries survive controller restarts. Health probes use up to eight concurrent requests. Notifications depend on the controller remaining available; an external monitoring service is needed to detect controller downtime itself.

API endpoints (bearer authentication):

- `GET /api/monitoring`: redacted channels, monitor configuration, health states and event history.
- `POST /api/monitoring/channels`, `PUT /api/monitoring/channels/{id}`, `DELETE /api/monitoring/channels/{id}`.
- `POST /api/monitoring/channels/{id}/test`: explicitly send a test notification.
- `POST /api/monitoring/monitors`, `PUT /api/monitoring/monitors/{id}`, `DELETE /api/monitoring/monitors/{id}`.

Use IAM `health-checks:read` / `health-checks:manage`. Manage permits configuring HTTP/TCP probe destinations and notification webhooks and sending test messages. Maximums: 100 custom endpoints, 20 channels. No notification channel is configured automatically.

## Server ping, ports and website availability

Custom check types:

- **Website · HTTP/HTTPS** (`http`): supply a full URL. HTTP 2xx/3xx is UP.
- **Server port · TCP** (`tcp`): supply `hostname:port`, `IPv4:port`, or `[IPv6]:port`. A successful TCP connection is UP.
- **Server ping · ICMP** (`ping`): supply an IP or hostname without URL or port. One ICMP echo reply is UP. The controller uses its installed `ping` utility, with an overall three-second timeout. Missing ping tools or missing ICMP permissions show ERROR instead of reporting a service outage. A server/firewall that blocks echo replies will report DOWN even if HTTP is working.

Ping uses the same consecutive-failure threshold, DOWN/recovery notifications and channel delivery retries as other checks. The controller must have permission to send ICMP (the local Alpine controller container supports this). No shell commands or arbitrary ping options are accepted as targets.
