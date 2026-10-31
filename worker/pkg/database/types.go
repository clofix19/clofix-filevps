// Package database defines the authenticated controller/worker database protocol.
package database

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var ID = regexp.MustCompile(`^(dbp|dbj|dbb|dbu)-[a-f0-9]{32}$`)
var Identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,47}$`)

func Name(name string) error {
	if !Identifier.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "clofix_") {
		return fmt.Errorf("names must start with a letter and use up to 48 letters, digits or underscores")
	}
	switch strings.ToLower(name) {
	case "admin", "local", "config", "mysql", "sys", "performance_schema", "information_schema", "postgres", "template0", "template1":
		return fmt.Errorf("system database name is reserved")
	}
	return nil
}
func Engine(engine string) bool {
	return engine == "mysql" || engine == "postgresql" || engine == "mongodb"
}

type Project struct {
	ID      string  `json:"id"`
	Engine  string  `json:"engine"`
	Version string  `json:"version"`
	RAMMB   int64   `json:"ram_mb"`
	CPU     float64 `json:"cpu"`
	DiskMB  int64   `json:"disk_mb"`
	Port    int     `json:"port"`
	Host    string  `json:"host"`
}

func (p Project) Validate() error {
	if !ID.MatchString(p.ID) || !strings.HasPrefix(p.ID, "dbp-") || !Engine(p.Engine) || p.RAMMB < 256 || p.RAMMB > 1048576 || math.IsNaN(p.CPU) || math.IsInf(p.CPU, 0) || p.CPU < 0.25 || p.CPU > 128 || p.DiskMB < 512 || p.DiskMB > 1048576 || p.Port < 26000 || p.Port > 26099 {
		return fmt.Errorf("invalid database project specification")
	}
	return nil
}

type Column struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Primary  bool    `json:"primary"`
	Unique   bool    `json:"unique"`
	Default  *string `json:"default,omitempty"`
}
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}
type Operation struct {
	JobID    string            `json:"job_id"`
	Project  Project           `json:"project"`
	Action   string            `json:"action"`
	Database string            `json:"database,omitempty"`
	NewName  string            `json:"new_name,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Grants   map[string]string `json:"grants,omitempty"`
	Table    string            `json:"table,omitempty"`
	Columns  []Column          `json:"columns,omitempty"`
	Index    *Index            `json:"index,omitempty"`
	Backup   *BackupSpec       `json:"backup,omitempty"`
	Restore  *RestoreSpec      `json:"restore,omitempty"`
}
type BackupSpec struct {
	ID         string   `json:"id"`
	Tables     []string `json:"tables,omitempty"`
	Key        []byte   `json:"key"`
	LimitBytes int64    `json:"limit_bytes"`
	CentralURL string   `json:"central_url,omitempty"`
}
type RestoreSpec struct {
	SafetyBackupID   string                       `json:"safety_backup_id,omitempty"`
	UserGrants       map[string]map[string]string `json:"user_grants,omitempty"`
	BackupID         string                       `json:"backup_id"`
	Key              []byte                       `json:"key"`
	SHA256           string                       `json:"sha256"`
	Format           string                       `json:"format"`
	SourceDatabase   string                       `json:"source_database"`
	EngineVersion    string                       `json:"engine_version"`
	Tables           []string                     `json:"tables,omitempty"`
	SourceURL        string                       `json:"source_url"`
	LimitBytes       int64                        `json:"limit_bytes"`
	ConfirmOverwrite bool                         `json:"confirm_overwrite"`
}
type Result struct {
	JobID       string          `json:"job_id"`
	ProjectID   string          `json:"project_id"`
	State       string          `json:"state"`
	Progress    int             `json:"progress"`
	Error       string          `json:"error,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	BackupBytes int64           `json:"backup_bytes,omitempty"`
	SHA256      string          `json:"sha256,omitempty"`
	At          time.Time       `json:"at"`
}
type Status struct {
	ProjectID     string  `json:"project_id"`
	State         string  `json:"state"`
	CPUPercent    float64 `json:"cpu_percent"`
	RAMBytes      uint64  `json:"ram_bytes"`
	DiskBytes     uint64  `json:"disk_bytes"`
	Connections   int     `json:"connections"`
	EngineVersion string  `json:"engine_version"`
	Error         string  `json:"error,omitempty"`
}
