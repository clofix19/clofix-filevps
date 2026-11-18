// Package config loads versioned CloFix Guard configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const SchemaVersion = 1

// Config is the versioned Guard configuration schema.
type Config struct {
	SchemaVersion int            `json:"schema_version"`
	Mode          string         `json:"mode"` // standard | strict | immutable
	DataDir       string         `json:"data_dir"`
	Intervals     Intervals      `json:"intervals"`
	FIM           FIMConfig      `json:"fim"`
	SSH           SSHConfig      `json:"ssh"`
	Firewall      FirewallConfig `json:"firewall"`
	Process       ProcessConfig  `json:"process"`
	Network       NetworkConfig  `json:"network"`
	Malware       MalwareConfig  `json:"malware"`
	Vuln          VulnConfig     `json:"vulnerability"`
	AutoPilot     AutoPilotConfig `json:"autopilot"`
	HostAPI       HostAPIConfig  `json:"host_api"`
	AllowlistIPs  []string       `json:"allowlist_ips"`
}

type Intervals struct {
	FIMSeconds       int `json:"fim_seconds"`
	ProcessSeconds   int `json:"process_seconds"`
	NetworkSeconds   int `json:"network_seconds"`
	AuthSeconds      int `json:"auth_seconds"`
	VulnHours        int `json:"vuln_hours"`
	ScoreSeconds     int `json:"score_seconds"`
}

type FIMConfig struct {
	Enabled  bool     `json:"enabled"`
	Paths    []string `json:"paths"`
	Exclude  []string `json:"exclude"`
}

type SSHConfig struct {
	Enabled           bool     `json:"enabled"`
	ConfigPath        string   `json:"config_path"`
	AuthLogPaths      []string `json:"auth_log_paths"`
	FailThreshold     int      `json:"fail_threshold"`
	FailWindowSeconds int      `json:"fail_window_seconds"`
	PreserveEmergency bool     `json:"preserve_emergency_access"`
}

type FirewallConfig struct {
	Enabled           bool `json:"enabled"`
	ApplyDefaultRules bool `json:"apply_default_rules"`
	RequireNFT        bool `json:"require_nft"`
}

type ProcessConfig struct {
	Enabled              bool `json:"enabled"`
	AllowTerminate       bool `json:"allow_terminate"`
	RequireExplicitRule  bool `json:"require_explicit_rule"`
}

type NetworkConfig struct {
	Enabled bool `json:"enabled"`
}

type MalwareConfig struct {
	Enabled           bool   `json:"enabled"`
	HashDBPath        string `json:"hash_db_path"`
	AllowQuarantine   bool   `json:"allow_quarantine"`
	QuarantineDir     string `json:"quarantine_dir"`
}

type VulnConfig struct {
	Enabled      bool `json:"enabled"`
	UseOSV       bool `json:"use_osv"`
	UseOffline   bool `json:"use_offline"`
}

type AutoPilotConfig struct {
	Enabled              bool `json:"enabled"`
	AllowSafeMutation    bool `json:"allow_safe_mutation"`
	AllowHighRisk        bool `json:"allow_high_risk"`
	AllowDestructive     bool `json:"allow_destructive"`
}

type HostAPIConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	// TokenPath points to a file; credentials are never embedded in binaries.
	TokenPath string `json:"token_path"`
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Mode:          "standard",
		DataDir:       "/var/lib/clofix/guard",
		Intervals: Intervals{
			FIMSeconds: 300, ProcessSeconds: 60, NetworkSeconds: 60,
			AuthSeconds: 30, VulnHours: 24, ScoreSeconds: 300,
		},
		FIM: FIMConfig{
			Enabled: true,
			Paths: []string{
				"/etc", "/usr/bin", "/usr/sbin", "/boot",
				"/etc/ssh", "/etc/cron.d", "/etc/crontab",
				"/etc/systemd/system", "/etc/sudoers", "/etc/sudoers.d",
			},
			Exclude: []string{"/etc/mtab", "/etc/resolv.conf"},
		},
		SSH: SSHConfig{
			Enabled: true, ConfigPath: "/etc/ssh/sshd_config",
			AuthLogPaths: []string{"/var/log/auth.log", "/var/log/secure"},
			FailThreshold: 8, FailWindowSeconds: 300, PreserveEmergency: true,
		},
		Firewall: FirewallConfig{Enabled: true, ApplyDefaultRules: false, RequireNFT: true},
		Process:  ProcessConfig{Enabled: true, AllowTerminate: false, RequireExplicitRule: true},
		Network:  NetworkConfig{Enabled: true},
		Malware: MalwareConfig{
			Enabled: true, HashDBPath: "/etc/clofix/guard/malware-hashes.txt",
			AllowQuarantine: false, QuarantineDir: "/var/lib/clofix/quarantine",
		},
		Vuln:      VulnConfig{Enabled: true, UseOSV: false, UseOffline: true},
		AutoPilot: AutoPilotConfig{Enabled: false, AllowSafeMutation: false},
		HostAPI:   HostAPIConfig{Enabled: false, Endpoint: "", TokenPath: "/etc/clofix/guard/host-token"},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	if cfg.SchemaVersion != SchemaVersion {
		return cfg, fmt.Errorf("unsupported config schema_version %d (want %d)", cfg.SchemaVersion, SchemaVersion)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = Default().DataDir
	}
	return cfg, nil
}

func (c Config) Interval(d time.Duration, seconds int) time.Duration {
	if seconds <= 0 {
		return d
	}
	return time.Duration(seconds) * time.Second
}
