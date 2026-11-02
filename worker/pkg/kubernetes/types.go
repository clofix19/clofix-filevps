// Package kubernetes defines the authenticated controller/worker CKS protocol.
package kubernetes

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var ID = regexp.MustCompile(`^(cks|ckn|ckp|ckj|ckb|ckr)-[a-f0-9]{32}$`)
var Name = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var Chart = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]{0,120}$`)

const (
	ModeDedicated = "dedicated"
	ModeFileVPS   = "filevps"

	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"

	ActionInit             = "init"
	ActionJoin             = "join"
	ActionJoinControlPlane = "join-control-plane"
	ActionReset            = "reset"
	ActionApply            = "apply"
	ActionDelete           = "delete"
	ActionGet              = "get"
	ActionLogs             = "logs"
	ActionScale            = "scale"
	ActionRestart          = "restart"
	ActionHelm             = "helm"
	ActionStorage          = "storage"
	ActionIngress          = "ingress"
	ActionRBAC             = "rbac"
	ActionHPA              = "hpa"
	ActionBackup           = "backup"
	ActionRestore          = "restore"
	ActionUpgrade          = "upgrade"

	StatePending      = "PENDING"
	StateProvisioning = "PROVISIONING"
	StateRunning      = "RUNNING"
	StateDegraded     = "DEGRADED"
	StateUpgrading    = "UPGRADING"
	StateFailed       = "FAILED"
	StateDeleting     = "DELETING"
	StateDeleted      = "DELETED"
)

// SupportedVersions are the kubeadm minor versions CKS will place.
var SupportedVersions = []string{"1.31", "1.32"}

// FileVPSReport explains why FileVPS guests cannot run kubelet.
var FileVPSReport = []CompatibilityIssue{
	{Requirement: "Privileged mounts / unshare", Status: "blocked", Detail: "FileVPS seccomp denies mount, umount2, unshare, pivot_root, bpf and the mount_setattr family."},
	{Requirement: "CAP_SYS_ADMIN / CAP_NET_ADMIN", Status: "blocked", Detail: "FileVPS drops host-admin capabilities required by CNI, kube-proxy and kubelet."},
	{Requirement: "/sys and cgroup delegation", Status: "blocked", Detail: "Guests do not mount /sys and CLONE_NEWCGROUP hides the host cgroup tree, so kubelet cannot create pod cgroups."},
	{Requirement: "Nested containerd / kubelet", Status: "blocked", Detail: "FileVPS is shared-kernel isolation, not a VM. Nested Kubernetes is not a supported product mode."},
	{Requirement: "CSI / privileged volume plugins", Status: "blocked", Detail: "Guests cannot mount hostPath or CSI node plugins that need CAP_SYS_ADMIN."},
	{Requirement: "Performance isolation", Status: "blocked", Detail: "Shared-kernel FileVPS cannot provide per-node kubelet noisy-neighbor isolation required for customer Kubernetes workloads."},
}

type CompatibilityIssue struct {
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
}

func VersionSupported(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	for _, allowed := range SupportedVersions {
		if v == allowed || strings.HasPrefix(v, allowed+".") {
			return true
		}
	}
	return false
}

func NormalizeVersion(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	for _, allowed := range SupportedVersions {
		if v == allowed || strings.HasPrefix(v, allowed+".") {
			return allowed
		}
	}
	return v
}

type Spec struct {
	ID                   string  `json:"id"`
	Version              string  `json:"version"`
	TargetVersion        string  `json:"target_version,omitempty"`
	InfrastructureMode   string  `json:"infrastructure_mode"`
	Role                 string  `json:"role"`
	CPU                  float64 `json:"cpu"`
	RAMMB                int64   `json:"ram_mb"`
	DiskMB               int64   `json:"disk_mb"`
	AdvertiseAddress     string  `json:"advertise_address,omitempty"`
	ControlPlaneEndpoint string  `json:"control_plane_endpoint,omitempty"`
	JoinToken            string  `json:"join_token,omitempty"`
	JoinHash             string  `json:"join_hash,omitempty"`
	JoinCertificateKey   string  `json:"join_certificate_key,omitempty"`
	SingleNode           bool    `json:"single_node,omitempty"`
	HA                   bool    `json:"ha,omitempty"`
}

func (s Spec) Validate() error {
	if !ID.MatchString(s.ID) || !strings.HasPrefix(s.ID, "cks-") {
		return fmt.Errorf("invalid kubernetes cluster id")
	}
	if !VersionSupported(s.Version) {
		return fmt.Errorf("unsupported kubernetes version")
	}
	if s.InfrastructureMode != ModeDedicated {
		return fmt.Errorf("only dedicated physical workers are supported")
	}
	if s.Role != RoleControlPlane && s.Role != RoleWorker {
		return fmt.Errorf("invalid kubernetes node role")
	}
	if math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < 0.5 || s.CPU > 128 || s.RAMMB < 1024 || s.RAMMB > 1048576 || s.DiskMB < 5120 || s.DiskMB > 1048576 {
		return fmt.Errorf("invalid kubernetes node resources")
	}
	return nil
}

type Operation struct {
	JobID      string `json:"job_id"`
	ClusterID  string `json:"cluster_id"`
	NodeID     string `json:"node_id,omitempty"`
	Action     string `json:"action"`
	Spec       Spec   `json:"spec"`
	YAML       string `json:"yaml,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Replicas   int    `json:"replicas,omitempty"`
	Tail       int    `json:"tail,omitempty"`
	Chart      string `json:"chart,omitempty"`
	Release    string `json:"release,omitempty"`
	Values     string `json:"values,omitempty"`
	HelmAction string `json:"helm_action,omitempty"`
	BackupID   string `json:"backup_id,omitempty"`
	Snapshot   []byte `json:"snapshot,omitempty"`
}

func (o Operation) Validate() error {
	if !ID.MatchString(o.JobID) || !strings.HasPrefix(o.JobID, "ckj-") || o.ClusterID != o.Spec.ID {
		return fmt.Errorf("invalid kubernetes job")
	}
	switch o.Action {
	case ActionInit, ActionJoin, ActionJoinControlPlane, ActionReset, ActionUpgrade:
		return o.Spec.Validate()
	case ActionApply, ActionStorage, ActionIngress, ActionRBAC, ActionHPA:
		if strings.TrimSpace(o.YAML) == "" || len(o.YAML) > 256<<10 {
			return fmt.Errorf("manifest YAML is required and must be 256 KiB or smaller")
		}
		return nil
	case ActionDelete:
		if strings.TrimSpace(o.YAML) == "" && (o.Kind == "" || o.Name == "") {
			return fmt.Errorf("delete requires YAML or kind and name")
		}
		if len(o.YAML) > 256<<10 {
			return fmt.Errorf("manifest YAML must be 256 KiB or smaller")
		}
		return nil
	case ActionGet:
		return nil
	case ActionLogs:
		if o.Namespace == "" || o.Name == "" {
			return fmt.Errorf("pod namespace and name are required")
		}
		return nil
	case ActionScale:
		if o.Namespace == "" || o.Name == "" || o.Replicas < 0 || o.Replicas > 1000 {
			return fmt.Errorf("scale requires namespace, name and a replica count between 0 and 1000")
		}
		return nil
	case ActionRestart:
		if o.Namespace == "" || o.Name == "" {
			return fmt.Errorf("restart requires namespace and name")
		}
		return nil
	case ActionHelm:
		action := strings.ToLower(strings.TrimSpace(o.HelmAction))
		if o.Release == "" || !Name.MatchString(o.Release) {
			return fmt.Errorf("helm release name is required")
		}
		if action != "uninstall" && (o.Chart == "" || !Chart.MatchString(o.Chart) || strings.Contains(o.Chart, "..")) {
			return fmt.Errorf("helm chart must be a safe chart reference")
		}
		if len(o.Values) > 64<<10 {
			return fmt.Errorf("helm values must be 64 KiB or smaller")
		}
		if action != "install" && action != "upgrade" && action != "uninstall" {
			return fmt.Errorf("helm action must be install, upgrade or uninstall")
		}
		return nil
	case ActionBackup:
		return nil
	case ActionRestore:
		if o.BackupID == "" && len(o.Snapshot) == 0 {
			return fmt.Errorf("restore requires a backup snapshot")
		}
		return nil
	default:
		return fmt.Errorf("unsupported kubernetes action")
	}
}

type Result struct {
	JobID     string          `json:"job_id"`
	ClusterID string          `json:"cluster_id"`
	NodeID    string          `json:"node_id,omitempty"`
	State     string          `json:"state"`
	Progress  int             `json:"progress"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	At        time.Time       `json:"at"`
}

type Status struct {
	ClusterID   string    `json:"cluster_id"`
	NodeID      string    `json:"node_id,omitempty"`
	Role        string    `json:"role,omitempty"`
	State       string    `json:"state"`
	Version     string    `json:"version,omitempty"`
	APIEndpoint string    `json:"api_endpoint,omitempty"`
	Inventory   Inventory `json:"inventory,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type Inventory struct {
	Namespaces             []string      `json:"namespaces,omitempty"`
	Pods                   []Pod         `json:"pods,omitempty"`
	Deployments            []Workload    `json:"deployments,omitempty"`
	ReplicaSets            []Workload    `json:"replica_sets,omitempty"`
	DaemonSets             []Workload    `json:"daemon_sets,omitempty"`
	StatefulSets           []Workload    `json:"stateful_sets,omitempty"`
	Jobs                   []Workload    `json:"jobs,omitempty"`
	CronJobs               []Workload    `json:"cron_jobs,omitempty"`
	Services               []Workload    `json:"services,omitempty"`
	Ingresses              []Workload    `json:"ingresses,omitempty"`
	ConfigMaps             []Workload    `json:"config_maps,omitempty"`
	Secrets                []Workload    `json:"secrets,omitempty"`
	PersistentVolumeClaims []Workload    `json:"persistent_volume_claims,omitempty"`
	PersistentVolumes      []Workload    `json:"persistent_volumes,omitempty"`
	StorageClasses         []Workload    `json:"storage_classes,omitempty"`
	ServiceAccounts        []Workload    `json:"service_accounts,omitempty"`
	NetworkPolicies        []Workload    `json:"network_policies,omitempty"`
	Events                 []Event       `json:"events,omitempty"`
	Logs                   string        `json:"logs,omitempty"`
	Helm                   []HelmRelease `json:"helm,omitempty"`
	Volumes                []Workload    `json:"volumes,omitempty"`
	RBAC                   []Workload    `json:"rbac,omitempty"`
	HPA                    []Workload    `json:"hpa,omitempty"`
	Backup                 *BackupInfo   `json:"backup,omitempty"`
}

type HelmRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Chart     string `json:"chart,omitempty"`
	Status    string `json:"status,omitempty"`
	Revision  int    `json:"revision,omitempty"`
}

type BackupInfo struct {
	ID        string `json:"id,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type Pod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Phase     string `json:"phase"`
	Node      string `json:"node,omitempty"`
	Restarts  int    `json:"restarts,omitempty"`
}

type Workload struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind,omitempty"`
	Replicas  int    `json:"replicas,omitempty"`
	Ready     int    `json:"ready,omitempty"`
	URL       string `json:"url,omitempty"`
	Image     string `json:"image,omitempty"`
	Port      int    `json:"port,omitempty"`
	Ports     []int  `json:"ports,omitempty"`
	NodePort  int    `json:"node_port,omitempty"`
	Type      string `json:"type,omitempty"`
	Storage   string `json:"storage,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Schedule  string `json:"schedule,omitempty"`
}

type Event struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Type      string `json:"type,omitempty"`
}
