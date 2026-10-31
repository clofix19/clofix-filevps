package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func (a *Agent) databaseTransfer(ctx context.Context, method, target string, body io.Reader) (io.ReadCloser, error) {
	path, e := url.Parse(target)
	if e != nil || path.IsAbs() || path.Host != "" || path.RawQuery != "" || path.Fragment != "" || !strings.HasPrefix(path.Path, "/v1/database/") {
		return nil, fmt.Errorf("invalid database transfer path")
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.Cfg.ControllerURL, "/")+path.String(), body)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Transport: a.hc.Transport, Timeout: 6 * time.Hour, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("database transfers do not follow redirects")
	}}
	response, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("secure database transfer failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("controller database transfer returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}
func (a *Agent) runDatabaseOperation(ctx context.Context, op proto.Operation) {
	if op.Action == "cancel-database" {
		a.Database.Cancel(op.DatabaseJobID)
		return
	}
	select {
	case a.databaseSlot <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-a.databaseSlot }()
		source, e := a.databaseTransfer(ctx, "GET", "/v1/database/jobs/"+op.DatabaseJobID, nil)
		if e != nil {
			return
		}
		var request db.Operation
		e = json.NewDecoder(io.LimitReader(source, 2<<20)).Decode(&request)
		source.Close()
		if e != nil || request.JobID != op.DatabaseJobID {
			return
		}
		if request.Action == "provision" {
			capacity := a.capacity()
			if _, e = scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: capacity}}, scheduler.Request{RAM: uint64(request.Project.RAMMB) << 20, CPU: request.Project.CPU, Disk: uint64(request.Project.DiskMB) << 20}, scheduler.DefaultConfig(), time.Now()); e != nil {
				a.Database.Reject(request, "worker database admission rejected: insufficient safe capacity")
				return
			}
		}
		a.Database.Run(ctx, request)
	}()
}
func (a *Agent) databaseMonitor(ctx context.Context) {
	a.Database.Recover(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		sample, stop := context.WithTimeout(ctx, 25*time.Second)
		statuses := a.Database.Statuses(sample)
		stop()
		engines, projects := a.Database.Inventory()
		a.mu.Lock()
		a.databaseStatuses = statuses
		a.databaseEngines = engines
		a.databaseProjects = projects
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
