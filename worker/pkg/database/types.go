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

// Keep in sync with proto.HostDatabaseBase/Count (avoid importing proto: cycle).
const hostDatabaseBase, hostDatabaseCount = 47000, 5000

var ID = regexp.MustCompile(`^(dbp|dbj|dbb|dbu)-[a-f0-9]{32}$`)
var Identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,47}$`)

func Name(name string) error {
	if !Identifier.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "clofix_") {
		return fmt.Errorf("names must start with a letter and use up to 48 letters, digits or underscores")
	}
	switch strings.ToLower(name) {
	case "admin", "local", "config", "mysql", "sys", "performance_schema", "information_schema", "postgres", "template0", "template1", "redis":
		return fmt.Errorf("system database name is reserved")
	}
	return nil
}
func Engine(engine string) bool {
	return engine == "mysql" || engine == "postgresql" || engine == "mongodb" || engine == "redis"
}

// Properties holds tunable performance settings applied at engine start.
// Zero values mean "fill recommended defaults for the instance RAM".
type Properties struct {
	MaxConnections int `json:"max_connections"`
	BufferMB       int `json:"buffer_mb"`
	WorkMemMB      int `json:"work_mem_mb"`
}

type Project struct {
	ID         string     `json:"id"`
	Engine     string     `json:"engine"`
	Version    string     `json:"version"`
	RAMMB      int64      `json:"ram_mb"`
	CPU        float64    `json:"cpu"`
	DiskMB     int64      `json:"disk_mb"`
	Port       int        `json:"port"`
	Host       string     `json:"host"`
	VPCID      string     `json:"vpc_id,omitempty"`
	Properties Properties `json:"properties"`
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// DefaultProperties returns engine-aware performance defaults for the given RAM.
func DefaultProperties(engine string, ramMB int64) Properties {
	if ramMB < 256 {
		ramMB = 256
	}
	maxConn := clampInt(int(ramMB/8), 50, 500)
	buffer := clampInt(int(ramMB*60/100), 128, int(ramMB*80/100))
	work := clampInt(int(ramMB/64), 4, 64)
	switch engine {
	case "postgresql":
		buffer = clampInt(int(ramMB/4), 128, int(ramMB/2))
		work = clampInt(int(ramMB/64), 4, 64)
	case "mongodb":
		buffer = clampInt(int(ramMB/2), 128, int(ramMB*70/100))
		work = 0
	case "redis":
		buffer = clampInt(int(ramMB*70/100), 64, int(ramMB*90/100))
		work = 0
		maxConn = clampInt(int(ramMB/4), 100, 10000)
	default: // mysql
		work = 0
	}
	return Properties{MaxConnections: maxConn, BufferMB: buffer, WorkMemMB: work}
}

func (p Properties) Empty() bool {
	return p.MaxConnections == 0 && p.BufferMB == 0 && p.WorkMemMB == 0
}

// NormalizeProperties fills defaults for zero fields and clamps to safe ranges.
func NormalizeProperties(engine string, ramMB int64, in Properties) Properties {
	def := DefaultProperties(engine, ramMB)
	out := in
	if out.MaxConnections == 0 {
		out.MaxConnections = def.MaxConnections
	}
	if out.BufferMB == 0 {
		out.BufferMB = def.BufferMB
	}
	if out.WorkMemMB == 0 && engine == "postgresql" {
		out.WorkMemMB = def.WorkMemMB
	}
	if engine != "postgresql" {
		out.WorkMemMB = 0
	}
	return out
}

func (p Properties) Validate(engine string, ramMB int64) error {
	if p.Empty() {
		return nil
	}
	maxBuffer := int(ramMB * 80 / 100)
	if maxBuffer < 128 {
		maxBuffer = 128
	}
	if p.MaxConnections < 10 || p.MaxConnections > 5000 {
		return fmt.Errorf("max_connections must be between 10 and 5000")
	}
	if p.BufferMB < 64 || p.BufferMB > maxBuffer {
		return fmt.Errorf("buffer_mb must be between 64 and %d for this RAM size", maxBuffer)
	}
	if engine == "postgresql" {
		if p.WorkMemMB < 1 || p.WorkMemMB > 1024 || int64(p.WorkMemMB) > ramMB/4 {
			return fmt.Errorf("work_mem_mb must be between 1 and min(1024, ram/4)")
		}
	} else if p.WorkMemMB != 0 {
		return fmt.Errorf("work_mem_mb is only valid for PostgreSQL")
	}
	return nil
}

func (p Project) Validate() error {
	if !ID.MatchString(p.ID) || !strings.HasPrefix(p.ID, "dbp-") || !Engine(p.Engine) || p.RAMMB < 256 || p.RAMMB > 1048576 || math.IsNaN(p.CPU) || math.IsInf(p.CPU, 0) || p.CPU < 0.25 || p.CPU > 128 || p.DiskMB < 512 || p.DiskMB > 1048576 || p.Port < hostDatabaseBase || p.Port >= hostDatabaseBase+hostDatabaseCount {
		return fmt.Errorf("invalid database project specification")
	}
	return p.Properties.Validate(p.Engine, p.RAMMB)
}

// CoreEqual compares identity/resources and ignores performance properties.
func (p Project) CoreEqual(other Project) bool {
	a, b := p, other
	a.Properties, b.Properties = Properties{}, Properties{}
	return a == b
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
	JobID         string            `json:"job_id"`
	RecoveryJobID string            `json:"recovery_job_id,omitempty"`
	Project       Project           `json:"project"`
	Action        string            `json:"action"`
	Database      string            `json:"database,omitempty"`
	NewName       string            `json:"new_name,omitempty"`
	Username      string            `json:"username,omitempty"`
	Password      string            `json:"password,omitempty"`
	Grants        map[string]string `json:"grants,omitempty"`
	Table         string            `json:"table,omitempty"`
	Columns       []Column          `json:"columns,omitempty"`
	Index         *Index            `json:"index,omitempty"`
	Query         string            `json:"query,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	Offset        int               `json:"offset,omitempty"`
	Values        map[string]*string `json:"values,omitempty"`
	Keys          map[string]string  `json:"keys,omitempty"`
	Properties    *Properties       `json:"properties,omitempty"`
	Backup        *BackupSpec       `json:"backup,omitempty"`
	Restore       *RestoreSpec      `json:"restore,omitempty"`
}
type BackupSpec struct {
	ID         string   `json:"id"`
	Tables     []string `json:"tables,omitempty"`
	Scope      string   `json:"scope,omitempty"`
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
