package license

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testToken = "license-test-admin-token-123"

func call(h http.Handler, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if auth {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestLicenseLifecycleAndActivationLimit(t *testing.T) {
	dir := t.TempDir()
	s, e := New(dir, testToken)
	if e != nil {
		t.Fatal(e)
	}
	h := s.Handler()
	if w := call(h, "GET", "/api/plans", nil, false); w.Code != 401 {
		t.Fatal("admin route lacks auth")
	}
	p := Plan{Name: "Starter", MaxVPS: 10, MaxWorkers: 2, MaxLB: 3, MaxActivations: 1, Active: true}
	w := call(h, "POST", "/api/plans", p, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	w = call(h, "POST", "/api/licenses", map[string]any{"installation_id": "controller-1", "duration_days": 30, "plan_id": p.ID, "customer": "Test customer"}, true)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var issued struct {
		Key     string  `json:"key"`
		License License `json:"license"`
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	if issued.Key == "" || issued.License.KeyHash != "" {
		t.Fatal("invalid issuance response")
	}
	validate := func(id string) *httptest.ResponseRecorder {
		return call(h, "POST", "/api/validate", map[string]string{"key": issued.Key, "installation_id": id}, false)
	}
	if w := validate("controller-1"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := validate("controller-1"); w.Code != 200 {
		t.Fatal("revalidation consumes slot")
	}
	if w := validate("controller-2"); w.Code != 403 {
		t.Fatal("activation limit ignored")
	}
	if w := call(h, "DELETE", "/api/licenses/"+issued.License.ID+"/activations/controller-1", nil, true); w.Code != 409 {
		t.Fatal("permanent binding was released")
	}
	if w := validate("controller-2"); w.Code != 403 {
		t.Fatal("second installation accepted")
	}
	raw, _ := os.ReadFile(s.file)
	if strings.Contains(string(raw), issued.Key) {
		t.Fatal("plaintext key persisted")
	}
	w = call(h, "GET", "/api/licenses", nil, true)
	if strings.Contains(w.Body.String(), "key_hash") || strings.Contains(w.Body.String(), issued.Key) {
		t.Fatal("key material leaked")
	}
	if w := call(h, "DELETE", "/api/plans/"+p.ID, nil, true); w.Code != 409 {
		t.Fatal("referenced plan deleted")
	}
	restored, e := New(dir, testToken)
	if e != nil || len(restored.Licenses) != 1 {
		t.Fatal("license persistence failed", e)
	}
	before := s.Licenses[issued.License.ID].Expires
	if w := call(h, "POST", "/api/licenses/"+issued.License.ID+"/renew", nil, true); w.Code != 200 || !s.Licenses[issued.License.ID].Expires.After(before) {
		t.Fatal("renewal failed")
	}
	if w := call(h, "POST", "/api/licenses/"+issued.License.ID+"/revoke", nil, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := validate("controller-2"); w.Code != 403 {
		t.Fatal("revoked key accepted")
	}
}
func TestLicenseExpiryAndDisabledPlan(t *testing.T) {
	s, _ := New(t.TempDir(), testToken)
	h := s.Handler()
	p := Plan{Name: "Plan", MaxVPS: 1, MaxWorkers: 1, MaxLB: 1, MaxActivations: 1, Active: false}
	w := call(h, "POST", "/api/plans", p, true)
	json.Unmarshal(w.Body.Bytes(), &p)
	if w := call(h, "POST", "/api/licenses", map[string]any{"installation_id": "controller-1", "duration_days": 30, "plan_id": p.ID, "customer": "Customer"}, true); w.Code != 400 {
		t.Fatal("disabled plan allows issue")
	}
	s.Licenses["expired"] = License{ID: "expired", PlanID: p.ID, KeyHash: hash("CFL-test-expired-license-key"), Expires: time.Now().Add(-time.Hour)}
	w = call(h, "POST", "/api/validate", map[string]string{"key": "CFL-test-expired-license-key", "installation_id": "controller-1"}, false)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "expired") {
		t.Fatal("expired key accepted")
	}
}

func TestPlanExtendedEntitlements(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, testToken)
	h := s.Handler()
	p := map[string]any{"name": "Complete", "max_vps": 10, "max_workers": 2, "max_load_balancers": 3, "max_activations": 1, "active": true, "max_database_projects": 4, "max_databases": 20, "max_database_users": 40, "max_database_schedules": 15, "max_health_checks": 25, "max_backups": 0, "max_volumes": 10, "max_storage_gb": 100, "max_s3_connections": 5, "max_applications": 20, "max_managed_apps": 10, "max_notification_channels": -1, "max_iam_users": 10, "max_iam_groups": 4, "max_iam_policies": 20, "max_access_keys": 30}
	w := call(h, "POST", "/api/plans", p, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var plan Plan
	json.Unmarshal(w.Body.Bytes(), &plan)
	if plan.MaxDatabaseProjects == nil || *plan.MaxDatabaseProjects != 4 || plan.MaxDatabases == nil || *plan.MaxDatabases != 20 || plan.MaxDatabaseUsers == nil || *plan.MaxDatabaseUsers != 40 || plan.MaxDatabaseSchedules == nil || *plan.MaxDatabaseSchedules != 15 || plan.MaxHealthChecks == nil || *plan.MaxHealthChecks != 25 || plan.MaxBackups == nil || *plan.MaxBackups != 0 || plan.MaxNotificationChannels == nil || *plan.MaxNotificationChannels != -1 {
		t.Fatal("quota semantics lost")
	}
	w = call(h, "POST", "/api/licenses", map[string]any{"installation_id": "controller-1", "duration_days": 30, "plan_id": plan.ID, "customer": "Customer"}, true)
	var issued struct {
		Key string `json:"key"`
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	w = call(h, "POST", "/api/validate", map[string]string{"key": issued.Key, "installation_id": "controller-1"}, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var grant Grant
	json.Unmarshal(w.Body.Bytes(), &grant)
	raw, _ := json.Marshal(grant.Plan)
	var returned map[string]any
	json.Unmarshal(raw, &returned)
	for key, value := range p {
		if strings.HasPrefix(key, "max_") {
			if returned[key] != float64(value.(int)) {
				t.Fatal("entitlement not returned", key, returned[key])
			}
		}
	}
	reloaded, e := New(dir, testToken)
	if e != nil || reloaded.Plans[plan.ID].MaxBackups == nil || *reloaded.Plans[plan.ID].MaxBackups != 0 {
		t.Fatal("quota persistence failed", e)
	}
	for _, value := range []int{-2, 1000001} {
		p["max_health_checks"] = value
		if w := call(h, "POST", "/api/plans", p, true); w.Code != 400 {
			t.Fatal("invalid quota accepted", value)
		}
	}
	legacy := Plan{Name: "Legacy", MaxVPS: 1, MaxWorkers: 1, MaxLB: 1, MaxActivations: 1, Active: true}
	w = call(h, "POST", "/api/plans", legacy, true)
	json.Unmarshal(w.Body.Bytes(), &legacy)
	if w.Code != 200 || legacy.MaxBackups != nil {
		t.Fatal("missing legacy quota converted to zero")
	}
}

func TestModifyAndDeleteLicenseRetainsHistory(t *testing.T) {
	s, _ := New(t.TempDir(), testToken)
	h := s.Handler()
	plan := Plan{Name: "Plan", MaxVPS: 5, MaxWorkers: 1, MaxLB: 1, MaxActivations: 1, Active: true}
	w := call(h, "POST", "/api/plans", plan, true)
	json.Unmarshal(w.Body.Bytes(), &plan)
	w = call(h, "POST", "/api/licenses", map[string]any{"installation_id": "controller-1", "duration_days": 30, "plan_id": plan.ID, "customer": "Original"}, true)
	var issued struct {
		Key     string  `json:"key"`
		License License `json:"license"`
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	validate := map[string]string{"key": issued.Key, "installation_id": "controller-1"}
	if w := call(h, "POST", "/api/validate", validate, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	expiry := issued.License.Expires.Add(24 * time.Hour)
	w = call(h, "PUT", "/api/licenses/"+issued.License.ID, map[string]any{"customer": "Updated", "plan_id": plan.ID, "expires": expiry}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved := s.Licenses[issued.License.ID]
	if saved.Customer != "Updated" || saved.KeyHash != hash(issued.Key) || len(saved.Installations) != 1 || !saved.Created.Equal(issued.License.Created) {
		t.Fatal("modification changed identity or activations")
	}
	w = call(h, "POST", "/api/validate", validate, false)
	var grant Grant
	json.Unmarshal(w.Body.Bytes(), &grant)
	if w.Code != 200 || grant.Customer != "Updated" || !grant.Expires.Equal(expiry) {
		t.Fatal("updated license not validated")
	}
	if w := call(h, "DELETE", "/api/licenses/"+issued.License.ID, nil, false); w.Code != 401 {
		t.Fatal("delete unprotected")
	}
	if w := call(h, "DELETE", "/api/licenses/"+issued.License.ID, nil, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(s.Licenses) != 1 || !s.Licenses[issued.License.ID].Deleted {
		t.Fatal("created history lost")
	}
	if w := call(h, "POST", "/api/validate", validate, false); w.Code != 403 || !strings.Contains(w.Body.String(), "deleted") {
		t.Fatal("deleted key accepted")
	}
	if w := call(h, "POST", "/api/licenses/"+issued.License.ID+"/renew", nil, true); w.Code != 409 {
		t.Fatal("deleted key renewed")
	}
}

func TestLicenseDurationIsPerKey(t *testing.T) {
	s, _ := New(t.TempDir(), testToken)
	h := s.Handler()
	plan := Plan{Name: "Same plan", MaxVPS: 1, MaxWorkers: 1, MaxLB: 1, MaxActivations: 1, Active: true}
	w := call(h, "POST", "/api/plans", plan, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "duration_days") {
		t.Fatal("plan still has duration")
	}
	json.Unmarshal(w.Body.Bytes(), &plan)
	for _, days := range []int{7, 30} {
		w := call(h, "POST", "/api/licenses", map[string]any{"plan_id": plan.ID, "customer": "Customer", "duration_days": days, "installation_id": "controller-1"}, true)
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
		var result struct {
			License License `json:"license"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		l := result.License
		if l.DurationDays != days || l.Expires.Sub(l.Created) != time.Duration(days)*24*time.Hour {
			t.Fatal("wrong key duration", days)
		}
		before := l.Expires
		w = call(h, "POST", "/api/licenses/"+l.ID+"/renew", nil, true)
		json.Unmarshal(w.Body.Bytes(), &l)
		if w.Code != 200 || l.Expires.Sub(before) != time.Duration(days)*24*time.Hour {
			t.Fatal("renewal used plan rather than key duration")
		}
	}
	for _, days := range []int{0, -1, 36501} {
		w := call(h, "POST", "/api/licenses", map[string]any{"plan_id": plan.ID, "customer": "Customer", "duration_days": days, "installation_id": "controller-1"}, true)
		if w.Code != 400 {
			t.Fatal("invalid key duration accepted", days)
		}
	}
}
func TestLegacyPlanDurationMigrationPreservesLicense(t *testing.T) {
	dir := t.TempDir()
	created := time.Now().UTC().Add(-24 * time.Hour)
	expires := created.Add(90 * 24 * time.Hour)
	raw, _ := json.Marshal(map[string]any{"plans": map[string]any{"old-plan": map[string]any{"id": "old-plan", "duration_days": 90}}, "licenses": map[string]any{"old-key": License{ID: "old-key", PlanID: "old-plan", KeyHash: "unchanged-hash", Created: created, Expires: expires, Installations: []string{"existing-controller"}}}})
	if e := os.WriteFile(dir+"/licenses.json", raw, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := New(dir, testToken)
	if e != nil {
		t.Fatal(e)
	}
	l := s.Licenses["old-key"]
	if l.DurationDays != 90 || !l.Expires.Equal(expires) || l.KeyHash != "unchanged-hash" || len(l.Installations) != 1 {
		t.Fatal("migration changed license")
	}
	if _, e := os.Stat(dir + "/licenses.json.before-key-duration"); e != nil {
		t.Fatal("missing migration backup")
	}
	again, e := New(dir, testToken)
	if e != nil || again.Licenses["old-key"].DurationDays != 90 {
		t.Fatal("migration not persisted", e)
	}
}

func TestEncryptedKeyCopyRestoreAndAccessControl(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, testToken)
	h := s.Handler()
	plan := Plan{Name: "Plan", MaxVPS: 1, MaxWorkers: 1, MaxLB: 1, MaxActivations: 1, Active: true}
	w := call(h, "POST", "/api/plans", plan, true)
	json.Unmarshal(w.Body.Bytes(), &plan)
	w = call(h, "POST", "/api/licenses", map[string]any{"installation_id": "controller-1", "plan_id": plan.ID, "customer": "Customer", "duration_days": 30}, true)
	var issued struct {
		Key     string  `json:"key"`
		License License `json:"license"`
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	id := issued.License.ID
	if !issued.License.KeyAvailable {
		t.Fatal("new key not copyable")
	}
	if w := call(h, "POST", "/api/licenses/"+id+"/key", nil, false); w.Code != 401 {
		t.Fatal("key reveal unprotected")
	}
	w = call(h, "POST", "/api/licenses/"+id+"/key", nil, true)
	var revealed struct {
		Key string `json:"key"`
	}
	json.Unmarshal(w.Body.Bytes(), &revealed)
	if w.Code != 200 || revealed.Key != issued.Key || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("key retrieval failed")
	}
	raw, _ := os.ReadFile(s.file)
	if strings.Contains(string(raw), issued.Key) {
		t.Fatal("plaintext key persisted")
	}
	w = call(h, "GET", "/api/licenses", nil, true)
	if strings.Contains(w.Body.String(), issued.Key) || strings.Contains(w.Body.String(), "encrypted_key") {
		t.Fatal("list leaks key material")
	}
	loaded, e := New(dir, testToken)
	if e != nil {
		t.Fatal(e)
	}
	w = call(loaded.Handler(), "POST", "/api/licenses/"+id+"/key", nil, true)
	json.Unmarshal(w.Body.Bytes(), &revealed)
	if w.Code != 200 || revealed.Key != issued.Key {
		t.Fatal("key lost on restart")
	}
	legacy := s.Licenses[id]
	legacy.EncryptedKey = nil
	s.Licenses[id] = legacy
	if w := call(h, "POST", "/api/licenses/"+id+"/key", nil, true); w.Code != 409 {
		t.Fatal("hash-only key not reported")
	}
	if w := call(h, "PUT", "/api/licenses/"+id+"/key", map[string]string{"key": "wrong-key"}, true); w.Code != 400 {
		t.Fatal("incorrect original key accepted")
	}
	if w := call(h, "PUT", "/api/licenses/"+id+"/key", map[string]string{"key": issued.Key}, true); w.Code != 200 {
		t.Fatal("restore rejected")
	}
	restored := s.Licenses[id]
	if restored.KeyHash != legacy.KeyHash || !restored.Expires.Equal(legacy.Expires) || len(restored.EncryptedKey) == 0 {
		t.Fatal("restoring key changed license")
	}
	call(h, "DELETE", "/api/licenses/"+id, nil, true)
	if w := call(h, "POST", "/api/licenses/"+id+"/key", nil, true); w.Code != 404 {
		t.Fatal("deleted key revealed")
	}
}

func TestPermanentSingleInstallationBinding(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, testToken)
	h := s.Handler()
	plan := Plan{Name: "Single", MaxVPS: 1, MaxWorkers: 1, MaxLB: 1, MaxActivations: 2, Active: true}
	if w := call(h, "POST", "/api/plans", plan, true); w.Code != 400 {
		t.Fatal("multi-controller plan allowed")
	}
	plan.MaxActivations = 1
	w := call(h, "POST", "/api/plans", plan, true)
	json.Unmarshal(w.Body.Bytes(), &plan)
	for _, id := range []string{"", "bad id with spaces"} {
		if w := call(h, "POST", "/api/licenses", map[string]any{"plan_id": plan.ID, "customer": "Customer", "duration_days": 30, "installation_id": id}, true); w.Code != 400 {
			t.Fatal("unbound or invalid key issued")
		}
	}
	s.Licenses["legacy"] = License{ID: "legacy", PlanID: plan.ID, DurationDays: 30, KeyHash: hash("CFL-single-binding-fixture-key"), Expires: time.Now().Add(time.Hour), Installations: []string{"controller-first", "controller-second"}}
	if e := s.save(); e != nil {
		t.Fatal(e)
	}
	migrated, e := New(dir, testToken)
	if e != nil {
		t.Fatal(e)
	}
	if migrated.Licenses["legacy"].InstallationID != "controller-first" || len(migrated.Licenses["legacy"].Installations) != 1 {
		t.Fatal("legacy binding migration failed")
	}
	for _, id := range []string{"controller-second", "controller-third"} {
		w := call(migrated.Handler(), "POST", "/api/validate", map[string]string{"key": "CFL-single-binding-fixture-key", "installation_id": id}, false)
		if w.Code != 403 {
			t.Fatal("second installation accepted")
		}
	}
	w = call(migrated.Handler(), "POST", "/api/validate", map[string]string{"key": "CFL-single-binding-fixture-key", "installation_id": "controller-first"}, false)
	var grant Grant
	json.Unmarshal(w.Body.Bytes(), &grant)
	if w.Code != 200 || grant.InstallationID != "controller-first" {
		t.Fatal("bound controller rejected")
	}
}
