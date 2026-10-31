package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type managedApp struct {
	ID         string            `json:"id"`
	Owner      string            `json:"owner"`
	Name       string            `json:"name"`
	Template   string            `json:"template"`
	WorkerID   string            `json:"worker_id"`
	State      string            `json:"state"`
	Desired    string            `json:"desired"`
	Ready      bool              `json:"ready"`
	Error      string            `json:"error,omitempty"`
	Created    time.Time         `json:"created"`
	URL        string            `json:"url"`
	Spec       proto.ManagedSpec `json:"spec"`
	Credential []byte            `json:"credential,omitempty"`
	Pending    string            `json:"pending,omitempty"`
	Logs       string            `json:"logs,omitempty"`
}

func safeManaged(a managedApp) managedApp {
	a.Credential = nil
	a.Spec.GatewayHash = ""
	a.Pending = ""
	a.Logs = ""
	return a
}
func (s *Server) managedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/managed-apps/templates", s.auth(s.managedTemplates))
	mux.HandleFunc("GET /api/managed-apps", s.auth(s.listManagedApps))
	mux.HandleFunc("POST /api/managed-apps", s.auth(s.deployManagedApp))
	mux.HandleFunc("POST /api/managed-apps/{id}/{action}", s.auth(s.controlManagedApp))
	mux.HandleFunc("GET /api/managed-apps/{id}/logs", s.auth(s.managedAppLogs))
}
func (s *Server) managedTemplates(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 500, "cannot list workers")
		return
	}
	out := []map[string]any{}
	for _, t := range proto.ManagedTemplates {
		count := 0
		for _, wk := range workers {
			if wk.Status != "active" || time.Since(wk.LastHeartbeat) > time.Minute {
				continue
			}
			for _, id := range wk.Capacity.ManagedTemplates {
				if id == t.ID {
					count++
				}
			}
		}
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "description": t.Description, "ram_mb": t.RAMMB, "ready_workers": count})
	}
	writeJSON(w, 200, out)
}
func (s *Server) listManagedApps(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	items := []managedApp{}
	for _, a := range d.Managed {
		if canAccessApplication(r, a.Owner) {
			items = append(items, safeManaged(a))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	writeJSON(w, 200, items)
}

var managedPublicHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

func managedAppURL(host string, port int) string {
	if !managedPublicHost.MatchString(host) && net.ParseIP(host) == nil {
		return ""
	}
	scheme := os.Getenv("CLOFIX_APP_PUBLIC_SCHEME")
	if scheme != "https" {
		scheme = "http"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
}
func (s *Server) deployManagedApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		Template string  `json:"template"`
		RAMMB    int64   `json:"ram_mb"`
		CPU      float64 `json:"cpu"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 80 {
		fail(w, 400, "application name is required (max 80 characters)")
		return
	}
	var template *proto.ManagedTemplate
	for i := range proto.ManagedTemplates {
		if proto.ManagedTemplates[i].ID == in.Template {
			template = &proto.ManagedTemplates[i]
		}
	}
	if template == nil {
		fail(w, 400, "unknown managed app template")
		return
	}
	if in.RAMMB == 0 {
		in.RAMMB = template.RAMMB
	}
	if in.CPU == 0 {
		in.CPU = 1
	}
	password := randHex(24)
	credential, err := s.encryptApplicationSecrets(map[string]string{"username": "client", "password": password})
	if err != nil {
		fail(w, 500, "cannot encrypt application credential")
		return
	}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	if len(d.Managed) >= 100 {
		fail(w, 409, "managed application limit reached")
		return
	}
	spec := proto.ManagedSpec{ID: "ma-" + randHex(8), Template: in.Template, RAMMB: in.RAMMB, CPU: in.CPU, GatewayHash: hashToken(password), Port: 25000, Address: "10.231.128.2"}
	if err = proto.ValidateManaged(spec); err != nil {
		fail(w, 400, err.Error())
		return
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 500, "cannot list workers")
		return
	}
	candidates := []scheduler.Candidate{}
	for _, wk := range workers {
		ready := false
		for _, id := range wk.Capacity.ManagedTemplates {
			if id == in.Template {
				ready = true
			}
		}
		if !ready {
			continue
		}
		cp := wk.Capacity
		for _, app := range d.Managed {
			if app.WorkerID == wk.ID && app.Pending != "" && app.Desired == "running" {
				cp.RAMCommitted += uint64(app.Spec.RAMMB) << 20
				cp.CPUAllocated += app.Spec.CPU
			}
		}
		candidates = append(candidates, scheduler.Candidate{ID: wk.ID, Active: wk.Status == "active", LastHeartbeat: wk.LastHeartbeat, Capacity: cp})
	}
	selection, err := scheduler.Place(candidates, scheduler.Request{RAM: uint64(in.RAMMB) << 20, CPU: in.CPU, Disk: 2 << 30}, s.Sched, time.Now())
	if err != nil {
		fail(w, 409, "no ready worker with this template and sufficient capacity")
		return
	}
	used := map[int]bool{}
	for _, a := range d.Managed {
		if a.WorkerID == selection.WorkerID {
			used[a.Spec.Port] = true
		}
	}
	for spec.Port < 25100 && used[spec.Port] {
		spec.Port++
	}
	if spec.Port >= 25100 {
		fail(w, 409, "worker application ports exhausted")
		return
	}
	spec.Address = fmt.Sprintf("10.231.128.%d", spec.Port-25000+2)
	host := os.Getenv("CLOFIX_APP_PUBLIC_HOST")
	if host == "" {
		host = "localhost"
	}
	wk, _ := s.Store.GetWorker(selection.WorkerID)
	if wk.Capacity.ManagedPublicHost != "" {
		host = wk.Capacity.ManagedPublicHost
	}
	appURL := managedAppURL(host, spec.Port)
	if appURL == "" {
		fail(w, 500, "invalid managed application public hostname")
		return
	}
	a := managedApp{ID: spec.ID, Owner: applicationOwner(r), Name: strings.TrimSpace(in.Name), Template: in.Template, WorkerID: selection.WorkerID, State: "DEPLOYING", Desired: "running", Created: time.Now().UTC(), URL: appURL, Spec: spec, Credential: credential}
	if err = s.queueManaged(&a, "deploy"); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(a.Owner, "managed-app.deploy", a.ID, in.Template)
	writeJSON(w, 202, safeManaged(a))
}

// Caller holds the application store lock. Queue IDs fence late results.
func (s *Server) queueManaged(a *managedApp, action string) error {
	d := s.applications
	old, exists := d.Managed[a.ID]
	a.Pending = "managed/" + a.ID + "/" + action + "/" + randHex(4)
	d.Managed[a.ID] = *a
	if err := d.save(); err != nil {
		if exists {
			d.Managed[a.ID] = old
		} else {
			delete(d.Managed, a.ID)
		}
		return fmt.Errorf("cannot persist managed operation")
	}
	if err := s.Store.QueueOp(a.WorkerID, proto.Operation{ID: a.Pending, Action: "managed-app", Managed: &a.Spec, ServiceAction: action}); err != nil {
		a.State = "FAILED"
		a.Error = "could not queue managed operation"
		a.Pending = ""
		d.Managed[a.ID] = *a
		_ = d.save()
		return err
	}
	return nil
}
func (s *Server) controlManagedApp(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Managed[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "managed application not found")
		return
	}
	if action == "credentials" {
		values, err := s.decryptApplicationSecrets(a.Credential)
		if err != nil {
			fail(w, 500, "cannot decrypt credentials")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, values)
		return
	}
	if action != "start" && action != "stop" && action != "restart" && action != "delete" {
		fail(w, 400, "unknown application action")
		return
	}
	if a.Pending != "" {
		fail(w, 409, "wait for the current operation to finish")
		return
	}
	if action == "delete" || action == "stop" {
		a.Desired = "stopped"
	} else {
		a.Desired = "running"
	}
	a.State = map[string]string{"start": "STARTING", "stop": "STOPPING", "restart": "RESTARTING", "delete": "DELETING"}[action]
	a.Error = ""
	if err := s.queueManaged(&a, action); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "managed-app."+action, a.ID, "queued")
	writeJSON(w, 202, safeManaged(a))
}
func (s *Server) managedAppLogs(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Managed[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "managed app not found")
		return
	}
	writeJSON(w, 200, map[string]string{"logs": a.Logs, "state": a.State, "error": a.Error})
}
func (s *Server) recordManagedApps(workerID string, items []proto.ManagedStatus) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	changed := false
	for _, reported := range items {
		a, ok := d.Managed[reported.ID]
		if !ok || a.WorkerID != workerID {
			continue
		}
		if len(reported.Logs) > 64<<10 {
			reported.Logs = reported.Logs[len(reported.Logs)-(64<<10):]
		}
		a.Logs = reported.Logs
		if a.Pending != "" && reported.OperationID == a.Pending {
			changed = true
			parts := strings.Split(a.Pending, "/")
			if len(parts) == 4 && parts[2] == "delete" && reported.State == "DELETED" {
				delete(d.Managed, a.ID)
				_ = d.save()
				continue
			}
			a.Pending = ""
			a.State = reported.State
			a.Desired = reported.Desired
			a.Error = reported.Error
		}
		a.Ready = reported.Ready
		if a.Pending == "" {
			a.State = reported.State
			a.Desired = reported.Desired
			a.Error = reported.Error
		}
		d.Managed[a.ID] = a
	}
	if changed {
		_ = d.save()
	}
}
func (s *Server) completeManaged(workerID string, result proto.OpResult) {
	parts := strings.Split(result.OpID, "/")
	if len(parts) != 4 {
		return
	}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Managed[parts[1]]
	if !ok || a.WorkerID != workerID || a.Pending != result.OpID {
		return
	}
	a.Pending = ""
	if result.OK {
		if parts[2] == "delete" {
			delete(d.Managed, a.ID)
			_ = d.save()
			return
		}
		if a.Desired == "running" {
			a.State = "RUNNING"
		} else {
			a.State = "STOPPED"
		}
		a.Error = ""
	} else {
		a.State = "FAILED"
		a.Error = result.Error
	}
	d.Managed[a.ID] = a
	_ = d.save()
}
