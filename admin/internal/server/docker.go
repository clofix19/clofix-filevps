package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type dockerJob struct {
	ID        string         `json:"id"`
	Owner     string         `json:"owner"`
	WorkerID  string         `json:"worker_id"`
	Action    string         `json:"action"`
	State     string         `json:"state"`
	Progress  int            `json:"progress"`
	Error     string         `json:"error,omitempty"`
	Logs      string         `json:"logs,omitempty"`
	Created   time.Time      `json:"created"`
	Updated   time.Time      `json:"updated"`
	Finished  time.Time      `json:"finished,omitempty"`
	Op        dock.Operation `json:"-"`
	OpRaw     json.RawMessage `json:"op,omitempty"`
	ResultData json.RawMessage `json:"result_data,omitempty"`
}

type dockerContainer struct {
	ID         string            `json:"id"`
	Owner      string            `json:"owner"`
	Name       string            `json:"name"`
	WorkerID   string            `json:"worker_id"`
	State      string            `json:"state"`
	Desired    string            `json:"desired"`
	Error      string            `json:"error,omitempty"`
	Created    time.Time         `json:"created"`
	Updated    time.Time         `json:"updated"`
	Spec       dock.ContainerSpec `json:"spec"`
	AppID      string            `json:"app_id,omitempty"`
	Runtime    *dock.ContainerInfo `json:"runtime,omitempty"`
	WAFState         string            `json:"waf_state,omitempty"`
	OWASPProtection  map[string]bool   `json:"owasp_protection,omitempty"`
	WAFErrorPages    map[string]string `json:"waf_error_pages,omitempty"`
	WAFProxyName     string            `json:"waf_proxy_name,omitempty"`
}

type dockerApplication struct {
	ID           string            `json:"id"`
	Owner        string            `json:"owner"`
	Name         string            `json:"name"`
	Method       string            `json:"method"` // image|compose|git|quickapp
	Template     string            `json:"template,omitempty"`
	WorkerID     string            `json:"worker_id"`
	State        string            `json:"state"`
	Error        string            `json:"error,omitempty"`
	Created      time.Time         `json:"created"`
	Updated      time.Time         `json:"updated"`
	ContainerIDs []string          `json:"container_ids,omitempty"`
	ComposeID    string            `json:"compose_id,omitempty"`
	Image        string            `json:"image,omitempty"`
	Releases     []dockerRelease   `json:"releases,omitempty"`
	GitURL       string            `json:"git_url,omitempty"`
	GitBranch    string            `json:"git_branch,omitempty"`
	WebhookSecret string           `json:"-"`
	Domain       string            `json:"domain,omitempty"`
	Events       []dockerEvent     `json:"events,omitempty"`
	Spec         dock.ContainerSpec `json:"spec,omitempty"`
	ComposeYAML  string            `json:"compose_yaml,omitempty"`
	WAFState         string            `json:"waf_state,omitempty"` // INSTALLING|INSTALLED|UNINSTALLING|FAILED|UNINSTALLED
	OWASPProtection  map[string]bool   `json:"owasp_protection,omitempty"`
	WAFErrorPages    map[string]string `json:"waf_error_pages,omitempty"`
	WAFProxyName     string            `json:"waf_proxy_name,omitempty"`
}

type dockerRelease struct {
	Number  int       `json:"number"`
	Image   string    `json:"image"`
	Digest  string    `json:"digest,omitempty"`
	Created time.Time `json:"created"`
	Commit  string    `json:"commit,omitempty"`
}

type dockerEvent struct {
	At      time.Time `json:"at"`
	State   string    `json:"state"`
	Message string    `json:"message"`
}

type dockerNetwork struct {
	ID       string    `json:"id"`
	Owner    string    `json:"owner"`
	Name     string    `json:"name"`
	WorkerID string    `json:"worker_id"`
	Internal bool      `json:"internal"`
	Created  time.Time `json:"created"`
}

type dockerVolume struct {
	ID               string    `json:"id"`
	Owner            string    `json:"owner"`
	Name             string    `json:"name"`
	WorkerID         string    `json:"worker_id"`
	DeleteProtection bool      `json:"delete_protection"`
	Created          time.Time `json:"created"`
}

type dockerRegistry struct {
	ID       string `json:"id"`
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"-"`
}

type dockerBackup struct {
	ID            string    `json:"id"`
	Owner         string    `json:"owner"`
	Kind          string    `json:"kind"` // volume|compose|config
	TargetID      string    `json:"target_id"`
	WorkerID      string    `json:"worker_id"`
	State         string    `json:"state"`
	Error         string    `json:"error,omitempty"`
	RedeployAfter bool      `json:"redeploy_after,omitempty"`
	Created       time.Time `json:"created"`
}

type dockerStore struct {
	mu           sync.Mutex
	file         string
	Enabled      bool                         `json:"enabled"`
	Containers   map[string]dockerContainer   `json:"containers"`
	Applications map[string]dockerApplication `json:"applications"`
	Networks     map[string]dockerNetwork     `json:"networks"`
	Volumes      map[string]dockerVolume      `json:"volumes"`
	Registries   map[string]dockerRegistry    `json:"registries"`
	Backups      map[string]dockerBackup      `json:"backups"`
	Jobs         map[string]dockerJob         `json:"jobs"`
	SecurityEvents []map[string]any           `json:"security_events,omitempty"`
	WorkerStatus map[string]dock.Status       `json:"-"`
}

func loadDocker(dir string) *dockerStore {
	d := &dockerStore{
		file: filepath.Join(dir, "docker.json"),
		Enabled: true,
		Containers: map[string]dockerContainer{},
		Applications: map[string]dockerApplication{},
		Networks: map[string]dockerNetwork{},
		Volumes: map[string]dockerVolume{},
		Registries: map[string]dockerRegistry{},
		Backups: map[string]dockerBackup{},
		Jobs: map[string]dockerJob{},
		WorkerStatus: map[string]dock.Status{},
	}
	b, err := os.ReadFile(d.file)
	if err == nil {
		_ = json.Unmarshal(b, d)
	}
	if d.Containers == nil {
		d.Containers = map[string]dockerContainer{}
	}
	if d.Applications == nil {
		d.Applications = map[string]dockerApplication{}
	}
	if d.Networks == nil {
		d.Networks = map[string]dockerNetwork{}
	}
	if d.Volumes == nil {
		d.Volumes = map[string]dockerVolume{}
	}
	if d.Registries == nil {
		d.Registries = map[string]dockerRegistry{}
	}
	if d.Backups == nil {
		d.Backups = map[string]dockerBackup{}
	}
	if d.Jobs == nil {
		d.Jobs = map[string]dockerJob{}
	}
	if d.WorkerStatus == nil {
		d.WorkerStatus = map[string]dock.Status{}
	}
	for id, j := range d.Jobs {
		if len(j.OpRaw) > 0 {
			_ = json.Unmarshal(j.OpRaw, &j.Op)
			d.Jobs[id] = j
		}
	}
	return d
}

func (d *dockerStore) save() error {
	for id, j := range d.Jobs {
		raw, _ := json.Marshal(j.Op)
		j.OpRaw = raw
		d.Jobs[id] = j
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.file)
}

func dockerRandID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func (s *Server) initDocker(dataDir string) {
	s.docker = loadDocker(dataDir)
}

func (s *Server) dockerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docker/overview", s.auth(s.dockerOverview))
	mux.HandleFunc("GET /api/docker/workers", s.auth(s.dockerWorkers))
	mux.HandleFunc("POST /api/docker/workers/{id}/engine/{action}", s.auth(s.dockerEngineAction))
	mux.HandleFunc("GET /api/docker/containers", s.auth(s.dockerListContainers))
	mux.HandleFunc("POST /api/docker/containers", s.auth(s.dockerCreateContainer))
	mux.HandleFunc("GET /api/docker/containers/{id}", s.auth(s.dockerGetContainer))
	mux.HandleFunc("PUT /api/docker/containers/{id}", s.auth(s.dockerUpdateContainer))
	mux.HandleFunc("POST /api/docker/containers/{id}/{action}", s.auth(s.dockerControlContainer))
	mux.HandleFunc("DELETE /api/docker/containers/{id}", s.auth(s.dockerDeleteContainer))
	mux.HandleFunc("GET /api/docker/containers/{id}/logs", s.auth(s.dockerContainerLogs))
	mux.HandleFunc("GET /api/docker/images", s.auth(s.dockerListImages))
	mux.HandleFunc("POST /api/docker/images/pull", s.auth(s.dockerPullImage))
	mux.HandleFunc("DELETE /api/docker/images", s.auth(s.dockerRemoveImage))
	mux.HandleFunc("POST /api/docker/images/prune", s.auth(s.dockerPruneImages))
	mux.HandleFunc("GET /api/docker/applications", s.auth(s.dockerListApps))
	mux.HandleFunc("POST /api/docker/applications", s.auth(s.dockerCreateApp))
	mux.HandleFunc("GET /api/docker/applications/{id}", s.auth(s.dockerGetApp))
	mux.HandleFunc("PUT /api/docker/applications/{id}", s.auth(s.dockerUpdateApp))
	mux.HandleFunc("POST /api/docker/applications/{id}/redeploy", s.auth(s.dockerRedeployApp))
	mux.HandleFunc("POST /api/docker/applications/{id}/rollback/{n}", s.auth(s.dockerRollbackApp))
	mux.HandleFunc("DELETE /api/docker/applications/{id}", s.auth(s.dockerDeleteApp))
	mux.HandleFunc("GET /api/docker/templates", s.auth(s.dockerTemplates))
	mux.HandleFunc("GET /api/docker/compose", s.auth(s.dockerListCompose))
	mux.HandleFunc("POST /api/docker/compose/deploy", s.auth(s.dockerDeployCompose))
	mux.HandleFunc("POST /api/docker/compose/{id}/{action}", s.auth(s.dockerComposeAction))
	mux.HandleFunc("GET /api/docker/networks", s.auth(s.dockerListNetworks))
	mux.HandleFunc("POST /api/docker/networks", s.auth(s.dockerCreateNetwork))
	mux.HandleFunc("DELETE /api/docker/networks/{id}", s.auth(s.dockerDeleteNetwork))
	mux.HandleFunc("GET /api/docker/volumes", s.auth(s.dockerListVolumes))
	mux.HandleFunc("POST /api/docker/volumes", s.auth(s.dockerCreateVolume))
	mux.HandleFunc("DELETE /api/docker/volumes/{id}", s.auth(s.dockerDeleteVolume))
	mux.HandleFunc("GET /api/docker/deployments", s.auth(s.dockerListDeployments))
	mux.HandleFunc("GET /api/docker/deployments/{id}", s.auth(s.dockerGetDeployment))
	mux.HandleFunc("GET /api/docker/metrics", s.auth(s.dockerMetrics))
	mux.HandleFunc("GET /api/docker/security", s.auth(s.dockerSecurity))
	mux.HandleFunc("POST /api/docker/security/scan", s.auth(s.dockerSecurityScan))
	mux.HandleFunc("POST /api/docker/applications/{id}/waf/install", s.auth(s.dockerWAFInstallApp))
	mux.HandleFunc("POST /api/docker/applications/{id}/waf/uninstall", s.auth(s.dockerWAFUninstallApp))
	mux.HandleFunc("POST /api/docker/applications/{id}/waf", s.auth(s.dockerWAFCategoryApp))
	mux.HandleFunc("PUT /api/docker/applications/{id}/waf/error-pages", s.auth(s.dockerWAFErrorPagesApp))
	mux.HandleFunc("POST /api/docker/containers/{id}/waf/install", s.auth(s.dockerWAFInstallContainer))
	mux.HandleFunc("POST /api/docker/containers/{id}/waf/uninstall", s.auth(s.dockerWAFUninstallContainer))
	mux.HandleFunc("POST /api/docker/containers/{id}/waf", s.auth(s.dockerWAFCategoryContainer))
	mux.HandleFunc("PUT /api/docker/containers/{id}/waf/error-pages", s.auth(s.dockerWAFErrorPagesContainer))
	mux.HandleFunc("GET /api/docker/backups", s.auth(s.dockerListBackups))
	mux.HandleFunc("POST /api/docker/backups", s.auth(s.dockerCreateBackup))
	mux.HandleFunc("POST /api/docker/backups/{id}/restore", s.auth(s.dockerRestoreBackup))
	mux.HandleFunc("DELETE /api/docker/backups/{id}", s.auth(s.dockerDeleteBackup))
	mux.HandleFunc("GET /api/docker/registries", s.auth(s.dockerListRegistries))
	mux.HandleFunc("POST /api/docker/registries", s.auth(s.dockerCreateRegistry))
	mux.HandleFunc("DELETE /api/docker/registries/{id}", s.auth(s.dockerDeleteRegistry))
	mux.HandleFunc("POST /api/docker/settings", s.auth(s.dockerSaveSettings))
	mux.HandleFunc("POST /hooks/docker/{id}", s.dockerWebhook)
}

func (s *Server) dockerWorkerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/docker/jobs/{id}", s.fetchDockerJob)
}

func canAccessDocker(r *http.Request, owner string) bool {
	return canAccessApplication(r, owner)
}

func (s *Server) queueDockerJob(workerID, owner, action string, op dock.Operation) (dockerJob, error) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	id := dockerRandID("dcj")
	op.JobID = id
	j := dockerJob{ID: id, Owner: owner, WorkerID: workerID, Action: action, State: "QUEUED", Created: time.Now().UTC(), Updated: time.Now().UTC(), Op: op}
	raw, _ := json.Marshal(op)
	j.OpRaw = raw
	d.Jobs[id] = j
	if err := d.save(); err != nil {
		delete(d.Jobs, id)
		return dockerJob{}, err
	}
	if err := s.Store.QueueOp(workerID, proto.Operation{ID: id, Action: "docker-job", DockerJobID: id}); err != nil {
		delete(d.Jobs, id)
		_ = d.save()
		return dockerJob{}, err
	}
	return j, nil
}

func (s *Server) fetchDockerJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.Jobs[id]
	if !ok {
		fail(w, 404, "job not found")
		return
	}
	writeJSON(w, 200, j.Op)
}

func (s *Server) dockerHeartbeat(workerID string, resultsRaw, statusRaw json.RawMessage) ([]proto.Operation, []string) {
	d := s.docker
	if d == nil {
		return nil, nil
	}
	type redeployHint struct{ VolumeID, Owner string }
	var redeploys []redeployHint
	d.mu.Lock()
	if len(statusRaw) > 0 {
		var st dock.Status
		if json.Unmarshal(statusRaw, &st) == nil {
			d.WorkerStatus[workerID] = st
			for _, c := range st.Containers {
				for id, saved := range d.Containers {
					if saved.WorkerID == workerID && ("clofix-"+saved.ID == c.Name || strings.Contains(c.Name, saved.ID)) {
						saved.State = c.State
						saved.Runtime = &c
						saved.Updated = time.Now().UTC()
						d.Containers[id] = saved
					}
				}
			}
		}
	}
	ack := []string{}
	if len(resultsRaw) > 0 {
		var results []dock.Result
		if json.Unmarshal(resultsRaw, &results) == nil {
			for _, res := range results {
				j, ok := d.Jobs[res.JobID]
				if !ok {
					continue
				}
				j.State, j.Progress, j.Error, j.Logs = res.State, res.Progress, res.Error, res.Logs
				j.ResultData = res.Data
				j.Updated = time.Now().UTC()
				if res.State == "SUCCEEDED" || res.State == "FAILED" || res.State == "CANCELLED" {
					j.Finished = time.Now().UTC()
					if vol, owner := s.applyDockerJobResultLocked(j, res); vol != "" {
						redeploys = append(redeploys, redeployHint{VolumeID: vol, Owner: owner})
					}
					ack = append(ack, res.JobID)
				}
				d.Jobs[res.JobID] = j
			}
		}
	}
	_ = d.save()
	ops := []proto.Operation{}
	for _, j := range d.Jobs {
		if j.WorkerID == workerID && (j.State == "QUEUED" || j.State == "RUNNING") {
			ops = append(ops, proto.Operation{ID: j.ID, Action: "docker-job", DockerJobID: j.ID})
		}
	}
	d.mu.Unlock()
	for _, hint := range redeploys {
		_ = s.dockerRedeployAfterVolumeRestore(hint.VolumeID, hint.Owner)
	}
	return ops, ack
}

// applyDockerJobResultLocked updates docker store state for a finished job.
// Caller must hold d.mu. Returns volumeID/owner when a post-restore redeploy should run after unlock.
func (s *Server) applyDockerJobResultLocked(j dockerJob, res dock.Result) (redeployVolumeID, redeployOwner string) {
	d := s.docker
	switch {
	case j.Action == "container-logs":
		// Logs jobs must not flip container/app lifecycle state.
		return "", ""
	case j.Action == "compose-logs":
		return "", ""
	case strings.HasPrefix(j.Action, "container-"):
		cid := ""
		if j.Op.Container != nil {
			cid = j.Op.Container.ID
		}
		for id, c := range d.Containers {
			if cid != "" && c.ID != cid {
				continue
			}
			if cid == "" {
				continue
			}
			if res.State == "SUCCEEDED" {
				if res.Container != nil {
					c.State = res.Container.State
					c.Runtime = res.Container
				} else if j.Action == "container-remove" {
					delete(d.Containers, id)
					return "", ""
				} else if j.Action == "container-stop" {
					c.State = "Stopped"
				} else if j.Action == "container-start" || j.Action == "container-create" || j.Action == "container-recreate" {
					c.State = "Running"
				}
				c.Error = ""
			} else {
				c.State, c.Error = "Failed", res.Error
			}
			c.Updated = time.Now().UTC()
			d.Containers[id] = c
			if c.AppID != "" {
				if a, ok := d.Applications[c.AppID]; ok {
					if res.State == "SUCCEEDED" {
						a.State = "Running"
						a.Error = ""
					} else {
						a.State, a.Error = "Failed", res.Error
					}
					a.Updated = time.Now().UTC()
					d.Applications[c.AppID] = a
				}
			}
		}
	case j.Action == "waf-stats":
		tid := j.Op.WAFTargetID
		s.applyWAFStatsJobResult(tid, j.Op.WAFMode, res.Data, res.State == "SUCCEEDED", res.Error)
	case j.Action == "waf-enable", j.Action == "waf-disable", j.Action == "waf-update":
		tid := j.Op.WAFTargetID
		if tid == "" && j.Op.Container != nil {
			tid = j.Op.Container.ID
		}
		if j.Op.WAFMode == "vps" {
			s.applyVPSWAFJobResult(j.Action, tid, res.Error, res.State == "SUCCEEDED")
			break
		}
		if c, ok := d.Containers[tid]; ok {
			if res.State == "SUCCEEDED" {
				switch j.Action {
				case "waf-enable":
					c.WAFState, c.WAFProxyName, c.Error = "INSTALLED", "clofix-waf-"+tid, ""
				case "waf-disable":
					c.WAFState, c.WAFProxyName, c.Error = "UNINSTALLED", "", ""
				case "waf-update":
					c.Error = ""
					if c.WAFState == "" {
						c.WAFState = "INSTALLED"
					}
				}
				if j.Op.WAFPolicy != nil {
					c.OWASPProtection = j.Op.WAFPolicy
				}
				if j.Op.WAFErrorPages != nil {
					c.WAFErrorPages = j.Op.WAFErrorPages
				}
			} else {
				c.Error = res.Error
				if j.Action == "waf-enable" {
					c.WAFState = "FAILED"
				} else if j.Action == "waf-disable" {
					c.WAFState = "INSTALLED"
				}
			}
			c.Updated = time.Now().UTC()
			d.Containers[tid] = c
			if c.AppID != "" {
				if a, ok := d.Applications[c.AppID]; ok {
					a.WAFState, a.WAFProxyName, a.OWASPProtection, a.WAFErrorPages = c.WAFState, c.WAFProxyName, c.OWASPProtection, c.WAFErrorPages
					a.Error, a.Updated = c.Error, c.Updated
					d.Applications[c.AppID] = a
				}
			}
		}
	case j.Action == "compose-deploy", j.Action == "git-build":
		composeID, imageHint := "", j.Op.Image
		if j.Op.Compose != nil {
			composeID = j.Op.Compose.ID
		}
		for id, a := range d.Applications {
			match := (composeID != "" && a.ComposeID == composeID) || (imageHint != "" && strings.Contains(imageHint, a.ID)) || (j.Action == "git-build" && strings.Contains(imageHint, a.ID))
			if !match {
				continue
			}
			if res.State == "SUCCEEDED" {
				a.State = "Running"
				a.Error = ""
				a.Events = append(a.Events, dockerEvent{At: time.Now().UTC(), State: "Running", Message: j.Action + " succeeded"})
				if j.Action == "git-build" && len(res.Data) > 0 {
					var data map[string]string
					_ = json.Unmarshal(res.Data, &data)
					if img := data["image"]; img != "" {
						a.Image = img
						a.Releases = append(a.Releases, dockerRelease{Number: len(a.Releases) + 1, Image: img, Created: time.Now().UTC()})
					}
				}
			} else {
				a.State, a.Error = "Failed", res.Error
				a.Events = append(a.Events, dockerEvent{At: time.Now().UTC(), State: "Failed", Message: res.Error})
			}
			a.Updated = time.Now().UTC()
			d.Applications[id] = a
		}
	case j.Action == "volume-backup":
		for id, b := range d.Backups {
			if b.ID == j.Op.BackupID {
				if res.State == "SUCCEEDED" {
					b.State, b.Error = "READY", ""
				} else {
					b.State, b.Error = "FAILED", res.Error
				}
				d.Backups[id] = b
			}
		}
	case j.Action == "volume-restore":
		for id, b := range d.Backups {
			if b.ID != j.Op.BackupID {
				continue
			}
			if res.State == "SUCCEEDED" {
				b.Error = ""
				if b.State == "RESTORING" {
					b.State = "READY"
				}
				if b.RedeployAfter {
					redeployVolumeID, redeployOwner = b.TargetID, b.Owner
					b.RedeployAfter = false
				}
			} else if b.State == "RESTORING" {
				b.State, b.Error, b.RedeployAfter = "READY", res.Error, false
			}
			d.Backups[id] = b
		}
	case j.Action == "volume-backup-delete":
		if res.State == "SUCCEEDED" {
			delete(d.Backups, j.Op.BackupID)
		} else if b, ok := d.Backups[j.Op.BackupID]; ok {
			b.State, b.Error = "READY", res.Error
			d.Backups[j.Op.BackupID] = b
		}
	}
	return redeployVolumeID, redeployOwner
}

func (s *Server) dockerOverview(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	workers, _ := s.Store.ListWorkers()
	ready := 0
	for _, wk := range workers {
		if wk.Status == "active" && time.Since(wk.LastHeartbeat) <= time.Minute && wk.Capacity.DockerReady {
			ready++
		}
	}
	containers, apps, failed := 0, 0, 0
	for _, c := range d.Containers {
		if canAccessDocker(r, c.Owner) {
			containers++
			if c.State == "Failed" || c.State == "Unhealthy" {
				failed++
			}
		}
	}
	for _, a := range d.Applications {
		if canAccessDocker(r, a.Owner) {
			apps++
		}
	}
	writeJSON(w, 200, map[string]any{
		"enabled": d.Enabled,
		"docker_ready_workers": ready,
		"containers": containers,
		"applications": apps,
		"failed": failed,
		"jobs": len(d.Jobs),
	})
}

func (s *Server) dockerWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 500, "cannot list workers")
		return
	}
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []map[string]any{}
	for _, wk := range workers {
		if wk.Status != "active" {
			continue
		}
		item := map[string]any{
			"id": wk.ID, "hostname": wk.Hostname, "docker_ready": wk.Capacity.DockerReady,
			"docker_version": wk.Capacity.DockerVersion, "docker_host": wk.Capacity.DockerHost,
			"ram_committed": wk.Capacity.RAMCommitted, "cpu_allocated": wk.Capacity.CPUAllocated,
			"last_heartbeat": wk.LastHeartbeat,
		}
		if st, ok := d.WorkerStatus[wk.ID]; ok {
			item["engine"] = st.Engine
			item["container_count"] = len(st.Containers)
			item["image_count"] = len(st.Images)
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}

func (s *Server) dockerEngineAction(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(r)
	if !ok || (p.Kind != "admin" && p.Role != "Full Administrator") {
		fail(w, 403, "platform admin required for Docker engine control")
		return
	}
	workerID := r.PathValue("id")
	action := r.PathValue("action")
	opAction := "engine-" + action
	switch action {
	case "install", "start", "stop", "restart", "upgrade", "status":
	default:
		fail(w, 400, "invalid engine action")
		return
	}
	if action == "status" {
		opAction = "engine-status"
	}
	owner := p.AccountID
	if owner == "" {
		owner = "acc-platform"
	}
	j, err := s.queueDockerJob(workerID, owner, opAction, dock.Operation{Action: opAction, EngineAction: action})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(p.UserID, "docker.engine."+action, workerID, "queued")
	writeJSON(w, 202, j)
}

func (s *Server) dockerPlace(ramMB int64, cpu float64, diskMB int64, preferred string) (string, error) {
	workers, err := s.Store.ListWorkers()
	if err != nil {
		return "", err
	}
	cands := []scheduler.Candidate{}
	now := time.Now()
	for _, wk := range workers {
		if wk.Status != "active" || now.Sub(wk.LastHeartbeat) > time.Minute || !wk.Capacity.DockerReady {
			continue
		}
		if preferred != "" && wk.ID != preferred {
			continue
		}
		cands = append(cands, scheduler.Candidate{ID: wk.ID, Active: true, LastHeartbeat: wk.LastHeartbeat, Capacity: wk.Capacity})
	}
	dec, err := scheduler.Place(cands, scheduler.Request{RAM: uint64(ramMB) << 20, CPU: cpu, Disk: uint64(diskMB) << 20}, s.Sched, now)
	if err != nil {
		return "", fmt.Errorf("no docker-ready worker with capacity: %w", err)
	}
	return dec.WorkerID, nil
}

func (s *Server) dockerListContainers(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerContainer{}
	for _, c := range d.Containers {
		if canAccessDocker(r, c.Owner) {
			items = append(items, c)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateContainer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string            `json:"name"`
		Image      string            `json:"image"`
		Tag        string            `json:"tag"`
		Digest     string            `json:"digest"`
		WorkerID   string            `json:"worker_id"`
		RAMMB      int64             `json:"ram_mb"`
		CPU        float64           `json:"cpu"`
		Env        map[string]string `json:"env"`
		Ports      []dock.PortPublish `json:"ports"`
		RestartPolicy string         `json:"restart_policy"`
		ReadOnlyRoot bool            `json:"read_only_root"`
		User       string            `json:"user"`
		Command    []string          `json:"command"`
		Volumes    []dock.VolumeMount `json:"volumes"`
		Networks   []string          `json:"networks"`
		Domain     string            `json:"domain"`
		Health     *dock.HealthCheck `json:"health"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	if in.RAMMB == 0 {
		in.RAMMB = 256
	}
	if in.CPU == 0 {
		in.CPU = 0.5
	}
	owner := accountID(r)
	workerID, err := s.dockerPlace(in.RAMMB, in.CPU, 1024, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	if err := s.checkPortConflicts(workerID, in.Ports, ""); err != nil {
		fail(w, 409, err.Error())
		return
	}
	id := dockerRandID("dck")
	spec := dock.ContainerSpec{
		ID: id, Name: in.Name, Image: in.Image, Tag: in.Tag, Digest: in.Digest, Owner: owner,
		Env: in.Env, Ports: in.Ports, RestartPolicy: in.RestartPolicy, ReadOnlyRoot: in.ReadOnlyRoot,
		User: in.User, Command: in.Command, Volumes: in.Volumes, Networks: in.Networks,
		Domain: in.Domain, Health: in.Health, RAMMB: in.RAMMB, CPU: in.CPU,
	}
	if err := spec.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	c := dockerContainer{ID: id, Owner: owner, Name: in.Name, WorkerID: workerID, State: "Creating", Desired: "running", Created: time.Now().UTC(), Updated: time.Now().UTC(), Spec: spec}
	d := s.docker
	d.mu.Lock()
	d.Containers[id] = c
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(workerID, owner, "container-create", dock.Operation{Action: "container-create", Container: &spec})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.container.create", id, "queued")
	writeJSON(w, 202, map[string]any{"container": c, "job": j})
}

func (s *Server) checkPortConflicts(workerID string, ports []dock.PortPublish, excludeContainerID string) error {
	used := map[int]bool{}
	// FileVPS published ports
	instances, _ := s.Store.ListInstances()
	for _, inst := range instances {
		if inst.WorkerID != workerID {
			continue
		}
		if p := inst.Spec.SSHPort; p > 0 {
			used[p] = true
			used[p+5000] = true
			used[p+10000] = true
		}
		for _, m := range inst.Spec.PortMappings {
			used[m.HostPort] = true
		}
	}
	// Local NLB / compose publish on this controller host (lab)
	used[18080] = true
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.Containers {
		if c.WorkerID != workerID || c.ID == excludeContainerID {
			continue
		}
		for _, p := range c.Spec.Ports {
			if p.HostPort > 0 {
				used[p.HostPort] = true
			}
		}
	}
	for _, p := range ports {
		if p.HostPort > 0 && used[p.HostPort] {
			return fmt.Errorf("host port %d already in use on worker", p.HostPort)
		}
	}
	return nil
}

func (s *Server) dockerGetContainer(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.Containers[r.PathValue("id")]
	if !ok || !canAccessDocker(r, c.Owner) {
		fail(w, 404, "container not found")
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) dockerUpdateContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, c.Owner) {
		fail(w, 404, "container not found")
		return
	}
	var in struct {
		Name          string             `json:"name"`
		Image         string             `json:"image"`
		Tag           string             `json:"tag"`
		Digest        string             `json:"digest"`
		Env           map[string]string  `json:"env"`
		Ports         []dock.PortPublish `json:"ports"`
		RAMMB         int64              `json:"ram_mb"`
		CPU           float64            `json:"cpu"`
		RestartPolicy string             `json:"restart_policy"`
		ReadOnlyRoot  bool               `json:"read_only_root"`
		User          string             `json:"user"`
		Command       []string           `json:"command"`
		Domain        string             `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	spec := c.Spec
	if in.Name != "" {
		spec.Name = in.Name
		c.Name = in.Name
	}
	if in.Image != "" {
		spec.Image = in.Image
	}
	if in.Tag != "" {
		spec.Tag = in.Tag
	}
	if in.Digest != "" {
		spec.Digest = in.Digest
	}
	if in.Env != nil {
		spec.Env = in.Env
	}
	if in.Ports != nil {
		spec.Ports = in.Ports
	}
	if in.RAMMB > 0 {
		spec.RAMMB = in.RAMMB
	}
	if in.CPU > 0 {
		spec.CPU = in.CPU
	}
	if in.RestartPolicy != "" {
		spec.RestartPolicy = in.RestartPolicy
	}
	spec.ReadOnlyRoot = in.ReadOnlyRoot
	if in.User != "" {
		spec.User = in.User
	}
	if in.Command != nil {
		spec.Command = in.Command
	}
	spec.Domain = in.Domain
	if err := spec.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.checkPortConflicts(c.WorkerID, spec.Ports, c.ID); err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.Spec = spec
	c.State = "Updating"
	c.Desired = "running"
	c.Updated = time.Now().UTC()
	c.Error = ""
	d.mu.Lock()
	d.Containers[id] = c
	if c.AppID != "" {
		if a, ok := d.Applications[c.AppID]; ok {
			a.Spec = spec
			a.Name = c.Name
			a.Image = spec.Image
			a.Updated = c.Updated
			a.State = "Updating"
			a.Events = append(a.Events, dockerEvent{At: c.Updated, State: "Updating", Message: "Container edit queued"})
			d.Applications[c.AppID] = a
		}
	}
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(c.WorkerID, c.Owner, "container-recreate", dock.Operation{Action: "container-recreate", Container: &spec})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.container.update", id, "queued")
	writeJSON(w, 202, map[string]any{"container": c, "job": j})
}

func (s *Server) dockerControlContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, c.Owner) {
		fail(w, 404, "container not found")
		return
	}
	opAction := ""
	switch action {
	case "start":
		opAction = "container-start"
	case "stop":
		opAction = "container-stop"
	case "restart":
		opAction = "container-restart"
	case "pause":
		opAction = "container-pause"
	case "unpause":
		opAction = "container-unpause"
	case "recreate":
		opAction = "container-recreate"
	case "inspect":
		opAction = "container-inspect"
	case "stats":
		opAction = "container-stats"
	case "exec":
		var in struct{ Command []string `json:"command"` }
		_ = json.NewDecoder(r.Body).Decode(&in)
		j, err := s.queueDockerJob(c.WorkerID, c.Owner, "container-exec", dock.Operation{Action: "container-exec", Container: &c.Spec, ExecCommand: in.Command})
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 202, j)
		return
	default:
		fail(w, 400, "invalid action")
		return
	}
	j, err := s.queueDockerJob(c.WorkerID, c.Owner, opAction, dock.Operation{Action: opAction, Container: &c.Spec})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.container."+action, id, "queued")
	writeJSON(w, 202, j)
}

func (s *Server) dockerDeleteContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, c.Owner) {
		fail(w, 404, "container not found")
		return
	}
	j, err := s.queueDockerJob(c.WorkerID, c.Owner, "container-remove", dock.Operation{Action: "container-remove", Container: &c.Spec})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.container.delete", id, "queued")
	writeJSON(w, 202, j)
}

func (s *Server) dockerContainerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, c.Owner) {
		fail(w, 404, "container not found")
		return
	}
	spec := c.Spec
	if spec.ID == "" {
		spec.ID = c.ID
	}
	j, err := s.queueDockerJob(c.WorkerID, c.Owner, "container-logs", dock.Operation{Action: "container-logs", Container: &spec, LogTail: 500})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerListImages(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	images := []dock.ImageInfo{}
	seen := map[string]bool{}
	for _, st := range d.WorkerStatus {
		for _, img := range st.Images {
			key := img.Repository + ":" + img.Tag + "@" + img.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			images = append(images, img)
		}
	}
	writeJSON(w, 200, images)
}

func (s *Server) dockerPullImage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image    string `json:"image"`
		WorkerID string `json:"worker_id"`
		Registry string `json:"registry_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Image == "" {
		fail(w, 400, "image required")
		return
	}
	workerID, err := s.dockerPlace(128, 0.25, 512, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	var reg *dock.RegistryAuth
	if in.Registry != "" {
		d := s.docker
		d.mu.Lock()
		rr, ok := d.Registries[in.Registry]
		d.mu.Unlock()
		if ok && canAccessDocker(r, rr.Owner) {
			reg = &dock.RegistryAuth{Server: rr.Server, Username: rr.Username, Password: rr.Password}
		}
	}
	j, err := s.queueDockerJob(workerID, accountID(r), "image-pull", dock.Operation{Action: "image-pull", Image: in.Image, Registry: reg})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.image.pull", in.Image, "queued")
	writeJSON(w, 202, j)
}

func (s *Server) dockerRemoveImage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image    string `json:"image"`
		WorkerID string `json:"worker_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Image == "" {
		fail(w, 400, "image required")
		return
	}
	workerID, err := s.dockerPlace(64, 0.1, 0, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	j, err := s.queueDockerJob(workerID, accountID(r), "image-remove", dock.Operation{Action: "image-remove", Image: in.Image})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerPruneImages(w http.ResponseWriter, r *http.Request) {
	var in struct{ WorkerID string `json:"worker_id"` }
	_ = json.NewDecoder(r.Body).Decode(&in)
	workerID, err := s.dockerPlace(64, 0.1, 0, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	j, err := s.queueDockerJob(workerID, accountID(r), "image-prune", dock.Operation{Action: "image-prune"})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

var dockerQuickTemplates = []map[string]any{
	{"id": "nginx", "name": "Nginx", "image": "nginx", "tag": "1.27-alpine", "ram_mb": 128, "cpu": 0.25, "ports": []map[string]any{{"container_port": 80}}},
	{"id": "apache", "name": "Apache HTTP Server", "image": "httpd", "tag": "2.4-alpine", "ram_mb": 256, "cpu": 0.5, "ports": []map[string]any{{"container_port": 80}}},
	{"id": "wordpress", "name": "WordPress", "image": "wordpress", "tag": "6-apache", "ram_mb": 512, "cpu": 1, "ports": []map[string]any{{"container_port": 80}}, "needs_volume": true},
	{"id": "node", "name": "Node.js", "image": "node", "tag": "20-alpine", "ram_mb": 256, "cpu": 0.5, "command": []string{"node", "-e", "require('http').createServer((q,s)=>s.end('ok')).listen(3000)"}, "ports": []map[string]any{{"container_port": 3000}}},
	{"id": "python", "name": "Python", "image": "python", "tag": "3.12-alpine", "ram_mb": 256, "cpu": 0.5, "command": []string{"python", "-m", "http.server", "8000"}, "ports": []map[string]any{{"container_port": 8000}}},
	{"id": "php", "name": "PHP", "image": "php", "tag": "8.3-apache", "ram_mb": 256, "cpu": 0.5, "ports": []map[string]any{{"container_port": 80}}},
	{"id": "redis", "name": "Redis", "image": "redis", "tag": "7.4-alpine", "ram_mb": 256, "cpu": 0.5, "ports": []map[string]any{{"container_port": 6379}}, "needs_volume": true},
	{"id": "postgres", "name": "PostgreSQL", "image": "postgres", "tag": "16-alpine", "ram_mb": 512, "cpu": 1, "ports": []map[string]any{{"container_port": 5432}}, "needs_volume": true, "env": map[string]string{"POSTGRES_PASSWORD": "change-me"}},
	{"id": "mysql", "name": "MySQL", "image": "mysql", "tag": "8.4", "ram_mb": 512, "cpu": 1, "ports": []map[string]any{{"container_port": 3306}}, "needs_volume": true, "env": map[string]string{"MYSQL_ROOT_PASSWORD": "change-me"}},
	{"id": "mongo", "name": "MongoDB", "image": "mongo", "tag": "7", "ram_mb": 512, "cpu": 1, "ports": []map[string]any{{"container_port": 27017}}, "needs_volume": true},
	{"id": "rabbitmq", "name": "RabbitMQ", "image": "rabbitmq", "tag": "3.13-management-alpine", "ram_mb": 512, "cpu": 1, "ports": []map[string]any{{"container_port": 5672}, {"container_port": 15672}}, "needs_volume": true},
	{"id": "custom", "name": "Custom Docker image", "image": "", "tag": "latest", "ram_mb": 256, "cpu": 0.5},
}

func (s *Server) dockerTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, dockerQuickTemplates)
}

func (s *Server) dockerListApps(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerApplication{}
	for _, a := range d.Applications {
		if canAccessDocker(r, a.Owner) {
			cp := a
			cp.WebhookSecret = ""
			items = append(items, cp)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		Method    string `json:"method"`
		Template  string `json:"template"`
		WorkerID  string `json:"worker_id"`
		Image     string `json:"image"`
		Tag       string `json:"tag"`
		RAMMB     int64  `json:"ram_mb"`
		CPU       float64 `json:"cpu"`
		Env       map[string]string `json:"env"`
		Ports     []dock.PortPublish `json:"ports"`
		ComposeYAML string `json:"compose_yaml"`
		GitURL    string `json:"git_url"`
		GitBranch string `json:"git_branch"`
		GitToken  string `json:"git_token"`
		Domain    string `json:"domain"`
		RestartPolicy string `json:"restart_policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	if in.Method == "" {
		in.Method = "image"
	}
	if in.RAMMB == 0 {
		in.RAMMB = 256
	}
	if in.CPU == 0 {
		in.CPU = 0.5
	}
	if in.Method == "quickapp" && in.Template != "" {
		for _, t := range dockerQuickTemplates {
			if t["id"] == in.Template {
				if in.Image == "" {
					in.Image, _ = t["image"].(string)
				}
				if in.Tag == "" {
					in.Tag, _ = t["tag"].(string)
				}
				if env, ok := t["env"].(map[string]string); ok && in.Env == nil {
					in.Env = env
				}
				break
			}
		}
		in.Method = "image"
	}
	owner := accountID(r)
	workerID, err := s.dockerPlace(in.RAMMB, in.CPU, 2048, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	appID := dockerRandID("dca")
	app := dockerApplication{
		ID: appID, Owner: owner, Name: in.Name, Method: in.Method, Template: in.Template, WorkerID: workerID,
		State: "Pending", Created: time.Now().UTC(), Updated: time.Now().UTC(), Domain: in.Domain,
		GitURL: in.GitURL, GitBranch: in.GitBranch, ComposeYAML: in.ComposeYAML, Image: in.Image,
		Events: []dockerEvent{{At: time.Now().UTC(), State: "Pending", Message: "Deployment accepted"}},
	}
	var job dockerJob
	switch in.Method {
	case "compose":
		if err := validateComposePolicy(in.ComposeYAML); err != nil {
			fail(w, 400, err.Error())
			return
		}
		cid := dockerRandID("dcp")
		app.ComposeID = cid
		app.State = "Validating"
		app.Events = append(app.Events, dockerEvent{At: time.Now().UTC(), State: "Scheduling", Message: "Compose deploy queued"})
		d := s.docker
		d.mu.Lock()
		d.Applications[appID] = app
		_ = d.save()
		d.mu.Unlock()
		job, err = s.queueDockerJob(workerID, owner, "compose-deploy", dock.Operation{Action: "compose-deploy", Compose: &dock.ComposeSpec{ID: cid, Name: in.Name, Owner: owner, YAML: in.ComposeYAML, Project: "clofix-" + cid}})
	case "git":
		app.State = "Pulling/Building"
		d := s.docker
		d.mu.Lock()
		d.Applications[appID] = app
		_ = d.save()
		d.mu.Unlock()
		tag := "clofix-build/" + appID + ":latest"
		job, err = s.queueDockerJob(workerID, owner, "git-build", dock.Operation{Action: "git-build", GitURL: in.GitURL, GitBranch: in.GitBranch, GitToken: in.GitToken, Image: tag})
	default:
		if in.Image == "" {
			fail(w, 400, "image required")
			return
		}
		cid := dockerRandID("dck")
		spec := dock.ContainerSpec{ID: cid, Name: in.Name, Image: in.Image, Tag: in.Tag, Owner: owner, Env: in.Env, Ports: in.Ports, RAMMB: in.RAMMB, CPU: in.CPU, RestartPolicy: in.RestartPolicy, Domain: in.Domain}
		if err := spec.Validate(); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := s.checkPortConflicts(workerID, in.Ports, ""); err != nil {
			fail(w, 409, err.Error())
			return
		}
		app.ContainerIDs = []string{cid}
		app.Spec = spec
		app.State = "Creating"
		app.Events = append(app.Events, dockerEvent{At: time.Now().UTC(), State: "Creating", Message: "Container create queued"})
		c := dockerContainer{ID: cid, Owner: owner, Name: in.Name, WorkerID: workerID, State: "Creating", Desired: "running", Created: time.Now().UTC(), Updated: time.Now().UTC(), Spec: spec, AppID: appID}
		d := s.docker
		d.mu.Lock()
		d.Applications[appID] = app
		d.Containers[cid] = c
		_ = d.save()
		d.mu.Unlock()
		job, err = s.queueDockerJob(workerID, owner, "container-create", dock.Operation{Action: "container-create", Container: &spec})
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.application.create", appID, "queued")
	writeJSON(w, 202, map[string]any{"application": app, "job": job})
}

func validateComposePolicy(yaml string) error {
	lower := strings.ToLower(yaml)
	for _, bad := range []string{"privileged: true", "privileged:true", "network_mode: host", "network_mode:host", "/var/run/docker.sock", "pid: host"} {
		if strings.Contains(lower, bad) {
			return fmt.Errorf("compose rejected by security policy (%s)", bad)
		}
	}
	if strings.TrimSpace(yaml) == "" {
		return fmt.Errorf("compose yaml required")
	}
	return nil
}

func (s *Server) dockerGetApp(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.Applications[r.PathValue("id")]
	if !ok || !canAccessDocker(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	a.WebhookSecret = ""
	writeJSON(w, 200, a)
}

func (s *Server) dockerUpdateApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	var in struct {
		Name        string             `json:"name"`
		ComposeYAML string             `json:"compose_yaml"`
		Image       string             `json:"image"`
		Tag         string             `json:"tag"`
		Env         map[string]string  `json:"env"`
		Ports       []dock.PortPublish `json:"ports"`
		RAMMB       int64              `json:"ram_mb"`
		CPU         float64            `json:"cpu"`
		Domain      string             `json:"domain"`
		GitURL      string             `json:"git_url"`
		GitBranch   string             `json:"git_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	if in.Name != "" {
		a.Name = in.Name
	}
	a.Domain = in.Domain
	now := time.Now().UTC()
	var j dockerJob
	var err error
	switch a.Method {
	case "compose":
		yaml := in.ComposeYAML
		if yaml == "" {
			yaml = a.ComposeYAML
		}
		if err := validateComposePolicy(yaml); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.ComposeYAML = yaml
		a.State = "Updating"
		a.Updated = now
		a.Events = append(a.Events, dockerEvent{At: now, State: "Updating", Message: "Compose edit queued"})
		d.mu.Lock()
		d.Applications[id] = a
		_ = d.save()
		d.mu.Unlock()
		j, err = s.queueDockerJob(a.WorkerID, a.Owner, "compose-deploy", dock.Operation{Action: "compose-deploy", Compose: &dock.ComposeSpec{ID: a.ComposeID, Name: a.Name, Owner: a.Owner, YAML: a.ComposeYAML, Project: "clofix-" + a.ComposeID}})
	default:
		spec := a.Spec
		if spec.ID == "" && len(a.ContainerIDs) > 0 {
			d.mu.Lock()
			if c, ok := d.Containers[a.ContainerIDs[0]]; ok {
				spec = c.Spec
			}
			d.mu.Unlock()
		}
		if in.Image != "" {
			spec.Image = in.Image
			a.Image = in.Image
		}
		if in.Tag != "" {
			spec.Tag = in.Tag
		}
		if in.Env != nil {
			spec.Env = in.Env
		}
		if in.Ports != nil {
			spec.Ports = in.Ports
		}
		if in.RAMMB > 0 {
			spec.RAMMB = in.RAMMB
		}
		if in.CPU > 0 {
			spec.CPU = in.CPU
		}
		spec.Domain = a.Domain
		spec.Name = a.Name
		if err := spec.Validate(); err != nil {
			fail(w, 400, err.Error())
			return
		}
		exclude := ""
		if len(a.ContainerIDs) > 0 {
			exclude = a.ContainerIDs[0]
		}
		if err := s.checkPortConflicts(a.WorkerID, spec.Ports, exclude); err != nil {
			fail(w, 409, err.Error())
			return
		}
		if in.GitURL != "" {
			a.GitURL = in.GitURL
		}
		if in.GitBranch != "" {
			a.GitBranch = in.GitBranch
		}
		a.Spec = spec
		a.State = "Updating"
		a.Updated = now
		a.Events = append(a.Events, dockerEvent{At: now, State: "Updating", Message: "Application edit queued"})
		d.mu.Lock()
		d.Applications[id] = a
		if exclude != "" {
			if c, ok := d.Containers[exclude]; ok {
				c.Spec = spec
				c.Name = a.Name
				c.State = "Updating"
				c.Updated = now
				d.Containers[exclude] = c
			}
		}
		_ = d.save()
		d.mu.Unlock()
		if a.Method == "git" && in.GitURL != "" {
			j, err = s.queueDockerJob(a.WorkerID, a.Owner, "git-build", dock.Operation{Action: "git-build", GitURL: a.GitURL, GitBranch: a.GitBranch, Image: "clofix-build/" + a.ID + ":latest"})
		} else {
			j, err = s.queueDockerJob(a.WorkerID, a.Owner, "container-recreate", dock.Operation{Action: "container-recreate", Container: &spec})
		}
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.application.update", id, "queued")
	a.WebhookSecret = ""
	writeJSON(w, 202, map[string]any{"application": a, "job": j})
}

func (s *Server) dockerRedeployApp(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[r.PathValue("id")]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	var j dockerJob
	var err error
	switch a.Method {
	case "compose":
		j, err = s.queueDockerJob(a.WorkerID, a.Owner, "compose-deploy", dock.Operation{Action: "compose-deploy", Compose: &dock.ComposeSpec{ID: a.ComposeID, Name: a.Name, Owner: a.Owner, YAML: a.ComposeYAML, Project: "clofix-" + a.ComposeID}})
	default:
		if a.Spec.ID == "" {
			fail(w, 400, "no container spec")
			return
		}
		j, err = s.queueDockerJob(a.WorkerID, a.Owner, "container-recreate", dock.Operation{Action: "container-recreate", Container: &a.Spec})
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerRollbackApp(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[r.PathValue("id")]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	n := 0
	fmt.Sscanf(r.PathValue("n"), "%d", &n)
	var rel *dockerRelease
	for i := range a.Releases {
		if a.Releases[i].Number == n {
			rel = &a.Releases[i]
			break
		}
	}
	if rel == nil {
		d.mu.Unlock()
		fail(w, 404, "release not found")
		return
	}
	a.Spec.Image = rel.Image
	a.Image = rel.Image
	a.State = "Creating"
	d.Applications[a.ID] = a
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(a.WorkerID, a.Owner, "container-recreate", dock.Operation{Action: "container-recreate", Container: &a.Spec})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	delete(d.Applications, id)
	_ = d.save()
	d.mu.Unlock()
	if a.Method == "compose" && a.ComposeID != "" {
		_, _ = s.queueDockerJob(a.WorkerID, a.Owner, "compose-remove", dock.Operation{Action: "compose-remove", Compose: &dock.ComposeSpec{ID: a.ComposeID, YAML: a.ComposeYAML}})
	}
	for _, cid := range a.ContainerIDs {
		if c, ok := s.docker.Containers[cid]; ok {
			_, _ = s.queueDockerJob(c.WorkerID, c.Owner, "container-remove", dock.Operation{Action: "container-remove", Container: &c.Spec})
		}
	}
	s.Store.Audit(actorID(r), "docker.application.delete", id, "ok")
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) dockerListCompose(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerApplication{}
	for _, a := range d.Applications {
		if a.Method == "compose" && canAccessDocker(r, a.Owner) {
			items = append(items, a)
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) dockerDeployCompose(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var in struct {
		Name     string  `json:"name"`
		YAML     string  `json:"yaml"`
		WorkerID string  `json:"worker_id"`
		RAMMB    int64   `json:"ram_mb"`
		CPU      float64 `json:"cpu"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	// Placement reservation only — Compose services size themselves. Keep defaults low so
	// lab workers with FileVPS RAM committed can still schedule Docker projects.
	if in.RAMMB <= 0 {
		in.RAMMB = 128
	}
	if in.CPU <= 0 {
		in.CPU = 0.25
	}
	body, _ := json.Marshal(map[string]any{"name": in.Name, "method": "compose", "compose_yaml": in.YAML, "worker_id": in.WorkerID, "ram_mb": in.RAMMB, "cpu": in.CPU})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/docker/applications", strings.NewReader(string(body)))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	s.dockerCreateApp(w, req)
}

func (s *Server) dockerComposeAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	d.mu.Unlock()
	if !ok || a.Method != "compose" || !canAccessDocker(r, a.Owner) {
		fail(w, 404, "compose project not found")
		return
	}
	op := "compose-" + action
	switch action {
	case "start", "stop", "restart", "remove", "logs":
	case "scale":
		var in struct {
			Service string `json:"service"`
			Count   int    `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		j, err := s.queueDockerJob(a.WorkerID, a.Owner, "compose-scale", dock.Operation{Action: "compose-scale", Compose: &dock.ComposeSpec{ID: a.ComposeID, YAML: a.ComposeYAML}, ServiceName: in.Service, Scale: in.Count})
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 202, j)
		return
	default:
		fail(w, 400, "invalid action")
		return
	}
	j, err := s.queueDockerJob(a.WorkerID, a.Owner, op, dock.Operation{Action: op, Compose: &dock.ComposeSpec{ID: a.ComposeID, Name: a.Name, Owner: a.Owner, YAML: a.ComposeYAML, Project: "clofix-" + a.ComposeID}})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerListNetworks(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerNetwork{}
	for _, n := range d.Networks {
		if canAccessDocker(r, n.Owner) {
			items = append(items, n)
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateNetwork(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		WorkerID string `json:"worker_id"`
		Internal bool   `json:"internal"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	workerID, err := s.dockerPlace(32, 0.1, 0, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	id := dockerRandID("dcn")
	n := dockerNetwork{ID: id, Owner: accountID(r), Name: in.Name, WorkerID: workerID, Internal: in.Internal, Created: time.Now().UTC()}
	d := s.docker
	d.mu.Lock()
	d.Networks[id] = n
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(workerID, n.Owner, "network-create", dock.Operation{Action: "network-create", Network: &dock.NetworkSpec{ID: id, Name: in.Name, Internal: in.Internal, Owner: n.Owner}})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"network": n, "job": j})
}

func (s *Server) dockerDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	n, ok := d.Networks[r.PathValue("id")]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, n.Owner) {
		fail(w, 404, "network not found")
		return
	}
	j, err := s.queueDockerJob(n.WorkerID, n.Owner, "network-remove", dock.Operation{Action: "network-remove", Network: &dock.NetworkSpec{ID: n.ID}})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	d.mu.Lock()
	delete(d.Networks, n.ID)
	_ = d.save()
	d.mu.Unlock()
	writeJSON(w, 202, j)
}

func (s *Server) dockerListVolumes(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerVolume{}
	for _, v := range d.Volumes {
		if canAccessDocker(r, v.Owner) {
			items = append(items, v)
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateVolume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name             string `json:"name"`
		WorkerID         string `json:"worker_id"`
		DeleteProtection bool   `json:"delete_protection"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	workerID, err := s.dockerPlace(32, 0.1, 1024, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	id := dockerRandID("dcv")
	v := dockerVolume{ID: id, Owner: accountID(r), Name: in.Name, WorkerID: workerID, DeleteProtection: in.DeleteProtection, Created: time.Now().UTC()}
	d := s.docker
	d.mu.Lock()
	d.Volumes[id] = v
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(workerID, v.Owner, "volume-create", dock.Operation{Action: "volume-create", Volume: &dock.VolumeSpec{ID: id, Name: in.Name, Owner: v.Owner, DeleteProtection: in.DeleteProtection}})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"volume": v, "job": j})
}

func (s *Server) dockerDeleteVolume(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	v, ok := d.Volumes[r.PathValue("id")]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, v.Owner) {
		fail(w, 404, "volume not found")
		return
	}
	if v.DeleteProtection {
		fail(w, 409, "volume has delete protection enabled")
		return
	}
	j, err := s.queueDockerJob(v.WorkerID, v.Owner, "volume-remove", dock.Operation{Action: "volume-remove", Volume: &dock.VolumeSpec{ID: v.ID}})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	d.mu.Lock()
	delete(d.Volumes, v.ID)
	_ = d.save()
	d.mu.Unlock()
	writeJSON(w, 202, j)
}

func (s *Server) dockerListDeployments(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerJob{}
	for _, j := range d.Jobs {
		if canAccessDocker(r, j.Owner) {
			cp := j
			cp.Op = dock.Operation{}
			items = append(items, cp)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	writeJSON(w, 200, items)
}

func (s *Server) dockerGetDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.Jobs[id]
	if !ok || !canAccessDocker(r, j.Owner) {
		fail(w, 404, "job not found")
		return
	}
	cp := j
	cp.Op = dock.Operation{}
	writeJSON(w, 200, cp)
}

func (s *Server) dockerMetrics(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	writeJSON(w, 200, map[string]any{"workers": d.WorkerStatus, "containers": len(d.Containers), "applications": len(d.Applications)})
}

func (s *Server) dockerSecurity(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	targets := []map[string]any{}
	for _, a := range d.Applications {
		if !canAccessDocker(r, a.Owner) {
			continue
		}
		if a.Method == "compose" {
			continue // WAF edge attaches to a single image-backed container/app
		}
		targets = append(targets, map[string]any{
			"kind": "application", "id": a.ID, "name": a.Name, "worker_id": a.WorkerID,
			"state": a.State, "domain": a.Domain, "method": a.Method,
			"waf_state": a.WAFState, "owasp_protection": a.OWASPProtection,
			"waf_error_pages": a.WAFErrorPages, "ports": a.Spec.Ports,
			"container_ids": a.ContainerIDs,
		})
	}
	for _, c := range d.Containers {
		if !canAccessDocker(r, c.Owner) || c.AppID != "" {
			continue
		}
		targets = append(targets, map[string]any{
			"kind": "container", "id": c.ID, "name": c.Name, "worker_id": c.WorkerID,
			"state": c.State, "domain": c.Spec.Domain,
			"waf_state": c.WAFState, "owasp_protection": c.OWASPProtection,
			"waf_error_pages": c.WAFErrorPages, "ports": c.Spec.Ports,
		})
	}
	writeJSON(w, 200, map[string]any{
		"defaults": map[string]any{
			"no_privileged": true, "no_host_network": true, "no_docker_socket": true,
			"cap_drop_all": true, "no_new_privileges": true, "tenant_isolation": true,
		},
		"waf": map[string]any{
			"engine": "OWASP CRS · nginx ModSecurity edge proxy",
			"targets": targets,
		},
		"events":  d.SecurityEvents,
		"scanner": "trivy (optional on worker)",
	})
}

func dockerDefaultWAFPolicy() map[string]bool {
	out := map[string]bool{}
	for cat := range wafCategories {
		out[cat] = true
	}
	return out
}

func (s *Server) dockerWAFPortsFromSpec(spec dock.ContainerSpec) ([]dock.PortPublish, int, error) {
	var ports []dock.PortPublish
	backendPort := 80
	for _, p := range spec.Ports {
		if p.ContainerPort <= 0 {
			continue
		}
		if backendPort == 80 || p.ContainerPort == 80 || p.ContainerPort == 8080 || p.ContainerPort == 443 {
			backendPort = p.ContainerPort
		}
		if p.HostPort > 0 {
			ports = append(ports, p)
		}
	}
	if len(ports) == 0 {
		return nil, 0, fmt.Errorf("publish at least one host port on the container/app before installing WAF")
	}
	return ports, backendPort, nil
}

func (s *Server) dockerQueueWAF(workerID, owner, action, targetID string, container *dock.ContainerSpec, ports []dock.PortPublish, backendPort int, policy map[string]bool, pages map[string]string) (dockerJob, error) {
	backend := fmt.Sprintf("http://%s:%d", "clofix-"+targetID, backendPort)
	op := dock.Operation{
		Action: action, Container: container, WAFTargetID: targetID,
		WAFBackend: backend, WAFPorts: ports, WAFPolicy: policy, WAFErrorPages: pages,
	}
	return s.queueDockerJob(workerID, owner, action, op)
}

func (s *Server) dockerWAFInstallApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	if a.Method == "compose" {
		d.mu.Unlock()
		fail(w, 409, "Compose stacks: install WAF on an image-backed application or container")
		return
	}
	if a.WAFState == "INSTALLED" || a.WAFState == "INSTALLING" {
		d.mu.Unlock()
		fail(w, 409, "WAF already installed or installing")
		return
	}
	cid := ""
	if len(a.ContainerIDs) > 0 {
		cid = a.ContainerIDs[0]
	}
	if cid == "" {
		d.mu.Unlock()
		fail(w, 409, "application has no container yet")
		return
	}
	c, cok := d.Containers[cid]
	if !cok {
		d.mu.Unlock()
		fail(w, 409, "application container missing")
		return
	}
	ports, backendPort, err := s.dockerWAFPortsFromSpec(c.Spec)
	if err != nil {
		d.mu.Unlock()
		fail(w, 409, err.Error())
		return
	}
	policy := a.OWASPProtection
	if len(policy) == 0 {
		policy = dockerDefaultWAFPolicy()
	}
	spec := c.Spec
	a.WAFState, a.OWASPProtection, a.Updated = "INSTALLING", policy, time.Now().UTC()
	d.Applications[id] = a
	c.WAFState, c.OWASPProtection, c.Updated = "INSTALLING", policy, time.Now().UTC()
	d.Containers[cid] = c
	_ = d.save()
	workerID, owner := a.WorkerID, a.Owner
	pages := a.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-enable", cid, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFUninstallApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	cid := ""
	if len(a.ContainerIDs) > 0 {
		cid = a.ContainerIDs[0]
	}
	c, cok := d.Containers[cid]
	if cid == "" || !cok {
		d.mu.Unlock()
		fail(w, 409, "application container missing")
		return
	}
	ports, backendPort, err := s.dockerWAFPortsFromSpec(c.Spec)
	if err != nil {
		// fall back to stored ports even if host publish currently on WAF
		ports, backendPort = c.Spec.Ports, 80
		for _, p := range c.Spec.Ports {
			if p.ContainerPort > 0 {
				backendPort = p.ContainerPort
				break
			}
		}
	}
	spec := c.Spec
	a.WAFState, a.Updated = "UNINSTALLING", time.Now().UTC()
	d.Applications[id] = a
	c.WAFState, c.Updated = "UNINSTALLING", time.Now().UTC()
	d.Containers[cid] = c
	_ = d.save()
	workerID, owner, policy, pages := a.WorkerID, a.Owner, a.OWASPProtection, a.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-disable", cid, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFCategoryApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Category string `json:"category"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil || wafCategories[in.Category] == "" {
		fail(w, 400, "category and enabled are required")
		return
	}
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	if a.WAFState != "INSTALLED" {
		d.mu.Unlock()
		fail(w, 409, "install OWASP CRS WAF before changing rules")
		return
	}
	if a.OWASPProtection == nil {
		a.OWASPProtection = dockerDefaultWAFPolicy()
	}
	if cur, exists := a.OWASPProtection[in.Category]; exists && cur == *in.Enabled {
		d.mu.Unlock()
		writeJSON(w, 200, map[string]any{"unchanged": true})
		return
	}
	a.OWASPProtection[in.Category] = *in.Enabled
	a.Updated = time.Now().UTC()
	d.Applications[id] = a
	cid := ""
	if len(a.ContainerIDs) > 0 {
		cid = a.ContainerIDs[0]
	}
	var spec *dock.ContainerSpec
	ports := a.Spec.Ports
	backendPort := 80
	if c, ok := d.Containers[cid]; ok {
		c.OWASPProtection = a.OWASPProtection
		d.Containers[cid] = c
		cp := c.Spec
		spec = &cp
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports = c.Spec.Ports
		}
	}
	_ = d.save()
	workerID, owner, policy, pages := a.WorkerID, a.Owner, a.OWASPProtection, a.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-update", cid, spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFErrorPagesApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Status string `json:"status"`
		HTML   string `json:"html"`
		Pages  map[string]string `json:"pages"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWAFErrorPageBytes+4096)).Decode(&in); err != nil {
		fail(w, 400, "invalid body")
		return
	}
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	if !ok || !canAccessDocker(r, a.Owner) {
		d.mu.Unlock()
		fail(w, 404, "application not found")
		return
	}
	if a.WAFState != "INSTALLED" {
		d.mu.Unlock()
		fail(w, 409, "install OWASP CRS WAF before setting error pages")
		return
	}
	if a.WAFErrorPages == nil {
		a.WAFErrorPages = map[string]string{}
	}
	if in.Pages != nil {
		for status, html := range in.Pages {
			if !wafErrorPageStatuses[status] {
				d.mu.Unlock()
				fail(w, 400, "unsupported status "+status)
				return
			}
			if len(html) > maxWAFErrorPageBytes {
				d.mu.Unlock()
				fail(w, 400, "error page too large")
				return
			}
			a.WAFErrorPages[status] = html
		}
	} else {
		if !wafErrorPageStatuses[in.Status] {
			d.mu.Unlock()
			fail(w, 400, "unsupported status")
			return
		}
		if len(in.HTML) > maxWAFErrorPageBytes {
			d.mu.Unlock()
			fail(w, 400, "error page too large")
			return
		}
		a.WAFErrorPages[in.Status] = in.HTML
	}
	a.Updated = time.Now().UTC()
	d.Applications[id] = a
	cid := ""
	if len(a.ContainerIDs) > 0 {
		cid = a.ContainerIDs[0]
	}
	var spec *dock.ContainerSpec
	ports := a.Spec.Ports
	backendPort := 80
	if c, ok := d.Containers[cid]; ok {
		c.WAFErrorPages = a.WAFErrorPages
		d.Containers[cid] = c
		cp := c.Spec
		spec = &cp
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports = c.Spec.Ports
		}
	}
	_ = d.save()
	workerID, owner, policy, pages := a.WorkerID, a.Owner, a.OWASPProtection, a.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-update", cid, spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFInstallContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	if !ok || !canAccessDocker(r, c.Owner) {
		d.mu.Unlock()
		fail(w, 404, "container not found")
		return
	}
	if c.WAFState == "INSTALLED" || c.WAFState == "INSTALLING" {
		d.mu.Unlock()
		fail(w, 409, "WAF already installed or installing")
		return
	}
	ports, backendPort, err := s.dockerWAFPortsFromSpec(c.Spec)
	if err != nil {
		d.mu.Unlock()
		fail(w, 409, err.Error())
		return
	}
	policy := c.OWASPProtection
	if len(policy) == 0 {
		policy = dockerDefaultWAFPolicy()
	}
	spec := c.Spec
	c.WAFState, c.OWASPProtection, c.Updated = "INSTALLING", policy, time.Now().UTC()
	d.Containers[id] = c
	_ = d.save()
	workerID, owner, pages := c.WorkerID, c.Owner, c.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-enable", id, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFUninstallContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	if !ok || !canAccessDocker(r, c.Owner) {
		d.mu.Unlock()
		fail(w, 404, "container not found")
		return
	}
	ports, backendPort, _ := s.dockerWAFPortsFromSpec(c.Spec)
	if len(ports) == 0 {
		ports, backendPort = c.Spec.Ports, 80
	}
	spec := c.Spec
	c.WAFState, c.Updated = "UNINSTALLING", time.Now().UTC()
	d.Containers[id] = c
	_ = d.save()
	workerID, owner, policy, pages := c.WorkerID, c.Owner, c.OWASPProtection, c.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-disable", id, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFCategoryContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Category string `json:"category"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil || wafCategories[in.Category] == "" {
		fail(w, 400, "category and enabled are required")
		return
	}
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	if !ok || !canAccessDocker(r, c.Owner) {
		d.mu.Unlock()
		fail(w, 404, "container not found")
		return
	}
	if c.WAFState != "INSTALLED" {
		d.mu.Unlock()
		fail(w, 409, "install OWASP CRS WAF before changing rules")
		return
	}
	if c.OWASPProtection == nil {
		c.OWASPProtection = dockerDefaultWAFPolicy()
	}
	if cur, exists := c.OWASPProtection[in.Category]; exists && cur == *in.Enabled {
		d.mu.Unlock()
		writeJSON(w, 200, map[string]any{"unchanged": true})
		return
	}
	c.OWASPProtection[in.Category] = *in.Enabled
	c.Updated = time.Now().UTC()
	d.Containers[id] = c
	spec := c.Spec
	ports, backendPort, _ := s.dockerWAFPortsFromSpec(c.Spec)
	_ = d.save()
	workerID, owner, policy, pages := c.WorkerID, c.Owner, c.OWASPProtection, c.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-update", id, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerWAFErrorPagesContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Status string `json:"status"`
		HTML   string `json:"html"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWAFErrorPageBytes+4096)).Decode(&in); err != nil {
		fail(w, 400, "invalid body")
		return
	}
	if !wafErrorPageStatuses[in.Status] {
		fail(w, 400, "unsupported status")
		return
	}
	if len(in.HTML) > maxWAFErrorPageBytes {
		fail(w, 400, "error page too large")
		return
	}
	d := s.docker
	d.mu.Lock()
	c, ok := d.Containers[id]
	if !ok || !canAccessDocker(r, c.Owner) {
		d.mu.Unlock()
		fail(w, 404, "container not found")
		return
	}
	if c.WAFState != "INSTALLED" {
		d.mu.Unlock()
		fail(w, 409, "install OWASP CRS WAF before setting error pages")
		return
	}
	if c.WAFErrorPages == nil {
		c.WAFErrorPages = map[string]string{}
	}
	c.WAFErrorPages[in.Status] = in.HTML
	c.Updated = time.Now().UTC()
	d.Containers[id] = c
	spec := c.Spec
	ports, backendPort, _ := s.dockerWAFPortsFromSpec(c.Spec)
	_ = d.save()
	workerID, owner, policy, pages := c.WorkerID, c.Owner, c.OWASPProtection, c.WAFErrorPages
	d.mu.Unlock()
	j, err := s.dockerQueueWAF(workerID, owner, "waf-update", id, &spec, ports, backendPort, policy, pages)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) dockerSecurityScan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image    string `json:"image"`
		WorkerID string `json:"worker_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	workerID, err := s.dockerPlace(128, 0.5, 512, in.WorkerID)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	j, err := s.queueDockerJob(workerID, accountID(r), "security-scan", dock.Operation{Action: "security-scan", Image: in.Image})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	d := s.docker
	d.mu.Lock()
	d.SecurityEvents = append(d.SecurityEvents, map[string]any{"at": time.Now().UTC(), "action": "scan", "image": in.Image, "job": j.ID})
	_ = d.save()
	d.mu.Unlock()
	writeJSON(w, 202, j)
}

func (s *Server) dockerListBackups(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []dockerBackup{}
	for _, b := range d.Backups {
		if canAccessDocker(r, b.Owner) {
			items = append(items, b)
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateBackup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string `json:"kind"`
		TargetID string `json:"target_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	d := s.docker
	d.mu.Lock()
	v, ok := d.Volumes[in.TargetID]
	d.mu.Unlock()
	if !ok || !canAccessDocker(r, v.Owner) {
		fail(w, 404, "volume not found")
		return
	}
	id := dockerRandID("dcb")
	b := dockerBackup{ID: id, Owner: v.Owner, Kind: "volume", TargetID: v.ID, WorkerID: v.WorkerID, State: "RUNNING", Created: time.Now().UTC()}
	d.mu.Lock()
	d.Backups[id] = b
	_ = d.save()
	d.mu.Unlock()
	j, err := s.queueDockerJob(v.WorkerID, v.Owner, "volume-backup", dock.Operation{Action: "volume-backup", Volume: &dock.VolumeSpec{ID: v.ID}, BackupID: id})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"backup": b, "job": j})
}

func (s *Server) dockerRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Redeploy bool `json:"redeploy"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	d := s.docker
	d.mu.Lock()
	b, ok := d.Backups[r.PathValue("id")]
	v, vok := d.Volumes[b.TargetID]
	if !ok || !vok || !canAccessDocker(r, b.Owner) {
		d.mu.Unlock()
		fail(w, 404, "backup not found")
		return
	}
	if b.State != "READY" && b.State != "FAILED" {
		d.mu.Unlock()
		fail(w, 409, "backup is not ready to restore")
		return
	}
	b.State, b.Error, b.RedeployAfter = "RESTORING", "", in.Redeploy
	d.Backups[b.ID] = b
	_ = d.save()
	workerID, owner, volID, backupID := b.WorkerID, b.Owner, v.ID, b.ID
	d.mu.Unlock()
	j, err := s.queueDockerJob(workerID, owner, "volume-restore", dock.Operation{Action: "volume-restore", Volume: &dock.VolumeSpec{ID: volID}, BackupID: backupID})
	if err != nil {
		d.mu.Lock()
		if cur, ok := d.Backups[backupID]; ok {
			cur.State, cur.Error, cur.RedeployAfter = "READY", err.Error(), false
			d.Backups[backupID] = cur
			_ = d.save()
		}
		d.mu.Unlock()
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.backup.restore", backupID, volID)
	writeJSON(w, 202, map[string]any{"job": j, "redeploy_after": in.Redeploy})
}

func (s *Server) dockerDeleteBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.docker
	d.mu.Lock()
	b, ok := d.Backups[id]
	if !ok || !canAccessDocker(r, b.Owner) {
		d.mu.Unlock()
		fail(w, 404, "backup not found")
		return
	}
	switch b.State {
	case "RESTORING", "DELETING":
		d.mu.Unlock()
		fail(w, 409, "backup is busy")
		return
	}
	b.State, b.Error = "DELETING", ""
	d.Backups[id] = b
	_ = d.save()
	workerID, owner := b.WorkerID, b.Owner
	d.mu.Unlock()
	j, err := s.queueDockerJob(workerID, owner, "volume-backup-delete", dock.Operation{Action: "volume-backup-delete", BackupID: id})
	if err != nil {
		// Fall back to metadata-only delete if the worker cannot be reached.
		d.mu.Lock()
		delete(d.Backups, id)
		_ = d.save()
		d.mu.Unlock()
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(actorID(r), "docker.backup.delete", id, "queued")
	writeJSON(w, 202, map[string]any{"id": id, "job": j})
}

type dockerRedeployTask struct {
	WorkerID string
	Owner    string
	Action   string
	Op       dock.Operation
	Label    string
}

// dockerRedeployAfterVolumeRestore queues recreate/redeploy for apps/containers that mount the volume.
func (s *Server) dockerRedeployAfterVolumeRestore(volumeID, owner string) []string {
	if s.docker == nil || volumeID == "" {
		return nil
	}
	d := s.docker
	d.mu.Lock()
	tasks := []dockerRedeployTask{}
	seenApp := map[string]bool{}
	for _, c := range d.Containers {
		if c.Owner != owner {
			continue
		}
		uses := false
		for _, m := range c.Spec.Volumes {
			if m.VolumeID == volumeID {
				uses = true
				break
			}
		}
		if !uses {
			continue
		}
		if c.AppID != "" {
			if seenApp[c.AppID] {
				continue
			}
			seenApp[c.AppID] = true
			if a, ok := d.Applications[c.AppID]; ok {
				switch a.Method {
				case "compose":
					tasks = append(tasks, dockerRedeployTask{
						WorkerID: a.WorkerID, Owner: a.Owner, Action: "compose-deploy", Label: a.ID,
						Op: dock.Operation{Action: "compose-deploy", Compose: &dock.ComposeSpec{ID: a.ComposeID, Name: a.Name, Owner: a.Owner, YAML: a.ComposeYAML, Project: "clofix-" + a.ComposeID}},
					})
				default:
					spec := a.Spec
					if spec.ID == "" {
						spec.ID = a.ID
					}
					tasks = append(tasks, dockerRedeployTask{
						WorkerID: a.WorkerID, Owner: a.Owner, Action: "container-recreate", Label: a.ID,
						Op: dock.Operation{Action: "container-recreate", Container: &spec},
					})
				}
			}
			continue
		}
		spec := c.Spec
		if spec.ID == "" {
			spec.ID = c.ID
		}
		tasks = append(tasks, dockerRedeployTask{
			WorkerID: c.WorkerID, Owner: c.Owner, Action: "container-recreate", Label: c.ID,
			Op: dock.Operation{Action: "container-recreate", Container: &spec},
		})
	}
	d.mu.Unlock()
	out := []string{}
	for _, t := range tasks {
		if _, err := s.queueDockerJob(t.WorkerID, t.Owner, t.Action, t.Op); err == nil {
			out = append(out, t.Label)
		}
	}
	return out
}

func (s *Server) dockerListRegistries(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	defer d.mu.Unlock()
	items := []map[string]any{}
	for _, reg := range d.Registries {
		if canAccessDocker(r, reg.Owner) {
			items = append(items, map[string]any{"id": reg.ID, "name": reg.Name, "server": reg.Server, "username": reg.Username})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) dockerCreateRegistry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	id := dockerRandID("dcr")
	reg := dockerRegistry{ID: id, Owner: accountID(r), Name: in.Name, Server: in.Server, Username: in.Username, Password: in.Password}
	d := s.docker
	d.mu.Lock()
	d.Registries[id] = reg
	_ = d.save()
	d.mu.Unlock()
	s.Store.Audit(actorID(r), "docker.registry.create", id, "ok")
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "server": in.Server, "username": in.Username})
}

func (s *Server) dockerDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	d := s.docker
	d.mu.Lock()
	reg, ok := d.Registries[r.PathValue("id")]
	if !ok || !canAccessDocker(r, reg.Owner) {
		d.mu.Unlock()
		fail(w, 404, "registry not found")
		return
	}
	delete(d.Registries, reg.ID)
	_ = d.save()
	d.mu.Unlock()
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) dockerSaveSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(r)
	if !ok || (p.Kind != "admin" && p.Role != "Full Administrator") {
		fail(w, 403, "admin required")
		return
	}
	var in struct{ Enabled bool `json:"enabled"` }
	_ = json.NewDecoder(r.Body).Decode(&in)
	d := s.docker
	d.mu.Lock()
	d.Enabled = in.Enabled
	_ = d.save()
	d.mu.Unlock()
	writeJSON(w, 200, map[string]any{"enabled": d.Enabled})
}

func (s *Server) dockerWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	secret := r.Header.Get("X-CloFix-Webhook-Key")
	d := s.docker
	d.mu.Lock()
	a, ok := d.Applications[id]
	d.mu.Unlock()
	if !ok || a.WebhookSecret == "" || secret == "" || a.WebhookSecret != secret {
		fail(w, 401, "unauthorized")
		return
	}
	var j dockerJob
	var err error
	if a.Method == "git" {
		j, err = s.queueDockerJob(a.WorkerID, a.Owner, "git-build", dock.Operation{Action: "git-build", GitURL: a.GitURL, GitBranch: a.GitBranch, Image: "clofix-build/" + a.ID + ":latest"})
	} else {
		j, err = s.queueDockerJob(a.WorkerID, a.Owner, "container-recreate", dock.Operation{Action: "container-recreate", Container: &a.Spec})
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

func accountID(r *http.Request) string {
	p := principalFrom(r)
	if p.AccountID != "" {
		return p.AccountID
	}
	return "acc-platform"
}

func actorID(r *http.Request) string {
	p := principalFrom(r)
	if p.UserID != "" {
		return p.UserID
	}
	if p.Email != "" {
		return p.Email
	}
	return "unknown"
}
