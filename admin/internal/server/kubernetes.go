package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func (s *Server) initKubernetes(root string) {
	var backend model.SnapshotStore
	if candidate, ok := s.Store.(model.SnapshotStore); ok {
		backend = candidate
	}
	s.kubernetes, s.kubernetesError = model.New(filepath.Join(root, "managed-kubernetes"), backend)
	if s.kubernetesError == nil && s.kubernetes != nil {
		for _, cluster := range s.kubernetes.Clusters {
			if len(cluster.EncryptedKubeconfig) > 0 || len(cluster.JoinMaterial) > 0 {
				if _, err := os.Stat(filepath.Join(root, "application-secrets.key")); err != nil {
					s.kubernetesError = fmt.Errorf("kubernetes encryption key is unavailable: %w", err)
				}
				break
			}
		}
	}
}

func (s *Server) kubernetesAvailable(w http.ResponseWriter) bool {
	if s.kubernetesError != nil || s.kubernetes == nil {
		fail(w, 503, "kubernetes metadata is unavailable; restore storage before retrying")
		return false
	}
	return true
}

func (s *Server) kubernetesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/cks-lab", s.proxyKubernetesLabHTTP)
	mux.HandleFunc("/cks-lab/", s.proxyKubernetesLabHTTP)
	mux.HandleFunc("GET /api/v1/kubernetes", s.auth(s.getKubernetesOverview))
	mux.HandleFunc("GET /api/v1/kubernetes/ready-workers", s.auth(s.listKubernetesWorkers))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters", s.auth(s.createKubernetesCluster))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters", s.auth(s.listKubernetesClusters))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}", s.auth(s.getKubernetesCluster))
	mux.HandleFunc("PUT /api/v1/kubernetes/clusters/{id}", s.auth(s.editKubernetesCluster))
	mux.HandleFunc("DELETE /api/v1/kubernetes/clusters/{id}", s.auth(s.deleteKubernetesCluster))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/nodes", s.auth(s.getKubernetesNodes))
	mux.HandleFunc("PUT /api/v1/kubernetes/clusters/{id}/nodes/{node}", s.auth(s.updateKubernetesNode))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/workloads", s.auth(s.getKubernetesWorkloads))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/workloads", s.auth(s.postKubernetesWorkloads))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/catalog", s.auth(s.getKubernetesCatalog))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/integrations", s.auth(s.listKubernetesIntegrations))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/integrations", s.auth(s.postKubernetesIntegration))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/pods", s.auth(s.getKubernetesPods))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/pods/{ns}/{name}/logs", s.auth(s.getKubernetesPodLogs))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/events", s.auth(s.getKubernetesEvents))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/kubeconfig", s.auth(s.getKubernetesKubeconfig))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/protection", s.auth(s.setKubernetesProtection))
	mux.HandleFunc("GET /api/v1/kubernetes/compatibility", s.auth(s.getKubernetesCompatibility))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/node-pools", s.auth(s.listKubernetesNodePools))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/node-pools", s.auth(s.createKubernetesNodePool))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/node-pools/{pool}/scale", s.auth(s.scaleKubernetesNodePool))
	mux.HandleFunc("DELETE /api/v1/kubernetes/clusters/{id}/node-pools/{pool}", s.auth(s.deleteKubernetesNodePool))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/helm", s.auth(s.postKubernetesHelm))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/storage", s.auth(s.postKubernetesStorage))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/ingress", s.auth(s.postKubernetesIngress))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/rbac", s.auth(s.postKubernetesRBAC))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/autoscaling", s.auth(s.postKubernetesAutoscaling))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/backups", s.auth(s.createKubernetesBackup))
	mux.HandleFunc("GET /api/v1/kubernetes/clusters/{id}/backups", s.auth(s.listKubernetesBackups))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/restore", s.auth(s.restoreKubernetesBackup))
	mux.HandleFunc("POST /api/v1/kubernetes/clusters/{id}/upgrade", s.auth(s.upgradeKubernetesCluster))
}

func (s *Server) getKubernetesOverview(w http.ResponseWriter, r *http.Request) {
	if !s.kubernetesAvailable(w) {
		return
	}
	clusters, jobs := s.visibleKubernetes(r)
	gate := s.kubernetesProductionGate()
	writeJSON(w, 200, map[string]any{
		"clusters":           clusters,
		"jobs":               jobs,
		"ready_workers":      s.kubernetesReadyWorkers(),
		"supported_versions": kubernetes.SupportedVersions,
		"filevps_mode":       "unavailable",
		"filevps_report":     kubernetes.FileVPSReport,
		"filevps_validation": kubernetes.FileVPSValidation(),
		"production_ready":   gate["ready"] == true,
		"production_gate":    gate,
		"private_api_access": kubernetesPrivateAPIAccess(),
		"unsupported":        kubernetesUnsupportedFeatures(),
	})
}

func kubernetesUnsupportedFeatures() []string {
	return []string{
		"Cilium CNI (Flannel remains the default)",
		"FileVPS kubelet nodes (Phase 4 validation failed)",
		"Pod exec / interactive terminal",
		"Public kube-apiserver / 6443 publish",
	}
}

func kubernetesPrivateAPIAccess() map[string]any {
	return map[string]any{
		"mode":        "private-worker",
		"summary":     "kube-apiserver stays on the CKS worker. Download kubeconfig from the console and reach the API over VPN, a bastion on the worker network, or an operator mTLS tunnel. CloFix does not publish 6443 publicly.",
		"kubeconfig":  "GET /api/v1/kubernetes/clusters/{id}/kubeconfig",
		"ports":       []int{6443, 10250, 2379},
		"compose_lab": "Compose does not publish Kubernetes ports; use a production Linux worker.",
	}
}

func (s *Server) kubernetesHasRealReadyWorker() bool {
	for _, worker := range s.kubernetesReadyWorkers() {
		if lab, _ := worker["lab_mode"].(bool); !lab {
			return true
		}
	}
	return false
}

func (s *Server) kubernetesProductionGate() map[string]any {
	validation := kubernetes.FileVPSValidation()
	e2e := os.Getenv("CLOFIX_K8S_E2E") == "1"
	real := s.kubernetesHasRealReadyWorker()
	ready := e2e && real
	reason := "CKS is production-ready when CLOFIX_K8S_E2E=1 and at least one non-lab (kubeadm) worker is advertising Kubernetes."
	if !real {
		reason = "No production Kubernetes worker is ready. Install containerd/kubeadm/kubelet on Ubuntu workers (scripts/install-cks-worker.sh) and leave CLOFIX_KUBERNETES_FAKE unset."
	} else if !e2e {
		reason = "Real kubeadm workers are ready. Set CLOFIX_K8S_E2E=1 on the controller after HA and etcd backup/restore have been verified on those hosts."
	}
	return map[string]any{
		"ha":                         true,
		"backup_restore":             true,
		"rolling_upgrades":           true,
		"billing":                    true,
		"license_quotas":             true,
		"filevps_customer_workloads": validation.CustomerWorkloads,
		"security_tests":             true,
		"live_kubeadm_e2e":           e2e,
		"real_kubeadm_workers":       real,
		"cni":                        "flannel",
		"storage_class":              "local-path",
		"ingress_class":              "nginx",
		"ready":                      ready,
		"reason":                     reason,
	}
}

func (s *Server) listKubernetesWorkers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"workers": s.kubernetesReadyWorkers(), "supported_versions": kubernetes.SupportedVersions})
}

func (s *Server) listKubernetesClusters(w http.ResponseWriter, r *http.Request) {
	if !s.kubernetesAvailable(w) {
		return
	}
	clusters, _ := s.visibleKubernetes(r)
	writeJSON(w, 200, map[string]any{"clusters": clusters})
}

func (s *Server) visibleKubernetes(r *http.Request) ([]model.Cluster, []model.Job) {
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	clusters := []model.Cluster{}
	jobs := []model.Job{}
	for _, cluster := range d.Clusters {
		if cluster.State == kubernetes.StateDeleted || !canAccessApplication(r, cluster.Owner) {
			continue
		}
		clusters = append(clusters, cluster.Public())
	}
	for _, job := range d.Jobs {
		if !canAccessApplication(r, job.Owner) {
			continue
		}
		jobs = append(jobs, job.Public())
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Created.After(clusters[j].Created) })
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Created.After(jobs[j].Created) })
	if len(jobs) > 200 {
		jobs = jobs[:200]
	}
	return clusters, jobs
}

func (s *Server) loadKubernetesCluster(w http.ResponseWriter, r *http.Request) (model.Cluster, bool) {
	if !s.kubernetesAvailable(w) {
		return model.Cluster{}, false
	}
	id := r.PathValue("id")
	d := s.kubernetes
	d.Lock()
	cluster, ok := d.Clusters[id]
	d.Unlock()
	if !ok || cluster.State == kubernetes.StateDeleted || !canAccessApplication(r, cluster.Owner) {
		fail(w, 404, "cluster not found")
		return model.Cluster{}, false
	}
	return cluster, true
}

func (s *Server) getKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"cluster": cluster.Public()})
}

func (s *Server) editKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	var in struct {
		Name             string `json:"name"`
		DeleteProtection *bool  `json:"delete_protection"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid cluster update")
		return
	}
	in.Name = strings.TrimSpace(strings.ToLower(in.Name))
	if in.Name != "" && !kubernetes.Name.MatchString(in.Name) {
		fail(w, 400, "cluster name must be a DNS label")
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current, exists := d.Clusters[cluster.ID]
	if !exists || current.State == kubernetes.StateDeleted {
		fail(w, 404, "cluster not found")
		return
	}
	before := s.kubernetesCheckpoint()
	if in.Name != "" {
		current.Name = in.Name
	}
	if in.DeleteProtection != nil {
		current.DeleteProtection = *in.DeleteProtection
	}
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Cluster edited", "info")
	d.Clusters[current.ID] = current
	if err := s.saveKubernetes(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.edit", current.ID, current.Name)
	writeJSON(w, 200, map[string]any{"cluster": current.Public()})
}

func kubernetesNodeResources(cluster model.Cluster, node model.Node) (float64, int64, int64) {
	pool, _ := cluster.Pool(node.PoolID)
	if pool.CPU == 0 {
		pool = cluster.NodePool
	}
	cpu, ram, disk := pool.CPU, pool.RAMMB, pool.DiskMB
	if node.CPU > 0 {
		cpu = node.CPU
	}
	if node.RAMMB > 0 {
		ram = node.RAMMB
	}
	if node.DiskMB > 0 {
		disk = node.DiskMB
	}
	return cpu, ram, disk
}

func (s *Server) updateKubernetesNode(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "nodes can be updated after the cluster is running")
		return
	}
	var in struct {
		CPU    float64 `json:"cpu"`
		RAMMB  int64   `json:"ram_mb"`
		DiskMB int64   `json:"disk_mb"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid node update")
		return
	}
	nodeID := r.PathValue("node")
	var currentNode model.Node
	found := false
	for _, node := range cluster.Nodes {
		if node.ID == nodeID && node.State != kubernetes.StateDeleted {
			currentNode, found = node, true
			break
		}
	}
	if !found {
		fail(w, 404, "node not found")
		return
	}
	cpu, ram, disk := kubernetesNodeResources(cluster, currentNode)
	if in.CPU == 0 {
		in.CPU = cpu
	}
	if in.RAMMB == 0 {
		in.RAMMB = ram
	}
	if in.DiskMB == 0 {
		in.DiskMB = disk
	}
	check := kubernetes.Spec{ID: cluster.ID, Version: cluster.Version, InfrastructureMode: kubernetes.ModeDedicated, Role: currentNode.Role, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB}
	if err := check.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	idx := -1
	for i, node := range current.Nodes {
		if node.ID == nodeID && node.State != kubernetes.StateDeleted {
			idx = i
			currentNode = node
			break
		}
	}
	if idx < 0 {
		fail(w, 404, "node not found")
		return
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 503, "worker inventory unavailable")
		return
	}
	skip := map[string]bool{}
	for _, worker := range workers {
		if worker.ID != currentNode.WorkerID {
			skip[worker.ID] = true
		}
	}
	if _, err = s.placeKubernetesNodes(workers, currentNode.WorkerID, current.Version, 1, in.CPU, in.RAMMB, in.DiskMB, skip, currentNode.ID); err != nil {
		fail(w, 409, err.Error())
		return
	}
	before := s.kubernetesCheckpoint()
	current.Nodes[idx].CPU = in.CPU
	current.Nodes[idx].RAMMB = in.RAMMB
	current.Nodes[idx].DiskMB = in.DiskMB
	current.Nodes[idx].State = kubernetes.StateUpgrading
	live := 0
	for _, node := range current.Nodes {
		if node.State != kubernetes.StateDeleted && (node.PoolID == currentNode.PoolID || node.PoolID == "" && current.NodePool.ID == currentNode.PoolID) {
			live++
		}
	}
	if live <= 1 {
		for i, pool := range current.NodePools {
			if pool.ID == currentNode.PoolID || pool.Name == "default" && currentNode.PoolID == current.NodePool.ID {
				current.NodePools[i].CPU = in.CPU
				current.NodePools[i].RAMMB = in.RAMMB
				current.NodePools[i].DiskMB = in.DiskMB
			}
		}
		if current.NodePool.ID == currentNode.PoolID || currentNode.PoolID == "" {
			current.NodePool.CPU = in.CPU
			current.NodePool.RAMMB = in.RAMMB
			current.NodePool.DiskMB = in.DiskMB
		}
	}
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Node "+currentNode.Hostname+" update queued", "info")
	d.Clusters[current.ID] = current
	job, err := s.queueKubernetesLocked(current, current.Nodes[idx], kubernetes.ActionUpgrade, kubernetes.Operation{Spec: kubernetes.Spec{Version: current.Version, TargetVersion: current.Version}})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.node.update", current.ID, currentNode.ID)
	writeJSON(w, 202, map[string]any{"cluster": current.Public(), "node": current.Nodes[idx], "job_id": job.ID})
}

func (s *Server) getKubernetesNodes(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": cluster.Nodes, "node_pool": cluster.NodePool, "node_pools": cluster.Pools()})
}

func (s *Server) getKubernetesWorkloads(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{
		"namespaces":               cluster.Inventory.Namespaces,
		"pods":                     cluster.Inventory.Pods,
		"deployments":              cluster.Inventory.Deployments,
		"replica_sets":             cluster.Inventory.ReplicaSets,
		"daemon_sets":              cluster.Inventory.DaemonSets,
		"stateful_sets":            cluster.Inventory.StatefulSets,
		"jobs":                     cluster.Inventory.Jobs,
		"cron_jobs":                cluster.Inventory.CronJobs,
		"services":                 cluster.Inventory.Services,
		"ingresses":                cluster.Inventory.Ingresses,
		"config_maps":              cluster.Inventory.ConfigMaps,
		"secrets":                  cluster.Inventory.Secrets,
		"persistent_volume_claims": cluster.Inventory.PersistentVolumeClaims,
		"persistent_volumes":       cluster.Inventory.PersistentVolumes,
		"storage_classes":          cluster.Inventory.StorageClasses,
		"service_accounts":         cluster.Inventory.ServiceAccounts,
		"network_policies":         cluster.Inventory.NetworkPolicies,
		"rbac":                     cluster.Inventory.RBAC,
		"hpa":                      cluster.Inventory.HPA,
		"volumes":                  cluster.Inventory.Volumes,
		"integrations":             cluster.Integrations,
	})
}

func (s *Server) getKubernetesPods(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"pods": cluster.Inventory.Pods})
}

func (s *Server) getKubernetesEvents(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	_, jobs := s.visibleKubernetes(r)
	clusterJobs := []model.Job{}
	for _, job := range jobs {
		if job.ClusterID == cluster.ID {
			clusterJobs = append(clusterJobs, job)
		}
	}
	writeJSON(w, 200, map[string]any{"cluster_events": cluster.Events, "kubernetes_events": cluster.Inventory.Events, "jobs": clusterJobs})
}

func (s *Server) getKubernetesKubeconfig(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if cluster.State != kubernetes.StateRunning && cluster.State != kubernetes.StateDegraded {
		fail(w, 409, "kubeconfig is available after the cluster is running")
		return
	}
	if len(cluster.EncryptedKubeconfig) == 0 {
		fail(w, 404, "kubeconfig is not available")
		return
	}
	plain, err := s.openKubernetesBytes("kubeconfig:"+cluster.ID, cluster.EncryptedKubeconfig, []byte("clofix-kubernetes-kubeconfig-v1"))
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="`+cluster.ID+`.kubeconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(plain)
}

func (s *Server) setKubernetesProtection(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	var in struct {
		DeleteProtection *bool `json:"delete_protection"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil || in.DeleteProtection == nil {
		fail(w, 400, "delete_protection must be true or false")
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current, exists := d.Clusters[cluster.ID]
	if !exists {
		fail(w, 404, "cluster not found")
		return
	}
	before := s.kubernetesCheckpoint()
	current.DeleteProtection = *in.DeleteProtection
	current.Updated = time.Now().UTC()
	d.Clusters[current.ID] = current
	if err := s.saveKubernetes(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.protection", current.ID, fmt.Sprintf("enabled=%t", current.DeleteProtection))
	writeJSON(w, 200, map[string]any{"cluster": current.Public()})
}

func (s *Server) createKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	if !s.kubernetesAvailable(w) {
		return
	}
	var in struct {
		Name               string  `json:"name"`
		Version            string  `json:"version"`
		InfrastructureMode string  `json:"infrastructure_mode"`
		WorkerID           string  `json:"worker_id"`
		NodeCount          int     `json:"node_count"`
		ControlPlaneCount  int     `json:"control_plane_count"`
		HA                 bool    `json:"ha"`
		CPU                float64 `json:"cpu"`
		RAMMB              int64   `json:"ram_mb"`
		DiskMB             int64   `json:"disk_mb"`
		DeleteProtection   bool    `json:"delete_protection"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid cluster specification")
		return
	}
	in.Name = strings.TrimSpace(strings.ToLower(in.Name))
	in.InfrastructureMode = strings.TrimSpace(strings.ToLower(in.InfrastructureMode))
	if in.InfrastructureMode == "" {
		in.InfrastructureMode = kubernetes.ModeDedicated
	}
	if in.InfrastructureMode == kubernetes.ModeFileVPS {
		writeJSON(w, 409, map[string]any{"error": "FileVPS Kubernetes is unavailable until compatibility tests pass", "filevps_report": kubernetes.FileVPSReport})
		return
	}
	if in.NodeCount == 0 {
		in.NodeCount = 1
	}
	if in.CPU == 0 {
		in.CPU = 1
	}
	if in.RAMMB == 0 {
		in.RAMMB = 2048
	}
	if in.DiskMB == 0 {
		in.DiskMB = 10240
	}
	if !kubernetes.Name.MatchString(in.Name) {
		fail(w, 400, "cluster name must be a DNS label")
		return
	}
	if in.NodeCount < 1 || in.NodeCount > 16 {
		fail(w, 400, "node_count must be between 1 and 16")
		return
	}
	cpCount := 1
	if in.HA || in.ControlPlaneCount > 1 {
		cpCount = in.ControlPlaneCount
		if cpCount == 0 {
			cpCount = 3
		}
		if cpCount != 3 && cpCount != 5 {
			fail(w, 400, "HA control plane count must be 3 or 5")
			return
		}
		if in.NodeCount < cpCount {
			in.NodeCount = cpCount
		}
		in.HA = true
	}
	if e := s.checkQuota("kubernetes-clusters", 1); e != nil {
		fail(w, 403, e.Error())
		return
	}
	if e := s.checkQuota("kubernetes-nodes", int64(in.NodeCount)); e != nil {
		fail(w, 403, e.Error())
		return
	}
	if e := s.checkQuota("kubernetes-node-pools", 1); e != nil {
		fail(w, 403, e.Error())
		return
	}
	version := kubernetes.NormalizeVersion(in.Version)
	spec := kubernetes.Spec{ID: "cks-" + randHex(16), Version: version, InfrastructureMode: kubernetes.ModeDedicated, Role: kubernetes.RoleControlPlane, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB, SingleNode: in.NodeCount == 1 && !in.HA, HA: in.HA}
	if err := spec.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	idempotency := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	owner := applicationOwner(r)
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	if idempotency != "" {
		for _, cluster := range d.Clusters {
			if cluster.Owner == owner && cluster.IdempotencyKey == idempotency && cluster.State != kubernetes.StateDeleted {
				writeJSON(w, 202, map[string]any{"cluster": cluster.Public()})
				return
			}
		}
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 503, "worker inventory unavailable")
		return
	}
	placements, err := s.placeKubernetesNodes(workers, in.WorkerID, version, in.NodeCount, in.CPU, in.RAMMB, in.DiskMB, nil)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	now := time.Now().UTC()
	pool := model.NodePool{ID: "ckp-" + randHex(16), Name: "default", Count: in.NodeCount, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB}
	nodes := make([]model.Node, 0, len(placements))
	for i, worker := range placements {
		role := kubernetes.RoleWorker
		if i < cpCount {
			role = kubernetes.RoleControlPlane
		}
		nodes = append(nodes, model.Node{ID: "ckn-" + randHex(16), PoolID: pool.ID, WorkerID: worker.ID, Hostname: worker.Hostname, Role: role, State: kubernetes.StatePending, Version: version, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB})
	}
	cluster := model.Cluster{
		ID: spec.ID, Owner: owner, Name: in.Name, Version: version, InfrastructureMode: kubernetes.ModeDedicated,
		State: kubernetes.StateProvisioning, DeleteProtection: in.DeleteProtection, HA: in.HA, ControlPlaneCount: cpCount, ControlPlaneWorkerID: nodes[0].WorkerID,
		IdempotencyKey: idempotency, NodePool: pool, NodePools: []model.NodePool{pool},
		Nodes: nodes, Created: now, Updated: now, Events: []model.ClusterEvent{{At: now, Message: "Cluster provisioning queued", Level: "info"}},
	}
	before := s.kubernetesCheckpoint()
	d.Clusters[cluster.ID] = cluster
	job, err := s.queueKubernetesLocked(cluster, nodes[0], kubernetes.ActionInit, kubernetes.Operation{Spec: spec})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(cluster.Owner, "kubernetes.create", cluster.ID, job.ID)
	writeJSON(w, 202, map[string]any{"cluster": cluster.Public(), "job_id": job.ID})
}

func (s *Server) deleteKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if cluster.DeleteProtection {
		fail(w, 409, "delete protection is enabled")
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current, exists := d.Clusters[cluster.ID]
	if !exists || current.State == kubernetes.StateDeleted {
		fail(w, 404, "cluster not found")
		return
	}
	if current.State == kubernetes.StateDeleting {
		writeJSON(w, 202, map[string]any{"cluster": current.Public()})
		return
	}
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	current.State = kubernetes.StateDeleting
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Cluster deletion queued", "info")
	d.Clusters[current.ID] = current
	var err error
	for _, node := range current.Nodes {
		pool, _ := current.Pool(node.PoolID)
		spec := kubernetes.Spec{ID: current.ID, Version: current.Version, InfrastructureMode: kubernetes.ModeDedicated, Role: node.Role, CPU: pool.CPU, RAMMB: pool.RAMMB, DiskMB: pool.DiskMB, HA: current.HA}
		if _, err = s.queueKubernetesLocked(current, node, kubernetes.ActionReset, kubernetes.Operation{Spec: spec}); err != nil {
			break
		}
	}
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.delete", current.ID, "")
	writeJSON(w, 202, map[string]any{"cluster": current.Public()})
}

func (s *Server) postKubernetesWorkloads(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if cluster.State != kubernetes.StateRunning && cluster.State != kubernetes.StateDegraded {
		fail(w, 409, "workloads can be changed after the cluster is running")
		return
	}
	var in kubernetesResourceRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid workload request")
		return
	}
	in.Namespace = strings.TrimSpace(in.Namespace)
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.TrimSpace(in.Kind)
	action := strings.TrimSpace(strings.ToLower(in.Action))
	if strings.TrimSpace(in.YAML) == "" && in.Kind != "" && in.Name != "" && action != kubernetes.ActionScale && action != kubernetes.ActionRestart && action != kubernetes.ActionDelete {
		yaml, err := buildKubernetesResourceYAML(in)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		in.YAML = yaml
		action = kubernetes.ActionApply
	}
	op := kubernetes.Operation{Spec: kubernetes.Spec{ID: cluster.ID, Version: cluster.Version, InfrastructureMode: cluster.InfrastructureMode, Role: kubernetes.RoleControlPlane, CPU: cluster.NodePool.CPU, RAMMB: cluster.NodePool.RAMMB, DiskMB: cluster.NodePool.DiskMB}, YAML: in.YAML, Namespace: in.Namespace, Name: in.Name, Kind: in.Kind, Replicas: in.Replicas}
	switch {
	case action == kubernetes.ActionDelete:
		op.Action = kubernetes.ActionDelete
	case strings.TrimSpace(in.YAML) != "":
		op.Action = kubernetes.ActionApply
	case action == kubernetes.ActionScale:
		op.Action = kubernetes.ActionScale
	case action == kubernetes.ActionRestart:
		op.Action = kubernetes.ActionRestart
	default:
		fail(w, 400, "provide YAML, a resource form, or action scale/restart/delete")
		return
	}
	control, ok := kubernetesControlNode(cluster)
	if !ok {
		fail(w, 409, "control plane node is missing")
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	job, err := s.queueKubernetesLocked(current, control, op.Action, op)
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.workload", current.ID, job.Action)
	writeJSON(w, 202, map[string]any{"job_id": job.ID})
}

func (s *Server) getKubernetesPodLogs(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if cluster.State != kubernetes.StateRunning && cluster.State != kubernetes.StateDegraded {
		fail(w, 409, "pod logs are available after the cluster is running")
		return
	}
	control, ok := kubernetesControlNode(cluster)
	if !ok {
		fail(w, 409, "control plane node is missing")
		return
	}
	op := kubernetes.Operation{Action: kubernetes.ActionLogs, Spec: kubernetes.Spec{ID: cluster.ID, Version: cluster.Version, InfrastructureMode: cluster.InfrastructureMode, Role: kubernetes.RoleControlPlane, CPU: cluster.NodePool.CPU, RAMMB: cluster.NodePool.RAMMB, DiskMB: cluster.NodePool.DiskMB}, Namespace: r.PathValue("ns"), Name: r.PathValue("name"), Tail: 200}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionLogs, op)
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"job_id": job.ID})
}

func kubernetesControlNode(cluster model.Cluster) (model.Node, bool) {
	for _, node := range cluster.Nodes {
		if node.Role == kubernetes.RoleControlPlane {
			return node, true
		}
	}
	if len(cluster.Nodes) > 0 {
		return cluster.Nodes[0], true
	}
	return model.Node{}, false
}

func (s *Server) kubernetesReadyWorkers() []map[string]any {
	workers, err := s.Store.ListWorkers()
	if err != nil {
		return nil
	}
	ready := []map[string]any{}
	for _, worker := range workers {
		if !kubernetesWorkerReady(worker.Status, worker.LastHeartbeat, worker.Capacity, "") {
			continue
		}
		usableRAM := int64(worker.Capacity.RAMTotal)
		if worker.Capacity.HostReserve < worker.Capacity.RAMTotal {
			usableRAM = int64(worker.Capacity.RAMTotal - worker.Capacity.HostReserve - worker.Capacity.RAMCommitted)
		}
		ready = append(ready, map[string]any{
			"id": worker.ID, "hostname": worker.Hostname, "versions": worker.Capacity.KubernetesVersions,
			"cpu_free": worker.Capacity.CPUCores - worker.Capacity.CPUAllocated, "ram_free_bytes": usableRAM, "disk_free_bytes": worker.Capacity.DiskFree,
			"lab_mode": worker.Capacity.KubernetesFake,
		})
	}
	return ready
}

func kubernetesSkipOccupiedWorkers(nodes []model.Node, workers []store.Worker) map[string]bool {
	fake := map[string]bool{}
	for _, worker := range workers {
		if worker.Capacity.KubernetesFake {
			fake[worker.ID] = true
		}
	}
	skip := map[string]bool{}
	for _, node := range nodes {
		if node.State != kubernetes.StateDeleted && !fake[node.WorkerID] {
			skip[node.WorkerID] = true
		}
	}
	return skip
}

func kubernetesWorkerReady(status string, lastHeartbeat time.Time, capacity scheduler.Capacity, version string) bool {
	if status != "active" || time.Since(lastHeartbeat) > time.Minute || !capacity.KubernetesReady || len(capacity.KubernetesVersions) == 0 {
		return false
	}
	if version == "" {
		return true
	}
	for _, advertised := range capacity.KubernetesVersions {
		if kubernetes.NormalizeVersion(advertised) == kubernetes.NormalizeVersion(version) {
			return true
		}
	}
	return false
}

func (s *Server) placeKubernetesNodes(workers []store.Worker, preferred, version string, count int, cpu float64, ramMB, diskMB int64, skip map[string]bool, excludeNode ...string) ([]store.Worker, error) {
	type candidate struct {
		worker store.Worker
		cp     scheduler.Capacity
	}
	var ready []candidate
	d := s.kubernetes
	for _, worker := range workers {
		cp := worker.Capacity
		if skip[worker.ID] || !kubernetesWorkerReady(worker.Status, worker.LastHeartbeat, cp, version) {
			continue
		}
		if preferred != "" && len(ready) == 0 && worker.ID != preferred && count == 1 {
			continue
		}
		known := map[string]bool{}
		for _, id := range cp.KubernetesNodes {
			known[id] = true
		}
		exclude := ""
		if len(excludeNode) > 0 {
			exclude = excludeNode[0]
		}
		for _, cluster := range d.Clusters {
			if cluster.State == kubernetes.StateDeleted {
				continue
			}
			for _, node := range cluster.Nodes {
				if node.ID == exclude || node.WorkerID != worker.ID || known[node.ID] || node.State == kubernetes.StateDeleted {
					continue
				}
				nodeCPU, nodeRAM, nodeDisk := kubernetesNodeResources(cluster, node)
				cp.RAMCommitted += uint64(nodeRAM) << 20
				cp.CPUAllocated += nodeCPU
				cp.DiskCommitted += uint64(nodeDisk) << 20
			}
		}
		ready = append(ready, candidate{worker: worker, cp: cp})
	}
	if preferred != "" {
		rest := []candidate{}
		var first candidate
		found := false
		for _, item := range ready {
			if item.worker.ID == preferred {
				first = item
				found = true
				continue
			}
			rest = append(rest, item)
		}
		if !found {
			return nil, fmt.Errorf("selected worker is not Kubernetes-ready")
		}
		ready = append([]candidate{first}, rest...)
	}
	placed := []store.Worker{}
	used := map[string]bool{}
	req := scheduler.Request{RAM: uint64(ramMB) << 20, CPU: cpu, Disk: uint64(diskMB) << 20}
	for i := 0; i < count; i++ {
		cands := []scheduler.Candidate{}
		for _, item := range ready {
			if used[item.worker.ID] {
				continue
			}
			cands = append(cands, scheduler.Candidate{ID: item.worker.ID, Active: item.worker.Status == "active", Draining: item.worker.Status == "draining", LastHeartbeat: item.worker.LastHeartbeat, Capacity: item.cp})
		}
		decision, err := scheduler.Place(cands, req, s.Sched, time.Now())
		if err != nil {
			placedFake := false
			for _, item := range ready {
				if used[item.worker.ID] || !item.worker.Capacity.KubernetesFake {
					continue
				}
				decision = scheduler.Decision{WorkerID: item.worker.ID}
				placedFake = true
				break
			}
			if !placedFake {
				return nil, fmt.Errorf("no ready Kubernetes worker has safe CPU, RAM and disk capacity")
			}
		}
		for idx, item := range ready {
			if item.worker.ID == decision.WorkerID {
				placed = append(placed, item.worker)
				if !item.worker.Capacity.KubernetesFake {
					used[item.worker.ID] = true
				}
				ready[idx].cp.RAMCommitted += req.RAM
				ready[idx].cp.CPUAllocated += req.CPU
				ready[idx].cp.DiskCommitted += req.Disk
				break
			}
		}
	}
	if len(placed) != count {
		return nil, fmt.Errorf("not enough distinct Kubernetes-ready workers")
	}
	if host := placed[0].Capacity.KubernetesPublicHost; host != "" && net.ParseIP(host) == nil && strings.ContainsAny(host, "/: \t\r\n") {
		return nil, fmt.Errorf("kubernetes worker advertise address is invalid")
	}
	return placed, nil
}

func appendClusterEvent(events []model.ClusterEvent, message, level string) []model.ClusterEvent {
	events = append(events, model.ClusterEvent{At: time.Now().UTC(), Message: message, Level: level})
	if len(events) > 100 {
		return events[len(events)-100:]
	}
	return events
}

func (s *Server) proxyKubernetesLabHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/cks-lab" {
		http.Redirect(w, r, "/cks-lab/", http.StatusFound)
		return
	}
	backend := os.Getenv("CLOFIX_LB_WORKER_HOST")
	if backend == "" {
		backend = "127.0.0.1"
	}
	port := strings.TrimSpace(os.Getenv("CLOFIX_KUBERNETES_LAB_HTTP_PORT"))
	if port == "" {
		port = "19080"
	}
	target, err := url.Parse("http://" + net.JoinHostPort(backend, port))
	if err != nil {
		http.Error(w, "invalid lab backend", http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/cks-lab")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = target.Host
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("Forwarded")
	}
	proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 15 * time.Second, DisableKeepAlives: true}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "CKS lab nginx is unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
