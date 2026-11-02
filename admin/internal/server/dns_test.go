package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	databaseState "github.com/clofix/filevps/admin/internal/database"
	kubernetesState "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/database"
	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestDNSAutoRecordsFromInventory(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	if page, title, _, ok := uiPage("/dns"); !ok || page != "dns" || title != "DNS Service" {
		t.Fatalf("dns page route missing: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/dns") != "dns" || permissionResource("/api/dns/zonefile") != "dns" {
		t.Fatal("dns must be its own IAM resource")
	}
	h := s.AdminHandler()
	empty := do(h, "GET", "/api/dns", "", tok)
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), `"domain":"clofix.internal"`) {
		t.Fatal(empty.Body.String())
	}
	m.UpsertWorker(store.Worker{ID: "wrk-dns01", Hostname: "203.0.113.20", Status: "active", LastHeartbeat: time.Now()})
	m.SaveInstance(store.Instance{ID: "vps-dns01ab", State: proto.StateRunning, Spec: proto.InstanceSpec{IPAddress: "10.231.0.2", Hostname: "web1", SSHPort: 22000}})
	s.balancers.Lock()
	s.balancers.Items["lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] = &loadBalancer{ID: "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Mode: "nlb", PublicIP: "203.0.113.10", Slug: "edge", Name: "edge-nlb"}
	s.balancers.Items["lb-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"] = &loadBalancer{ID: "lb-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Mode: "http", Name: "clofix-lb", Healthy: map[string]string{"vps-1": fmt.Sprintf("http://worker:%d", proto.HostHTTPPort(22000))}}
	s.balancers.Unlock()
	if s.databases != nil {
		s.databases.Lock()
		s.databases.Projects["dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] = databaseState.Project{
			Name: "orders", Spec: database.Project{ID: "dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Engine: "mysql", Host: "203.0.113.30", Port: proto.HostDatabaseBase + 10},
		}
		s.databases.Unlock()
	}
	s.applications.Lock()
	s.applications.Managed["ma-app01"] = managedApp{ID: "ma-app01", Name: "storefront", URL: "http://203.0.113.40:25001/", Spec: proto.ManagedSpec{Address: "10.231.128.3"}}
	s.applications.Unlock()
	s.kubernetes.Lock()
	s.kubernetes.Clusters["cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] = kubernetesState.Cluster{
		ID: "cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "test-cluster", State: "RUNNING",
		Integrations: []kubernetesState.Integration{{Kind: "lb", ResourceID: "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "web", K8sKind: "Service", K8sName: "web"}},
		Inventory: k8s.Inventory{
			Services: []k8s.Workload{
				{Name: "kubernetes", Namespace: "default", Kind: "Service"},
				{Name: "nginx", Namespace: "default", Kind: "Service", Port: 80, URL: "http://127.0.0.1:8080/cks-lab"},
			},
			Ingresses: []k8s.Workload{
				{Name: "nginx", Namespace: "default", Kind: "Ingress", URL: "http://127.0.0.1:8080/cks-lab"},
			},
		},
	}
	s.kubernetes.Unlock()

	got := do(h, "GET", "/api/dns", "", tok)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var view struct {
		Zone    dnsZone
		Records []dnsRecord
		Targets []dnsTarget
	}
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	need := map[string]string{
		"vps-dns01ab.vps.clofix.internal":                          "10.231.0.2",
		"web1.vps.clofix.internal":                                 "vps-dns01ab.vps.clofix.internal.",
		"lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.lb.clofix.internal":   "203.0.113.10",
		"edge.lb.clofix.internal":                                  "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.lb.clofix.internal.",
		"edge-nlb.lb.clofix.internal":                              "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.lb.clofix.internal.",
		"lb-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.alb.clofix.internal":  "127.0.0.1",
		"clofix-lb.alb.clofix.internal":                            "lb-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.alb.clofix.internal.",
		"wrk-dns01.worker.clofix.internal":                         "203.0.113.20",
		"ma-app01.app.clofix.internal":                             "203.0.113.40",
		"cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.cks.clofix.internal": "203.0.113.10",
		"test-cluster.cks.clofix.internal":                         "cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.cks.clofix.internal.",
		"web.test-cluster.cks.clofix.internal":                     "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.lb.clofix.internal.",
		"nginx.svc.test-cluster.cks.clofix.internal":               "cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.cks.clofix.internal.",
		"nginx.ing.test-cluster.cks.clofix.internal":               "cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.cks.clofix.internal.",
	}
	found := map[string]string{}
	for _, rec := range view.Records {
		found[rec.Name] = rec.Value
		if rec.Source != "auto" {
			t.Fatalf("unexpected manual record: %+v", rec)
		}
	}
	for name, value := range need {
		if found[name] != value {
			t.Fatalf("%s = %q, want %q in %s", name, found[name], value, got.Body.String())
		}
	}
	if s.databases != nil && found["dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.db.clofix.internal"] != "203.0.113.30" {
		t.Fatalf("database record missing: %s", got.Body.String())
	}
	hasVPS, hasLB, hasDB, hasCKS, hasCKSSvc, hasCKSIng := false, false, false, false, false, false
	for _, rec := range view.Records {
		if rec.Resource == "vps" && rec.Type == "A" && rec.Port != 22000 {
			t.Fatalf("vps port: %+v", rec)
		}
		if rec.Resource == "db" && rec.Type == "A" && rec.Port != proto.HostDatabaseBase+10 {
			t.Fatalf("db port: %+v", rec)
		}
	}
	for _, item := range view.Targets {
		if item.Resource == "vps" && item.ID == "vps-dns01ab" && item.Port == 22000 {
			hasVPS = true
		}
		if item.Resource == "lb" && item.ID == "lb-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			hasLB = true
		}
		if item.Resource == "db" && item.ID == "dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && item.Port == proto.HostDatabaseBase+10 {
			hasDB = true
		}
		if item.Resource == "cks" && item.ID == "cks-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			hasCKS = true
			needPorts := map[int]bool{80: false, 443: false, 8080: false, 19080: false}
			for _, p := range item.Ports {
				if _, ok := needPorts[p.Port]; ok {
					needPorts[p.Port] = true
				}
			}
			for port, ok := range needPorts {
				if !ok {
					t.Fatalf("cks cluster missing port %d in %+v", port, item.Ports)
				}
			}
		}
		if item.Resource == "cks" && strings.Contains(item.ID, "/ingress/") {
			if !strings.Contains(item.Label, "Ingress") {
				t.Fatalf("cks ingress target: %+v", item)
			}
			hasCKSIng = true
		}
		if item.Resource == "cks" && strings.Contains(item.ID, "/service/") {
			if !strings.Contains(item.Label, "Service") {
				t.Fatalf("cks service target: %+v", item)
			}
			hasCKSSvc = true
		}
	}
	if !hasVPS || !hasLB || !hasCKS || !hasCKSSvc || !hasCKSIng || s.databases != nil && !hasDB {
		t.Fatalf("selectable targets missing: %+v", view.Targets)
	}
	picked := do(h, "POST", "/api/dns/records", `{"name":"orders","resource":"db","resource_id":"dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, tok)
	if s.databases != nil {
		if picked.Code != 200 || !strings.Contains(picked.Body.String(), fmt.Sprintf(`"port":%d`, proto.HostDatabaseBase+10)) || !strings.Contains(picked.Body.String(), "dbp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.db.clofix.internal.") {
			t.Fatal(picked.Body.String())
		}
	}
	if missing := do(h, "POST", "/api/dns/records", `{"name":"gone","resource":"vps","resource_id":"vps-missing"}`, tok); missing.Code != 404 {
		t.Fatal(missing.Body.String())
	}
	manual := do(h, "POST", "/api/dns/records", `{"name":"www","type":"A","value":"198.51.100.8","port":443,"description":"manual web"}`, tok)
	if manual.Code != 200 || !strings.Contains(manual.Body.String(), "www.clofix.internal") {
		t.Fatal(manual.Body.String())
	}
	var created struct{ Record dnsRecord }
	json.Unmarshal(manual.Body.Bytes(), &created)
	if created.Record.Source != "manual" || created.Record.Value != "198.51.100.8" || created.Record.Port != 443 {
		t.Fatal(manual.Body.String())
	}
	autoID := ""
	for _, rec := range view.Records {
		if rec.Resource == "vps" && rec.Type == "A" {
			autoID = rec.ID
			break
		}
	}
	if blocked := do(h, "DELETE", "/api/dns/records/"+autoID, "", tok); blocked.Code != 409 {
		t.Fatal(blocked.Body.String())
	}
	if del := do(h, "DELETE", "/api/dns/records/"+created.Record.ID, "", tok); del.Code != 200 {
		t.Fatal(del.Body.String())
	}
	zonefile := do(h, "GET", "/api/dns/zonefile", "", tok)
	if zonefile.Code != 200 || !strings.Contains(zonefile.Header().Get("Content-Type"), "text/plain") || !strings.Contains(zonefile.Body.String(), "$ORIGIN clofix.internal.") || !strings.Contains(zonefile.Body.String(), "vps-dns01ab") || !strings.Contains(zonefile.Body.String(), "IN\tSRV") || !strings.Contains(zonefile.Body.String(), "22000") {
		t.Fatal(zonefile.Header().Get("Content-Type"), zonefile.Body.String())
	}
	if missing := do(h, "DELETE", "/api/dns/records/dns-missing", "", tok); missing.Code != 404 {
		t.Fatal(missing.Code, missing.Body.String())
	}
}

func TestDNSRecordEditAndVPSPortMappings(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	m.SaveInstance(store.Instance{ID: "vps-dns01ab", State: proto.StateRunning, Spec: proto.InstanceSpec{
		IPAddress: "10.231.0.2", Hostname: "web1", SSHPort: 22000,
		PortMappings: []proto.PortMapping{{Protocol: "tcp", GuestPort: 8080, HostPort: proto.HostCustomBase}},
	}})
	h := s.AdminHandler()
	got := do(h, "GET", "/api/dns", "", tok)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var view struct{ Targets []dnsTarget }
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var vps dnsTarget
	for _, item := range view.Targets {
		if item.Resource == "vps" && item.ID == "vps-dns01ab" {
			vps = item
		}
	}
	if vps.Port != 22000 {
		t.Fatalf("default vps port: %+v", vps)
	}
	need := map[int]string{22000: "SSH", proto.HostHTTPPort(22000): "HTTP", proto.HostHTTPSPort(22000): "HTTPS", proto.HostCustomBase: "Port mapping"}
	found := map[int]string{}
	for _, p := range vps.Ports {
		found[p.Port] = p.Label
	}
	for port, note := range need {
		if !strings.Contains(found[port], note) {
			t.Fatalf("missing %d %s in %+v", port, note, vps.Ports)
		}
	}
	created := do(h, "POST", "/api/dns/records", fmt.Sprintf(`{"name":"web","resource":"vps","resource_id":"vps-dns01ab","port":%d}`, proto.HostHTTPPort(22000)), tok)
	if created.Code != 200 || !strings.Contains(created.Body.String(), fmt.Sprintf(`"port":%d`, proto.HostHTTPPort(22000))) {
		t.Fatal(created.Body.String())
	}
	var rec struct{ Record dnsRecord }
	if err := json.Unmarshal(created.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	updated := do(h, "PUT", "/api/dns/records/"+rec.Record.ID, fmt.Sprintf(`{"name":"web","resource":"vps","resource_id":"vps-dns01ab","port":%d,"description":"mapped http"}`, proto.HostCustomBase), tok)
	if updated.Code != 200 || !strings.Contains(updated.Body.String(), fmt.Sprintf(`"port":%d`, proto.HostCustomBase)) || !strings.Contains(updated.Body.String(), rec.Record.ID) {
		t.Fatal(updated.Body.String())
	}
	autoID := ""
	listed := do(h, "GET", "/api/dns", "", tok)
	var after struct{ Records []dnsRecord }
	json.Unmarshal(listed.Body.Bytes(), &after)
	for _, item := range after.Records {
		if item.Source == "auto" && item.Resource == "vps" && item.Type == "A" {
			autoID = item.ID
			break
		}
	}
	if auto := do(h, "PUT", "/api/dns/records/"+autoID, `{"name":"blocked","type":"A","value":"198.51.100.8"}`, tok); auto.Code != 409 {
		t.Fatal(auto.Body.String())
	}
	if missing := do(h, "PUT", "/api/dns/records/dns-missing", `{"name":"gone","type":"A","value":"198.51.100.8"}`, tok); missing.Code != 404 {
		t.Fatal(missing.Body.String())
	}
}
