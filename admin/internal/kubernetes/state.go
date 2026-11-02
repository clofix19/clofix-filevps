// Package kubernetes owns encrypted controller metadata for managed clusters.
package kubernetes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	wire "github.com/clofix/filevps/worker/pkg/kubernetes"
)

type Cluster struct {
	ID                   string             `json:"id"`
	Owner                string             `json:"owner"`
	Name                 string             `json:"name"`
	Version              string             `json:"version"`
	InfrastructureMode   string             `json:"infrastructure_mode"`
	State                string             `json:"state"`
	Error                string             `json:"error,omitempty"`
	DeleteProtection     bool               `json:"delete_protection"`
	HA                   bool               `json:"ha,omitempty"`
	ControlPlaneCount    int                `json:"control_plane_count,omitempty"`
	ControlPlaneWorkerID string             `json:"control_plane_worker_id"`
	APIEndpoint          string             `json:"api_endpoint,omitempty"`
	EncryptedKubeconfig  []byte             `json:"encrypted_kubeconfig,omitempty"`
	JoinMaterial         []byte             `json:"join_material,omitempty"`
	IdempotencyKey       string             `json:"idempotency_key,omitempty"`
	NodePool             NodePool           `json:"node_pool"`
	NodePools            []NodePool         `json:"node_pools,omitempty"`
	Nodes                []Node             `json:"nodes"`
	Helm                 []wire.HelmRelease `json:"helm,omitempty"`
	Backups              []Backup           `json:"backups,omitempty"`
	IngressInstalled     bool               `json:"ingress_installed,omitempty"`
	StorageInstalled     bool               `json:"storage_installed,omitempty"`
	Integrations         []Integration      `json:"integrations,omitempty"`
	Inventory            wire.Inventory     `json:"inventory,omitempty"`
	Events               []ClusterEvent     `json:"events,omitempty"`
	Created              time.Time          `json:"created"`
	Updated              time.Time          `json:"updated"`
}

type NodePool struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind,omitempty"`
	Count       int     `json:"count"`
	Min         int     `json:"min,omitempty"`
	Max         int     `json:"max,omitempty"`
	CPU         float64 `json:"cpu"`
	RAMMB       int64   `json:"ram_mb"`
	DiskMB      int64   `json:"disk_mb"`
	WorkerID    string  `json:"worker_id,omitempty"`
	Autoscaling bool    `json:"autoscaling,omitempty"`
}

type Node struct {
	ID       string  `json:"id"`
	PoolID   string  `json:"pool_id,omitempty"`
	WorkerID string  `json:"worker_id"`
	Hostname string  `json:"hostname,omitempty"`
	Role     string  `json:"role"`
	State    string  `json:"state"`
	Version  string  `json:"version,omitempty"`
	CPU      float64 `json:"cpu,omitempty"`
	RAMMB    int64   `json:"ram_mb,omitempty"`
	DiskMB   int64   `json:"disk_mb,omitempty"`
	Error    string  `json:"error,omitempty"`
}

type Backup struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	Digest    string    `json:"digest,omitempty"`
	Snapshot  []byte    `json:"snapshot,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

type Integration struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
	K8sKind    string `json:"k8s_kind,omitempty"`
	K8sName    string `json:"k8s_name,omitempty"`
	Status     string `json:"status,omitempty"`
	URL        string `json:"url,omitempty"`
}

type ClusterEvent struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
	Level   string    `json:"level,omitempty"`
}

type Job struct {
	ID        string          `json:"id"`
	Owner     string          `json:"owner"`
	ClusterID string          `json:"cluster_id"`
	NodeID    string          `json:"node_id,omitempty"`
	WorkerID  string          `json:"worker_id"`
	Action    string          `json:"action"`
	State     string          `json:"state"`
	Progress  int             `json:"progress"`
	Error     string          `json:"error,omitempty"`
	Payload   []byte          `json:"payload,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Created   time.Time       `json:"created"`
	Updated   time.Time       `json:"updated"`
}

type SnapshotStore interface {
	LoadKubernetesState() ([]byte, error)
	SaveKubernetesState([]byte) error
}

type State struct {
	sync.Mutex
	Clusters map[string]Cluster `json:"clusters"`
	Jobs     map[string]Job     `json:"jobs"`
	backend  SnapshotStore
	root     string
}

func New(root string, backend SnapshotStore) (*State, error) {
	s := &State{backend: backend, root: root}
	var raw []byte
	var e error
	if backend != nil {
		raw, e = backend.LoadKubernetesState()
	} else {
		raw, e = os.ReadFile(filepath.Join(root, "kubernetes-state.json"))
		if os.IsNotExist(e) {
			e = nil
		}
	}
	if e != nil {
		return nil, e
	}
	if len(raw) > 0 {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("invalid null kubernetes state")
		}
		if e = json.Unmarshal(raw, s); e != nil {
			return nil, fmt.Errorf("invalid kubernetes state: %w", e)
		}
	}
	if s.Clusters == nil {
		s.Clusters = map[string]Cluster{}
	}
	if s.Jobs == nil {
		s.Jobs = map[string]Job{}
	}
	return s, nil
}

func (s *State) Save() error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	if s.backend != nil {
		return s.backend.SaveKubernetesState(raw)
	}
	if e = os.MkdirAll(s.root, 0700); e != nil {
		return e
	}
	file := filepath.Join(s.root, "kubernetes-state.json")
	if e = os.WriteFile(file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(file+".tmp", file)
}

func (c Cluster) Public() Cluster {
	c.EncryptedKubeconfig = nil
	c.JoinMaterial = nil
	if len(c.Backups) > 0 {
		out := make([]Backup, len(c.Backups))
		copy(out, c.Backups)
		for i := range out {
			out[i].Snapshot = nil
		}
		c.Backups = out
	}
	return c
}

func (c Cluster) Pools() []NodePool {
	if len(c.NodePools) > 0 {
		return c.NodePools
	}
	if c.NodePool.ID != "" {
		return []NodePool{c.NodePool}
	}
	return nil
}

func (c Cluster) Pool(id string) (NodePool, bool) {
	for _, pool := range c.Pools() {
		if pool.ID == id || pool.Name == id || id == "" && pool.Name == "default" {
			return pool, true
		}
	}
	if c.NodePool.ID != "" && (id == "" || id == c.NodePool.ID || id == c.NodePool.Name) {
		return c.NodePool, true
	}
	return NodePool{}, false
}

func (c Cluster) LiveNodes() int {
	n := 0
	for _, node := range c.Nodes {
		if node.State != wire.StateDeleted {
			n++
		}
	}
	return n
}

func (j Job) Public() Job {
	j.Payload = nil
	return j
}
