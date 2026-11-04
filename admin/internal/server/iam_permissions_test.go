package server

import (
	"encoding/json"
	"testing"
)

func TestIAMPermissionCatalogCoversResources(t *testing.T) {
	details := iamPermissionDetails()
	for _, permission := range iamPermissionCatalog() {
		if details[permission].Label == "" || details[permission].Description == "" || details[permission].Category == "" {
			t.Fatalf("missing permission description or category: %s", permission)
		}
	}
	routes := map[string]string{
		"/api/applications": "applications", "/api/application-runs/run-1/cancel": "applications", "/api/functions/app-1/invoke": "applications",
		"/api/applications/app-1/rollback/1": "applications", "/api/applications/app-1/webhook": "applications",
		"/api/backups": "backups", "/api/instances/vps-1/backups": "backups",
		"/api/instances/vps-1/resources": "instances", "/api/ssh-keys": "instances", "/api/workers/w-1/drain": "workers", "/api/enrollment-tokens": "workers",
		"/api/volumes/v-1/file": "volumes", "/api/volumes/v-1/archive": "volumes", "/api/volumes/v-1/move": "volumes",
		"/api/storage/connections/c-1/objects": "storage", "/api/storage/connections/c-1/archive": "storage", "/api/efs": "storage", "/api/efs/efs-1/attach": "storage",
		"/api/instances/vps-1/waf/logs": "security", "/api/instances/vps-1/install-service": "services",
		"/api/iam/policies": "iam", "/api/accounts": "accounts", "/api/ca.pem": "platform",
		"/api/pricing/plans": "pricing", "/api/billing/invoices": "billing", "/api/billing/invoices/inv-1/pdf": "billing",
		"/api/dns": "dns", "/api/dns/records": "dns", "/api/dns/records/dns-1": "dns",
		"/api/vpcs": "vpc", "/api/vpcs/vpc-default": "vpc",
		"/api/logs": "logs", "/api/logs/sources": "logs", "/api/logs/vps-1/tail": "logs",
		"/api/firewall": "security", "/api/firewall/workers/w-1": "security",
		"/api/security/overview": "security", "/api/security/modes": "security", "/api/audit": "security",
		"/api/v1/kubernetes": "kubernetes", "/api/v1/kubernetes/clusters/cks-1": "kubernetes",
	}
	for path, want := range routes {
		if got := permissionResource(path); got != want {
			t.Fatalf("%s: resource %s, want %s", path, got, want)
		}
		for _, suffix := range []string{":read", ":manage"} {
			if !iamPermissionSet[want+suffix] {
				t.Fatalf("missing permission %s%s", want, suffix)
			}
		}
	}
}
func TestIAMBackupCompatibilityAndScopeIsolation(t *testing.T) {
	tests := []struct {
		grant, requested string
		allow            bool
	}{
		{"instances:read", "backups:read", true}, {"instances:read", "backups:manage", false},
		{"instances:manage", "backups:manage", true}, {"backups:manage", "backups:read", true},
		{"backups:manage", "instances:manage", false}, {"read:*", "applications:read", true},
		{"read:*", "applications:manage", false}, {"applications:manage", "applications:read", true},
		{"applications:manage", "iam:manage", false}, {"*", "iam:manage", true},
	}
	for _, test := range tests {
		if got := hasIAMPermission(iamPrincipal{Permissions: []string{test.grant}}, test.requested); got != test.allow {
			t.Fatalf("%s -> %s = %v", test.grant, test.requested, got)
		}
	}
}
func TestIAMPermissionCatalogAPIAndPolicySave(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	response := do(h, "GET", "/api/iam/policies", "", tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var catalog struct {
		Permissions []string                       `json:"permissions"`
		Details     map[string]iamPermissionDetail `json:"permission_details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Permissions) != len(iamPermissionSet) || len(catalog.Details) != len(iamPermissionSet) {
		t.Fatal("catalog/description count mismatch")
	}
	builtIn := iamBuiltInPolicies()
	if len(builtIn) < 10 {
		t.Fatalf("expected expanded built-in policy templates, got %d", len(builtIn))
	}
	seen := map[string]bool{}
	for _, policy := range builtIn {
		name, _ := policy["name"].(string)
		category, _ := policy["category"].(string)
		perms, _ := policy["permissions"].([]string)
		if name == "" || category == "" || len(perms) == 0 || seen[name] {
			t.Fatalf("invalid built-in policy: %+v", policy)
		}
		seen[name] = true
		for _, permission := range perms {
			if !iamPermissionSet[permission] {
				t.Fatalf("built-in %s references unknown permission %s", name, permission)
			}
		}
	}
	body := `{"name":"ApplicationsAndBackups","permissions":["applications:read","applications:manage","backups:read","backups:manage","iam:manage","read:*"]}`
	if saved := do(h, "POST", "/api/iam/policies", body, tok); saved.Code != 200 {
		t.Fatalf("save policy: %d %s", saved.Code, saved.Body.String())
	}
}
