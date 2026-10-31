// Package database owns encrypted controller metadata for managed database jobs.
package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	wire "github.com/clofix/filevps/worker/pkg/database"
)

type Project struct {
	Inventory json.RawMessage              `json:"inventory,omitempty"`
	Spec      wire.Project                 `json:"spec"`
	Owner     string                       `json:"owner"`
	Name      string                       `json:"name"`
	WorkerID  string                       `json:"worker_id"`
	State     string                       `json:"state"`
	Error     string                       `json:"error,omitempty"`
	CA        string                       `json:"ca,omitempty"`
	Created   time.Time                    `json:"created"`
	Databases map[string]bool              `json:"databases"`
	Users     map[string]map[string]string `json:"users"`
	Metrics   wire.Status                  `json:"metrics"`
}
type Backup struct {
	ProjectSpec      wire.Project `json:"project_spec"`
	JobID            string       `json:"job_id"`
	PlainBytes       int64        `json:"plain_bytes"`
	ScheduleID       string       `json:"schedule_id,omitempty"`
	ProtectedBy      string       `json:"protected_by,omitempty"`
	Attempts         int          `json:"attempts"`
	NextAttempt      time.Time    `json:"next_attempt"`
	TransferAttempts int          `json:"transfer_attempts"`
	ID               string       `json:"id"`
	Owner            string       `json:"owner"`
	ProjectID        string       `json:"project_id"`
	Database         string       `json:"database"`
	Engine           string       `json:"engine"`
	EngineVersion    string       `json:"engine_version"`
	Tables           []string     `json:"tables,omitempty"`
	Format           string       `json:"format"`
	State            string       `json:"state"`
	Destination      string       `json:"destination"`
	ConnectionID     string       `json:"connection_id,omitempty"`
	WorkerID         string       `json:"worker_id"`
	EncryptedKey     []byte       `json:"encrypted_key"`
	Bytes            int64        `json:"bytes"`
	ReservedBytes    int64        `json:"reserved_bytes"`
	SHA256           string       `json:"sha256"`
	Created          time.Time    `json:"created"`
	Error            string       `json:"error,omitempty"`
}
type Job struct {
	SourceBackupID string          `json:"source_backup_id,omitempty"`
	ID             string          `json:"id"`
	Owner          string          `json:"owner"`
	ProjectID      string          `json:"project_id"`
	WorkerID       string          `json:"worker_id"`
	Action         string          `json:"action"`
	State          string          `json:"state"`
	Progress       int             `json:"progress"`
	Attempts       int             `json:"attempts"`
	Error          string          `json:"error,omitempty"`
	Payload        []byte          `json:"payload"`
	Result         json.RawMessage `json:"result,omitempty"`
	Created        time.Time       `json:"created"`
	Updated        time.Time       `json:"updated"`
	After          string          `json:"after,omitempty"`
	Cancellation   bool            `json:"cancellation"`
	BackupID       string          `json:"backup_id,omitempty"`
}
type Schedule struct {
	LimitMB      int64  `json:"limit_mb"`
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	ProjectID    string `json:"project_id"`
	Database     string `json:"database"`
	Cron         string `json:"cron"`
	Retention    int    `json:"retention"`
	Destination  string `json:"destination"`
	ConnectionID string `json:"connection_id"`
	Enabled      bool   `json:"enabled"`
	LastMinute   string `json:"last_minute"`
	Error        string `json:"error,omitempty"`
}
type Upload struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	ProjectID    string    `json:"project_id"`
	Database     string    `json:"database"`
	Filename     string    `json:"filename"`
	Format       string    `json:"format"`
	Total        int64     `json:"total"`
	Offset       int64     `json:"offset"`
	SHA256       string    `json:"sha256"`
	Parts        []int64   `json:"parts"`
	EncryptedKey []byte    `json:"encrypted_key"`
	Created      time.Time `json:"created"`
}
type SnapshotStore interface {
	LoadDatabaseState() ([]byte, error)
	SaveDatabaseState([]byte) error
}
type State struct {
	sync.Mutex
	Projects  map[string]Project  `json:"projects"`
	Backups   map[string]Backup   `json:"backups"`
	Jobs      map[string]Job      `json:"jobs"`
	Schedules map[string]Schedule `json:"schedules"`
	Uploads   map[string]Upload   `json:"uploads"`
	backend   SnapshotStore
	root      string
}

func New(root string, backend SnapshotStore) (*State, error) {
	s := &State{backend: backend, root: root}
	var raw []byte
	var e error
	if backend != nil {
		raw, e = backend.LoadDatabaseState()
	} else {
		raw, e = os.ReadFile(filepath.Join(root, "database-state.json"))
		if os.IsNotExist(e) {
			e = nil
		}
	}
	if e != nil {
		return nil, e
	}
	if len(raw) > 0 {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("invalid null managed database state")
		}
		if e = json.Unmarshal(raw, s); e != nil {
			return nil, fmt.Errorf("invalid managed database state: %w", e)
		}
	}
	if s.Projects == nil {
		s.Projects = map[string]Project{}
	}
	if s.Backups == nil {
		s.Backups = map[string]Backup{}
	}
	if s.Jobs == nil {
		s.Jobs = map[string]Job{}
	}
	if s.Schedules == nil {
		s.Schedules = map[string]Schedule{}
	}
	if s.Uploads == nil {
		s.Uploads = map[string]Upload{}
	}
	return s, nil
}

// Callers hold the state mutex and roll back in-memory changes if persistence fails.
func (s *State) Save() error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	if s.backend != nil {
		return s.backend.SaveDatabaseState(raw)
	}
	if e = os.MkdirAll(s.root, 0700); e != nil {
		return e
	}
	file := filepath.Join(s.root, "database-state.json")
	if e = os.WriteFile(file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(file+".tmp", file)
}
func (s *State) Root() string { return s.root }
