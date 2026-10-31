//go:build linux

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func (m *Manager) Inventory() (map[string]string, []string) {
	ready := m.Ready()
	m.Lock()
	defer m.Unlock()
	engines := map[string]string{}
	for _, name := range ready {
		engines[name] = m.images[name].Version
	}
	projects := []string{}
	for id := range m.records {
		projects = append(projects, id)
	}
	return engines, projects
}
func (m *Manager) Acknowledge(ids []string) error {
	m.Lock()
	defer m.Unlock()
	removed := map[string]db.Result{}
	for _, id := range ids {
		if result, ok := m.results[id]; ok && result.State != "RUNNING" {
			removed[id] = result
			delete(m.results, id)
		}
	}
	if e := m.journal(); e != nil {
		for id, result := range removed {
			m.results[id] = result
		}
		return e
	}
	return nil
}

// Recover only starts instances with a durable READY record. Interrupted work is
// fenced by New; it never replays a provisioning or restore command.
func (m *Manager) Recover(ctx context.Context) {
	m.Lock()
	records := []record{}
	for _, r := range m.records {
		if r.State == "READY" {
			records = append(records, r)
		}
	}
	m.Unlock()
	for _, r := range records {
		if ctx.Err() != nil {
			return
		}
		if _, e := m.provision(ctx, r.Spec); e != nil {
			m.Lock()
			r.State = "NEEDS_INSPECTION"
			m.records[r.Spec.ID] = r
			m.save()
			m.Unlock()
		}
	}
}
func (m *Manager) removeBackup(r record, op db.Operation) (json.RawMessage, error) {
	if op.Backup == nil || !db.ID.MatchString(op.Backup.ID) || !strings.HasPrefix(op.Backup.ID, "dbb-") {
		return nil, fmt.Errorf("invalid backup ID")
	}
	for _, suffix := range []string{".enc", ".enc.json", ".enc.tmp", ".enc.json.tmp"} {
		if e := os.Remove(filepath.Join(m.path(r.Spec.ID), "backups", op.Backup.ID+suffix)); e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	}
	return json.RawMessage(`{"deleted":true}`), nil
}

func (m *Manager) Reject(op db.Operation, message string) {
	m.Lock()
	defer m.Unlock()
	if _, exists := m.results[op.JobID]; exists {
		return
	}
	m.results[op.JobID] = db.Result{JobID: op.JobID, ProjectID: op.Project.ID, State: "FAILED", Error: message, At: time.Now().UTC()}
	m.journal()
}
