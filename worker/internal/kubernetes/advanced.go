package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
)

func (m *Manager) joinControlPlane(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		m.Lock()
		m.records[op.NodeID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleControlPlane, State: k8s.StateRunning}
		m.Unlock()
		_ = m.persist()
		return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, At: time.Now().UTC()}
	}
	if op.Spec.JoinToken == "" || op.Spec.ControlPlaneEndpoint == "" || op.Spec.JoinCertificateKey == "" {
		return failResult(op, "control-plane join requires token, endpoint and certificate key")
	}
	args := []string{"join", op.Spec.ControlPlaneEndpoint + ":6443", "--token", op.Spec.JoinToken, "--control-plane", "--certificate-key", op.Spec.JoinCertificateKey}
	if op.Spec.JoinHash != "" {
		args = append(args, "--discovery-token-ca-cert-hash", op.Spec.JoinHash)
	}
	if out, err := m.cmd(ctx, 20*time.Minute, "kubeadm", args...); err != nil {
		return failResult(op, "kubeadm join control-plane: "+err.Error()+" "+string(out))
	}
	m.Lock()
	m.records[op.NodeID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleControlPlane, State: k8s.StateRunning}
	m.Unlock()
	_ = m.persist()
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, At: time.Now().UTC()}
}

func (m *Manager) helm(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeHelm(op)
	}
	if _, err := exec.LookPath("helm"); err != nil {
		return m.fakeHelm(op)
	}
	env := append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf")
	ns := op.Namespace
	if ns == "" {
		ns = "default"
	}
	var out []byte
	var err error
	switch strings.ToLower(op.HelmAction) {
	case "uninstall":
		out, err = m.cmdEnv(ctx, 10*time.Minute, env, "helm", "uninstall", op.Release, "-n", ns)
	case "upgrade":
		args := []string{"upgrade", "--install", op.Release, op.Chart, "-n", ns, "--create-namespace"}
		if op.Values != "" {
			file := filepath.Join(m.Root, op.JobID+"-values.yaml")
			if e := os.WriteFile(file, []byte(op.Values), 0600); e != nil {
				return failResult(op, e.Error())
			}
			defer os.Remove(file)
			args = append(args, "-f", file)
		}
		out, err = m.cmdEnv(ctx, 15*time.Minute, env, "helm", args...)
	default:
		args := []string{"install", op.Release, op.Chart, "-n", ns, "--create-namespace"}
		if op.Values != "" {
			file := filepath.Join(m.Root, op.JobID+"-values.yaml")
			if e := os.WriteFile(file, []byte(op.Values), 0600); e != nil {
				return failResult(op, e.Error())
			}
			defer os.Remove(file)
			args = append(args, "-f", file)
		}
		out, err = m.cmdEnv(ctx, 15*time.Minute, env, "helm", args...)
	}
	if err != nil {
		return failResult(op, "helm: "+err.Error()+" "+string(out))
	}
	return m.fakeHelm(op)
}

func (m *Manager) fakeHelm(op k8s.Operation) k8s.Result {
	ns := op.Namespace
	if ns == "" {
		ns = "default"
	}
	m.Lock()
	rec := m.records[op.Spec.ID]
	if strings.ToLower(op.HelmAction) == "uninstall" {
		kept := []k8s.HelmRelease{}
		for _, item := range rec.Inventory.Helm {
			if item.Name != op.Release || item.Namespace != ns {
				kept = append(kept, item)
			}
		}
		rec.Inventory.Helm = kept
	} else {
		rev := 1
		for _, item := range rec.Inventory.Helm {
			if item.Name == op.Release && item.Namespace == ns {
				rev = item.Revision + 1
			}
		}
		rec.Inventory.Helm = upsertHelm(rec.Inventory.Helm, k8s.HelmRelease{Name: op.Release, Namespace: ns, Chart: op.Chart, Status: "deployed", Revision: rev})
	}
	m.records[op.Spec.ID] = rec
	inv := rec.Inventory
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(inv)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) storage(ctx context.Context, op k8s.Operation) k8s.Result {
	return m.applyKind(ctx, op, "StorageClass", func(rec *record, kind, name, ns string) {
		rec.Inventory.Volumes = upsertWorkload(rec.Inventory.Volumes, k8s.Workload{Name: name, Namespace: ns, Kind: kind})
	})
}

func (m *Manager) ingress(ctx context.Context, op k8s.Operation) k8s.Result {
	return m.applyKind(ctx, op, "Ingress", func(rec *record, kind, name, ns string) {
		rec.Inventory.Ingresses = upsertWorkload(rec.Inventory.Ingresses, k8s.Workload{Name: name, Namespace: ns, Kind: kind})
	})
}

func (m *Manager) rbac(ctx context.Context, op k8s.Operation) k8s.Result {
	return m.applyKind(ctx, op, "Role", func(rec *record, kind, name, ns string) {
		rec.Inventory.RBAC = upsertWorkload(rec.Inventory.RBAC, k8s.Workload{Name: name, Namespace: ns, Kind: kind})
	})
}

func (m *Manager) hpa(ctx context.Context, op k8s.Operation) k8s.Result {
	return m.applyKind(ctx, op, "HorizontalPodAutoscaler", func(rec *record, kind, name, ns string) {
		rec.Inventory.HPA = upsertWorkload(rec.Inventory.HPA, k8s.Workload{Name: name, Namespace: ns, Kind: kind, Replicas: op.Replicas})
	})
}

func (m *Manager) applyKind(ctx context.Context, op k8s.Operation, fallback string, update func(*record, string, string, string)) k8s.Result {
	if !m.useFake() {
		if result := m.apply(ctx, op); result.State != "SUCCEEDED" {
			return result
		}
	}
	kind, name, ns := parseYAMLMeta(op.YAML)
	if kind == "" {
		kind = fallback
	}
	if name == "" {
		name = strings.ToLower(fallback)
	}
	m.Lock()
	rec := m.records[op.Spec.ID]
	update(&rec, kind, name, ns)
	m.records[op.Spec.ID] = rec
	inv := rec.Inventory
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(inv)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) backup(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeBackup(op)
	}
	file := filepath.Join(m.Root, "etcd-"+op.JobID+".db")
	out, err := m.cmdEnv(ctx, 5*time.Minute, append(os.Environ(), "ETCDCTL_API=3"), "etcdctl", "--endpoints=https://127.0.0.1:2379", "--cacert=/etc/kubernetes/pki/etcd/ca.crt", "--cert=/etc/kubernetes/pki/etcd/server.crt", "--key=/etc/kubernetes/pki/etcd/server.key", "snapshot", "save", file)
	if err != nil {
		return failResult(op, "etcd snapshot: "+err.Error()+" "+string(out))
	}
	raw, err := os.ReadFile(file)
	_ = os.Remove(file)
	if err != nil {
		return failResult(op, "etcd snapshot read: "+err.Error())
	}
	sum := sha256.Sum256(raw)
	info := k8s.BackupInfo{ID: op.BackupID, SizeBytes: int64(len(raw)), Digest: hex.EncodeToString(sum[:])}
	data, _ := json.Marshal(map[string]any{"backup": info, "snapshot": raw})
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) fakeBackup(op k8s.Operation) k8s.Result {
	raw, _ := json.Marshal(m.currentInventory(op.Spec.ID))
	sum := sha256.Sum256(raw)
	info := k8s.BackupInfo{ID: op.BackupID, SizeBytes: int64(len(raw)), Digest: hex.EncodeToString(sum[:])}
	data, _ := json.Marshal(map[string]any{"backup": info, "snapshot": raw})
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) restore(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		if len(op.Snapshot) > 0 {
			var inv k8s.Inventory
			_ = json.Unmarshal(op.Snapshot, &inv)
			m.Lock()
			rec := m.records[op.Spec.ID]
			if inv.Pods != nil || inv.Deployments != nil {
				rec.Inventory = inv
			}
			m.records[op.Spec.ID] = rec
			m.Unlock()
			_ = m.persist()
		}
		data, _ := json.Marshal(m.currentInventory(op.Spec.ID))
		return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
	}
	if len(op.Snapshot) == 0 {
		return failResult(op, "restore snapshot is empty")
	}
	file := filepath.Join(m.Root, "etcd-restore-"+op.JobID+".db")
	if err := os.WriteFile(file, op.Snapshot, 0600); err != nil {
		return failResult(op, err.Error())
	}
	defer os.Remove(file)
	out, err := m.cmd(ctx, 10*time.Minute, "etcdctl", "snapshot", "restore", file, "--data-dir=/var/lib/etcd-restore")
	if err != nil {
		return failResult(op, "etcd restore: "+err.Error()+" "+string(out))
	}
	return m.get(ctx, op)
}

func (m *Manager) upgrade(ctx context.Context, op k8s.Operation) k8s.Result {
	version := k8s.NormalizeVersion(op.Spec.TargetVersion)
	if version == "" {
		version = k8s.NormalizeVersion(op.Spec.Version)
	}
	if !k8s.VersionSupported(version) {
		return failResult(op, "unsupported kubernetes version")
	}
	if !m.useFake() {
		if out, err := m.cmd(ctx, 25*time.Minute, "kubeadm", "upgrade", "apply", "v"+version, "-y"); err != nil {
			return failResult(op, "kubeadm upgrade: "+err.Error()+" "+string(out))
		}
		_, _ = m.cmd(ctx, 5*time.Minute, "kubeadm", "upgrade", "node")
	}
	m.Lock()
	rec := m.records[op.Spec.ID]
	if rec.Spec.ID == "" {
		rec = m.records[op.NodeID]
	}
	rec.Spec.Version = version
	rec.State = k8s.StateRunning
	if rec.Spec.ID != "" {
		m.records[op.Spec.ID] = rec
	}
	if op.NodeID != "" {
		node := m.records[op.NodeID]
		node.Spec.Version = version
		node.State = k8s.StateRunning
		m.records[op.NodeID] = node
	}
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(map[string]string{"version": version})
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func upsertHelm(list []k8s.HelmRelease, item k8s.HelmRelease) []k8s.HelmRelease {
	for i, existing := range list {
		if existing.Name == item.Name && existing.Namespace == item.Namespace {
			list[i] = item
			return list
		}
	}
	return append(list, item)
}
