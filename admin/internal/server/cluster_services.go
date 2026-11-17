package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

const (
	clusterAppRedis = "redis"

	clusterTopoMasterMaster = "master_master"
	clusterTopoMasterSlave  = "master_slave"

	clusterStorageTemp       = "temp"
	clusterStoragePersistent = "persistent"

	clusterStatusProvisioning = "PROVISIONING"
	clusterStatusAttaching    = "ATTACHING"
	clusterStatusConfiguring  = "CONFIGURING"
	clusterStatusJoining      = "JOINING"
	clusterStatusReady        = "READY"
	clusterStatusFailed       = "FAILED"
	clusterStatusDeleting     = "DELETING"

	clusterNodePending     = "PENDING"
	clusterNodeProvisioning = "PROVISIONING"
	clusterNodeRunning     = "RUNNING"
	clusterNodeAttached    = "ATTACHED"
	clusterNodeConfigured  = "CONFIGURED"
	clusterNodeJoined      = "JOINED"
	clusterNodeFailed      = "FAILED"

	clusterEFSGuestPath = "/data/redis"
)

var clusterServiceID = regexp.MustCompile(`^cs-[0-9a-f]{16}$`)

type clusterServiceNode struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id,omitempty"`
	Role       string `json:"role"`
	Status     string `json:"status"`
	Address    string `json:"address,omitempty"`
	PendingOp  string `json:"pending_op,omitempty"`
	Error      string `json:"error,omitempty"`
}

type clusterService struct {
	ID         string               `json:"id"`
	AccountID  string               `json:"account_id,omitempty"`
	Name       string               `json:"name"`
	App        string               `json:"app"`
	Topology   string               `json:"topology"`
	Storage    string               `json:"storage"`
	EFSID      string               `json:"efs_id,omitempty"`
	NodeCount  int                  `json:"node_count"`
	VPCID      string               `json:"vpc_id,omitempty"`
	RAMMB      int64                `json:"ram_mb"`
	CPU        float64              `json:"cpu"`
	DiskMB     int64                `json:"disk_mb"`
	Status     string               `json:"status"`
	Error      string               `json:"error,omitempty"`
	Nodes      []clusterServiceNode `json:"nodes"`
	Endpoints  []string             `json:"endpoints,omitempty"`
	Created    time.Time            `json:"created"`
	Updated    time.Time            `json:"updated"`
	Deleting   bool                 `json:"deleting,omitempty"`
}

type clusterServiceStore struct {
	sync.Mutex
	Items map[string]clusterService `json:"items"`
	file  string
}

func loadClusterServices(dir string) *clusterServiceStore {
	d := &clusterServiceStore{file: filepath.Join(dir, "cluster-services.json"), Items: map[string]clusterService{}}
	if raw, err := os.ReadFile(d.file); err == nil {
		_ = json.Unmarshal(raw, d)
	}
	if d.Items == nil {
		d.Items = map[string]clusterService{}
	}
	return d
}

func (d *clusterServiceStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) clusterServiceRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/cluster-services", s.auth(s.listClusterServices))
	m.HandleFunc("POST /api/cluster-services", s.auth(s.createClusterService))
	m.HandleFunc("GET /api/cluster-services/{id}", s.auth(s.getClusterService))
	m.HandleFunc("DELETE /api/cluster-services/{id}", s.auth(s.deleteClusterService))
}

func (s *Server) listClusterServices(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	d := s.clusters
	d.Lock()
	defer d.Unlock()
	out := make([]clusterService, 0, len(d.Items))
	for _, item := range d.Items {
		if !s.canAccessAccount(p, item.AccountID) && item.AccountID != "" && p.Kind != "admin" {
			continue
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"clusters": out})
}

func (s *Server) getClusterService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !clusterServiceID.MatchString(id) {
		fail(w, 400, "invalid cluster id")
		return
	}
	d := s.clusters
	d.Lock()
	item, ok := d.Items[id]
	d.Unlock()
	if !ok {
		fail(w, 404, "cluster not found")
		return
	}
	p := principalFrom(r)
	if item.AccountID != "" && !s.canAccessAccount(p, item.AccountID) && p.Kind != "admin" {
		fail(w, 404, "cluster not found")
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) createClusterService(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string  `json:"name"`
		App       string  `json:"app"`
		Topology  string  `json:"topology"`
		Storage   string  `json:"storage"`
		EFSID     string  `json:"efs_id"`
		NodeCount int     `json:"node_count"`
		VPCID     string  `json:"vpc_id"`
		RAMMB     int64   `json:"ram_mb"`
		CPU       float64 `json:"cpu"`
		DiskMB    int64   `json:"disk_mb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
		fail(w, 400, "invalid body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.App = strings.ToLower(strings.TrimSpace(in.App))
	in.Topology = strings.ToLower(strings.TrimSpace(in.Topology))
	in.Storage = strings.ToLower(strings.TrimSpace(in.Storage))
	in.EFSID = strings.TrimSpace(in.EFSID)
	in.VPCID = strings.TrimSpace(in.VPCID)
	if in.Name == "" || len(in.Name) > 80 {
		fail(w, 400, "name is required (max 80 chars)")
		return
	}
	if in.App != clusterAppRedis {
		fail(w, 400, "only app=redis is supported in this release")
		return
	}
	if in.Topology != clusterTopoMasterMaster && in.Topology != clusterTopoMasterSlave {
		fail(w, 400, "topology must be master_master or master_slave")
		return
	}
	if in.Storage != clusterStorageTemp && in.Storage != clusterStoragePersistent {
		fail(w, 400, "storage must be temp or persistent")
		return
	}
	if in.Topology == clusterTopoMasterMaster && in.NodeCount < 3 {
		fail(w, 400, "master_master requires at least 3 nodes")
		return
	}
	if in.Topology == clusterTopoMasterSlave && in.NodeCount < 2 {
		fail(w, 400, "master_slave requires at least 2 nodes")
		return
	}
	if in.NodeCount > 9 {
		fail(w, 400, "node_count max is 9")
		return
	}
	if in.Storage == clusterStoragePersistent {
		if in.EFSID == "" || !strings.HasPrefix(in.EFSID, "efs-") {
			fail(w, 400, "efs_id is required for persistent storage")
			return
		}
		s.efs.Lock()
		_, ok := s.efs.Shares[in.EFSID]
		s.efs.Unlock()
		if !ok {
			fail(w, 400, "EFS share not found")
			return
		}
	} else {
		in.EFSID = ""
	}
	if in.RAMMB <= 0 {
		in.RAMMB = 256
	}
	if in.CPU <= 0 {
		in.CPU = 0.5
	}
	if in.DiskMB <= 0 {
		in.DiskMB = 2048
	}
	if in.RAMMB < 256 || in.RAMMB > 8192 || in.CPU < 0.25 || in.CPU > 8 || in.DiskMB < 1024 || in.DiskMB > 102400 {
		fail(w, 400, "invalid ram_mb, cpu, or disk_mb")
		return
	}
	if reason := s.clusterCapacityReason(in.RAMMB, in.CPU, in.DiskMB, in.NodeCount); reason != "" {
		fail(w, http.StatusConflict, reason)
		return
	}
	p := principalFrom(r)
	account := p.AccountID
	if account == "" {
		account = "acc-platform"
	}
	now := time.Now().UTC()
	id := "cs-" + randHex(8)
	nodes := make([]clusterServiceNode, 0, in.NodeCount)
	for i := 0; i < in.NodeCount; i++ {
		role := "member"
		if in.Topology == clusterTopoMasterSlave {
			if i == 0 {
				role = "primary"
			} else {
				role = "replica"
			}
		}
		nodes = append(nodes, clusterServiceNode{
			ID:     fmt.Sprintf("%s-n%d", id, i+1),
			Role:   role,
			Status: clusterNodePending,
		})
	}
	item := clusterService{
		ID: id, AccountID: account, Name: in.Name, App: in.App, Topology: in.Topology,
		Storage: in.Storage, EFSID: in.EFSID, NodeCount: in.NodeCount, VPCID: in.VPCID,
		RAMMB: in.RAMMB, CPU: in.CPU, DiskMB: in.DiskMB,
		Status: clusterStatusProvisioning, Nodes: nodes, Created: now, Updated: now,
	}
	d := s.clusters
	d.Lock()
	d.Items[id] = item
	_ = d.save()
	d.Unlock()
	s.Store.Audit(p.Email, "cluster-services.create", id, in.App)
	writeJSON(w, 202, item)
}

func (s *Server) deleteClusterService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !clusterServiceID.MatchString(id) {
		fail(w, 400, "invalid cluster id")
		return
	}
	d := s.clusters
	d.Lock()
	item, ok := d.Items[id]
	if !ok {
		d.Unlock()
		fail(w, 404, "cluster not found")
		return
	}
	p := principalFrom(r)
	if item.AccountID != "" && !s.canAccessAccount(p, item.AccountID) && p.Kind != "admin" {
		d.Unlock()
		fail(w, 404, "cluster not found")
		return
	}
	item.Deleting = true
	item.Status = clusterStatusDeleting
	item.Updated = time.Now().UTC()
	d.Items[id] = item
	_ = d.save()
	d.Unlock()
	s.Store.Audit(p.Email, "cluster-services.delete", id, item.App)
	writeJSON(w, 202, map[string]any{"id": id, "status": clusterStatusDeleting})
}

func (s *Server) RunClusterServiceScheduler(ctxDone <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	s.reconcileClusterServices()
	for {
		select {
		case <-ctxDone:
			return
		case <-t.C:
			s.reconcileClusterServices()
		}
	}
}

func (s *Server) reconcileClusterServices() {
	d := s.clusters
	d.Lock()
	ids := make([]string, 0, len(d.Items))
	for id := range d.Items {
		ids = append(ids, id)
	}
	d.Unlock()
	for _, id := range ids {
		s.reconcileClusterService(id)
	}
}

func (s *Server) reconcileClusterService(id string) {
	d := s.clusters
	d.Lock()
	item, ok := d.Items[id]
	d.Unlock()
	if !ok {
		return
	}
	if item.Deleting || item.Status == clusterStatusDeleting {
		s.teardownClusterService(item)
		return
	}
	if item.Status == clusterStatusReady || item.Status == clusterStatusFailed {
		return
	}
	switch item.Status {
	case clusterStatusProvisioning:
		s.provisionClusterNodes(&item)
	case clusterStatusAttaching:
		s.attachClusterEFS(&item)
	case clusterStatusConfiguring:
		s.configureClusterNodes(&item)
	case clusterStatusJoining:
		s.joinClusterNodes(&item)
	}
}

func clusterBoolFalse() *bool { v := false; return &v }

func clusterCreateErr(err error) string {
	msg := strings.TrimSpace(err.Error())
	var wrap struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(msg), &wrap) == nil && wrap.Error != "" {
		msg = wrap.Error
	}
	if strings.Contains(msg, "RAM commit") || strings.Contains(msg, "safe capacity") {
		return "no worker has enough free RAM for another cluster VPS (RAM commit limit). Delete or stop unused VPS/CKS capacity, or lower ram_mb / node_count."
	}
	return msg
}

// clusterCapacityReason preflights N nodes against active workers before accepting create.
func (s *Server) clusterCapacityReason(ramMB int64, cpu float64, diskMB int64, nodes int) string {
	// Cluster VPS use swap_mb=0 so Redis nodes do not compete for scarce host swap.
	needRAM := uint64(ramMB) << 20
	needDisk := uint64(diskMB) << 20
	ws, _ := s.Store.ListWorkers()
	bestHeadroom := int64(-1)
	var lastReject string
	for _, wk := range ws {
		if wk.Status != "active" {
			continue
		}
		cp := wk.Capacity
		usable := cp.RAMTotal
		if cp.HostReserve < usable {
			usable -= cp.HostReserve
		} else {
			usable = 0
		}
		headroom := int64(usable) - int64(cp.RAMCommitted)
		if headroom > bestHeadroom {
			bestHeadroom = headroom
		}
		totalNeed := int64(needRAM) * int64(nodes)
		if headroom < totalNeed {
			lastReject = "RAM commit limit"
			continue
		}
		if cp.MemAvailable < needRAM {
			lastReject = "insufficient MemAvailable"
			continue
		}
		if cp.CPUAllocated+cpu*float64(nodes) > cp.CPUCores*s.Sched.MaxCPUOvercommit {
			lastReject = "CPU allocation limit"
			continue
		}
		if cp.DiskFree < needDisk {
			lastReject = "insufficient free disk"
			continue
		}
		if float64(cp.DiskCommitted+needDisk*uint64(nodes)) > float64(cp.DiskTotal)*s.Sched.MaxDiskCommit {
			lastReject = "disk commit limit"
			continue
		}
		return ""
	}
	needMiB := int64(nodes) * ramMB
	freeMiB := bestHeadroom / (1 << 20)
	if freeMiB < 0 {
		freeMiB = 0
	}
	if lastReject != "" && lastReject != "RAM commit limit" {
		return fmt.Sprintf("not enough worker capacity for %d nodes: %s. Free capacity or lower ram_mb/node_count.", nodes, lastReject)
	}
	return fmt.Sprintf("not enough worker capacity for %d nodes (%d MiB RAM total). About %d MiB RAM headroom is free after reserve. Delete unused VPS or CKS nodes, or lower ram_mb/node_count.", nodes, needMiB, freeMiB)
}

func (s *Server) clusterNodeSpec(item clusterService, node clusterServiceNode) proto.InstanceSpec {
	zero := int64(0)
	peers := true
	return proto.InstanceSpec{
		Image:          proto.ImageRef{Name: "ubuntu", Version: "24.04"},
		RAMMB:          item.RAMMB,
		SwapMB:         &zero,
		CPU:            item.CPU,
		DiskMB:         item.DiskMB,
		Hostname:       strings.ReplaceAll(node.ID, "_", "-"),
		VPCID:          item.VPCID,
		ProtectionMode: clusterBoolFalse(),
		AllowVPCPeers:  &peers,
	}
}

func (s *Server) createClusterVPS(spec proto.InstanceSpec) (string, error) {
	payload := map[string]any{"spec": spec, "generate_pem": true, "auth_method": "key"}
	raw, _ := json.Marshal(payload)
	r, _ := http.NewRequest("POST", "/api/instances", bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), quotaLockKey{}, true))
	w := &lifecycleResponse{header: http.Header{}}
	s.createInstance(w, r)
	if w.code >= 400 {
		return "", fmt.Errorf("%s", strings.TrimSpace(w.String()))
	}
	var result struct {
		Instance struct {
			ID string `json:"id"`
		} `json:"instance"`
	}
	if e := json.Unmarshal(w.Bytes(), &result); e != nil {
		return "", e
	}
	if result.Instance.ID == "" {
		return "", fmt.Errorf("create returned empty instance id")
	}
	return result.Instance.ID, nil
}

func (s *Server) provisionClusterNodes(item *clusterService) {
	changed := false
	allRunning := true
	for i := range item.Nodes {
		node := &item.Nodes[i]
		if node.InstanceID == "" {
			iid, err := s.createClusterVPS(s.clusterNodeSpec(*item, *node))
			if err != nil {
				msg := clusterCreateErr(err)
				item.Status = clusterStatusFailed
				item.Error = "create VPS failed: " + msg
				node.Status = clusterNodeFailed
				node.Error = msg
				s.saveClusterService(*item)
				return
			}
			node.InstanceID = iid
			node.Status = clusterNodeProvisioning
			changed = true
		}
		inst, err := s.Store.GetInstance(node.InstanceID)
		if err != nil {
			allRunning = false
			continue
		}
		if inst.State == proto.StateFailed {
			item.Status = clusterStatusFailed
			item.Error = "VPS " + node.InstanceID + " failed"
			node.Status = clusterNodeFailed
			node.Error = inst.Error
			s.saveClusterService(*item)
			return
		}
		if inst.State == proto.StateRunning {
			node.Address = inst.Spec.IPAddress
			if node.Status != clusterNodeRunning && node.Status != clusterNodeAttached && node.Status != clusterNodeConfigured && node.Status != clusterNodeJoined {
				node.Status = clusterNodeRunning
				changed = true
			}
		} else {
			allRunning = false
		}
	}
	if allRunning && len(item.Nodes) == item.NodeCount {
		if item.Storage == clusterStoragePersistent {
			item.Status = clusterStatusAttaching
		} else {
			item.Status = clusterStatusConfiguring
		}
		item.Error = ""
		changed = true
	}
	if changed {
		item.Updated = time.Now().UTC()
		s.saveClusterService(*item)
	}
}

func (s *Server) attachClusterEFS(item *clusterService) {
	allAttached := true
	changed := false
	for i := range item.Nodes {
		node := &item.Nodes[i]
		if node.Status == clusterNodeAttached || node.Status == clusterNodeConfigured || node.Status == clusterNodeJoined {
			continue
		}
		inst, err := s.Store.GetInstance(node.InstanceID)
		if err != nil || inst.State != proto.StateRunning {
			allAttached = false
			continue
		}
		already := false
		for _, m := range inst.Spec.EFSMounts {
			if m.ID == item.EFSID && m.GuestPath == clusterEFSGuestPath {
				already = true
				break
			}
		}
		if !already {
			if err := s.attachEFSShare(item.EFSID, node.InstanceID, clusterEFSGuestPath); err != nil {
				item.Status = clusterStatusFailed
				item.Error = "EFS attach failed: " + err.Error()
				node.Status = clusterNodeFailed
				node.Error = err.Error()
				s.saveClusterService(*item)
				return
			}
			changed = true
			allAttached = false
			continue
		}
		// Wait until guest is running again after attach restart.
		if inst.State == proto.StateRunning {
			node.Status = clusterNodeAttached
			changed = true
		} else {
			allAttached = false
		}
	}
	if allAttached {
		item.Status = clusterStatusConfiguring
		changed = true
	}
	if changed {
		item.Updated = time.Now().UTC()
		s.saveClusterService(*item)
	}
}

func (s *Server) configureClusterNodes(item *clusterService) {
	changed := false
	allConfigured := true
	for i := range item.Nodes {
		node := &item.Nodes[i]
		if node.Status == clusterNodeConfigured || node.Status == clusterNodeJoined {
			continue
		}
		if node.PendingOp != "" {
			allConfigured = false
			continue
		}
		inst, err := s.Store.GetInstance(node.InstanceID)
		if err != nil || inst.State != proto.StateRunning {
			allConfigured = false
			continue
		}
		node.Address = inst.Spec.IPAddress
		dataDir := path.Join(clusterEFSGuestPath, item.ID, node.ID)
		opID := node.InstanceID + "/" + proto.ActionConfigureClusterNode + "/" + randHex(4)
		op := proto.Operation{
			ID: opID, Action: proto.ActionConfigureClusterNode, InstanceID: node.InstanceID,
			ClusterNode: &proto.ClusterNodeSpec{
				App: item.App, ClusterID: item.ID, NodeID: node.ID, Topology: item.Topology,
				Role: node.Role, DataDir: dataDir, Persistent: item.Storage == clusterStoragePersistent,
			},
		}
		if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
			item.Status = clusterStatusFailed
			item.Error = "configure queue failed: " + err.Error()
			node.Status = clusterNodeFailed
			node.Error = err.Error()
			s.saveClusterService(*item)
			return
		}
		node.PendingOp = opID
		allConfigured = false
		changed = true
	}
	if allConfigured {
		item.Status = clusterStatusJoining
		changed = true
	}
	if changed {
		item.Updated = time.Now().UTC()
		s.saveClusterService(*item)
	}
}

func (s *Server) joinClusterNodes(item *clusterService) {
	// Wait until no pending configure ops.
	for _, node := range item.Nodes {
		if node.PendingOp != "" {
			return
		}
		if node.Status != clusterNodeConfigured && node.Status != clusterNodeJoined {
			return
		}
	}
	primary := item.Nodes[0]
	for i := range item.Nodes {
		if item.Nodes[i].Role == "primary" {
			primary = item.Nodes[i]
			break
		}
	}
	if primary.PendingOp != "" {
		return
	}
	if primary.Status == clusterNodeJoined {
		item.Status = clusterStatusReady
		item.Endpoints = clusterEndpoints(*item)
		item.Updated = time.Now().UTC()
		s.saveClusterService(*item)
		return
	}
	inst, err := s.Store.GetInstance(primary.InstanceID)
	if err != nil || inst.State != proto.StateRunning {
		return
	}
	addrs := make([]string, 0, len(item.Nodes))
	for _, n := range item.Nodes {
		if n.Address == "" {
			return
		}
		addrs = append(addrs, n.Address+":6379")
	}
	// For master_slave, queue REPLICAOF on each replica instead of cluster create on primary.
	if item.Topology == clusterTopoMasterSlave {
		changed := false
		for i := range item.Nodes {
			node := &item.Nodes[i]
			if node.Role == "primary" {
				node.Status = clusterNodeJoined
				changed = true
				continue
			}
			if node.Status == clusterNodeJoined || node.PendingOp != "" {
				continue
			}
			rep, err := s.Store.GetInstance(node.InstanceID)
			if err != nil || rep.State != proto.StateRunning {
				continue
			}
			ropID := node.InstanceID + "/" + proto.ActionJoinCluster + "/" + randHex(4)
			rop := proto.Operation{
				ID: ropID, Action: proto.ActionJoinCluster, InstanceID: node.InstanceID,
				ClusterNode: &proto.ClusterNodeSpec{
					App: item.App, ClusterID: item.ID, NodeID: node.ID, Topology: item.Topology,
					Role: node.Role, DataDir: path.Join(clusterEFSGuestPath, item.ID, node.ID),
					Persistent: item.Storage == clusterStoragePersistent,
					PrimaryAddr: primary.Address + ":6379",
				},
			}
			if err := s.Store.QueueOp(rep.WorkerID, rop); err != nil {
				item.Status = clusterStatusFailed
				item.Error = "replica join queue failed: " + err.Error()
				s.saveClusterService(*item)
				return
			}
			node.PendingOp = ropID
			changed = true
		}
		allJoined := true
		for _, n := range item.Nodes {
			if n.Status != clusterNodeJoined {
				allJoined = false
				break
			}
		}
		if allJoined {
			item.Status = clusterStatusReady
			item.Endpoints = clusterEndpoints(*item)
		}
		if changed || allJoined {
			item.Updated = time.Now().UTC()
			s.saveClusterService(*item)
		}
		return
	}
	dataDir := path.Join(clusterEFSGuestPath, item.ID, primary.ID)
	opID := primary.InstanceID + "/" + proto.ActionJoinCluster + "/" + randHex(4)
	op := proto.Operation{
		ID: opID, Action: proto.ActionJoinCluster, InstanceID: primary.InstanceID,
		ClusterNode: &proto.ClusterNodeSpec{
			App: item.App, ClusterID: item.ID, NodeID: primary.ID, Topology: item.Topology,
			Role: primary.Role, DataDir: dataDir, Persistent: item.Storage == clusterStoragePersistent,
			PrimaryAddr: primary.Address + ":6379", MemberAddrs: addrs,
		},
	}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		item.Status = clusterStatusFailed
		item.Error = "cluster join queue failed: " + err.Error()
		s.saveClusterService(*item)
		return
	}
	for i := range item.Nodes {
		if item.Nodes[i].ID == primary.ID {
			item.Nodes[i].PendingOp = opID
		}
	}
	item.Updated = time.Now().UTC()
	s.saveClusterService(*item)
}

func clusterEndpoints(item clusterService) []string {
	out := make([]string, 0, len(item.Nodes))
	for _, n := range item.Nodes {
		if n.Address != "" {
			out = append(out, n.Address+":6379")
		}
	}
	return out
}

func (s *Server) teardownClusterService(item clusterService) {
	for i := range item.Nodes {
		node := &item.Nodes[i]
		if node.InstanceID == "" {
			continue
		}
		if item.EFSID != "" {
			s.detachClusterEFS(item.EFSID, node.InstanceID)
		}
		_, _ = s.lbLifecycle("delete", node.InstanceID, proto.InstanceSpec{})
		node.InstanceID = ""
		node.Status = clusterNodePending
	}
	d := s.clusters
	d.Lock()
	delete(d.Items, item.ID)
	_ = d.save()
	d.Unlock()
}

func (s *Server) saveClusterService(item clusterService) {
	d := s.clusters
	d.Lock()
	defer d.Unlock()
	item.Updated = time.Now().UTC()
	d.Items[item.ID] = item
	_ = d.save()
}

func (s *Server) completeClusterServiceOp(instanceID, action string, res proto.OpResult) {
	d := s.clusters
	d.Lock()
	defer d.Unlock()
	for id, item := range d.Items {
		changed := false
		for i := range item.Nodes {
			node := &item.Nodes[i]
			if node.InstanceID != instanceID || node.PendingOp == "" || !strings.HasPrefix(node.PendingOp, instanceID+"/"+action+"/") {
				continue
			}
			node.PendingOp = ""
			if !res.OK {
				node.Status = clusterNodeFailed
				node.Error = res.Error
				item.Status = clusterStatusFailed
				item.Error = action + " failed: " + res.Error
				changed = true
				break
			}
			switch action {
			case proto.ActionConfigureClusterNode:
				node.Status = clusterNodeConfigured
			case proto.ActionJoinCluster:
				node.Status = clusterNodeJoined
				if item.Topology == clusterTopoMasterMaster {
					for j := range item.Nodes {
						item.Nodes[j].Status = clusterNodeJoined
						item.Nodes[j].PendingOp = ""
					}
					item.Status = clusterStatusReady
					item.Endpoints = clusterEndpoints(item)
				}
			}
			changed = true
		}
		if changed {
			item.Updated = time.Now().UTC()
			d.Items[id] = item
			_ = d.save()
			return
		}
	}
}

func (s *Server) detachClusterEFS(shareID, instanceID string) {
	s.efs.Lock()
	share, ok := s.efs.Shares[shareID]
	if ok {
		filtered := make([]efsAttachment, 0, len(share.Attachments))
		for _, a := range share.Attachments {
			if a.InstanceID != instanceID {
				filtered = append(filtered, a)
			}
		}
		share.Attachments = filtered
		s.efs.Shares[shareID] = share
		_ = s.efs.save()
	}
	s.efs.Unlock()
	inst, err := s.Store.GetInstance(instanceID)
	if err != nil {
		return
	}
	inst.Spec.EFSMounts = removeInstanceEFS(inst.Spec.EFSMounts, shareID)
	_ = s.Store.SaveInstance(inst)
}
