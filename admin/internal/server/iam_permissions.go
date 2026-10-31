package server

// Permission descriptions reflect the scopes enforced by the API middleware.
type iamPermissionDetail struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func iamPermissionDetails() map[string]iamPermissionDetail {
	details := map[string]iamPermissionDetail{
		"*":      {"Full access", "All API resource scopes. IAM administration still requires the Full Administrator role or root."},
		"read:*": {"Read all resources", "View all API resources without granting create, update, delete or execution actions."},
	}
	resources := map[string][2]string{
		"databases":      {"Managed databases", "Owned MySQL, PostgreSQL and MongoDB projects, users, schema, backups, downloads, restores, jobs and schedules. Manage includes provisioning and destructive actions."},
		"license":        {"License", "View controller license status and plan entitlements. Manage configures the license server/key and validates the current license."},
		"health-checks":  {"Health Checks & alerts", "Monitor HTTP/TCP endpoints and worker/LB availability. Manage configures notification webhooks, sends test messages and edits monitors."},
		"load-balancers": {"Load Balancers", "HTTP routing, TCP NLB listeners, allocated IP pools, health checks and owned VPS autoscaling. Manage includes IP pool changes and provisioning and deleting backend VPS instances."},
		"managed-apps":   {"Managed Apps", "Persistent Quick Apps and deployment status. Manage covers deployment, credentials, start, stop, restart and deletion."},
		"applications":   {"Applications · Jobs & Functions", "Applications, runtimes, run history, logs and output files. Manage also covers execution, cancellation, secrets, schedules, webhooks, versions and rollback."},
		"instances":      {"FileVPS", "VPS inventory, details and logs. Manage covers creation, resource changes, start, stop, restart, retry and deletion; includes legacy backup access."},
		"workers":        {"Workers", "Worker inventory and capacity. Manage covers enrollment tokens, activation, draining and revocation."},
		"volumes":        {"Volumes & files", "Persistent volumes and file browsing. Manage covers volume creation, changes, deletion, attachment, uploads, editing and folders."},
		"storage":        {"S3 storage", "Storage configuration, connections and objects. Manage covers credentials, connections, uploads, folders and object deletion. VPS mount actions use FileVPS manage."},
		"security":       {"Security", "VPS protection settings, WAF logs and port controls. Manage covers Protection Mode, SSH blocking, deletion protection, WAF rules and port forwarding."},
		"services":       {"Library & services", "Application-service information and error logs. Manage covers catalog installation, removal and service control."},
		"backups":        {"Backups", "Backup inventory. Manage creates and deletes VPS backups. Restoring to a new VPS also requires FileVPS manage."},
		"iam":            {"IAM", "Users, groups, policies and access-key information. Manage covers identity and policy changes; requires the Full Administrator role or root."},
		"platform":       {"Platform", "Controller-level API scope and fallback for platform endpoints. Does not grant access to other resource scopes."},
	}
	for key, resource := range resources {
		details[key+":read"] = iamPermissionDetail{resource[0] + " · Read", resource[1]}
		details[key+":manage"] = iamPermissionDetail{resource[0] + " · Manage", resource[1] + " Manage includes read access."}
	}
	return details
}
