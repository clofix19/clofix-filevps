package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type applicationVersion struct {
	Number           int             `json:"number"`
	Created          time.Time       `json:"created"`
	Execution        proto.Execution `json:"execution"`
	EncryptedSecrets []byte          `json:"encrypted_secrets,omitempty"`
}
type application struct {
	Version          int                  `json:"version"`
	Versions         []applicationVersion `json:"versions,omitempty"`
	Secrets          map[string]string    `json:"secrets,omitempty"`
	EncryptedSecrets []byte               `json:"encrypted_secrets,omitempty"`
	Schedule         string               `json:"schedule,omitempty"`
	LastSchedule     string               `json:"last_schedule,omitempty"`
	ScheduleError    string               `json:"schedule_error,omitempty"`
	WebhookHash      string               `json:"webhook_hash,omitempty"`

	ID        string          `json:"id"`
	Owner     string          `json:"owner"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Created   time.Time       `json:"created"`
	Execution proto.Execution `json:"execution"`
}
type applicationRun struct {
	Version  int                    `json:"version"`
	ID       string                 `json:"id"`
	AppID    string                 `json:"app_id"`
	Owner    string                 `json:"owner"`
	WorkerID string                 `json:"worker_id"`
	State    string                 `json:"state"`
	Created  time.Time              `json:"created"`
	Finished *time.Time             `json:"finished,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Result   *proto.ExecutionResult `json:"result,omitempty"`
}
type serverlessStore struct {
	Managed map[string]managedApp `json:"managed_apps,omitempty"`
	sync.Mutex
	Apps map[string]application    `json:"apps"`
	Runs map[string]applicationRun `json:"runs"`
	file string
}

func loadServerless(dir string) *serverlessStore {
	d := &serverlessStore{Apps: map[string]application{}, Runs: map[string]applicationRun{}, file: filepath.Join(dir, "serverless.json")}
	if b, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(b, d) != nil {
			d.Apps = map[string]application{}
			d.Runs = map[string]applicationRun{}
		}
	}
	if d.Managed == nil {
		d.Managed = map[string]managedApp{}
	}
	if d.Apps == nil {
		d.Apps = map[string]application{}
	}
	if d.Runs == nil {
		d.Runs = map[string]applicationRun{}
	}
	for id, a := range d.Apps {
		if a.Version == 0 {
			a.Version = 1
			a.Versions = []applicationVersion{{Number: 1, Created: a.Created, Execution: a.Execution, EncryptedSecrets: a.EncryptedSecrets}}
			d.Apps[id] = a
		}
	}
	for id, run := range d.Runs {
		if activeApplicationRun(run.State) {
			run.State = "INTERRUPTED"
			run.Error = "controller restarted; inspect worker before retrying"
			d.Runs[id] = run
		}
	}
	return d
}
func (d *serverlessStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := d.file + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.file)
}
func applicationOwner(r *http.Request) string {
	p := principalFrom(r)
	if p.Root {
		return "root"
	}
	return p.UserID
}
func canAccessApplication(r *http.Request, owner string) bool {
	return principalFrom(r).Root || owner == applicationOwner(r)
}
func (s *Server) applicationRoutes(mux *http.ServeMux) {
	s.applicationExtraRoutes(mux)
	mux.HandleFunc("GET /api/applications", s.auth(s.listApplications))
	mux.HandleFunc("POST /api/applications", s.auth(s.createApplication))
	mux.HandleFunc("DELETE /api/applications/{id}", s.auth(s.deleteApplication))
	mux.HandleFunc("POST /api/applications/{id}/run", s.auth(s.runApplication))
	mux.HandleFunc("POST /api/functions/{id}/invoke", s.auth(s.invokeFunction))
	mux.HandleFunc("GET /api/application-runs", s.auth(s.listApplicationRuns))
	mux.HandleFunc("DELETE /api/application-runs/{id}", s.auth(s.deleteApplicationRun))
	mux.HandleFunc("POST /api/application-runs/{id}/cancel", s.auth(s.cancelApplicationRun))
	mux.HandleFunc("GET /api/application-runs/{id}", s.auth(s.getApplicationRun))
}
func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	out := []application{}
	for _, a := range d.Apps {
		if canAccessApplication(r, a.Owner) {
			a = safeApplication(a)
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, 200, out)
}
func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var a application
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20)).Decode(&a) != nil {
		fail(w, 400, "invalid application JSON")
		return
	}
	if err := validateApplication(a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	encrypted, err := s.encryptApplicationSecrets(a.Secrets)
	if err != nil {
		fail(w, 500, "could not encrypt secrets")
		return
	}
	a.Secrets = nil
	a.EncryptedSecrets = encrypted
	a.Version = 1
	a.Versions = nil
	a.WebhookHash = ""
	a.LastSchedule = ""
	a.ScheduleError = ""
	a.ID = "app-" + randHex(8)
	a.Owner = applicationOwner(r)
	a.Created = time.Now().UTC()
	a.Versions = []applicationVersion{{Number: 1, Created: a.Created, Execution: a.Execution, EncryptedSecrets: encrypted}}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	if len(d.Apps) >= 100 {
		fail(w, 409, "application limit reached (100)")
		return
	}
	d.Apps[a.ID] = a
	if err := d.save(); err != nil {
		delete(d.Apps, a.ID)
		fail(w, 500, "could not persist application")
		return
	}
	s.Store.Audit(a.Owner, "application.create", a.ID, a.Kind)
	a = safeApplication(a)
	writeJSON(w, 201, a)
}
func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	for _, run := range d.Runs {
		if run.AppID == a.ID && activeApplicationRun(run.State) {
			fail(w, 409, "wait for the active run to finish")
			return
		}
	}
	delete(d.Apps, a.ID)
	if d.save() != nil {
		d.Apps[a.ID] = a
		fail(w, 500, "could not persist deletion")
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) queueApplication(r *http.Request, kind string) (applicationRun, error) {
	if e := s.checkQuota("applications", 0); e != nil {
		return applicationRun{}, e
	}
	var input struct {
		Input string `json:"input"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, r.Body, 96<<10)).Decode(&input) != nil {
		return applicationRun{}, fmt.Errorf("send JSON with an input string")
	}
	if len(input.Input) > 64<<10 {
		return applicationRun{}, fmt.Errorf("input exceeds 64 KiB")
	}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) || a.Kind != kind {
		return applicationRun{}, fmt.Errorf("%s not found", kind)
	}
	if len(d.Runs) >= 1000 {
		return applicationRun{}, fmt.Errorf("run history limit reached (1000)")
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		return applicationRun{}, err
	}
	candidates := []scheduler.Candidate{}
	for _, wk := range workers {
		busy := false
		for _, run := range d.Runs {
			if run.WorkerID == wk.ID && activeApplicationRun(run.State) {
				busy = true
				break
			}
		}
		supported := false
		for _, runtime := range wk.Capacity.ServerlessRuntimes {
			if runtime == a.Execution.Runtime {
				supported = true
			}
		}
		if supported && !busy {
			candidates = append(candidates, scheduler.Candidate{ID: wk.ID, Active: wk.Status == "active", LastHeartbeat: wk.LastHeartbeat, Capacity: wk.Capacity})
		}
	}
	selection, err := scheduler.Place(candidates, scheduler.Request{RAM: uint64(a.Execution.RAMMB) << 20, CPU: a.Execution.CPU}, s.Sched, time.Now())
	if err != nil {
		return applicationRun{}, fmt.Errorf("no available serverless worker: %w", err)
	}
	run := applicationRun{Version: a.Version, ID: "run-" + randHex(8), AppID: a.ID, Owner: a.Owner, WorkerID: selection.WorkerID, State: "QUEUED", Created: time.Now().UTC()}
	d.Runs[run.ID] = run
	if err = d.save(); err != nil {
		delete(d.Runs, run.ID)
		return applicationRun{}, err
	}
	execution := a.Execution
	execution.Env = cloneApplicationEnv(a.Execution.Env)
	secretEnv, err := s.decryptApplicationSecrets(a.EncryptedSecrets)
	if err != nil {
		delete(d.Runs, run.ID)
		_ = d.save()
		return applicationRun{}, fmt.Errorf("cannot decrypt application secrets")
	}
	for k, v := range secretEnv {
		execution.Env[k] = v
	}
	execution.Input = input.Input
	execution.Deadline = time.Now().Add(5 * time.Minute)
	if err = s.Store.QueueOp(run.WorkerID, proto.Operation{ID: "execution/" + run.ID, Action: "execute", Execution: &execution}); err != nil {
		run.State = "FAILED"
		run.Error = err.Error()
		d.Runs[run.ID] = run
		_ = d.save()
		return applicationRun{}, err
	}
	s.Store.Audit(applicationOwner(r), "application.run", a.ID, run.ID)
	return run, nil
}
func (s *Server) runApplication(w http.ResponseWriter, r *http.Request) {
	run, err := s.queueApplication(r, "job")
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, run)
}
func (s *Server) invokeFunction(w http.ResponseWriter, r *http.Request) {
	run, err := s.queueApplication(r, "function")
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.Header().Set("X-CloFix-Run-ID", run.ID)
	// Bounded HTTP wait; long cold starts can be followed using the run endpoint.
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			writeJSON(w, 202, run)
			return
		case <-ticker.C:
			d := s.applications
			d.Lock()
			current := d.Runs[run.ID]
			d.Unlock()
			if !activeApplicationRun(current.State) {
				code := 200
				if current.State != "SUCCEEDED" {
					code = 502
				}
				writeJSON(w, code, current)
				return
			}
		}
	}
}
func (s *Server) listApplicationRuns(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	out := []applicationRun{}
	for id, run := range d.Runs {
		if activeApplicationRun(run.State) && time.Since(run.Created) > 8*time.Minute {
			run.State = "EXPIRED"
			run.Error = "worker result was not received before the queue and execution deadline"
			now := time.Now().UTC()
			run.Finished = &now
			d.Runs[id] = run
			_ = d.save()
		}
		if canAccessApplication(r, run.Owner) {
			if run.Result != nil {
				copyResult := *run.Result
				copyResult.OutputZIP = nil
				copyResult.Stdout = ""
				copyResult.Stderr = ""
				run.Result = &copyResult
			}
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, 200, out)
}
func (s *Server) getApplicationRun(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	run, ok := d.Runs[r.PathValue("id")]
	if !ok || !canAccessApplication(r, run.Owner) {
		fail(w, 404, "run not found")
		return
	}
	writeJSON(w, 200, run)
}
func (s *Server) completeExecution(workerID string, result proto.OpResult) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	id := result.OpID[len("execution/"):]
	run, ok := d.Runs[id]
	if !ok || run.WorkerID != workerID || !activeApplicationRun(run.State) {
		return
	}
	run.State = "FAILED"
	if result.Execution != nil && result.Execution.Cancelled {
		run.State = "CANCELLED"
	}
	if result.OK && result.Execution != nil {
		run.State = "SUCCEEDED"
	}
	now := time.Now().UTC()
	run.Finished = &now
	if result.Execution != nil {
		copyResult := *result.Execution
		if len(copyResult.OutputZIP) > 0 {
			dir := filepath.Join(filepath.Dir(d.file), "application-output")
			err := os.MkdirAll(dir, 0700)
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, run.ID+".zip"), copyResult.OutputZIP, 0600)
			}
			if err != nil {
				copyResult.OutputError = "cannot persist output files"
			} else {
				copyResult.OutputAvailable = true
			}
			copyResult.OutputZIP = nil
		}
		run.Result = &copyResult
	}
	run.Error = result.Error
	d.Runs[id] = run
	_ = d.save()
}

func (s *Server) cancelApplicationRun(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	run, ok := d.Runs[r.PathValue("id")]
	if !ok || !canAccessApplication(r, run.Owner) {
		fail(w, 404, "run not found")
		return
	}
	if !activeApplicationRun(run.State) {
		fail(w, 409, "run is already finished")
		return
	}
	if err := s.Store.QueueOp(run.WorkerID, proto.Operation{ID: "cancel/" + run.ID, Action: "cancel-execution", InstanceID: "execution/" + run.ID}); err != nil {
		fail(w, 500, "could not queue cancellation")
		return
	}
	writeJSON(w, 202, map[string]string{"state": "CANCELLATION_REQUESTED"})
}

func (s *Server) deleteApplicationRun(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	run, ok := d.Runs[r.PathValue("id")]
	if !ok || !canAccessApplication(r, run.Owner) {
		fail(w, 404, "run not found")
		return
	}
	if activeApplicationRun(run.State) {
		fail(w, 409, "stop the run and wait for completion before deleting its record")
		return
	}
	delete(d.Runs, run.ID)
	if d.save() != nil {
		d.Runs[run.ID] = run
		fail(w, 500, "could not persist deletion")
		return
	}
	_ = os.Remove(filepath.Join(filepath.Dir(d.file), "application-output", run.ID+".zip"))
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
