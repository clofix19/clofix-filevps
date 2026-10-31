// Package proto holds the controller <-> worker wire types.
package proto

import (
	db "github.com/clofix/filevps/worker/pkg/database"
	"time"

	"github.com/clofix/filevps/worker/pkg/scheduler"
)

const (
	ActionCreate                 = "create"
	ActionStart                  = "start"
	ActionStop                   = "stop"
	ActionRestart                = "restart"
	ActionDelete                 = "delete"
	ActionUpdatePorts            = "update-ports"
	ActionUpdateProtection       = "update-protection"
	ActionUpdateSSHLogin         = "update-ssh-login"
	ActionUpdateDeleteProtection = "update-delete-protection"
	ActionUpdateWAF              = "update-waf"
	ActionUpdateS3Mount          = "update-s3-mount"
	ActionUpdateResources        = "update-resources"
	ActionUpdateStorageMounts    = "update-storage-mounts"
	ActionCreateVolume           = "create-volume"
	ActionDeleteVolume           = "delete-volume"
	ActionServiceControl         = "service-control"
	ActionInstallService         = "install-service"
	ActionUninstallService       = "uninstall-service"
	ActionCreateBackup           = "create-backup"
	ActionDeleteBackup           = "delete-backup"
)

const DefaultSwapMB int64 = 512

// SwapLimitMB returns the compatibility default for VPS specifications created
// before per-instance swap was added.
func SwapLimitMB(spec InstanceSpec) int64 {
	if spec.SwapMB == nil {
		return DefaultSwapMB
	}
	return *spec.SwapMB
}

// Instance states (spec section 11).
const (
	StateProvisioning = "PROVISIONING"
	StateStopped      = "STOPPED"
	StateStarting     = "STARTING"
	StateRunning      = "RUNNING"
	StateStopping     = "STOPPING"
	StateRestarting   = "RESTARTING"
	StateFailed       = "FAILED"
	StateDeleting     = "DELETING"
	StatePending      = "PENDING"
)

type EnrollRequest struct {
	Token    string             `json:"token"`
	CSRPEM   string             `json:"csr_pem"`
	Hostname string             `json:"hostname"`
	Version  string             `json:"version"`
	Capacity scheduler.Capacity `json:"capacity"`
}

type EnrollResponse struct {
	WorkerID        string `json:"worker_id"`
	CertPEM         string `json:"cert_pem"`
	CAPEM           string `json:"ca_pem"`
	S3MasterAddress string `json:"s3_master_address,omitempty"`
}

// ImageRef points at a base image. URL is short-lived (presigned) and never contains long-term credentials.
type ImageRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

type InstanceSpec struct {
	Image            ImageRef        `json:"image"`
	RAMMB            int64           `json:"ram_mb"`
	SwapMB           *int64          `json:"swap_mb,omitempty"`
	CPU              float64         `json:"cpu"`
	DiskMB           int64           `json:"disk_mb"`
	Pids             int64           `json:"pids"`
	Hostname         string          `json:"hostname"`
	Init             []string        `json:"init,omitempty"`
	IPAddress        string          `json:"ip_address,omitempty"`
	SSHPort          int             `json:"ssh_port,omitempty"`
	SSHAuthorizedKey string          `json:"ssh_authorized_key,omitempty"`
	PortMappings     []PortMapping   `json:"port_mappings,omitempty"`
	ProtectionMode   *bool           `json:"protection_mode,omitempty"`
	SSHLoginBlocked  *bool           `json:"ssh_login_blocked,omitempty"`
	DeleteProtection bool            `json:"delete_protection,omitempty"`
	OWASPProtection  map[string]bool `json:"owasp_protection,omitempty"`
	S3MountPath      string          `json:"s3_mount_path,omitempty"`
	S3ConnectionID   string          `json:"s3_connection_id,omitempty"`
	StorageMounts    []StorageMount  `json:"storage_mounts,omitempty"`
	BackupID         string          `json:"backup_id,omitempty"`
}

// StorageMount is a per-VPS host-backed persistent directory mounted in the guest.
// It has no size quota; bytes consume the worker's shared storage pool.
type StorageMount struct {
	ID        string `json:"id"`
	GuestPath string `json:"guest_path"`
}

type VolumeFileRequest struct {
	VolumeID  string `json:"volume_id"`
	Action    string `json:"action"` // list|read|write|mkdir|delete
	Path      string `json:"path"`   // relative to the volume root; empty only for list root
	Content   []byte `json:"content,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}

type VolumeFileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     string    `json:"kind"` // file|folder|symlink
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified,omitempty"`
}

type VolumeFileResult struct {
	Entries []VolumeFileEntry `json:"entries,omitempty"`
	Content []byte            `json:"content,omitempty"`
}

// S3Mount carries a one-time credential payload over the worker's mTLS channel.
// The worker stores credentials separately from ordinary instance metadata.
type S3Mount struct {
	ConnectionID string `json:"connection_id,omitempty"`
	Endpoint     string `json:"endpoint"`
	Bucket       string `json:"bucket"`
	Region       string `json:"region,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	GuestPath    string `json:"guest_path"`
}

type PortMapping struct {
	Protocol  string `json:"protocol"`
	GuestPort int    `json:"guest_port"`
	HostPort  int    `json:"host_port"`
}

type Operation struct {
	DatabaseJobID string             `json:"database_job_id,omitempty"`
	Managed       *ManagedSpec       `json:"managed,omitempty"`
	Execution     *Execution         `json:"execution,omitempty"`
	ID            string             `json:"id"`
	Action        string             `json:"action"`
	InstanceID    string             `json:"instance_id"`
	Spec          *InstanceSpec      `json:"spec,omitempty"` // create or update operation
	S3Mount       *S3Mount           `json:"s3_mount,omitempty"`
	UnmountS3     bool               `json:"unmount_s3,omitempty"`
	VolumeID      string             `json:"volume_id,omitempty"`
	VolumeName    string             `json:"volume_name,omitempty"`
	VolumeLimitMB int64              `json:"volume_limit_mb,omitempty"`
	VolumeFile    *VolumeFileRequest `json:"volume_file,omitempty"`
	ServiceName   string             `json:"service_name,omitempty"`
	ServiceAction string             `json:"service_action,omitempty"`
	ServiceID     string             `json:"service_id,omitempty"`
	BackupID      string             `json:"backup_id,omitempty"`
	WorkerID      string             `json:"worker_id,omitempty"`
	WAFCategory   string             `json:"waf_category,omitempty"`
	WAFEnabled    bool               `json:"waf_enabled,omitempty"`
}

type OpResult struct {
	Execution  *ExecutionResult  `json:"execution,omitempty"`
	OpID       string            `json:"op_id"`
	OK         bool              `json:"ok"`
	Error      string            `json:"error,omitempty"`
	VolumeFile *VolumeFileResult `json:"volume_file,omitempty"`
}

type InstanceStatus struct {
	ID              string            `json:"id"`
	State           string            `json:"state"`
	Error           string            `json:"error,omitempty"`
	Pid             int               `json:"pid,omitempty"`
	Metrics         InstanceMetrics   `json:"metrics"`
	Services        []ServiceStatus   `json:"services,omitempty"`
	ServiceInstalls map[string]string `json:"service_installs,omitempty"`
}

type ServiceStatus struct {
	Name        string  `json:"name"`
	State       string  `json:"state"` // RUNNING|ENABLED
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	SwapBytes   uint64  `json:"swap_bytes"`
	Logs        string  `json:"logs,omitempty"`
}

type InstanceMetrics struct {
	CPUPercent         float64               `json:"cpu_percent"`
	MemoryCurrentBytes uint64                `json:"memory_current_bytes"`
	MemoryLimitBytes   uint64                `json:"memory_limit_bytes"`
	SwapCurrentBytes   uint64                `json:"swap_current_bytes"`
	SwapLimitBytes     uint64                `json:"swap_limit_bytes"`
	DiskUsageBytes     uint64                `json:"disk_usage_bytes"`
	StorageVolumes     []StorageVolumeMetric `json:"storage_volumes,omitempty"`
}

type StorageVolumeMetric struct {
	ID         string `json:"id"`
	UsageBytes uint64 `json:"usage_bytes"`
}

type Heartbeat struct {
	DatabaseResults   []db.Result          `json:"database_results,omitempty"`
	DatabaseStatuses  []db.Status          `json:"database_statuses,omitempty"`
	ManagedApps       []ManagedStatus      `json:"managed_apps,omitempty"`
	ExecutionProgress []ExecutionProgress  `json:"execution_progress,omitempty"`
	Version           string               `json:"version"`
	Capacity          scheduler.Capacity   `json:"capacity"`
	Instances         []InstanceStatus     `json:"instances"`
	Volumes           []WorkerVolumeStatus `json:"volumes,omitempty"`
	Backups           []BackupInfo         `json:"backups,omitempty"`
	Results           []OpResult           `json:"results"`
}

// BackupInfo describes a worker-local snapshot. It intentionally excludes SSH keys and mount credentials.
type BackupInfo struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	WorkerID   string    `json:"worker_id"`
	Created    time.Time `json:"created"`
	SizeBytes  uint64    `json:"size_bytes"`
	RAMMB      int64     `json:"ram_mb"`
	CPU        float64   `json:"cpu"`
	SwapMB     int64     `json:"swap_mb"`
	DiskMB     int64     `json:"disk_mb"`
	Image      string    `json:"image"`
	State      string    `json:"state"`
	Error      string    `json:"error,omitempty"`
}

type WorkerVolumeStatus struct {
	ID         string `json:"id"`
	UsageBytes uint64 `json:"usage_bytes"`
}

type HeartbeatResponse struct {
	DatabaseAcknowledged []string    `json:"database_acknowledged,omitempty"`
	Operations           []Operation `json:"operations"`
	S3MasterAddress      string      `json:"s3_master_address,omitempty"`
}
