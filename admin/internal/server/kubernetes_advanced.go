package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/worker/pkg/kubernetes"
)

func kubernetesClusterReady(cluster model.Cluster) bool {
	return cluster.State == kubernetes.StateRunning || cluster.State == kubernetes.StateDegraded
}

func kubernetesNodePoolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "vps", "vps-group", "vpsgroup", "filevps-group":
		return "vps-group"
	default:
		return strings.TrimSpace(kind)
	}
}

func (s *Server) getKubernetesCompatibility(w http.ResponseWriter, r *http.Request) {
	gate := s.kubernetesProductionGate()
	writeJSON(w, 200, map[string]any{
		"filevps":            kubernetes.FileVPSValidation(),
		"production_gate":    gate,
		"production_ready":   gate["ready"] == true,
		"private_api_access": kubernetesPrivateAPIAccess(),
	})
}

func (s *Server) listKubernetesNodePools(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"node_pools": cluster.Pools(), "nodes": cluster.Nodes})
}

func (s *Server) createKubernetesNodePool(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "node pools can be added after the cluster is running")
		return
	}
	var in struct {
		Name        string  `json:"name"`
		Kind        string  `json:"kind"`
		Count       int     `json:"count"`
		Min         int     `json:"min"`
		Max         int     `json:"max"`
		CPU         float64 `json:"cpu"`
		RAMMB       int64   `json:"ram_mb"`
		DiskMB      int64   `json:"disk_mb"`
		WorkerID    string  `json:"worker_id"`
		Autoscaling bool    `json:"autoscaling"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid node pool")
		return
	}
	in.Name = strings.TrimSpace(strings.ToLower(in.Name))
	if !kubernetes.Name.MatchString(in.Name) {
		fail(w, 400, "node pool name must be a DNS label")
		return
	}
	in.Kind = kubernetesNodePoolKind(in.Kind)
	in.WorkerID = strings.TrimSpace(in.WorkerID)
	if in.Count == 0 {
		in.Count = 1
	}
	if in.Count < 1 || in.Count > 8 {
		fail(w, 400, "node pool count must be between 1 and 8")
		return
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
	if in.Autoscaling {
		if in.Min < 0 || in.Max < in.Min || in.Max > 16 {
			fail(w, 400, "autoscaling min/max must be between 0 and 16")
			return
		}
		if in.Min == 0 && in.Max == 0 {
			in.Min, in.Max = in.Count, in.Count
		}
	}
	if e := s.checkQuota("kubernetes-node-pools", 1); e != nil {
		fail(w, 403, e.Error())
		return
	}
	if e := s.checkQuota("kubernetes-nodes", int64(in.Count)); e != nil {
		fail(w, 403, e.Error())
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	for _, pool := range current.Pools() {
		if pool.Name == in.Name {
			fail(w, 409, "node pool name already exists")
			return
		}
	}
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 503, "worker inventory unavailable")
		return
	}
	skip := kubernetesSkipOccupiedWorkers(current.Nodes, workers)
	placements, err := s.placeKubernetesNodes(workers, in.WorkerID, current.Version, in.Count, in.CPU, in.RAMMB, in.DiskMB, skip)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	pool := model.NodePool{ID: "ckp-" + randHex(16), Name: in.Name, Kind: in.Kind, Count: in.Count, Min: in.Min, Max: in.Max, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB, WorkerID: in.WorkerID, Autoscaling: in.Autoscaling}
	before := s.kubernetesCheckpoint()
	added := []model.Node{}
	for _, worker := range placements {
		node := model.Node{ID: "ckn-" + randHex(16), PoolID: pool.ID, WorkerID: worker.ID, Hostname: worker.Hostname, Role: kubernetes.RoleWorker, State: kubernetes.StatePending, Version: current.Version, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB}
		current.Nodes = append(current.Nodes, node)
		added = append(added, node)
	}
	current.NodePools = append(current.Pools(), pool)
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Node pool "+pool.Name+" queued", "info")
	d.Clusters[current.ID] = current
	var job model.Job
	for _, node := range added {
		job, err = s.queueKubernetesLocked(current, node, kubernetes.ActionJoin, kubernetes.Operation{})
		if err != nil {
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
	s.Store.Audit(current.Owner, "kubernetes.nodepool.create", current.ID, pool.ID)
	writeJSON(w, 202, map[string]any{"cluster": current.Public(), "node_pool": pool, "job_id": job.ID})
}

func (s *Server) scaleKubernetesNodePool(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "node pools can be scaled after the cluster is running")
		return
	}
	var in struct {
		Count int `json:"count"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil || in.Count < 0 || in.Count > 16 {
		fail(w, 400, "count must be between 0 and 16")
		return
	}
	pool, ok := cluster.Pool(r.PathValue("pool"))
	if !ok {
		fail(w, 404, "node pool not found")
		return
	}
	if pool.Name == "default" && in.Count < cluster.ControlPlaneCount {
		in.Count = cluster.ControlPlaneCount
		if in.Count < 1 {
			in.Count = 1
		}
	}
	live := 0
	for _, node := range cluster.Nodes {
		if node.State == kubernetes.StateDeleted {
			continue
		}
		if node.PoolID == pool.ID || node.PoolID == "" && pool.ID == cluster.NodePool.ID {
			live++
		}
	}
	delta := in.Count - live
	if delta > 0 {
		if e := s.checkQuota("kubernetes-nodes", int64(delta)); e != nil {
			fail(w, 403, e.Error())
			return
		}
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	pool, ok = current.Pool(pool.ID)
	if !ok {
		fail(w, 404, "node pool not found")
		return
	}
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	var err error
	var job model.Job
	if delta > 0 {
		workers, listErr := s.Store.ListWorkers()
		if listErr != nil {
			fail(w, 503, "worker inventory unavailable")
			return
		}
		skip := kubernetesSkipOccupiedWorkers(current.Nodes, workers)
		placements, placeErr := s.placeKubernetesNodes(workers, "", current.Version, delta, pool.CPU, pool.RAMMB, pool.DiskMB, skip)
		if placeErr != nil {
			fail(w, 409, placeErr.Error())
			return
		}
		for _, worker := range placements {
			node := model.Node{ID: "ckn-" + randHex(16), PoolID: pool.ID, WorkerID: worker.ID, Hostname: worker.Hostname, Role: kubernetes.RoleWorker, State: kubernetes.StatePending, Version: current.Version, CPU: pool.CPU, RAMMB: pool.RAMMB, DiskMB: pool.DiskMB}
			current.Nodes = append(current.Nodes, node)
			job, err = s.queueKubernetesLocked(current, node, kubernetes.ActionJoin, kubernetes.Operation{})
			if err != nil {
				break
			}
		}
	}
	if delta < 0 {
		removed := 0
		need := -delta
		for i := len(current.Nodes) - 1; i >= 0 && removed < need; i-- {
			node := current.Nodes[i]
			if node.State == kubernetes.StateDeleted || node.Role == kubernetes.RoleControlPlane {
				continue
			}
			if node.PoolID != pool.ID && !(node.PoolID == "" && pool.ID == current.NodePool.ID) {
				continue
			}
			job, err = s.queueKubernetesLocked(current, node, kubernetes.ActionReset, kubernetes.Operation{})
			if err != nil {
				break
			}
			removed++
		}
	}
	for i, item := range current.NodePools {
		if item.ID == pool.ID {
			current.NodePools[i].Count = in.Count
			pool = current.NodePools[i]
		}
	}
	if current.NodePool.ID == pool.ID {
		current.NodePool.Count = in.Count
	}
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, fmt.Sprintf("Node pool %s scaled to %d", pool.Name, in.Count), "info")
	d.Clusters[current.ID] = current
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.nodepool.scale", current.ID, pool.ID)
	writeJSON(w, 202, map[string]any{"cluster": current.Public(), "node_pool": pool, "job_id": job.ID})
}

func (s *Server) deleteKubernetesNodePool(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	poolID := r.PathValue("pool")
	pool, ok := current.Pool(poolID)
	if !ok {
		for _, candidate := range current.Pools() {
			if candidate.Name == poolID {
				pool, ok = candidate, true
			}
		}
	}
	if !ok {
		fail(w, 404, "node pool not found")
		return
	}
	if pool.Name == "default" || pool.ID == current.NodePool.ID {
		fail(w, 409, "the default node pool cannot be deleted")
		return
	}
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	var err error
	var job model.Job
	for _, node := range current.Nodes {
		if node.PoolID != pool.ID || node.State == kubernetes.StateDeleted {
			continue
		}
		job, err = s.queueKubernetesLocked(current, node, kubernetes.ActionReset, kubernetes.Operation{})
		if err != nil {
			break
		}
	}
	kept := []model.NodePool{}
	for _, item := range current.Pools() {
		if item.ID != pool.ID {
			kept = append(kept, item)
		}
	}
	current.NodePools = kept
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Node pool "+pool.Name+" deletion queued", "info")
	d.Clusters[current.ID] = current
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.nodepool.delete", current.ID, pool.ID)
	writeJSON(w, 202, map[string]any{"cluster": current.Public(), "job_id": job.ID})
}

func (s *Server) queueClusterManifest(w http.ResponseWriter, r *http.Request, action, yaml, audit string) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "cluster must be running")
		return
	}
	if strings.TrimSpace(yaml) == "" {
		fail(w, 400, "manifest YAML is required")
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
	job, err := s.queueKubernetesLocked(current, control, action, kubernetes.Operation{YAML: yaml})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, audit, current.ID, job.ID)
	writeJSON(w, 202, map[string]any{"job_id": job.ID})
}

func (s *Server) postKubernetesHelm(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "Helm can be used after the cluster is running")
		return
	}
	var in struct {
		Release    string `json:"release"`
		Chart      string `json:"chart"`
		Namespace  string `json:"namespace"`
		Values     string `json:"values"`
		HelmAction string `json:"action"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 80<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid helm request")
		return
	}
	if in.HelmAction == "" {
		in.HelmAction = "install"
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
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionHelm, kubernetes.Operation{Release: strings.TrimSpace(in.Release), Chart: strings.TrimSpace(in.Chart), Namespace: strings.TrimSpace(in.Namespace), Values: in.Values, HelmAction: strings.ToLower(strings.TrimSpace(in.HelmAction))})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.helm", current.ID, job.ID)
	writeJSON(w, 202, map[string]any{"job_id": job.ID})
}

func (s *Server) postKubernetesStorage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		YAML    string `json:"yaml"`
		Install bool   `json:"install"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid storage request")
		return
	}
	if in.Install || strings.TrimSpace(in.YAML) == "" {
		in.YAML = cksLocalStorageManifest
	}
	s.queueClusterManifest(w, r, kubernetes.ActionStorage, in.YAML, "kubernetes.storage")
}

func (s *Server) postKubernetesIngress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		YAML    string `json:"yaml"`
		Install bool   `json:"install"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid ingress request")
		return
	}
	if in.Install || strings.TrimSpace(in.YAML) == "" {
		in.YAML = cksIngressClassManifest
	}
	s.queueClusterManifest(w, r, kubernetes.ActionIngress, in.YAML, "kubernetes.ingress")
}

func (s *Server) postKubernetesRBAC(w http.ResponseWriter, r *http.Request) {
	var in struct {
		YAML string `json:"yaml"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid RBAC request")
		return
	}
	s.queueClusterManifest(w, r, kubernetes.ActionRBAC, in.YAML, "kubernetes.rbac")
}

func (s *Server) postKubernetesAutoscaling(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	var in struct {
		YAML        string `json:"yaml"`
		PoolID      string `json:"pool_id"`
		Min         *int   `json:"min"`
		Max         *int   `json:"max"`
		Autoscaling *bool  `json:"autoscaling"`
		Name        string `json:"name"`
		Namespace   string `json:"namespace"`
		Replicas    int    `json:"replicas"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid autoscaling request")
		return
	}
	if strings.TrimSpace(in.YAML) != "" || in.Name != "" {
		yaml := in.YAML
		if yaml == "" {
			ns := in.Namespace
			if ns == "" {
				ns = "default"
			}
			yaml = fmt.Sprintf("apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  minReplicas: 1\n  maxReplicas: %d\n  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: %s\n", in.Name, ns, max(in.Replicas, 3), in.Name)
		}
		s.queueClusterManifest(w, r, kubernetes.ActionHPA, yaml, "kubernetes.hpa")
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	before := s.kubernetesCheckpoint()
	for i, pool := range current.NodePools {
		if in.PoolID != "" && pool.ID != in.PoolID && pool.Name != in.PoolID {
			continue
		}
		if in.Autoscaling != nil {
			current.NodePools[i].Autoscaling = *in.Autoscaling
		}
		if in.Min != nil {
			current.NodePools[i].Min = *in.Min
		}
		if in.Max != nil {
			current.NodePools[i].Max = *in.Max
		}
	}
	if current.NodePool.ID != "" {
		for _, pool := range current.NodePools {
			if pool.ID == current.NodePool.ID {
				current.NodePool = pool
			}
		}
	}
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Node pool autoscaling updated", "info")
	d.Clusters[current.ID] = current
	if err := s.saveKubernetes(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"cluster": current.Public()})
}

func (s *Server) createKubernetesBackup(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "backups are available after the cluster is running")
		return
	}
	if e := s.checkQuota("backups", 1); e != nil {
		fail(w, 403, e.Error())
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
	now := time.Now().UTC()
	backup := model.Backup{ID: "ckb-" + randHex(16), State: kubernetes.StatePending, Created: now, Updated: now}
	before := s.kubernetesCheckpoint()
	current.Backups = append(current.Backups, backup)
	current.Updated = now
	d.Clusters[current.ID] = current
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionBackup, kubernetes.Operation{BackupID: backup.ID})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.backup", current.ID, backup.ID)
	writeJSON(w, 202, map[string]any{"backup": backup, "job_id": job.ID})
}

func (s *Server) listKubernetesBackups(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"backups": cluster.Public().Backups})
}

func (s *Server) restoreKubernetesBackup(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	var in struct {
		BackupID string `json:"backup_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil || in.BackupID == "" {
		fail(w, 400, "backup_id is required")
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
	var backup model.Backup
	found := false
	for _, item := range current.Backups {
		if item.ID == in.BackupID {
			backup, found = item, true
		}
	}
	if !found || len(backup.Snapshot) == 0 {
		fail(w, 404, "backup not found")
		return
	}
	plain, err := s.openKubernetesBytes("backup:"+backup.ID, backup.Snapshot, []byte("kubernetes:backup:"+backup.ID))
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	before := s.kubernetesCheckpoint()
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionRestore, kubernetes.Operation{BackupID: backup.ID, Snapshot: plain})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.restore", current.ID, backup.ID)
	writeJSON(w, 202, map[string]any{"job_id": job.ID})
}

func (s *Server) upgradeKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "upgrades require a running cluster")
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid upgrade request")
		return
	}
	version := kubernetes.NormalizeVersion(in.Version)
	if !kubernetes.VersionSupported(version) {
		fail(w, 400, "unsupported kubernetes version")
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
	current.State = kubernetes.StateUpgrading
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Rolling upgrade to "+version+" queued", "info")
	d.Clusters[current.ID] = current
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionUpgrade, kubernetes.Operation{Spec: kubernetes.Spec{TargetVersion: version, Version: version}})
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.upgrade", current.ID, version)
	writeJSON(w, 202, map[string]any{"cluster": current.Public(), "job_id": job.ID})
}

const cksLocalStorageManifest = `apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: cks-local-path
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: false
`

const cksIngressClassManifest = `apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: nginx
  labels:
    app.kubernetes.io/name: ingress-nginx
spec:
  controller: k8s.io/ingress-nginx
`
