// Package docker defines the authenticated controller/worker Docker product protocol.
package docker

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// Host port range for published Docker containers (below managed-app / DB ranges).
const HostPortBase, HostPortCount = 18000, 4000

var ID = regexp.MustCompile(`^(dck|dca|dcj|dcn|dcv|dcd|dcb|dcr|dcp)-[a-f0-9]{16,32}$`)

func ValidID(prefix, id string) bool {
	return ID.MatchString(id) && strings.HasPrefix(id, prefix+"-")
}

type PortPublish struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"`
	Protocol      string `json:"protocol,omitempty"` // tcp|udp
	PublishAll    bool   `json:"publish_all,omitempty"`
}

type HealthCheck struct {
	Test        []string `json:"test,omitempty"`
	IntervalSec int      `json:"interval_sec,omitempty"`
	TimeoutSec  int      `json:"timeout_sec,omitempty"`
	Retries     int      `json:"retries,omitempty"`
}

type ContainerSpec struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Tag           string            `json:"tag,omitempty"`
	Digest        string            `json:"digest,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Ports         []PortPublish     `json:"ports,omitempty"`
	Volumes       []VolumeMount     `json:"volumes,omitempty"`
	Networks      []string          `json:"networks,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
	Health        *HealthCheck      `json:"health,omitempty"`
	RAMMB         int64             `json:"ram_mb"`
	CPU           float64           `json:"cpu"`
	DiskMB        int64             `json:"disk_mb,omitempty"`
	WorkDir       string            `json:"workdir,omitempty"`
	User          string            `json:"user,omitempty"`
	ReadOnlyRoot  bool              `json:"read_only_root,omitempty"`
	Privileged    bool              `json:"privileged,omitempty"` // rejected unless allow_privileged
	HostNetwork   bool              `json:"host_network,omitempty"` // rejected
	Domain        string            `json:"domain,omitempty"`
}

type VolumeMount struct {
	VolumeID  string `json:"volume_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	Create    bool   `json:"create,omitempty"`
}

func (s ContainerSpec) ImageRef() string {
	img := strings.TrimSpace(s.Image)
	if s.Digest != "" {
		if i := strings.IndexByte(img, '@'); i >= 0 {
			img = img[:i]
		}
		return img + "@" + strings.TrimPrefix(s.Digest, "sha256:")
	}
	tag := s.Tag
	if tag == "" {
		tag = "latest"
	}
	if strings.Contains(img, "@") || strings.Contains(img, ":") && !strings.HasPrefix(img, "sha256:") {
		// already tagged or digested
		if !strings.Contains(img, "@") && s.Tag != "" && !strings.Contains(img, ":") {
			return img + ":" + tag
		}
		if strings.Contains(img, ":") || strings.Contains(img, "@") {
			return img
		}
	}
	return img + ":" + tag
}

func (s ContainerSpec) Validate() error {
	if !ValidID("dck", s.ID) {
		return fmt.Errorf("invalid container id")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 128 {
		return fmt.Errorf("invalid container name")
	}
	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("image is required")
	}
	if s.Privileged || s.HostNetwork {
		return fmt.Errorf("privileged and host network are not allowed")
	}
	if s.RAMMB < 32 || s.RAMMB > 1048576 || math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < 0.1 || s.CPU > 128 {
		return fmt.Errorf("invalid resource limits")
	}
	for _, p := range s.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("invalid container port")
		}
		if p.HostPort != 0 && (p.HostPort < HostPortBase || p.HostPort >= HostPortBase+HostPortCount) {
			return fmt.Errorf("host port must be in %d-%d", HostPortBase, HostPortBase+HostPortCount-1)
		}
		proto := strings.ToLower(p.Protocol)
		if proto != "" && proto != "tcp" && proto != "udp" {
			return fmt.Errorf("invalid port protocol")
		}
	}
	rp := strings.ToLower(s.RestartPolicy)
	switch rp {
	case "", "no", "always", "unless-stopped", "on-failure":
	default:
		return fmt.Errorf("invalid restart policy")
	}
	return nil
}

type NetworkSpec struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver,omitempty"` // bridge
	Internal bool   `json:"internal,omitempty"`
	Owner    string `json:"owner,omitempty"`
}

type VolumeSpec struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Owner            string `json:"owner,omitempty"`
	DeleteProtection bool   `json:"delete_protection,omitempty"`
	QuotaMB          int64  `json:"quota_mb,omitempty"`
}

type ComposeSpec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	YAML    string `json:"yaml"`
	Project string `json:"project,omitempty"`
}

type RegistryAuth struct {
	Server   string `json:"server,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // never logged
}

type Operation struct {
	JobID         string            `json:"job_id"`
	Action        string            `json:"action"`
	Container     *ContainerSpec    `json:"container,omitempty"`
	Network       *NetworkSpec      `json:"network,omitempty"`
	Volume        *VolumeSpec       `json:"volume,omitempty"`
	Compose       *ComposeSpec      `json:"compose,omitempty"`
	Image         string            `json:"image,omitempty"`
	Registry      *RegistryAuth     `json:"registry,omitempty"`
	ExecCommand   []string          `json:"exec_command,omitempty"`
	LogTail       int               `json:"log_tail,omitempty"`
	Scale         int               `json:"scale,omitempty"`
	ServiceName   string            `json:"service_name,omitempty"`
	GitURL        string            `json:"git_url,omitempty"`
	GitBranch     string            `json:"git_branch,omitempty"`
	GitToken      string            `json:"git_token,omitempty"`
	Dockerfile    string            `json:"dockerfile,omitempty"`
	BuildContext  string            `json:"build_context,omitempty"`
	BackupID      string            `json:"backup_id,omitempty"`
	BackupDest    string            `json:"backup_dest,omitempty"`
	EngineAction  string            `json:"engine_action,omitempty"` // install|start|stop|restart|upgrade
	Env           map[string]string `json:"env,omitempty"`
	// WAF edge proxy (OWASP CRS nginx sidecar in front of a CloFix container or FileVPS).
	WAFTargetID   string            `json:"waf_target_id,omitempty"`
	WAFBackend    string            `json:"waf_backend,omitempty"`
	WAFPorts      []PortPublish     `json:"waf_ports,omitempty"`
	WAFPolicy     map[string]bool   `json:"waf_policy,omitempty"`
	WAFErrorPages map[string]string `json:"waf_error_pages,omitempty"`
	// WAFMode: "" = Docker container edge; "vps" = FileVPS host gateway (no guest install).
	WAFMode string `json:"waf_mode,omitempty"`
}

// WAFCount is a labeled counter for WAF analytics (IP, URI, attack tag).
type WAFCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// WAFEvent is a recent ModSecurity / access event for a binding.
type WAFEvent struct {
	Time    string `json:"time,omitempty"`
	IP      string `json:"ip,omitempty"`
	Method  string `json:"method,omitempty"`
	URI     string `json:"uri,omitempty"`
	Status  int    `json:"status,omitempty"`
	Blocked bool   `json:"blocked,omitempty"`
	Attack  string `json:"attack,omitempty"`
	Message string `json:"message,omitempty"`
	RuleID  string `json:"rule_id,omitempty"`
}

// WAFStats summarizes recent ModSecurity audit + nginx access logs for one edge proxy.
type WAFStats struct {
	Hits        int        `json:"hits"`
	Blocks      int        `json:"blocks"`
	Attacks     int        `json:"attacks"`
	UniqueIPs   int        `json:"unique_ips"`
	TopIPs      []WAFCount `json:"top_ips,omitempty"`
	TopURIs     []WAFCount `json:"top_uris,omitempty"`
	TopAttacks  []WAFCount `json:"top_attacks,omitempty"`
	Recent      []WAFEvent `json:"recent,omitempty"`
	Sampled     int        `json:"sampled"`
	Proxy       string     `json:"proxy,omitempty"`
	At          time.Time  `json:"at"`
	Error       string     `json:"error,omitempty"`
}

type Result struct {
	JobID     string          `json:"job_id"`
	State     string          `json:"state"` // SUCCEEDED|FAILED|RUNNING|CANCELLED
	Progress  int             `json:"progress"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Logs      string          `json:"logs,omitempty"`
	At        time.Time       `json:"at"`
	Container *ContainerInfo  `json:"container,omitempty"`
}

type ContainerInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Status    string            `json:"status,omitempty"`
	Health    string            `json:"health,omitempty"`
	Created   string            `json:"created,omitempty"`
	Ports     []string          `json:"ports,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	CPUPct    float64           `json:"cpu_percent,omitempty"`
	MemBytes  uint64            `json:"memory_bytes,omitempty"`
	MemLimit  uint64            `json:"memory_limit_bytes,omitempty"`
	DiskBytes uint64            `json:"disk_bytes,omitempty"`
	NetRx     uint64            `json:"net_rx_bytes,omitempty"`
	NetTx     uint64            `json:"net_tx_bytes,omitempty"`
	Restarts  int               `json:"restarts,omitempty"`
	Inspect   json.RawMessage   `json:"inspect,omitempty"`
}

type ImageInfo struct {
	ID         string   `json:"id"`
	Repository string   `json:"repository,omitempty"`
	Tag        string   `json:"tag,omitempty"`
	Digest     string   `json:"digest,omitempty"`
	Size       int64    `json:"size,omitempty"`
	Created    string   `json:"created,omitempty"`
}

type EngineInfo struct {
	Ready     bool   `json:"ready"`
	Version   string `json:"version,omitempty"`
	APIVersion string `json:"api_version,omitempty"`
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	RootDir   string `json:"root_dir,omitempty"`
	DiskTotal uint64 `json:"disk_total,omitempty"`
	DiskUsed  uint64 `json:"disk_used,omitempty"`
	Error     string `json:"error,omitempty"`
	Host      string `json:"host,omitempty"`
}

type Status struct {
	Containers []ContainerInfo `json:"containers,omitempty"`
	Images     []ImageInfo     `json:"images,omitempty"`
	Engine     EngineInfo      `json:"engine"`
	Networks   []NetworkSpec   `json:"networks,omitempty"`
	Volumes    []VolumeSpec    `json:"volumes,omitempty"`
}
