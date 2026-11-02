package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func (s *Server) kubernetesCheckpoint() []byte { raw, _ := json.Marshal(s.kubernetes); return raw }

func (s *Server) saveKubernetes(previous []byte) error {
	if e := s.kubernetes.Save(); e != nil {
		json.Unmarshal(previous, s.kubernetes)
		return fmt.Errorf("kubernetes metadata could not be persisted")
	}
	return nil
}

func (s *Server) sealKubernetes(id string, value any, aad []byte) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return s.sealKubernetesBytes(id, plain, aad)
}

func (s *Server) sealKubernetesBytes(id string, plain, aad []byte) ([]byte, error) {
	c, err := s.applicationCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	if len(aad) == 0 {
		aad = []byte("kubernetes:" + id)
	}
	return c.Seal(nonce, nonce, plain, aad), nil
}

func (s *Server) openKubernetes(id string, encrypted []byte, value any) error {
	plain, err := s.openKubernetesBytes("kubernetes:"+id, encrypted, []byte("kubernetes:"+id))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, value)
}

func (s *Server) openKubernetesBytes(label string, encrypted, aad []byte) ([]byte, error) {
	c, err := s.applicationCipher()
	if err != nil {
		return nil, err
	}
	n := c.NonceSize()
	if len(encrypted) < n {
		return nil, fmt.Errorf("invalid kubernetes secret")
	}
	plain, err := c.Open(nil, encrypted[:n], encrypted[n:], aad)
	if err != nil {
		return nil, fmt.Errorf("kubernetes secrets cannot be decrypted")
	}
	return plain, nil
}

func k8sTerminal(state string) bool {
	return state == "SUCCEEDED" || state == "FAILED" || state == "CANCELLED"
}

func (s *Server) kubernetesClusterBusyLocked(id string) bool {
	for _, job := range s.kubernetes.Jobs {
		if job.ClusterID == id && !k8sTerminal(job.State) {
			return true
		}
	}
	return false
}

func (s *Server) queueKubernetesLocked(cluster model.Cluster, node model.Node, action string, operation kubernetes.Operation) (model.Job, error) {
	operation.JobID = "ckj-" + randHex(16)
	operation.ClusterID = cluster.ID
	operation.NodeID = node.ID
	operation.Action = action
	pool, _ := cluster.Pool(node.PoolID)
	if pool.CPU == 0 {
		pool = cluster.NodePool
	}
	target := operation.Spec.TargetVersion
	operation.Spec.ID = cluster.ID
	if operation.Spec.Version == "" {
		operation.Spec.Version = cluster.Version
	}
	operation.Spec.TargetVersion = target
	operation.Spec.InfrastructureMode = kubernetes.ModeDedicated
	operation.Spec.Role = node.Role
	cpu, ram, disk := kubernetesNodeResources(cluster, node)
	if cpu == 0 {
		cpu = pool.CPU
	}
	if ram == 0 {
		ram = pool.RAMMB
	}
	if disk == 0 {
		disk = pool.DiskMB
	}
	operation.Spec.CPU = cpu
	operation.Spec.RAMMB = ram
	operation.Spec.DiskMB = disk
	operation.Spec.HA = cluster.HA
	operation.Spec.SingleNode = cluster.LiveNodes() <= 1 && !cluster.HA
	if host := s.kubernetesAdvertise(node.WorkerID); host != "" {
		operation.Spec.AdvertiseAddress = host
		if node.Role == kubernetes.RoleControlPlane && operation.Spec.ControlPlaneEndpoint == "" {
			operation.Spec.ControlPlaneEndpoint = host
		}
	}
	if cluster.HA && cluster.APIEndpoint != "" && operation.Spec.ControlPlaneEndpoint == "" {
		operation.Spec.ControlPlaneEndpoint = strings.TrimPrefix(strings.TrimPrefix(cluster.APIEndpoint, "https://"), "http://")
		operation.Spec.ControlPlaneEndpoint = strings.TrimSuffix(operation.Spec.ControlPlaneEndpoint, ":6443")
	}
	if action == kubernetes.ActionJoin || action == kubernetes.ActionJoinControlPlane {
		var join struct {
			Token        string `json:"join_token"`
			Hash         string `json:"join_hash"`
			APIEndpoint  string `json:"api_endpoint"`
			ControlPlane string `json:"control_plane_endpoint"`
			CertKey      string `json:"join_certificate_key"`
		}
		if len(cluster.JoinMaterial) > 0 {
			plain, err := s.openKubernetesBytes("join:"+cluster.ID, cluster.JoinMaterial, []byte("kubernetes:join:"+cluster.ID))
			if err != nil {
				return model.Job{}, err
			}
			if err = json.Unmarshal(plain, &join); err != nil {
				return model.Job{}, err
			}
		}
		operation.Spec.JoinToken = join.Token
		operation.Spec.JoinHash = join.Hash
		operation.Spec.JoinCertificateKey = join.CertKey
		operation.Spec.ControlPlaneEndpoint = join.ControlPlane
		if operation.Spec.ControlPlaneEndpoint == "" {
			operation.Spec.ControlPlaneEndpoint = join.APIEndpoint
		}
		operation.Spec.ControlPlaneEndpoint = strings.TrimPrefix(strings.TrimPrefix(operation.Spec.ControlPlaneEndpoint, "https://"), "http://")
		operation.Spec.ControlPlaneEndpoint = strings.TrimSuffix(operation.Spec.ControlPlaneEndpoint, ":6443")
	}
	if err := operation.Validate(); err != nil {
		return model.Job{}, err
	}
	payload, err := s.sealKubernetes(operation.JobID, operation, []byte("kubernetes:"+operation.JobID))
	if err != nil {
		return model.Job{}, err
	}
	now := time.Now().UTC()
	job := model.Job{ID: operation.JobID, Owner: cluster.Owner, ClusterID: cluster.ID, NodeID: node.ID, WorkerID: node.WorkerID, Action: action, State: "QUEUED", Payload: payload, Created: now, Updated: now}
	s.kubernetes.Jobs[job.ID] = job
	return job, nil
}

func (s *Server) kubernetesAdvertise(workerID string) string {
	worker, err := s.Store.GetWorker(workerID)
	if err != nil {
		return ""
	}
	if worker.Capacity.KubernetesPublicHost != "" {
		return worker.Capacity.KubernetesPublicHost
	}
	return worker.Hostname
}

func (s *Server) kubernetesWorkerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/kubernetes/jobs/{id}", s.fetchKubernetesJob)
}

func (s *Server) fetchKubernetesJob(w http.ResponseWriter, r *http.Request) {
	worker, err := s.databaseWorker(r)
	if err != nil {
		fail(w, 403, err.Error())
		return
	}
	if !s.kubernetesAvailable(w) {
		return
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	job, ok := d.Jobs[r.PathValue("id")]
	if !ok || job.WorkerID != worker {
		fail(w, 404, "job not found")
		return
	}
	if k8sTerminal(job.State) {
		fail(w, 409, "job is completed")
		return
	}
	var operation kubernetes.Operation
	if err = s.openKubernetes(job.ID, job.Payload, &operation); err != nil {
		fail(w, 503, err.Error())
		return
	}
	before := s.kubernetesCheckpoint()
	job.State = "RUNNING"
	job.Updated = time.Now().UTC()
	d.Jobs[job.ID] = job
	if err = s.saveKubernetes(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, operation)
}

func (s *Server) kubernetesHeartbeat(worker string, results []kubernetes.Result, statuses []kubernetes.Status) ([]proto.Operation, []string) {
	if s.kubernetes == nil || s.kubernetesError != nil {
		return nil, nil
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	before := s.kubernetesCheckpoint()
	ack := []string{}
	operations := []proto.Operation{}
	for _, status := range statuses {
		cluster, ok := d.Clusters[status.ClusterID]
		if !ok {
			continue
		}
		belongs := false
		for i, node := range cluster.Nodes {
			if node.WorkerID != worker {
				continue
			}
			belongs = true
			if status.NodeID != "" && node.ID != status.NodeID {
				continue
			}
			if status.State != "" {
				cluster.Nodes[i].State = status.State
			}
			if status.Version != "" {
				cluster.Nodes[i].Version = status.Version
			}
			if status.Error != "" {
				cluster.Nodes[i].Error = status.Error
			}
		}
		if !belongs {
			continue
		}
		if status.Role == kubernetes.RoleControlPlane || cluster.ControlPlaneWorkerID == worker {
			if status.APIEndpoint != "" {
				cluster.APIEndpoint = status.APIEndpoint
			}
			if status.Inventory.Pods != nil || status.Inventory.Deployments != nil || status.Inventory.Namespaces != nil {
				cluster.Inventory = status.Inventory
			}
		}
		d.Clusters[cluster.ID] = cluster
	}
	for _, result := range results {
		job, ok := d.Jobs[result.JobID]
		if !ok || job.WorkerID != worker || job.ClusterID != result.ClusterID {
			continue
		}
		if k8sTerminal(job.State) {
			ack = append(ack, job.ID)
			continue
		}
		if result.State == "RUNNING" {
			job.State = "RUNNING"
			job.Progress = result.Progress
			job.Updated = time.Now().UTC()
			d.Jobs[job.ID] = job
			continue
		}
		if result.State != "SUCCEEDED" && result.State != "FAILED" && result.State != "CANCELLED" {
			continue
		}
		job.State = result.State
		job.Error = result.Error
		job.Progress = result.Progress
		job.Result = result.Data
		job.Updated = time.Now().UTC()
		cluster, exists := d.Clusters[job.ClusterID]
		if result.State == "SUCCEEDED" {
			s.applyKubernetesSuccessLocked(&cluster, &job, result)
		} else if exists {
			s.applyKubernetesFailureLocked(&cluster, job)
		}
		if exists {
			d.Clusters[cluster.ID] = cluster
		}
		d.Jobs[job.ID] = job
		ack = append(ack, job.ID)
	}
	busy := map[string]bool{}
	for _, job := range d.Jobs {
		if job.WorkerID == worker && (job.State == "RUNNING" || job.State == "DISPATCHED") {
			busy[job.WorkerID] = true
			operations = append(operations, proto.Operation{ID: job.ID, Action: proto.ActionKubernetesJob, KubernetesJobID: job.ID})
		}
	}
	if !busy[worker] {
		queued := []model.Job{}
		for _, job := range d.Jobs {
			if job.WorkerID == worker && job.State == "QUEUED" {
				queued = append(queued, job)
			}
		}
		sort.Slice(queued, func(i, j int) bool { return queued[i].Created.Before(queued[j].Created) })
		if len(queued) > 0 {
			job := queued[0]
			job.State = "DISPATCHED"
			job.Updated = time.Now().UTC()
			d.Jobs[job.ID] = job
			operations = append(operations, proto.Operation{ID: job.ID, Action: proto.ActionKubernetesJob, KubernetesJobID: job.ID})
		}
	}
	for id, cluster := range d.Clusters {
		beforeState := cluster.State
		kubernetesRecoverRunning(&cluster)
		if cluster.State != beforeState {
			d.Clusters[id] = cluster
		}
	}
	if err := s.saveKubernetes(before); err != nil {
		return nil, nil
	}
	return operations, ack
}

func kubernetesRecoverRunning(cluster *model.Cluster) {
	if cluster == nil || cluster.State != kubernetes.StateDegraded {
		return
	}
	live := 0
	for _, node := range cluster.Nodes {
		if node.State == kubernetes.StateDeleted {
			continue
		}
		live++
		if node.State != kubernetes.StateRunning {
			return
		}
	}
	if live == 0 {
		return
	}
	cluster.State = kubernetes.StateRunning
	cluster.Error = ""
	cluster.JoinMaterial = nil
}

func (s *Server) applyKubernetesSuccessLocked(cluster *model.Cluster, job *model.Job, result kubernetes.Result) {
	now := time.Now().UTC()
	cluster.Updated = now
	switch job.Action {
	case kubernetes.ActionInit:
		var data struct {
			Kubeconfig           string `json:"kubeconfig"`
			APIEndpoint          string `json:"api_endpoint"`
			JoinToken            string `json:"join_token"`
			JoinHash             string `json:"join_hash"`
			ControlPlaneEndpoint string `json:"control_plane_endpoint"`
			JoinCertificateKey   string `json:"join_certificate_key"`
		}
		if json.Unmarshal(result.Data, &data) != nil || strings.TrimSpace(data.Kubeconfig) == "" {
			cluster.State = kubernetes.StateFailed
			cluster.Error = "worker did not return a kubeconfig"
			cluster.Events = appendClusterEvent(cluster.Events, cluster.Error, "error")
			return
		}
		sealed, err := s.sealKubernetesBytes(cluster.ID, []byte(data.Kubeconfig), []byte("clofix-kubernetes-kubeconfig-v1"))
		if err != nil {
			cluster.State = kubernetes.StateFailed
			cluster.Error = err.Error()
			return
		}
		join, _ := json.Marshal(map[string]string{"join_token": data.JoinToken, "join_hash": data.JoinHash, "api_endpoint": data.APIEndpoint, "control_plane_endpoint": data.ControlPlaneEndpoint, "join_certificate_key": data.JoinCertificateKey})
		joinSealed, err := s.sealKubernetesBytes(cluster.ID, join, []byte("kubernetes:join:"+cluster.ID))
		if err != nil {
			cluster.State = kubernetes.StateFailed
			cluster.Error = err.Error()
			return
		}
		cluster.EncryptedKubeconfig = sealed
		cluster.JoinMaterial = joinSealed
		cluster.APIEndpoint = data.APIEndpoint
		cluster.Error = ""
		job.Result, _ = json.Marshal(map[string]string{"api_endpoint": data.APIEndpoint})
		for i, node := range cluster.Nodes {
			if node.Role == kubernetes.RoleControlPlane {
				cluster.Nodes[i].State = kubernetes.StateRunning
				cluster.Nodes[i].Error = ""
			}
		}
		cluster.Events = appendClusterEvent(cluster.Events, "Control plane initialized", "info")
		pending := false
		for _, node := range cluster.Nodes {
			if node.ID == job.NodeID || node.State == kubernetes.StateRunning || node.State == kubernetes.StateDeleted {
				continue
			}
			action := kubernetes.ActionJoin
			if node.Role == kubernetes.RoleControlPlane {
				action = kubernetes.ActionJoinControlPlane
			}
			if _, err = s.queueKubernetesLocked(*cluster, node, action, kubernetes.Operation{}); err != nil {
				cluster.State = kubernetes.StateFailed
				cluster.Error = err.Error()
				return
			}
			pending = true
		}
		if pending {
			cluster.State = kubernetes.StateProvisioning
		} else {
			cluster.State = kubernetes.StateRunning
			cluster.JoinMaterial = nil
		}
	case kubernetes.ActionJoin, kubernetes.ActionJoinControlPlane:
		for i, node := range cluster.Nodes {
			if node.ID == job.NodeID {
				cluster.Nodes[i].State = kubernetes.StateRunning
				cluster.Nodes[i].Error = ""
			}
		}
		label := "Worker node joined "
		if job.Action == kubernetes.ActionJoinControlPlane {
			label = "Control-plane node joined "
		}
		cluster.Events = appendClusterEvent(cluster.Events, label+job.NodeID, "info")
		kubernetesRecoverRunning(cluster)
	case kubernetes.ActionReset:
		remaining := false
		for i, node := range cluster.Nodes {
			if node.ID == job.NodeID {
				cluster.Nodes[i].State = kubernetes.StateDeleted
			}
			if cluster.Nodes[i].State != kubernetes.StateDeleted {
				remaining = true
			}
		}
		if !remaining {
			cluster.State = kubernetes.StateDeleted
			cluster.EncryptedKubeconfig = nil
			cluster.JoinMaterial = nil
			cluster.Events = appendClusterEvent(cluster.Events, "Cluster deleted", "info")
		}
	case kubernetes.ActionApply, kubernetes.ActionDelete, kubernetes.ActionScale, kubernetes.ActionRestart, kubernetes.ActionGet, kubernetes.ActionHelm, kubernetes.ActionStorage, kubernetes.ActionIngress, kubernetes.ActionRBAC, kubernetes.ActionHPA:
		var inventory kubernetes.Inventory
		if json.Unmarshal(result.Data, &inventory) == nil && len(result.Data) > 2 {
			cluster.Inventory = inventory
			if inventory.Helm != nil {
				cluster.Helm = inventory.Helm
			}
		}
		if job.Action == kubernetes.ActionApply {
			for i, item := range cluster.Integrations {
				if item.Status == "PENDING" {
					cluster.Integrations[i].Status = "READY"
				}
			}
		}
		if job.Action == kubernetes.ActionStorage {
			cluster.StorageInstalled = true
		}
		if job.Action == kubernetes.ActionIngress {
			cluster.IngressInstalled = true
		}
		cluster.Events = appendClusterEvent(cluster.Events, job.Action+" succeeded", "info")
	case kubernetes.ActionLogs:
		var inventory kubernetes.Inventory
		if json.Unmarshal(result.Data, &inventory) == nil {
			cluster.Inventory.Logs = inventory.Logs
		}
	case kubernetes.ActionBackup:
		var payload struct {
			Backup   kubernetes.BackupInfo `json:"backup"`
			Snapshot []byte                `json:"snapshot"`
		}
		if json.Unmarshal(result.Data, &payload) != nil {
			cluster.Events = appendClusterEvent(cluster.Events, "Backup succeeded without payload", "info")
			break
		}
		sealed, err := s.sealKubernetesBytes(payload.Backup.ID, payload.Snapshot, []byte("kubernetes:backup:"+payload.Backup.ID))
		if err != nil {
			cluster.Events = appendClusterEvent(cluster.Events, "Backup seal failed: "+err.Error(), "error")
			break
		}
		for i, item := range cluster.Backups {
			if item.ID == payload.Backup.ID || payload.Backup.ID == "" && item.State == kubernetes.StatePending {
				cluster.Backups[i].State = "SUCCEEDED"
				cluster.Backups[i].SizeBytes = payload.Backup.SizeBytes
				cluster.Backups[i].Digest = payload.Backup.Digest
				cluster.Backups[i].Snapshot = sealed
				cluster.Backups[i].Updated = now
			}
		}
		job.Result, _ = json.Marshal(map[string]any{"id": payload.Backup.ID, "size_bytes": payload.Backup.SizeBytes, "digest": payload.Backup.Digest})
		cluster.Events = appendClusterEvent(cluster.Events, "etcd backup stored", "info")
	case kubernetes.ActionRestore:
		cluster.Events = appendClusterEvent(cluster.Events, "Cluster restore succeeded", "info")
		cluster.State = kubernetes.StateRunning
		cluster.Error = ""
	case kubernetes.ActionUpgrade:
		var data struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(result.Data, &data)
		if data.Version == "" {
			data.Version = cluster.Version
		}
		cluster.Version = data.Version
		for i := range cluster.Nodes {
			if cluster.Nodes[i].ID == job.NodeID || cluster.Nodes[i].Role == kubernetes.RoleControlPlane {
				cluster.Nodes[i].Version = data.Version
				cluster.Nodes[i].State = kubernetes.StateRunning
			}
		}
		pending := false
		for _, node := range cluster.Nodes {
			if node.Role == kubernetes.RoleWorker && node.State != kubernetes.StateDeleted && node.Version != data.Version {
				if _, err := s.queueKubernetesLocked(*cluster, node, kubernetes.ActionUpgrade, kubernetes.Operation{Spec: kubernetes.Spec{TargetVersion: data.Version, Version: data.Version}}); err != nil {
					cluster.State = kubernetes.StateDegraded
					cluster.Error = err.Error()
					return
				}
				pending = true
			}
		}
		if pending {
			cluster.State = kubernetes.StateUpgrading
		} else {
			cluster.State = kubernetes.StateRunning
			cluster.Error = ""
		}
		cluster.Events = appendClusterEvent(cluster.Events, "Upgrade applied "+data.Version, "info")
	}
}

func (s *Server) applyKubernetesFailureLocked(cluster *model.Cluster, job model.Job) {
	cluster.Updated = time.Now().UTC()
	cluster.Error = job.Error
	cluster.Events = appendClusterEvent(cluster.Events, job.Action+" failed: "+job.Error, "error")
	for i, node := range cluster.Nodes {
		if node.ID == job.NodeID {
			cluster.Nodes[i].State = kubernetes.StateFailed
			cluster.Nodes[i].Error = job.Error
		}
	}
	switch job.Action {
	case kubernetes.ActionInit:
		cluster.State = kubernetes.StateFailed
	case kubernetes.ActionJoin, kubernetes.ActionJoinControlPlane:
		if cluster.State != kubernetes.StateFailed {
			cluster.State = kubernetes.StateDegraded
		}
	case kubernetes.ActionReset:
		cluster.State = kubernetes.StateFailed
	case kubernetes.ActionUpgrade:
		cluster.State = kubernetes.StateDegraded
	case kubernetes.ActionBackup:
		for i, item := range cluster.Backups {
			if item.State == kubernetes.StatePending {
				cluster.Backups[i].State = "FAILED"
				cluster.Backups[i].Error = job.Error
			}
		}
	}
}
