// Package proto holds the controller <-> worker wire types.
package proto

import "github.com/clofix/filevps/worker/pkg/scheduler"

const (
	ActionCreate           = "create"
	ActionStart            = "start"
	ActionStop             = "stop"
	ActionRestart          = "restart"
	ActionDelete           = "delete"
	ActionUpdatePorts      = "update-ports"
	ActionUpdateProtection = "update-protection"
	ActionUpdateWAF        = "update-waf"
	ActionUpdateS3Mount    = "update-s3-mount"
	ActionServiceControl   = "service-control"
	ActionInstallService   = "install-service"
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
	WorkerID string `json:"worker_id"`
	CertPEM  string `json:"cert_pem"`
	CAPEM    string `json:"ca_pem"`
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
	OWASPProtection  map[string]bool `json:"owasp_protection,omitempty"`
	S3MountPath      string          `json:"s3_mount_path,omitempty"`
}

// S3Mount carries a one-time credential payload over the worker's mTLS channel.
// The worker stores credentials separately from ordinary instance metadata.
type S3Mount struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	GuestPath string `json:"guest_path"`
}

type PortMapping struct {
	Protocol  string `json:"protocol"`
	GuestPort int    `json:"guest_port"`
	HostPort  int    `json:"host_port"`
}

type Operation struct {
	ID            string        `json:"id"`
	Action        string        `json:"action"`
	InstanceID    string        `json:"instance_id"`
	Spec          *InstanceSpec `json:"spec,omitempty"` // create or update operation
	S3Mount       *S3Mount      `json:"s3_mount,omitempty"`
	UnmountS3     bool          `json:"unmount_s3,omitempty"`
	ServiceName   string        `json:"service_name,omitempty"`
	ServiceAction string        `json:"service_action,omitempty"`
	ServiceID     string        `json:"service_id,omitempty"`
	WAFCategory   string        `json:"waf_category,omitempty"`
	WAFEnabled    bool          `json:"waf_enabled,omitempty"`
}

type OpResult struct {
	OpID  string `json:"op_id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
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
	CPUPercent         float64 `json:"cpu_percent"`
	MemoryCurrentBytes uint64  `json:"memory_current_bytes"`
	MemoryLimitBytes   uint64  `json:"memory_limit_bytes"`
	SwapCurrentBytes   uint64  `json:"swap_current_bytes"`
	SwapLimitBytes     uint64  `json:"swap_limit_bytes"`
}

type Heartbeat struct {
	Version   string             `json:"version"`
	Capacity  scheduler.Capacity `json:"capacity"`
	Instances []InstanceStatus   `json:"instances"`
	Results   []OpResult         `json:"results"`
}

type HeartbeatResponse struct {
	Operations []Operation `json:"operations"`
}
