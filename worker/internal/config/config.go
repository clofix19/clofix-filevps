// Package config loads the worker configuration. Enrollment tokens are never stored here.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	ControllerURL      string `json:"controller_url"`
	WorkerID           string `json:"worker_id"`
	HostReserveMB      int64  `json:"host_reserve_mb"`
	ZRAMMB             int64  `json:"zram_mb"` // 0 = disabled
	HeartbeatSeconds   int    `json:"heartbeat_seconds"`
	UIDBase            uint32 `json:"uid_base"` // host uid/gid base mapped to container root
	UserNS             bool   `json:"user_ns"`
	S3MasterAddress    string `json:"s3_master_address,omitempty"`
	S3AdvertiseAddress string `json:"s3_advertise_address,omitempty"`
	S3DataDir          string `json:"s3_data_dir,omitempty"`
}

func Default() Config {
	return Config{HostReserveMB: 512, HeartbeatSeconds: 10, UIDBase: 100000, UserNS: true}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func ConfigDir() string   { return envOr("CLOFIX_CONFIG_DIR", "/etc/clofix-filevps") }
func StateDir() string    { return envOr("CLOFIX_STATE_DIR", "/var/lib/clofix-filevps") }
func SecurityDir() string { return filepath.Join(ConfigDir(), "security") }
func path() string        { return filepath.Join(ConfigDir(), "worker.json") }

func Load() (Config, error) {
	c := Default()
	b, err := os.ReadFile(path())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if raw, ok := os.LookupEnv("CLOFIX_USER_NS"); ok {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("CLOFIX_USER_NS: %w", err)
		}
		c.UserNS = v
	}
	if raw, ok := os.LookupEnv("CLOFIX_ZRAM_MB"); ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return c, fmt.Errorf("CLOFIX_ZRAM_MB must be a non-negative integer")
		}
		c.ZRAMMB = v
	}
	if v := os.Getenv("CLOFIX_S3_ADVERTISE_ADDRESS"); v != "" {
		c.S3AdvertiseAddress = v
	}
	if v := os.Getenv("CLOFIX_S3_DATA_DIR"); v != "" {
		c.S3DataDir = v
	}
	return c, nil
}

func (c Config) Save() error {
	if err := os.MkdirAll(ConfigDir(), 0o750); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path(), b, 0o640)
}
