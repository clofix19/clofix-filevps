package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func doIdempotent(h http.Handler, method, path, body, auth, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func kubernetesWorkerFixture() store.Worker {
	return store.Worker{ID: "wrk-k8s", Hostname: "k8s-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{KubernetesReady: true, KubernetesVersions: []string{"1.31", "1.32"}, KubernetesPublicHost: "192.0.2.40", RAMTotal: 16 << 30, HostReserve: 512 << 20, MemAvailable: 12 << 30, CPUCores: 8, DiskTotal: 200 << 30, DiskFree: 150 << 30}}
}

func TestKubernetesFileVPSModeRejected(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	response := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"demo","version":"1.31","infrastructure_mode":"filevps","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "FileVPS") {
		t.Fatalf("filevps mode: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "cgroup") {
		t.Fatal("compatibility report missing")
	}
}

func TestKubernetesPlacementSkipsUnreadyWorkers(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(store.Worker{ID: "wrk-plain", Hostname: "plain", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 16 << 30, HostReserve: 512 << 20, MemAvailable: 12 << 30, CPUCores: 8, DiskTotal: 200 << 30, DiskFree: 150 << 30}})
	response := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"demo","version":"1.31","infrastructure_mode":"dedicated","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if response.Code != 409 {
		t.Fatalf("unready worker must not be placed: %d %s", response.Code, response.Body.String())
	}
}

func TestKubernetesProvisioningAndKubeconfig(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	body := `{"name":"demo","version":"1.31","infrastructure_mode":"dedicated","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`
	response := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", body, tok)
	if response.Code != 202 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Cluster struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &created) != nil || created.JobID == "" || created.Cluster.ID == "" {
		t.Fatal("missing cluster or job")
	}
	s.kubernetes.Lock()
	job := s.kubernetes.Jobs[created.JobID]
	if strings.Contains(string(job.Payload), `"action"`) || strings.Contains(string(job.Payload), "init") {
		t.Fatal("kubernetes job payload stored in plaintext")
	}
	s.kubernetes.Unlock()
	ops, ack := s.kubernetesHeartbeat("wrk-k8s", nil, nil)
	if len(ack) != 0 || len(ops) != 1 || ops[0].KubernetesJobID != created.JobID {
		t.Fatalf("dispatch mismatch: %+v", ops)
	}
	resultData, _ := json.Marshal(map[string]string{"kubeconfig": "apiVersion: v1\nkind: Config\n", "api_endpoint": "https://192.0.2.40:6443", "join_token": "fake.token", "join_hash": "sha256:deadbeef"})
	_, ack = s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: created.JobID, ClusterID: created.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: resultData}}, nil)
	if len(ack) != 1 {
		t.Fatal("worker completion was not acknowledged")
	}
	s.kubernetes.Lock()
	cluster := s.kubernetes.Clusters[created.Cluster.ID]
	if strings.Contains(string(s.kubernetes.Jobs[created.JobID].Result), "kind: Config") {
		t.Fatal("plaintext kubeconfig stored in job result")
	}
	s.kubernetes.Unlock()
	if cluster.State != k8s.StateRunning || len(cluster.EncryptedKubeconfig) == 0 {
		t.Fatalf("cluster not completed: %+v", cluster)
	}
	kube := do(s.AdminHandler(), "GET", "/api/v1/kubernetes/clusters/"+created.Cluster.ID+"/kubeconfig", "", tok)
	if kube.Code != 200 || !strings.Contains(kube.Body.String(), "kind: Config") {
		t.Fatalf("kubeconfig: %d %s", kube.Code, kube.Body.String())
	}
}

func TestKubernetesTenantIsolation(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	h := s.AdminHandler()
	regA := do(h, "POST", "/api/auth/register", `{"email":"k8s-a@client.test","password":"client-password-123","account_name":"K8s A"}`, "")
	regB := do(h, "POST", "/api/auth/register", `{"email":"k8s-b@client.test","password":"client-password-123","account_name":"K8s B"}`, "")
	if regA.Code != 200 || regB.Code != 200 {
		t.Fatal(regA.Body.String(), regB.Body.String())
	}
	created := doCookie(h, "POST", "/api/v1/kubernetes/clusters", `{"name":"alpha","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, sessionCookieFrom(regA))
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	listed := doCookie(h, "GET", "/api/v1/kubernetes/clusters", "", sessionCookieFrom(regB))
	if listed.Code != 200 || strings.Contains(listed.Body.String(), out.Cluster.ID) {
		t.Fatal("client B must not see client A cluster", listed.Body.String())
	}
	if got := doCookie(h, "GET", "/api/v1/kubernetes/clusters/"+out.Cluster.ID, "", sessionCookieFrom(regB)); got.Code != 404 {
		t.Fatal("client B must not read client A cluster", got.Code, got.Body.String())
	}
	if got := doCookie(h, "GET", "/api/v1/kubernetes/clusters/"+out.Cluster.ID+"/kubeconfig", "", sessionCookieFrom(regB)); got.Code != 404 {
		t.Fatal("client B must not download client A kubeconfig", got.Code, got.Body.String())
	}
}

func TestKubernetesIAMDenialWithoutScope(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	user := store.IAMUser{ID: "usr-" + strings.Repeat("a", 16), Username: "k8s-ro", Kind: "iam", Role: "Read Only", Status: "active", AccountID: "acc-k8s", Created: time.Now()}
	if err := memory.SaveIAMUser(user); err != nil {
		t.Fatal(err)
	}
	accessID, secret := "CFXTESTK8SREADONLY01", "readonly-secret-123456"
	if err := memory.SaveIAMAccessKey(store.IAMAccessKey{AccessKeyID: accessID, UserID: user.ID, SecretHash: hashToken(secret), Active: true, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	key := accessID + "." + secret
	if got := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"denied","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, key); got.Code != 403 {
		t.Fatalf("read-only must not create clusters: %d %s", got.Code, got.Body.String())
	}
}

func TestKubernetesPageRenders(t *testing.T) {
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	page := do(s.AdminHandler(), "GET", "/kubernetes", "", tok)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "CKS") {
		t.Fatal(page.Code, page.Body.String())
	}
	if alias := do(s.AdminHandler(), "GET", "/cks", "", tok); alias.Code != 200 {
		t.Fatal(alias.Code, alias.Body.String())
	}
}

func TestKubernetesIdempotentCreate(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	h := s.AdminHandler()
	body := `{"name":"same","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`
	first := doIdempotent(h, "POST", "/api/v1/kubernetes/clusters", body, tok, "k8s-key-1")
	second := doIdempotent(h, "POST", "/api/v1/kubernetes/clusters", body, tok, "k8s-key-1")
	if first.Code != 202 || second.Code != 202 {
		t.Fatal(first.Body.String(), second.Body.String())
	}
	var a, b struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
	}
	json.Unmarshal(first.Body.Bytes(), &a)
	json.Unmarshal(second.Body.Bytes(), &b)
	if a.Cluster.ID == "" || a.Cluster.ID != b.Cluster.ID {
		t.Fatalf("idempotency mismatch %s %s", a.Cluster.ID, b.Cluster.ID)
	}
}

func completeKubernetesCluster(t *testing.T, s *Server, workerID, clusterID, jobID string) {
	t.Helper()
	resultData, _ := json.Marshal(map[string]string{"kubeconfig": "apiVersion: v1\nkind: Config\n", "api_endpoint": "https://192.0.2.40:6443", "join_token": "fake.token", "join_hash": "sha256:deadbeef", "join_certificate_key": "fake-certificate-key"})
	s.kubernetesHeartbeat(workerID, []k8s.Result{{JobID: jobID, ClusterID: clusterID, State: "SUCCEEDED", Progress: 100, Data: resultData}}, nil)
}

func TestKubernetesCompatibilityAndHA(t *testing.T) {
	t.Setenv("CLOFIX_K8S_E2E", "")
	s, memory := newSrv(t)
	for i := 0; i < 3; i++ {
		w := kubernetesWorkerFixture()
		w.ID = "wrk-k8s-" + string(rune('a'+i))
		w.Hostname = w.ID
		memory.UpsertWorker(w)
	}
	compat := do(s.AdminHandler(), "GET", "/api/v1/kubernetes/compatibility", "", tok)
	if compat.Code != 200 || !strings.Contains(compat.Body.String(), `"customer_workloads":false`) {
		t.Fatal(compat.Code, compat.Body.String())
	}
	created := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"ha","version":"1.31","ha":true,"control_plane_count":3,"node_count":3,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID                string `json:"id"`
			HA                bool   `json:"ha"`
			ControlPlaneCount int    `json:"control_plane_count"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	if !out.Cluster.HA || out.Cluster.ControlPlaneCount != 3 {
		t.Fatalf("HA cluster not recorded: %+v", out.Cluster)
	}
	overview := do(s.AdminHandler(), "GET", "/api/v1/kubernetes", "", tok)
	if overview.Code != 200 || strings.Contains(overview.Body.String(), `"production_ready":true`) {
		t.Fatal("lab/fake workers must not claim production-ready without E2E", overview.Body.String())
	}
}

func TestKubernetesProductionReadyWithRealWorkerAndE2E(t *testing.T) {
	t.Setenv("CLOFIX_K8S_E2E", "1")
	s, memory := newSrv(t)
	worker := kubernetesWorkerFixture()
	worker.Capacity.KubernetesFake = false
	memory.UpsertWorker(worker)
	overview := do(s.AdminHandler(), "GET", "/api/v1/kubernetes", "", tok)
	if overview.Code != 200 || !strings.Contains(overview.Body.String(), `"production_ready":true`) {
		t.Fatal("expected production_ready with real worker + CLOFIX_K8S_E2E=1", overview.Body.String())
	}
	if !strings.Contains(overview.Body.String(), `"private_api_access"`) {
		t.Fatal("missing private_api_access guidance", overview.Body.String())
	}
}

func TestKubernetesHelmStorageBackupAndUpgrade(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	created := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"ops","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	completeKubernetesCluster(t, s, "wrk-k8s", out.Cluster.ID, out.JobID)
	base := "/api/v1/kubernetes/clusters/" + out.Cluster.ID
	helm := do(s.AdminHandler(), "POST", base+"/helm", `{"release":"web","chart":"demo/web","action":"install"}`, tok)
	if helm.Code != 202 {
		t.Fatal(helm.Body.String())
	}
	var helmJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(helm.Body.Bytes(), &helmJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: helmJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100}}, nil)
	storage := do(s.AdminHandler(), "POST", base+"/storage", `{"install":true}`, tok)
	if storage.Code != 202 {
		t.Fatal(storage.Body.String())
	}
	var storageJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(storage.Body.Bytes(), &storageJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: storageJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: []byte(`{"volumes":[{"name":"cks-local-path","kind":"StorageClass"}]}`)}}, nil)
	backup := do(s.AdminHandler(), "POST", base+"/backups", `{}`, tok)
	if backup.Code != 202 {
		t.Fatal(backup.Body.String())
	}
	var backed struct {
		Backup struct {
			ID string `json:"id"`
		} `json:"backup"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(backup.Body.Bytes(), &backed)
	snap, _ := json.Marshal(map[string]any{"backup": map[string]any{"id": backed.Backup.ID, "size_bytes": 12, "digest": "abc"}, "snapshot": []byte("etcd-bytes")})
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: backed.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: snap}}, nil)
	s.kubernetes.Lock()
	stored := s.kubernetes.Clusters[out.Cluster.ID].Backups[0]
	s.kubernetes.Unlock()
	if len(stored.Snapshot) == 0 || strings.Contains(string(stored.Snapshot), "etcd-bytes") {
		t.Fatal("backup snapshot must be encrypted")
	}
	upgrade := do(s.AdminHandler(), "POST", base+"/upgrade", `{"version":"1.32"}`, tok)
	if upgrade.Code != 202 {
		t.Fatal(upgrade.Body.String())
	}
}

func TestKubernetesResourcesCatalogAndCloFixAttach(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	created := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"ui","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	completeKubernetesCluster(t, s, "wrk-k8s", out.Cluster.ID, out.JobID)
	base := "/api/v1/kubernetes/clusters/" + out.Cluster.ID
	catalog := do(s.AdminHandler(), "GET", base+"/catalog", "", tok)
	if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), `"load_balancers"`) || !strings.Contains(catalog.Body.String(), `"volumes"`) {
		t.Fatal(catalog.Code, catalog.Body.String())
	}
	pv := do(s.AdminHandler(), "POST", base+"/workloads", `{"kind":"pv","name":"data","storage":"2Gi"}`, tok)
	if pv.Code != 202 {
		t.Fatal(pv.Body.String())
	}
	var pvJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(pv.Body.Bytes(), &pvJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: pvJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: []byte(`{"persistent_volumes":[{"name":"data","kind":"PersistentVolume","storage":"2Gi"}]}`)}}, nil)
	apply := do(s.AdminHandler(), "POST", base+"/workloads", `{"kind":"deployment","name":"web","namespace":"default","image":"nginx:1.27-alpine","replicas":1}`, tok)
	if apply.Code != 202 {
		t.Fatal(apply.Body.String())
	}
	var applyJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(apply.Body.Bytes(), &applyJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: applyJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: []byte(`{"namespaces":["default"],"deployments":[{"name":"web","namespace":"default","kind":"Deployment","replicas":1,"ready":1}],"secrets":[{"name":"app","namespace":"default","kind":"Secret"}]}`)}}, nil)
	workloads := do(s.AdminHandler(), "GET", base+"/workloads", "", tok)
	if workloads.Code != 200 || !strings.Contains(workloads.Body.String(), `"web"`) || !strings.Contains(workloads.Body.String(), `"secrets"`) {
		t.Fatal(workloads.Body.String())
	}
	del := do(s.AdminHandler(), "POST", base+"/workloads", `{"action":"delete","kind":"Deployment","name":"web","namespace":"default"}`, tok)
	if del.Code != 202 {
		t.Fatal(del.Body.String())
	}
	var delJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(del.Body.Bytes(), &delJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: delJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: []byte(`{"namespaces":["default"]}`)}}, nil)
	s.balancers.Items["lb-cks"] = &loadBalancer{ID: "lb-cks", Name: "edge", Mode: "http", Algorithm: "round_robin"}
	attach := do(s.AdminHandler(), "POST", base+"/integrations", `{"kind":"lb","resource_id":"lb-cks","namespace":"default","name":"edge","selector":"web"}`, tok)
	if attach.Code != 202 {
		t.Fatal(attach.Body.String())
	}
	listed := do(s.AdminHandler(), "GET", base+"/integrations", "", tok)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), `"lb"`) {
		t.Fatal(listed.Body.String())
	}
}

func TestKubernetesEditClusterUpdateNodeAndVPSGroup(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	second := kubernetesWorkerFixture()
	second.ID = "wrk-k8s-b"
	second.Hostname = "k8s-worker-b"
	memory.UpsertWorker(second)
	created := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"lab","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID    string `json:"id"`
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	completeKubernetesCluster(t, s, "wrk-k8s", out.Cluster.ID, out.JobID)
	base := "/api/v1/kubernetes/clusters/" + out.Cluster.ID
	edited := do(s.AdminHandler(), "PUT", base, `{"name":"lab-edit","delete_protection":true}`, tok)
	if edited.Code != 200 || !strings.Contains(edited.Body.String(), `"lab-edit"`) || !strings.Contains(edited.Body.String(), `"delete_protection":true`) {
		t.Fatal(edited.Code, edited.Body.String())
	}
	if len(out.Cluster.Nodes) == 0 {
		t.Fatal("cluster node missing")
	}
	updated := do(s.AdminHandler(), "PUT", base+"/nodes/"+out.Cluster.Nodes[0].ID, `{"cpu":2,"ram_mb":4096,"disk_mb":20480}`, tok)
	if updated.Code != 202 || !strings.Contains(updated.Body.String(), `"ram_mb":4096`) {
		t.Fatal(updated.Code, updated.Body.String())
	}
	var nodeJob struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(updated.Body.Bytes(), &nodeJob)
	s.kubernetesHeartbeat("wrk-k8s", []k8s.Result{{JobID: nodeJob.JobID, ClusterID: out.Cluster.ID, State: "SUCCEEDED", Progress: 100, Data: []byte(`{"version":"1.31"}`)}}, nil)
	group := do(s.AdminHandler(), "POST", base+"/node-pools", `{"name":"app-vps","kind":"vps","count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240,"worker_id":"wrk-k8s-b"}`, tok)
	if group.Code != 202 || !strings.Contains(group.Body.String(), `"kind":"vps-group"`) || !strings.Contains(group.Body.String(), `"app-vps"`) {
		t.Fatal(group.Code, group.Body.String())
	}
	nodes := do(s.AdminHandler(), "GET", base+"/nodes", "", tok)
	if nodes.Code != 200 || !strings.Contains(nodes.Body.String(), `"vps-group"`) {
		t.Fatal(nodes.Code, nodes.Body.String())
	}
}

func TestKubernetesVPSGroupTwoNodesOnFakeWorker(t *testing.T) {
	s, memory := newSrv(t)
	fake := kubernetesWorkerFixture()
	fake.Capacity.KubernetesFake = true
	fake.Capacity.CPUCores = 2
	fake.Capacity.CPUAllocated = 1
	fake.Capacity.RAMTotal = 4 << 30
	fake.Capacity.HostReserve = 512 << 20
	fake.Capacity.RAMCommitted = 3 << 30
	fake.Capacity.MemAvailable = 512 << 20
	memory.UpsertWorker(fake)
	created := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"lab","version":"1.31","node_count":1,"cpu":1,"ram_mb":1024,"disk_mb":5120}`, tok)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
		JobID string `json:"job_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	completeKubernetesCluster(t, s, "wrk-k8s", out.Cluster.ID, out.JobID)
	group := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters/"+out.Cluster.ID+"/node-pools", `{"name":"app-vps","kind":"vps-group","count":2,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok)
	if group.Code != 202 || !strings.Contains(group.Body.String(), `"kind":"vps-group"`) || !strings.Contains(group.Body.String(), `"count":2`) {
		t.Fatal(group.Code, group.Body.String())
	}
	got := do(s.AdminHandler(), "GET", "/api/v1/kubernetes/clusters/"+out.Cluster.ID+"/nodes", "", tok)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if strings.Count(got.Body.String(), `"role":"worker"`) < 2 {
		t.Fatal("expected two VPS worker nodes", got.Body.String())
	}
}

func TestKubernetesLicenseQuotaZero(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(kubernetesWorkerFixture())
	zero := 0
	s.license.Grant.Plan.MaxKubernetesClusters = &zero
	if got := do(s.AdminHandler(), "POST", "/api/v1/kubernetes/clusters", `{"name":"blocked","version":"1.31","node_count":1,"cpu":1,"ram_mb":2048,"disk_mb":10240}`, tok); got.Code != 403 {
		t.Fatal("zero CKS quota must block create", got.Code, got.Body.String())
	}
}
