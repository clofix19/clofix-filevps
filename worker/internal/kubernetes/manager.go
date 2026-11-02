// Package kubernetes provisions kubeadm nodes on the worker host (not FileVPS guests).
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
)

type record struct {
	Spec        k8s.Spec       `json:"spec"`
	NodeID      string         `json:"node_id"`
	Role        string         `json:"role"`
	State       string         `json:"state"`
	Kubeconfig  string         `json:"kubeconfig,omitempty"`
	APIEndpoint string         `json:"api_endpoint,omitempty"`
	Inventory   k8s.Inventory  `json:"inventory,omitempty"`
	Workloads   []k8s.Workload `json:"workloads,omitempty"`
	Pods        []k8s.Pod      `json:"pods,omitempty"`
}

type Manager struct {
	Root    string
	Fake    bool
	Ready   bool
	Version string
	Host    string
	labURL  string
	labOnce sync.Once
	sync.Mutex
	records map[string]record
	results map[string]k8s.Result
	cancels map[string]context.CancelFunc
}

func New(root string) (*Manager, error) {
	m := &Manager{Root: filepath.Join(root, "kubernetes"), records: map[string]record{}, results: map[string]k8s.Result{}, cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(m.Root, 0700); err != nil {
		return nil, err
	}
	m.Host = os.Getenv("CLOFIX_KUBERNETES_PUBLIC_HOST")
	m.Fake = os.Getenv("CLOFIX_KUBERNETES_FAKE") == "1"
	m.probe()
	if m.Fake {
		m.ensureLabHTTP()
	}
	if raw, err := os.ReadFile(filepath.Join(m.Root, "nodes.json")); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &m.records)
	}
	return m, nil
}

func (m *Manager) probe() {
	if m.Fake {
		m.Ready = true
		m.Version = k8s.SupportedVersions[0]
		return
	}
	kubeadm, kubeadmErr := exec.LookPath("kubeadm")
	_, kubeletErr := exec.LookPath("kubelet")
	_, kubectlErr := exec.LookPath("kubectl")
	containerd := exec.Command("containerd", "--version")
	containerdErr := containerd.Run()
	if kubeadmErr != nil || kubeletErr != nil || kubectlErr != nil || containerdErr != nil {
		m.Ready = false
		return
	}
	out, err := exec.Command(kubeadm, "version", "-o", "short").Output()
	if err != nil {
		m.Ready = false
		return
	}
	version := k8s.NormalizeVersion(strings.TrimSpace(string(out)))
	if !k8s.VersionSupported(version) {
		m.Ready = false
		return
	}
	m.Ready = true
	m.Version = version
}

func (m *Manager) Inventory() (bool, []string, []string, string) {
	m.Lock()
	defer m.Unlock()
	if !m.Ready {
		return false, nil, nil, m.Host
	}
	nodes := []string{}
	for _, rec := range m.records {
		if rec.NodeID != "" {
			nodes = append(nodes, rec.NodeID)
		} else {
			nodes = append(nodes, rec.Spec.ID)
		}
	}
	versions := []string{m.Version}
	if m.Version != k8s.SupportedVersions[0] {
		versions = k8s.SupportedVersions
	} else {
		versions = append([]string{}, k8s.SupportedVersions...)
	}
	return true, versions, nodes, m.Host
}

func (m *Manager) Reserved() (uint64, float64, uint64) {
	m.Lock()
	defer m.Unlock()
	var ram, disk uint64
	var cpu float64
	for _, rec := range m.records {
		if rec.State == k8s.StateDeleted {
			continue
		}
		ram += uint64(rec.Spec.RAMMB) << 20
		cpu += rec.Spec.CPU
		disk += uint64(rec.Spec.DiskMB) << 20
	}
	return ram, cpu, disk
}

func (m *Manager) Results() []k8s.Result {
	m.Lock()
	defer m.Unlock()
	out := []k8s.Result{}
	for _, r := range m.results {
		out = append(out, r)
	}
	return out
}

func (m *Manager) Statuses() []k8s.Status {
	m.Lock()
	defer m.Unlock()
	out := []k8s.Status{}
	for _, rec := range m.records {
		if rec.State == k8s.StateDeleted {
			continue
		}
		out = append(out, k8s.Status{ClusterID: rec.Spec.ID, NodeID: rec.NodeID, Role: rec.Role, State: rec.State, Version: rec.Spec.Version, APIEndpoint: rec.APIEndpoint, Inventory: rec.Inventory})
	}
	return out
}

func (m *Manager) Acknowledge(ids []string) {
	m.Lock()
	defer m.Unlock()
	for _, id := range ids {
		delete(m.results, id)
	}
}

func (m *Manager) Reject(op k8s.Operation, message string) {
	m.storeResult(failResult(op, message))
}

func (m *Manager) Run(ctx context.Context, op k8s.Operation) k8s.Result {
	result := k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "FAILED", At: time.Now().UTC()}
	if !m.Ready && !m.Fake {
		result.Error = "kubernetes is not available on this worker"
		m.storeResult(result)
		return result
	}
	if err := op.Validate(); err != nil {
		result.Error = err.Error()
		m.storeResult(result)
		return result
	}
	m.Lock()
	if previous, ok := m.results[op.JobID]; ok {
		m.Unlock()
		return previous
	}
	jobCtx, cancel := context.WithCancel(ctx)
	m.cancels[op.JobID] = cancel
	m.Unlock()
	defer func() {
		m.Lock()
		delete(m.cancels, op.JobID)
		m.Unlock()
		cancel()
	}()
	switch op.Action {
	case k8s.ActionInit:
		result = m.init(jobCtx, op)
	case k8s.ActionJoin:
		result = m.join(jobCtx, op)
	case k8s.ActionJoinControlPlane:
		result = m.joinControlPlane(jobCtx, op)
	case k8s.ActionReset:
		result = m.reset(jobCtx, op)
	case k8s.ActionApply:
		result = m.apply(jobCtx, op)
	case k8s.ActionDelete:
		result = m.delete(jobCtx, op)
	case k8s.ActionGet:
		result = m.get(jobCtx, op)
	case k8s.ActionLogs:
		result = m.logs(jobCtx, op)
	case k8s.ActionScale:
		result = m.scale(jobCtx, op)
	case k8s.ActionRestart:
		result = m.restart(jobCtx, op)
	case k8s.ActionHelm:
		result = m.helm(jobCtx, op)
	case k8s.ActionStorage:
		result = m.storage(jobCtx, op)
	case k8s.ActionIngress:
		result = m.ingress(jobCtx, op)
	case k8s.ActionRBAC:
		result = m.rbac(jobCtx, op)
	case k8s.ActionHPA:
		result = m.hpa(jobCtx, op)
	case k8s.ActionBackup:
		result = m.backup(jobCtx, op)
	case k8s.ActionRestore:
		result = m.restore(jobCtx, op)
	case k8s.ActionUpgrade:
		result = m.upgrade(jobCtx, op)
	default:
		result.Error = "unsupported kubernetes action"
	}
	m.storeResult(result)
	return result
}

func (m *Manager) storeResult(result k8s.Result) {
	m.Lock()
	defer m.Unlock()
	m.results[result.JobID] = result
}

func (m *Manager) init(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeInit(op)
	}
	advertise := op.Spec.AdvertiseAddress
	if advertise == "" {
		advertise = "127.0.0.1"
	}
	version := k8s.NormalizeVersion(op.Spec.Version)
	if m.Version != "" {
		version = m.Version
	}
	args := []string{"init", "--kubernetes-version=v" + version, "--pod-network-cidr=10.244.0.0/16", "--apiserver-advertise-address=" + advertise}
	if op.Spec.ControlPlaneEndpoint != "" {
		args = append(args, "--control-plane-endpoint="+op.Spec.ControlPlaneEndpoint+":6443")
	}
	if op.Spec.HA {
		args = append(args, "--upload-certs")
	}
	if out, err := m.cmd(ctx, 20*time.Minute, "kubeadm", args...); err != nil {
		return failResult(op, "kubeadm init: "+err.Error()+" "+string(out))
	}
	kubeconfig, err := os.ReadFile("/etc/kubernetes/admin.conf")
	if err != nil {
		return failResult(op, "kubeconfig: "+err.Error())
	}
	env := append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf")
	if op.Spec.SingleNode {
		_, _ = m.cmdEnv(ctx, 2*time.Minute, env, "kubectl", "taint", "nodes", "--all", "node-role.kubernetes.io/control-plane-")
	}
	flannel := filepath.Join(m.Root, "flannel.yaml")
	if err := os.WriteFile(flannel, []byte(flannelManifest), 0600); err == nil {
		if out, err := m.cmdEnv(ctx, 5*time.Minute, env, "kubectl", "apply", "-f", flannel); err != nil {
			return failResult(op, "cni: "+err.Error()+" "+string(out))
		}
	}
	tokenOut, err := m.cmd(ctx, 30*time.Second, "kubeadm", "token", "create", "--print-join-command")
	if err != nil {
		return failResult(op, "join token: "+err.Error())
	}
	token, hash := parseJoin(string(tokenOut))
	certKey := ""
	if op.Spec.HA {
		if out, err := m.cmd(ctx, 30*time.Second, "kubeadm", "init", "phase", "upload-certs", "--upload-certs"); err == nil {
			certKey = parseCertificateKey(string(out))
		}
	}
	endpoint := "https://" + advertise + ":6443"
	m.Lock()
	m.records[op.Spec.ID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleControlPlane, State: k8s.StateRunning, Kubeconfig: string(kubeconfig), APIEndpoint: endpoint}
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(map[string]string{"kubeconfig": string(kubeconfig), "api_endpoint": endpoint, "join_token": token, "join_hash": hash, "control_plane_endpoint": advertise, "join_certificate_key": certKey})
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) join(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeJoin(op)
	}
	if op.Spec.JoinToken == "" || op.Spec.ControlPlaneEndpoint == "" {
		return failResult(op, "join token and control plane endpoint are required")
	}
	args := []string{"join", op.Spec.ControlPlaneEndpoint + ":6443", "--token", op.Spec.JoinToken}
	if op.Spec.JoinHash != "" {
		args = append(args, "--discovery-token-ca-cert-hash", op.Spec.JoinHash)
	}
	if out, err := m.cmd(ctx, 15*time.Minute, "kubeadm", args...); err != nil {
		return failResult(op, "kubeadm join: "+err.Error()+" "+string(out))
	}
	m.Lock()
	m.records[op.NodeID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleWorker, State: k8s.StateRunning}
	m.Unlock()
	_ = m.persist()
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, At: time.Now().UTC()}
}

func (m *Manager) reset(ctx context.Context, op k8s.Operation) k8s.Result {
	if !m.useFake() {
		_, _ = m.cmd(ctx, 5*time.Minute, "kubeadm", "reset", "-f")
	}
	m.Lock()
	delete(m.records, op.Spec.ID)
	delete(m.records, op.NodeID)
	m.Unlock()
	_ = m.persist()
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, At: time.Now().UTC()}
}

func (m *Manager) apply(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeApply(op)
	}
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf")
	cmd.Stdin = strings.NewReader(op.YAML)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return failResult(op, "kubectl apply: "+err.Error()+" "+string(out))
	}
	return m.get(ctx, op)
}

func (m *Manager) delete(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeDelete(op)
	}
	env := append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf")
	var out []byte
	var err error
	if strings.TrimSpace(op.YAML) != "" {
		cmd := exec.CommandContext(ctx, "kubectl", "delete", "-f", "-", "--ignore-not-found")
		cmd.Env = env
		cmd.Stdin = strings.NewReader(op.YAML)
		out, err = cmd.CombinedOutput()
	} else {
		args := []string{"delete", op.Kind, op.Name, "--ignore-not-found"}
		if op.Namespace != "" {
			args = append(args, "-n", op.Namespace)
		}
		out, err = m.cmdEnv(ctx, 2*time.Minute, env, "kubectl", args...)
	}
	if err != nil {
		return failResult(op, "kubectl delete: "+err.Error()+" "+string(out))
	}
	return m.get(ctx, op)
}

func (m *Manager) get(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeGet(op)
	}
	inv, err := m.liveInventory(ctx)
	if err != nil {
		return failResult(op, err.Error())
	}
	data, _ := json.Marshal(inv)
	m.Lock()
	rec := m.records[op.Spec.ID]
	rec.Inventory = inv
	m.records[op.Spec.ID] = rec
	m.Unlock()
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) logs(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		line := "fake pod log: " + op.Namespace + "/" + op.Name + " is running"
		if strings.Contains(strings.ToLower(op.Name), "nginx") {
			line = "127.0.0.1 - - [cks-lab] \"GET / HTTP/1.1\" 200 612 \"-\" \"curl\"\nnginx lab listener http://127.0.0.1:" + labHTTPPort + "\n"
		}
		data, _ := json.Marshal(k8s.Inventory{Logs: line})
		return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
	}
	tail := op.Tail
	if tail <= 0 {
		tail = 200
	}
	out, err := m.cmdEnv(ctx, 30*time.Second, append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf"), "kubectl", "logs", "-n", op.Namespace, op.Name, fmt.Sprintf("--tail=%d", tail))
	if err != nil {
		return failResult(op, "kubectl logs: "+err.Error()+" "+string(out))
	}
	data, _ := json.Marshal(k8s.Inventory{Logs: string(out)})
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) scale(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		return m.fakeScale(op)
	}
	kind := op.Kind
	if kind == "" {
		kind = "deployment"
	}
	out, err := m.cmdEnv(ctx, 2*time.Minute, append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf"), "kubectl", "scale", strings.ToLower(kind)+"/"+op.Name, "-n", op.Namespace, fmt.Sprintf("--replicas=%d", op.Replicas))
	if err != nil {
		return failResult(op, "kubectl scale: "+err.Error()+" "+string(out))
	}
	return m.get(ctx, op)
}

func (m *Manager) restart(ctx context.Context, op k8s.Operation) k8s.Result {
	if m.useFake() {
		data, _ := json.Marshal(m.currentInventory(op.Spec.ID))
		return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
	}
	kind := op.Kind
	if kind == "" {
		kind = "deployment"
	}
	out, err := m.cmdEnv(ctx, 2*time.Minute, append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf"), "kubectl", "rollout", "restart", strings.ToLower(kind)+"/"+op.Name, "-n", op.Namespace)
	if err != nil {
		return failResult(op, "kubectl rollout: "+err.Error()+" "+string(out))
	}
	return m.get(ctx, op)
}

func (m *Manager) useFake() bool { return m.Fake }

func (m *Manager) fakeInit(op k8s.Operation) k8s.Result {
	endpoint := "https://127.0.0.1:6443"
	if op.Spec.AdvertiseAddress != "" {
		endpoint = "https://" + op.Spec.AdvertiseAddress + ":6443"
	}
	kubeconfig := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- cluster:\n    server: %s\n    insecure-skip-tls-verify: true\n  name: %s\ncontexts:\n- context:\n    cluster: %s\n    user: admin\n  name: %s\ncurrent-context: %s\nusers:\n- name: admin\n  user:\n    token: fake-cks-token\n", endpoint, op.Spec.ID, op.Spec.ID, op.Spec.ID, op.Spec.ID)
	inv := k8s.Inventory{Namespaces: []string{"default", "kube-system"}, Pods: []k8s.Pod{{Name: "coredns-0", Namespace: "kube-system", Phase: "Running", Node: op.NodeID}}, Deployments: []k8s.Workload{{Name: "coredns", Namespace: "kube-system", Kind: "Deployment", Replicas: 1, Ready: 1}}, Services: []k8s.Workload{{Name: "kubernetes", Namespace: "default", Kind: "Service"}}}
	m.Lock()
	m.records[op.Spec.ID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleControlPlane, State: k8s.StateRunning, Kubeconfig: kubeconfig, APIEndpoint: endpoint, Inventory: inv}
	m.Unlock()
	_ = m.persist()
	host := op.Spec.AdvertiseAddress
	if host == "" {
		host = "127.0.0.1"
	}
	payload := map[string]string{"kubeconfig": kubeconfig, "api_endpoint": endpoint, "join_token": "fake.token", "join_hash": "sha256:deadbeef", "control_plane_endpoint": host}
	if op.Spec.HA {
		payload["join_certificate_key"] = "fake-certificate-key"
	}
	data, _ := json.Marshal(payload)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) fakeJoin(op k8s.Operation) k8s.Result {
	m.Lock()
	m.records[op.NodeID] = record{Spec: op.Spec, NodeID: op.NodeID, Role: k8s.RoleWorker, State: k8s.StateRunning}
	m.Unlock()
	_ = m.persist()
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, At: time.Now().UTC()}
}

func (m *Manager) fakeApply(op k8s.Operation) k8s.Result {
	docs := splitYAMLDocuments(op.YAML)
	if len(docs) == 0 {
		return failResult(op, "YAML must include metadata.name")
	}
	publicURL := m.ensureLabHTTP()
	m.Lock()
	rec := m.records[op.Spec.ID]
	applied := 0
	for _, doc := range docs {
		kind, name, ns := parseYAMLMeta(doc)
		if name == "" {
			continue
		}
		applied++
		item := k8s.Workload{Name: name, Namespace: ns, Kind: kind, Replicas: 1, Ready: 1}
		fillWorkloadSpec(&item, doc)
		switch strings.ToLower(kind) {
		case "namespace":
			rec.Inventory.Namespaces = appendUnique(rec.Inventory.Namespaces, name)
		case "deployment":
			item.Kind, item.URL = "Deployment", publicURL
			rec.Inventory.Deployments = upsertWorkload(rec.Inventory.Deployments, item)
			rec.Inventory.Pods = upsertPod(rec.Inventory.Pods, k8s.Pod{Name: name + "-0", Namespace: ns, Phase: "Running", Node: rec.NodeID})
		case "replicaset":
			item.Kind = "ReplicaSet"
			rec.Inventory.ReplicaSets = upsertWorkload(rec.Inventory.ReplicaSets, item)
		case "daemonset":
			item.Kind = "DaemonSet"
			rec.Inventory.DaemonSets = upsertWorkload(rec.Inventory.DaemonSets, item)
		case "statefulset":
			item.Kind = "StatefulSet"
			rec.Inventory.StatefulSets = upsertWorkload(rec.Inventory.StatefulSets, item)
			rec.Inventory.Pods = upsertPod(rec.Inventory.Pods, k8s.Pod{Name: name + "-0", Namespace: ns, Phase: "Running", Node: rec.NodeID})
		case "job":
			item.Kind = "Job"
			rec.Inventory.Jobs = upsertWorkload(rec.Inventory.Jobs, item)
		case "cronjob":
			item.Kind = "CronJob"
			rec.Inventory.CronJobs = upsertWorkload(rec.Inventory.CronJobs, item)
		case "service":
			item.Kind, item.URL, item.Replicas, item.Ready = "Service", publicURL, 0, 0
			rec.Inventory.Services = upsertWorkload(rec.Inventory.Services, item)
		case "ingress":
			item.Kind, item.URL, item.Replicas, item.Ready = "Ingress", publicURL, 0, 0
			rec.Inventory.Ingresses = upsertWorkload(rec.Inventory.Ingresses, item)
		case "secret":
			item.Kind, item.Replicas, item.Ready = "Secret", 0, 0
			rec.Inventory.Secrets = upsertWorkload(rec.Inventory.Secrets, item)
		case "configmap":
			item.Kind, item.Replicas, item.Ready = "ConfigMap", 0, 0
			rec.Inventory.ConfigMaps = upsertWorkload(rec.Inventory.ConfigMaps, item)
		case "persistentvolumeclaim":
			item.Kind, item.Replicas, item.Ready = "PersistentVolumeClaim", 0, 0
			rec.Inventory.PersistentVolumeClaims = upsertWorkload(rec.Inventory.PersistentVolumeClaims, item)
			rec.Inventory.Volumes = upsertWorkload(rec.Inventory.Volumes, item)
		case "persistentvolume":
			item.Kind, item.Replicas, item.Ready = "PersistentVolume", 0, 0
			rec.Inventory.PersistentVolumes = upsertWorkload(rec.Inventory.PersistentVolumes, item)
			rec.Inventory.Volumes = upsertWorkload(rec.Inventory.Volumes, item)
		case "storageclass":
			item.Kind, item.Replicas, item.Ready = "StorageClass", 0, 0
			rec.Inventory.StorageClasses = upsertWorkload(rec.Inventory.StorageClasses, item)
			rec.Inventory.Volumes = upsertWorkload(rec.Inventory.Volumes, item)
		case "serviceaccount":
			item.Kind, item.Replicas, item.Ready = "ServiceAccount", 0, 0
			rec.Inventory.ServiceAccounts = upsertWorkload(rec.Inventory.ServiceAccounts, item)
		case "networkpolicy":
			item.Kind, item.Replicas, item.Ready = "NetworkPolicy", 0, 0
			rec.Inventory.NetworkPolicies = upsertWorkload(rec.Inventory.NetworkPolicies, item)
		case "role", "rolebinding", "clusterrole", "clusterrolebinding":
			item.Replicas, item.Ready = 0, 0
			rec.Inventory.RBAC = upsertWorkload(rec.Inventory.RBAC, item)
		case "horizontalpodautoscaler":
			item.Kind = "HorizontalPodAutoscaler"
			rec.Inventory.HPA = upsertWorkload(rec.Inventory.HPA, item)
		case "pod":
			rec.Inventory.Pods = upsertPod(rec.Inventory.Pods, k8s.Pod{Name: name, Namespace: ns, Phase: "Running", Node: rec.NodeID})
		case "endpoints":
			item.Kind, item.Replicas, item.Ready = "Endpoints", 0, 0
			rec.Inventory.Services = upsertWorkload(rec.Inventory.Services, item)
		default:
			rec.Inventory.Deployments = upsertWorkload(rec.Inventory.Deployments, item)
		}
	}
	if applied == 0 {
		m.Unlock()
		return failResult(op, "YAML must include metadata.name")
	}
	m.records[op.Spec.ID] = rec
	inv := rec.Inventory
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(inv)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) fakeDelete(op k8s.Operation) k8s.Result {
	docs := splitYAMLDocuments(op.YAML)
	if len(docs) == 0 && op.Name != "" {
		docs = []string{"kind: " + op.Kind + "\nmetadata:\n  name: " + op.Name + "\n  namespace: " + op.Namespace + "\n"}
	}
	m.Lock()
	rec := m.records[op.Spec.ID]
	removed := 0
	for _, doc := range docs {
		kind, name, ns := parseYAMLMeta(doc)
		if name == "" {
			continue
		}
		removed++
		switch strings.ToLower(kind) {
		case "namespace":
			rec.Inventory.Namespaces = removeString(rec.Inventory.Namespaces, name)
		case "deployment":
			rec.Inventory.Deployments = removeWorkload(rec.Inventory.Deployments, name, ns)
			rec.Inventory.Pods = removePod(rec.Inventory.Pods, name+"-0", ns)
		case "replicaset":
			rec.Inventory.ReplicaSets = removeWorkload(rec.Inventory.ReplicaSets, name, ns)
		case "daemonset":
			rec.Inventory.DaemonSets = removeWorkload(rec.Inventory.DaemonSets, name, ns)
		case "statefulset":
			rec.Inventory.StatefulSets = removeWorkload(rec.Inventory.StatefulSets, name, ns)
			rec.Inventory.Pods = removePod(rec.Inventory.Pods, name+"-0", ns)
		case "job":
			rec.Inventory.Jobs = removeWorkload(rec.Inventory.Jobs, name, ns)
		case "cronjob":
			rec.Inventory.CronJobs = removeWorkload(rec.Inventory.CronJobs, name, ns)
		case "service", "endpoints":
			rec.Inventory.Services = removeWorkload(rec.Inventory.Services, name, ns)
		case "ingress":
			rec.Inventory.Ingresses = removeWorkload(rec.Inventory.Ingresses, name, ns)
		case "secret":
			rec.Inventory.Secrets = removeWorkload(rec.Inventory.Secrets, name, ns)
		case "configmap":
			rec.Inventory.ConfigMaps = removeWorkload(rec.Inventory.ConfigMaps, name, ns)
		case "persistentvolumeclaim":
			rec.Inventory.PersistentVolumeClaims = removeWorkload(rec.Inventory.PersistentVolumeClaims, name, ns)
			rec.Inventory.Volumes = removeWorkload(rec.Inventory.Volumes, name, ns)
		case "persistentvolume":
			rec.Inventory.PersistentVolumes = removeWorkload(rec.Inventory.PersistentVolumes, name, ns)
			rec.Inventory.Volumes = removeWorkload(rec.Inventory.Volumes, name, ns)
		case "storageclass":
			rec.Inventory.StorageClasses = removeWorkload(rec.Inventory.StorageClasses, name, ns)
			rec.Inventory.Volumes = removeWorkload(rec.Inventory.Volumes, name, ns)
		case "serviceaccount":
			rec.Inventory.ServiceAccounts = removeWorkload(rec.Inventory.ServiceAccounts, name, ns)
		case "networkpolicy":
			rec.Inventory.NetworkPolicies = removeWorkload(rec.Inventory.NetworkPolicies, name, ns)
		case "role", "rolebinding", "clusterrole", "clusterrolebinding":
			rec.Inventory.RBAC = removeWorkload(rec.Inventory.RBAC, name, ns)
		case "horizontalpodautoscaler":
			rec.Inventory.HPA = removeWorkload(rec.Inventory.HPA, name, ns)
		case "pod":
			rec.Inventory.Pods = removePod(rec.Inventory.Pods, name, ns)
		default:
			rec.Inventory.Deployments = removeWorkload(rec.Inventory.Deployments, name, ns)
		}
	}
	if removed == 0 {
		m.Unlock()
		return failResult(op, "delete requires kind and metadata.name")
	}
	m.records[op.Spec.ID] = rec
	inv := rec.Inventory
	m.Unlock()
	_ = m.persist()
	data, _ := json.Marshal(inv)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) fakeGet(op k8s.Operation) k8s.Result {
	data, _ := json.Marshal(m.currentInventory(op.Spec.ID))
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) fakeScale(op k8s.Operation) k8s.Result {
	m.Lock()
	rec := m.records[op.Spec.ID]
	rec.Inventory.Deployments = upsertWorkload(rec.Inventory.Deployments, k8s.Workload{Name: op.Name, Namespace: op.Namespace, Kind: "Deployment", Replicas: op.Replicas, Ready: op.Replicas})
	m.records[op.Spec.ID] = rec
	inv := rec.Inventory
	m.Unlock()
	data, _ := json.Marshal(inv)
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "SUCCEEDED", Progress: 100, Data: data, At: time.Now().UTC()}
}

func (m *Manager) currentInventory(id string) k8s.Inventory {
	m.Lock()
	defer m.Unlock()
	return m.records[id].Inventory
}

func (m *Manager) liveInventory(ctx context.Context) (k8s.Inventory, error) {
	env := append(os.Environ(), "KUBECONFIG=/etc/kubernetes/admin.conf")
	nsOut, err := m.cmdEnv(ctx, 30*time.Second, env, "kubectl", "get", "ns", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return k8s.Inventory{}, fmt.Errorf("kubectl get ns: %s", strings.TrimSpace(string(nsOut)+" "+err.Error()))
	}
	inv := k8s.Inventory{Namespaces: strings.Fields(string(nsOut))}
	return inv, nil
}

func (m *Manager) persist() error {
	m.Lock()
	raw, err := json.Marshal(m.records)
	m.Unlock()
	if err != nil {
		return err
	}
	file := filepath.Join(m.Root, "nodes.json")
	if err = os.WriteFile(file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}

func (m *Manager) cmd(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	return m.cmdEnv(ctx, timeout, nil, name, args...)
}

func (m *Manager) cmdEnv(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) ([]byte, error) {
	job, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(job, name, args...)
	if env != nil {
		cmd.Env = env
	}
	return cmd.CombinedOutput()
}

func failResult(op k8s.Operation, message string) k8s.Result {
	return k8s.Result{JobID: op.JobID, ClusterID: op.ClusterID, NodeID: op.NodeID, State: "FAILED", Error: message, At: time.Now().UTC()}
}

func parseCertificateKey(out string) string {
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if len(trim) >= 32 && !strings.Contains(trim, " ") && !strings.Contains(trim, ":") {
			return trim
		}
		fields := strings.Fields(trim)
		for i, field := range fields {
			if field == "--certificate-key" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

func parseJoin(command string) (token, hash string) {
	fields := strings.Fields(command)
	for i, field := range fields {
		if field == "--token" && i+1 < len(fields) {
			token = fields[i+1]
		}
		if field == "--discovery-token-ca-cert-hash" && i+1 < len(fields) {
			hash = fields[i+1]
		}
		if strings.HasPrefix(field, "sha256:") {
			hash = field
		}
	}
	return token, hash
}

func splitYAMLDocuments(raw string) []string {
	parts := strings.Split(raw, "\n---")
	out := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.TrimSpace(part) == "---" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func fillWorkloadSpec(item *k8s.Workload, raw string) {
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		lower := strings.ToLower(trim)
		value := strings.TrimSpace(trim[strings.Index(trim, ":")+1:])
		value = strings.Trim(value, `"'`)
		switch {
		case strings.HasPrefix(lower, "replicas:"):
			fmt.Sscanf(value, "%d", &item.Replicas)
		case strings.HasPrefix(lower, "image:"):
			item.Image = value
		case strings.HasPrefix(lower, "containerport:"), strings.HasPrefix(lower, "targetport:"):
			var port int
			fmt.Sscanf(value, "%d", &port)
			if port > 0 {
				if item.Port == 0 {
					item.Port = port
				}
				item.Ports = appendWorkloadPort(item.Ports, port)
			}
		case strings.HasPrefix(lower, "nodeport:"):
			fmt.Sscanf(value, "%d", &item.NodePort)
			item.Ports = appendWorkloadPort(item.Ports, item.NodePort)
		case strings.HasPrefix(lower, "port:"):
			var port int
			fmt.Sscanf(value, "%d", &port)
			if port > 0 {
				if item.Port == 0 {
					item.Port = port
				}
				item.Ports = appendWorkloadPort(item.Ports, port)
			}
		case strings.HasPrefix(lower, "type:") && (strings.EqualFold(value, "ClusterIP") || strings.EqualFold(value, "NodePort") || strings.EqualFold(value, "LoadBalancer")):
			item.Type = value
		case strings.HasPrefix(lower, "storage:"):
			item.Storage = value
		case strings.HasPrefix(lower, "schedule:"):
			item.Schedule = value
		case strings.HasPrefix(lower, "app:") && item.Selector == "":
			item.Selector = value
		}
	}
}

func appendWorkloadPort(ports []int, port int) []int {
	if port < 1 || port > 65535 {
		return ports
	}
	for _, existing := range ports {
		if existing == port {
			return ports
		}
	}
	return append(ports, port)
}

func parseYAMLMeta(raw string) (kind, name, ns string) {
	ns = "default"
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		lower := strings.ToLower(trim)
		if strings.HasPrefix(lower, "kind:") {
			kind = strings.TrimSpace(trim[5:])
		}
		if strings.HasPrefix(lower, "name:") && name == "" {
			name = strings.TrimSpace(trim[5:])
		}
		if strings.HasPrefix(lower, "namespace:") {
			ns = strings.TrimSpace(trim[10:])
		}
	}
	return kind, name, ns
}

func appendUnique(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func upsertWorkload(list []k8s.Workload, item k8s.Workload) []k8s.Workload {
	for i, existing := range list {
		if existing.Name == item.Name && existing.Namespace == item.Namespace {
			list[i] = item
			return list
		}
	}
	return append(list, item)
}

func removeWorkload(list []k8s.Workload, name, ns string) []k8s.Workload {
	out := list[:0]
	for _, item := range list {
		if item.Name == name && (ns == "" || item.Namespace == ns) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func removePod(list []k8s.Pod, name, ns string) []k8s.Pod {
	out := list[:0]
	for _, item := range list {
		if item.Name == name && (ns == "" || item.Namespace == ns) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func removeString(list []string, value string) []string {
	out := list[:0]
	for _, item := range list {
		if item == value {
			continue
		}
		out = append(out, item)
	}
	return out
}

func upsertPod(list []k8s.Pod, item k8s.Pod) []k8s.Pod {
	for i, existing := range list {
		if existing.Name == item.Name && existing.Namespace == item.Namespace {
			list[i] = item
			return list
		}
	}
	return append(list, item)
}

const flannelManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: kube-flannel
  labels:
    pod-security.kubernetes.io/enforce: privileged
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-flannel-ds
  namespace: kube-flannel
spec:
  selector:
    matchLabels:
      app: flannel
  template:
    metadata:
      labels:
        app: flannel
    spec:
      hostNetwork: true
      containers:
      - name: kube-flannel
        image: ghcr.io/flannel-io/flannel:v0.26.1
        command: ["/opt/bin/flanneld"]
        args: ["--ip-masq", "--kube-subnet-mgr"]
        securityContext:
          privileged: true
`
