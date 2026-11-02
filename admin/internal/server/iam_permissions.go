package server

// Permission descriptions reflect the scopes enforced by the API middleware.
type iamPermissionDetail struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

func iamPermissionDetails() map[string]iamPermissionDetail {
	details := map[string]iamPermissionDetail{
		"*":      {"Full access", "All API resource scopes. IAM administration still requires the Full Administrator role or root.", "Global"},
		"read:*": {"Read all resources", "View all API resources without granting create, update, delete or execution actions.", "Global"},
	}
	resources := map[string]struct {
		Label, Description, Category string
	}{
		"instances":      {"FileVPS", "VPS inventory, details and logs. Manage covers creation, resource changes, start, stop, restart, retry and deletion; includes legacy backup access.", "Compute"},
		"workers":        {"Workers", "Worker inventory and capacity. Manage covers enrollment tokens, activation, draining and revocation.", "Compute"},
		"managed-apps":   {"Managed Apps", "Persistent Quick Apps and deployment status. Manage covers deployment, credentials, start, stop, restart and deletion.", "Compute"},
		"applications":   {"Applications · Jobs & Functions", "Applications, runtimes, run history, logs and output files. Manage also covers execution, cancellation, secrets, schedules, webhooks, versions and rollback.", "Compute"},
		"services":       {"Library & services", "Application-service information and error logs. Manage covers catalog installation, removal and service control.", "Compute"},
		"kubernetes":     {"CKS", "Owned CloFix Kubernetes Service clusters, node pools, namespaces, pods, deployments, services, secrets, config maps, PVCs, Helm, CloFix LB/volume/EFS/S3/VPS attachments, ingress, RBAC, autoscaling, etcd backups, upgrades and kubeconfig. Manage covers create, scale, delete, protection and workload changes. FileVPS guests cannot run kubelet.", "Compute"},
		"volumes":        {"Volumes & files", "Persistent volumes and file browsing. Manage covers volume creation, changes, deletion, attachment, uploads, editing and folders.", "Storage"},
		"storage":        {"S3 storage", "Storage configuration, connections and objects. Manage covers credentials, connections, uploads, folders and object deletion. VPS mount actions use FileVPS manage.", "Storage"},
		"backups":        {"Backups", "Backup inventory. Manage creates and deletes VPS backups. Restoring to a new VPS also requires FileVPS manage.", "Storage"},
		"databases":      {"Managed databases", "Owned MySQL, PostgreSQL, MongoDB and Redis projects, users, schema, backups, downloads, restores, jobs and schedules. Manage includes provisioning and destructive actions.", "Storage"},
		"load-balancers": {"Load Balancers", "HTTP routing, TCP NLB listeners, allocated IP pools, health checks and owned VPS autoscaling. Manage includes IP pool changes and provisioning and deleting backend VPS instances.", "Networking"},
		"dns":            {"DNS Service", "Controller DNS zone and records auto-created from VPS, load balancer, CKS, database, managed app and worker inventory. Manage edits the zone and manual records. The controller does not listen on port 53 or publish public DNS.", "Networking"},
		"vpc":            {"VPC", "Private IP blocks on the FileVPS bridge 10.231.0.0/16. The default block cannot be deleted. Manage creates, edits and deletes extra subnets. VPS, databases and managed apps use the default VPC unless another block is selected.", "Networking"},
		"health-checks":  {"Health Checks & alerts", "Monitor HTTP/TCP endpoints and worker/LB availability. Manage configures notification webhooks, sends test messages and edits monitors.", "Networking"},
		"security":       {"Security", "VPS protection settings, WAF logs and port controls. Manage covers Protection Mode, SSH blocking, deletion protection, WAF rules and port forwarding.", "Security"},
		"iam":            {"IAM", "Users, groups, policies and access-key information. Manage covers identity and policy changes; requires the Full Administrator role or root.", "Security"},
		"accounts":       {"Accounts", "Partner and client account records. Manage creates partner or root-client logins. Public registration always creates a root client.", "Security"},
		"logs":           {"Log Service", "Central live tails for VPS, load balancer, database, Library and worker logs. Manage adds custom paths, downloads tails and configures keyword/level alerts that notify Settings channels.", "Security"},
		"platform":       {"Platform", "Controller-level API scope and fallback for platform endpoints. Does not grant access to other resource scopes.", "Platform"},
		"license":        {"License", "View controller license status and plan entitlements. Manage configures the license server/key and validates the current license.", "Platform"},
		"pricing":        {"Pricing & Plans", "Product catalog prices, resource unit rates, discounts, payment-gateway configuration, currency/tax and billing-notification flags. Manage creates and edits catalog prices. Changing a catalog price does not rewrite active subscription amounts.", "Platform"},
		"billing":        {"Billing & Payments", "Customer invoices, wallets, recorded payments, subscriptions, refunds, credits and revenue totals. Manage records bookkeeping entries. Card charges are not processed until a gateway processor is connected.", "Platform"},
	}
	for key, resource := range resources {
		details[key+":read"] = iamPermissionDetail{resource.Label + " · Read", resource.Description, resource.Category}
		details[key+":manage"] = iamPermissionDetail{resource.Label + " · Manage", resource.Description + " Manage includes read access.", resource.Category}
	}
	return details
}
