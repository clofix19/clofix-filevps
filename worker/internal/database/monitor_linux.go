//go:build linux

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"syscall"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func (m *Manager) engineVersion(ctx context.Context, r record) (string, error) {
	switch r.Spec.Engine {
	case "mysql":
		return m.sql(ctx, r, "", "SELECT VERSION();")
	case "postgresql":
		return m.sql(ctx, r, "postgres", "SHOW server_version;")
	default:
		return m.mongo(ctx, r, `print(db.version())`)
	}
}

// Statuses performs bounded, sequential sampling. An unavailable sample is marked
// with an error rather than reporting invented healthy metrics.
func (m *Manager) Statuses(ctx context.Context) []db.Status {
	m.Lock()
	records := make([]record, 0, len(m.records))
	for _, r := range m.records {
		records = append(records, r)
	}
	m.Unlock()
	out := make([]db.Status, 0, len(records))
	for _, r := range records {
		status := db.Status{ProjectID: r.Spec.ID, State: r.State, EngineVersion: r.Image.Version}
		sample, cancel := context.WithTimeout(ctx, 5*time.Second)
		if e := m.sample(sample, r, &status); e != nil {
			status.State = "UNHEALTHY"
			status.Error = "database metric sample failed"
		}
		cancel()
		out = append(out, status)
		if ctx.Err() != nil {
			break
		}
	}
	return out
}
func (m *Manager) sample(ctx context.Context, r record, status *db.Status) error {
	raw, e := m.docker(ctx, nil, "stats", "--no-stream", "--format", `{"cpu":{{json .CPUPerc}},"memory":{{json .MemUsage}}}`, container(r.Spec.ID))
	if e != nil {
		return e
	}
	var stats struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	}
	if json.Unmarshal(raw, &stats) != nil {
		return fmt.Errorf("invalid runtime metrics")
	}
	status.CPUPercent, e = strconv.ParseFloat(strings.TrimSuffix(stats.CPU, "%"), 64)
	if e != nil {
		return e
	}
	status.RAMBytes, e = parseRuntimeBytes(strings.TrimSpace(strings.Split(stats.Memory, "/")[0]))
	if e != nil {
		return e
	}
	// Count actual allocated blocks; the quota image itself is preallocated and is
	// intentionally excluded from logical database usage.
	var usage syscall.Statfs_t
	if e = syscall.Statfs(m.path(r.Spec.ID)+"/data", &usage); e != nil {
		return e
	}
	status.DiskBytes = (usage.Blocks - usage.Bfree) * uint64(usage.Bsize)
	var connections string
	switch r.Spec.Engine {
	case "mysql":
		connections, e = m.sql(ctx, r, "", "SELECT COUNT(*) FROM information_schema.processlist;")
	case "postgresql":
		connections, e = m.sql(ctx, r, "postgres", "SELECT COUNT(*) FROM pg_stat_activity WHERE backend_type='client backend';")
	default:
		connections, e = m.mongo(ctx, r, `print(db.serverStatus().connections.current)`)
	}
	if e != nil {
		return e
	}
	status.Connections, e = strconv.Atoi(strings.TrimSpace(connections))
	return e
}
func parseRuntimeBytes(value string) (uint64, error) {
	for _, unit := range []struct {
		name   string
		factor float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}} {
		if strings.HasSuffix(value, unit.name) {
			number, e := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, unit.name)), 64)
			if e != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > float64(^uint64(0))/unit.factor {
				return 0, fmt.Errorf("invalid memory metric")
			}
			return uint64(number * unit.factor), nil
		}
	}
	return 0, fmt.Errorf("unknown memory metric unit")
}
