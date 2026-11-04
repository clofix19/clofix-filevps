package server

import (
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
)

// wafBinding attaches the shared worker WAF engine to one VPS or Docker target.
// The engine itself is prepared once per worker; bindings only activate/deactivate.
type wafBinding struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	TargetKind      string            `json:"target_kind"` // vps | docker_app | docker_container
	TargetID        string            `json:"target_id"`
	WorkerID        string            `json:"worker_id,omitempty"`
	State           string            `json:"state"` // ACTIVE|ACTIVATING|DEACTIVATING|FAILED|INACTIVE
	Error           string            `json:"error,omitempty"`
	OWASPProtection map[string]bool   `json:"owasp_protection,omitempty"`
	WAFErrorPages   map[string]string `json:"waf_error_pages,omitempty"`
	Stats           *dock.WAFStats    `json:"stats,omitempty"`
	Owner           string            `json:"owner,omitempty"`
	Created         time.Time         `json:"created"`
	Updated         time.Time         `json:"updated"`
}

type wafStore struct {
	sync.Mutex
	Bindings map[string]wafBinding `json:"bindings"`
	file     string
}

func loadWAF(dir string) *wafStore {
	d := &wafStore{file: filepath.Join(dir, "waf-bindings.json"), Bindings: map[string]wafBinding{}}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid waf-bindings.json")
		}
	}
	if d.Bindings == nil {
		d.Bindings = map[string]wafBinding{}
	}
	return d
}

func (d *wafStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (d *wafStore) put(b wafBinding) error {
	d.Lock()
	defer d.Unlock()
	d.Bindings[b.ID] = b
	return d.save()
}

func (d *wafStore) get(id string) (wafBinding, bool) {
	d.Lock()
	defer d.Unlock()
	b, ok := d.Bindings[id]
	return b, ok
}

func (d *wafStore) drop(id string) error {
	d.Lock()
	defer d.Unlock()
	delete(d.Bindings, id)
	return d.save()
}

func (d *wafStore) list() []wafBinding {
	d.Lock()
	defer d.Unlock()
	out := make([]wafBinding, 0, len(d.Bindings))
	for _, b := range d.Bindings {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated.Equal(out[j].Updated) {
			return out[i].ID < out[j].ID
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

func (d *wafStore) findByTarget(kind, targetID string) (wafBinding, bool) {
	d.Lock()
	defer d.Unlock()
	for _, b := range d.Bindings {
		if b.TargetKind == kind && b.TargetID == targetID {
			return b, true
		}
	}
	return wafBinding{}, false
}

func (s *Server) wafRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/waf", s.auth(s.getWAF))
	mux.HandleFunc("POST /api/waf/bindings", s.auth(s.createWAFBinding))
	mux.HandleFunc("PUT /api/waf/bindings/{id}", s.auth(s.updateWAFBinding))
	mux.HandleFunc("DELETE /api/waf/bindings/{id}", s.auth(s.deleteWAFBinding))
	mux.HandleFunc("POST /api/waf/bindings/{id}/activate", s.auth(s.activateWAFBinding))
	mux.HandleFunc("POST /api/waf/bindings/{id}/deactivate", s.auth(s.deactivateWAFBinding))
	mux.HandleFunc("POST /api/waf/bindings/{id}/category", s.auth(s.wafBindingCategory))
	mux.HandleFunc("PUT /api/waf/bindings/{id}/error-pages", s.auth(s.wafBindingErrorPages))
	mux.HandleFunc("GET /api/waf/bindings/{id}/stats", s.auth(s.getWAFBindingStats))
	mux.HandleFunc("POST /api/waf/bindings/{id}/stats/refresh", s.auth(s.refreshWAFBindingStats))
}

func wafDefaultPolicy() map[string]bool {
	out := map[string]bool{}
	for cat := range wafCategories {
		out[cat] = true
	}
	return out
}

func (s *Server) syncWAFBindingStates() {
	if s.waf == nil {
		return
	}
	now := time.Now().UTC()
	for _, b := range s.waf.list() {
		changed := false
		switch b.TargetKind {
		case "vps":
			// Edge gateway state is driven by docker job results (applyVPSWAFJobResult).
			// Do not map legacy guest owasp-waf installs onto bindings.
		case "docker_app", "docker_container":
			if s.docker == nil {
				continue
			}
			s.docker.mu.Lock()
			var wafState, errMsg string
			if b.TargetKind == "docker_app" {
				if a, ok := s.docker.Applications[b.TargetID]; ok {
					wafState, errMsg = a.WAFState, a.Error
					if len(b.OWASPProtection) == 0 && len(a.OWASPProtection) > 0 {
						b.OWASPProtection = a.OWASPProtection
						changed = true
					}
					if len(b.WAFErrorPages) == 0 && len(a.WAFErrorPages) > 0 {
						b.WAFErrorPages = a.WAFErrorPages
						changed = true
					}
				}
			} else if c, ok := s.docker.Containers[b.TargetID]; ok {
				wafState, errMsg = c.WAFState, c.Error
				if len(b.OWASPProtection) == 0 && len(c.OWASPProtection) > 0 {
					b.OWASPProtection = c.OWASPProtection
					changed = true
				}
				if len(b.WAFErrorPages) == 0 && len(c.WAFErrorPages) > 0 {
					b.WAFErrorPages = c.WAFErrorPages
					changed = true
				}
			}
			s.docker.mu.Unlock()
			switch wafState {
			case "INSTALLED":
				if b.State != "ACTIVE" {
					b.State, b.Error, changed = "ACTIVE", "", true
				}
			case "INSTALLING":
				if b.State != "ACTIVATING" {
					b.State, changed = "ACTIVATING", true
				}
			case "UNINSTALLING":
				if b.State != "DEACTIVATING" {
					b.State, changed = "DEACTIVATING", true
				}
			case "FAILED":
				if b.State != "FAILED" {
					b.State, b.Error, changed = "FAILED", errMsg, true
					if b.Error == "" {
						b.Error = "edge WAF install failed"
					}
				}
			case "UNINSTALLED", "":
				if b.State == "DEACTIVATING" || b.State == "ACTIVE" || b.State == "ACTIVATING" {
					if b.State == "DEACTIVATING" {
						b.State, b.Error, changed = "INACTIVE", "", true
					}
				}
			}
		}
		if changed {
			b.Updated = now
			_ = s.waf.put(b)
		}
	}
}

func (s *Server) getWAF(w http.ResponseWriter, r *http.Request) {
	s.syncWAFBindingStates()
	workers, _ := s.Store.ListWorkers()
	engines := make([]map[string]any, 0, len(workers))
	now := time.Now()
	for _, wk := range workers {
		live := wk.Status == "active" && now.Sub(wk.LastHeartbeat) <= time.Minute
		engines = append(engines, map[string]any{
			"worker_id": wk.ID, "hostname": wk.Hostname, "status": wk.Status,
			"live": live, "ready": wk.Capacity.WAFReady, "version": wk.Capacity.WAFVersion,
			"error": wk.Capacity.WAFError, "docker_ready": wk.Capacity.DockerReady,
		})
	}
	sort.Slice(engines, func(i, j int) bool {
		return engines[i]["worker_id"].(string) < engines[j]["worker_id"].(string)
	})

	bindings := []map[string]any{}
	for _, b := range s.waf.list() {
		if b.Owner != "" && !canAccessApplication(r, b.Owner) {
			continue
		}
		bindings = append(bindings, map[string]any{
			"id": b.ID, "name": b.Name, "target_kind": b.TargetKind, "target_id": b.TargetID,
			"worker_id": b.WorkerID, "state": b.State, "error": b.Error,
			"owasp_protection": b.OWASPProtection, "waf_error_pages": b.WAFErrorPages,
			"stats": b.Stats, "owner": b.Owner, "created": b.Created, "updated": b.Updated,
		})
	}

	targets := s.wafTargetCatalog(r)
	cats := make([]map[string]string, 0, len(wafCategories))
	for k, v := range wafCategories {
		cats = append(cats, map[string]string{"key": k, "tag": v})
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i]["key"] < cats[j]["key"] })

	writeJSON(w, 200, map[string]any{
		"engine":   "OWASP CRS · ModSecurity (shared worker engine)",
		"engines":  engines,
		"bindings": bindings,
		"targets":  targets,
		"categories": cats,
	})
}

func (s *Server) wafTargetCatalog(r *http.Request) []map[string]any {
	out := []map[string]any{}
	instances, _ := s.Store.ListInstances()
	for _, inst := range instances {
		owner := inst.AccountID
		if owner != "" && !canAccessApplication(r, owner) {
			continue
		}
		s.usageMu.RLock()
		st := s.serviceInstalls[inst.ID]["owasp-waf"]
		s.usageMu.RUnlock()
		bound, _ := s.waf.findByTarget("vps", inst.ID)
		out = append(out, map[string]any{
			"kind": "vps", "id": inst.ID, "name": inst.ID, "worker_id": inst.WorkerID,
			"state": inst.State, "waf_state": st, "bound": bound.ID != "",
		})
	}
	if s.docker != nil {
		s.docker.mu.Lock()
		for _, a := range s.docker.Applications {
			if !canAccessDocker(r, a.Owner) || a.Method == "compose" {
				continue
			}
			bound, _ := s.waf.findByTarget("docker_app", a.ID)
			out = append(out, map[string]any{
				"kind": "docker_app", "id": a.ID, "name": a.Name, "worker_id": a.WorkerID,
				"state": a.State, "waf_state": a.WAFState, "bound": bound.ID != "", "ports": a.Spec.Ports,
			})
		}
		for _, c := range s.docker.Containers {
			if !canAccessDocker(r, c.Owner) || c.AppID != "" {
				continue
			}
			bound, _ := s.waf.findByTarget("docker_container", c.ID)
			out = append(out, map[string]any{
				"kind": "docker_container", "id": c.ID, "name": c.Name, "worker_id": c.WorkerID,
				"state": c.State, "waf_state": c.WAFState, "bound": bound.ID != "", "ports": c.Spec.Ports,
			})
		}
		s.docker.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := out[i]["kind"].(string), out[j]["kind"].(string)
		if ki != kj {
			return ki < kj
		}
		return out[i]["id"].(string) < out[j]["id"].(string)
	})
	return out
}

func (s *Server) createWAFBinding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		TargetKind string `json:"target_kind"`
		TargetID   string `json:"target_id"`
		Activate   *bool  `json:"activate"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	kind := strings.TrimSpace(in.TargetKind)
	tid := strings.TrimSpace(in.TargetID)
	if tid == "" || (kind != "vps" && kind != "docker_app" && kind != "docker_container") {
		fail(w, 400, "target_kind must be vps, docker_app, or docker_container")
		return
	}
	if _, exists := s.waf.findByTarget(kind, tid); exists {
		fail(w, 409, "a WAF binding already exists for this target")
		return
	}
	workerID, owner, name, err := s.resolveWAFTarget(r, kind, tid)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		name = strings.TrimSpace(in.Name)
	}
	now := time.Now().UTC()
	b := wafBinding{
		ID: "waf-" + randHex(16), Name: name, TargetKind: kind, TargetID: tid,
		WorkerID: workerID, State: "INACTIVE", OWASPProtection: wafDefaultPolicy(),
		Owner: owner, Created: now, Updated: now,
	}
	if err := s.waf.put(b); err != nil {
		fail(w, 500, err.Error())
		return
	}
	activate := in.Activate == nil || *in.Activate
	if activate {
		if err := s.activateWAFBindingLocked(&b); err != nil {
			b.State, b.Error, b.Updated = "FAILED", err.Error(), time.Now().UTC()
			_ = s.waf.put(b)
			fail(w, 409, err.Error())
			return
		}
		_ = s.waf.put(b)
	}
	s.Store.Audit(accountID(r), "waf.binding.create", b.ID, kind+":"+tid)
	writeJSON(w, 201, b)
}

func (s *Server) resolveWAFTarget(r *http.Request, kind, tid string) (workerID, owner, name string, err error) {
	switch kind {
	case "vps":
		inst, e := s.Store.GetInstance(tid)
		if e != nil {
			return "", "", "", fmt.Errorf("VPS not found")
		}
		owner := inst.AccountID
		if owner != "" && !canAccessApplication(r, owner) {
			return "", "", "", fmt.Errorf("VPS not found")
		}
		return inst.WorkerID, owner, inst.ID, nil
	case "docker_app":
		if s.docker == nil {
			return "", "", "", fmt.Errorf("docker product not available")
		}
		s.docker.mu.Lock()
		defer s.docker.mu.Unlock()
		a, ok := s.docker.Applications[tid]
		if !ok || !canAccessDocker(r, a.Owner) {
			return "", "", "", fmt.Errorf("application not found")
		}
		if a.Method == "compose" {
			return "", "", "", fmt.Errorf("Compose stacks: bind WAF to an image-backed application or container")
		}
		return a.WorkerID, a.Owner, a.Name, nil
	case "docker_container":
		if s.docker == nil {
			return "", "", "", fmt.Errorf("docker product not available")
		}
		s.docker.mu.Lock()
		defer s.docker.mu.Unlock()
		c, ok := s.docker.Containers[tid]
		if !ok || !canAccessDocker(r, c.Owner) || c.AppID != "" {
			return "", "", "", fmt.Errorf("container not found")
		}
		return c.WorkerID, c.Owner, c.Name, nil
	}
	return "", "", "", fmt.Errorf("unknown target kind")
}

func (s *Server) workerWAFReady(workerID string) error {
	wk, err := s.Store.GetWorker(workerID)
	if err != nil {
		return fmt.Errorf("worker not found")
	}
	if wk.Status != "active" || time.Since(wk.LastHeartbeat) > time.Minute {
		return fmt.Errorf("worker is offline")
	}
	if !wk.Capacity.WAFReady {
		msg := "WAF engine is not ready on this worker"
		if wk.Capacity.WAFError != "" {
			msg += ": " + wk.Capacity.WAFError
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (s *Server) activateWAFBindingLocked(b *wafBinding) error {
	switch b.TargetKind {
	case "vps":
		if err := s.workerWAFReady(b.WorkerID); err != nil {
			return err
		}
		return s.activateVPSWAF(b)
	case "docker_app", "docker_container":
		if err := s.workerWAFReady(b.WorkerID); err != nil {
			return err
		}
		return s.activateDockerWAF(b)
	}
	return fmt.Errorf("unknown target kind")
}

func (s *Server) activateVPSWAF(b *wafBinding) error {
	inst, err := s.Store.GetInstance(b.TargetID)
	if err != nil {
		return fmt.Errorf("VPS not found")
	}
	if inst.State != proto.StateRunning {
		return fmt.Errorf("VPS must be running")
	}
	if strings.TrimSpace(inst.Spec.IPAddress) == "" || inst.Spec.SSHPort < proto.HostSSHBase {
		return fmt.Errorf("VPS has no private IP or SSH publish yet")
	}
	s.usageMu.RLock()
	st := s.serviceInstalls[b.TargetID]["owasp-waf"]
	s.usageMu.RUnlock()
	if st == "INSTALLED" || st == "INSTALLING" || st == "UNINSTALLING" {
		return fmt.Errorf("legacy guest OWASP WAF is still present (%s) — uninstall it from CloFix Library first; edge WAF does not install inside the VPS", st)
	}
	if b.State == "ACTIVE" || b.State == "ACTIVATING" {
		return nil
	}
	policy := b.OWASPProtection
	if len(policy) == 0 {
		policy = wafDefaultPolicy()
	}
	pages := b.WAFErrorPages
	httpPort := proto.HostHTTPPort(inst.Spec.SSHPort)
	off := false
	inst.Spec.PublishHTTP = &off
	if err := s.Store.SaveInstance(inst); err != nil {
		return fmt.Errorf("could not update VPS publish flags")
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: b.TargetID + "/" + proto.ActionUpdatePorts + "/" + randHex(4),
		Action: proto.ActionUpdatePorts, InstanceID: b.TargetID, Spec: &spec,
	}); err != nil {
		return fmt.Errorf("could not unpublish HTTP DNAT for WAF gateway")
	}
	backend := "http://" + inst.Spec.IPAddress + ":80"
	ports := []dock.PortPublish{{HostPort: httpPort, ContainerPort: 80, Protocol: "tcp"}}
	op := dock.Operation{
		Action: "waf-enable", WAFMode: "vps", WAFTargetID: b.TargetID,
		WAFBackend: backend, WAFPorts: ports, WAFPolicy: policy, WAFErrorPages: pages,
	}
	owner := b.Owner
	if owner == "" {
		owner = inst.AccountID
	}
	if _, err := s.queueDockerJob(inst.WorkerID, owner, "waf-enable", op); err != nil {
		return err
	}
	b.State, b.Error, b.OWASPProtection, b.Updated = "ACTIVATING", "", policy, time.Now().UTC()
	return nil
}

func (s *Server) activateDockerWAF(b *wafBinding) error {
	if s.docker == nil {
		return fmt.Errorf("docker product not available")
	}
	policy := b.OWASPProtection
	if len(policy) == 0 {
		policy = wafDefaultPolicy()
	}
	pages := b.WAFErrorPages
	d := s.docker

	var workerID, owner, cid string
	var spec dock.ContainerSpec
	var ports []dock.PortPublish
	var backendPort int

	d.mu.Lock()
	if b.TargetKind == "docker_app" {
		a, ok := d.Applications[b.TargetID]
		if !ok {
			d.mu.Unlock()
			return fmt.Errorf("application not found")
		}
		if a.WAFState == "INSTALLED" || a.WAFState == "INSTALLING" {
			b.State = "ACTIVE"
			if a.WAFState == "INSTALLING" {
				b.State = "ACTIVATING"
			}
			b.Error, b.Updated = "", time.Now().UTC()
			b.OWASPProtection = policy
			d.mu.Unlock()
			return nil
		}
		if len(a.ContainerIDs) > 0 {
			cid = a.ContainerIDs[0]
		}
		if cid == "" {
			d.mu.Unlock()
			return fmt.Errorf("application has no container yet")
		}
		c, cok := d.Containers[cid]
		if !cok {
			d.mu.Unlock()
			return fmt.Errorf("application container missing")
		}
		var err error
		ports, backendPort, err = s.dockerWAFPortsFromSpec(c.Spec)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		spec = c.Spec
		a.WAFState, a.OWASPProtection, a.WAFErrorPages, a.Updated = "INSTALLING", policy, pages, time.Now().UTC()
		d.Applications[b.TargetID] = a
		c.WAFState, c.OWASPProtection, c.WAFErrorPages, c.Updated = "INSTALLING", policy, pages, time.Now().UTC()
		d.Containers[cid] = c
		_ = d.save()
		workerID, owner = a.WorkerID, a.Owner
		d.mu.Unlock()
	} else {
		c, ok := d.Containers[b.TargetID]
		if !ok {
			d.mu.Unlock()
			return fmt.Errorf("container not found")
		}
		if c.WAFState == "INSTALLED" || c.WAFState == "INSTALLING" {
			b.State = "ACTIVE"
			if c.WAFState == "INSTALLING" {
				b.State = "ACTIVATING"
			}
			b.Error, b.OWASPProtection, b.Updated = "", policy, time.Now().UTC()
			d.mu.Unlock()
			return nil
		}
		var err error
		ports, backendPort, err = s.dockerWAFPortsFromSpec(c.Spec)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		cid = b.TargetID
		spec = c.Spec
		c.WAFState, c.OWASPProtection, c.WAFErrorPages, c.Updated = "INSTALLING", policy, pages, time.Now().UTC()
		d.Containers[b.TargetID] = c
		_ = d.save()
		workerID, owner = c.WorkerID, c.Owner
		d.mu.Unlock()
	}
	if _, err := s.dockerQueueWAF(workerID, owner, "waf-enable", cid, &spec, ports, backendPort, policy, pages); err != nil {
		return err
	}
	b.State, b.Error, b.OWASPProtection, b.Updated = "ACTIVATING", "", policy, time.Now().UTC()
	return nil
}

func (s *Server) deactivateWAFBindingLocked(b *wafBinding) error {
	switch b.TargetKind {
	case "vps":
		return s.deactivateVPSWAF(b)
	case "docker_app", "docker_container":
		return s.deactivateDockerWAF(b)
	}
	return fmt.Errorf("unknown target kind")
}

func (s *Server) deactivateVPSWAF(b *wafBinding) error {
	inst, err := s.Store.GetInstance(b.TargetID)
	if err != nil {
		b.State, b.Error, b.Updated = "INACTIVE", "", time.Now().UTC()
		return nil
	}
	if b.State == "INACTIVE" || b.State == "" {
		return nil
	}
	owner := b.Owner
	if owner == "" {
		owner = inst.AccountID
	}
	op := dock.Operation{Action: "waf-disable", WAFMode: "vps", WAFTargetID: b.TargetID}
	if _, err := s.queueDockerJob(inst.WorkerID, owner, "waf-disable", op); err != nil {
		return err
	}
	on := true
	inst.Spec.PublishHTTP = &on
	_ = s.Store.SaveInstance(inst)
	spec := inst.Spec
	_ = s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: b.TargetID + "/" + proto.ActionUpdatePorts + "/" + randHex(4),
		Action: proto.ActionUpdatePorts, InstanceID: b.TargetID, Spec: &spec,
	})
	b.State, b.Error, b.Updated = "DEACTIVATING", "", time.Now().UTC()
	return nil
}

func (s *Server) deactivateDockerWAF(b *wafBinding) error {
	if s.docker == nil {
		b.State = "INACTIVE"
		return nil
	}
	d := s.docker
	var cid, workerID, owner, appID string
	var spec dock.ContainerSpec
	var ports []dock.PortPublish
	var backendPort int
	var policy map[string]bool
	var pages map[string]string

	d.mu.Lock()
	if b.TargetKind == "docker_app" {
		a, ok := d.Applications[b.TargetID]
		if !ok {
			d.mu.Unlock()
			b.State = "INACTIVE"
			return nil
		}
		if a.WAFState == "" || a.WAFState == "UNINSTALLED" {
			b.State, b.Error, b.Updated = "INACTIVE", "", time.Now().UTC()
			d.mu.Unlock()
			return nil
		}
		if len(a.ContainerIDs) > 0 {
			cid = a.ContainerIDs[0]
		}
		c, cok := d.Containers[cid]
		if cid == "" || !cok {
			d.mu.Unlock()
			return fmt.Errorf("application container missing")
		}
		appID = b.TargetID
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports, backendPort = c.Spec.Ports, 80
			for _, p := range c.Spec.Ports {
				if p.ContainerPort > 0 {
					backendPort = p.ContainerPort
					break
				}
			}
		}
		spec = c.Spec
		c.WAFState, c.Updated = "UNINSTALLING", time.Now().UTC()
		d.Containers[cid] = c
		a.WAFState, a.Updated = "UNINSTALLING", time.Now().UTC()
		d.Applications[appID] = a
		_ = d.save()
		workerID, owner, policy, pages = c.WorkerID, c.Owner, c.OWASPProtection, c.WAFErrorPages
		d.mu.Unlock()
	} else {
		c, ok := d.Containers[b.TargetID]
		if !ok {
			d.mu.Unlock()
			b.State = "INACTIVE"
			return nil
		}
		if c.WAFState == "" || c.WAFState == "UNINSTALLED" {
			b.State, b.Error, b.Updated = "INACTIVE", "", time.Now().UTC()
			d.mu.Unlock()
			return nil
		}
		cid = b.TargetID
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports, backendPort = c.Spec.Ports, 80
			for _, p := range c.Spec.Ports {
				if p.ContainerPort > 0 {
					backendPort = p.ContainerPort
					break
				}
			}
		}
		spec = c.Spec
		c.WAFState, c.Updated = "UNINSTALLING", time.Now().UTC()
		d.Containers[cid] = c
		_ = d.save()
		workerID, owner, policy, pages = c.WorkerID, c.Owner, c.OWASPProtection, c.WAFErrorPages
		d.mu.Unlock()
	}
	if _, err := s.dockerQueueWAF(workerID, owner, "waf-disable", cid, &spec, ports, backendPort, policy, pages); err != nil {
		return err
	}
	b.State, b.Error, b.Updated = "DEACTIVATING", "", time.Now().UTC()
	return nil
}

func (s *Server) updateWAFBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	if b.Owner != "" && !canAccessApplication(r, b.Owner) {
		fail(w, 404, "binding not found")
		return
	}
	var in struct {
		Name            string            `json:"name"`
		OWASPProtection map[string]bool   `json:"owasp_protection"`
		WAFErrorPages   map[string]string `json:"waf_error_pages"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWAFErrorPageBytes+8192)).Decode(&in); err != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		b.Name = strings.TrimSpace(in.Name)
	}
	if in.OWASPProtection != nil {
		for k := range in.OWASPProtection {
			if wafCategories[k] == "" {
				fail(w, 400, "unknown category "+k)
				return
			}
		}
		b.OWASPProtection = in.OWASPProtection
	}
	if in.WAFErrorPages != nil {
		b.WAFErrorPages = in.WAFErrorPages
	}
	b.Updated = time.Now().UTC()
	if b.State == "ACTIVE" {
		if err := s.reapplyWAFBinding(b); err != nil {
			fail(w, 409, err.Error())
			return
		}
	}
	_ = s.waf.put(b)
	writeJSON(w, 200, b)
}

func (s *Server) reapplyWAFBinding(b wafBinding) error {
	switch b.TargetKind {
	case "vps":
		return s.queueVPSWAFUpdate(b)
	case "docker_app", "docker_container":
		return s.queueDockerWAFUpdate(b)
	}
	return nil
}

func (s *Server) queueVPSWAFUpdate(b wafBinding) error {
	inst, err := s.Store.GetInstance(b.TargetID)
	if err != nil {
		return fmt.Errorf("VPS not found")
	}
	owner := b.Owner
	if owner == "" {
		owner = inst.AccountID
	}
	httpPort := proto.HostHTTPPort(inst.Spec.SSHPort)
	op := dock.Operation{
		Action: "waf-update", WAFMode: "vps", WAFTargetID: b.TargetID,
		WAFBackend: "http://" + inst.Spec.IPAddress + ":80",
		WAFPorts:   []dock.PortPublish{{HostPort: httpPort, ContainerPort: 80, Protocol: "tcp"}},
		WAFPolicy: b.OWASPProtection, WAFErrorPages: b.WAFErrorPages,
	}
	_, err = s.queueDockerJob(inst.WorkerID, owner, "waf-update", op)
	return err
}

// applyVPSWAFJobResult updates a FileVPS edge WAF binding after a worker docker job finishes.
func (s *Server) applyVPSWAFJobResult(action, targetID, errMsg string, ok bool) {
	if s.waf == nil || targetID == "" {
		return
	}
	b, found := s.waf.findByTarget("vps", targetID)
	if !found {
		return
	}
	now := time.Now().UTC()
	switch action {
	case "waf-enable", "waf-update":
		// Ignore stale enable results if this binding is being torn down or already inactive.
		if b.State != "ACTIVATING" && b.State != "FAILED" && b.State != "ACTIVE" {
			return
		}
		if !ok {
			b.State, b.Error, b.Updated = "FAILED", errMsg, now
			if action == "waf-enable" {
				s.restoreVPSHTTPPublish(targetID)
			}
		} else {
			b.State, b.Error, b.Updated = "ACTIVE", "", now
		}
	case "waf-disable":
		if b.State != "DEACTIVATING" {
			return
		}
		if !ok {
			b.State, b.Error, b.Updated = "ACTIVE", errMsg, now
		} else {
			b.State, b.Error, b.Updated = "INACTIVE", "", now
		}
	default:
		return
	}
	_ = s.waf.put(b)
}

func (s *Server) restoreVPSHTTPPublish(instanceID string) {
	inst, err := s.Store.GetInstance(instanceID)
	if err != nil {
		return
	}
	on := true
	inst.Spec.PublishHTTP = &on
	if err := s.Store.SaveInstance(inst); err != nil {
		return
	}
	spec := inst.Spec
	_ = s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: instanceID + "/" + proto.ActionUpdatePorts + "/" + randHex(4),
		Action: proto.ActionUpdatePorts, InstanceID: instanceID, Spec: &spec,
	})
}

func (s *Server) queueDockerWAFUpdate(b wafBinding) error {
	if s.docker == nil {
		return fmt.Errorf("docker product not available")
	}
	d := s.docker
	var cid, workerID, owner string
	var spec dock.ContainerSpec
	var ports []dock.PortPublish
	var backendPort int
	policy, pages := b.OWASPProtection, b.WAFErrorPages
	d.mu.Lock()
	if b.TargetKind == "docker_app" {
		a, ok := d.Applications[b.TargetID]
		if !ok {
			d.mu.Unlock()
			return fmt.Errorf("application not found")
		}
		if len(a.ContainerIDs) == 0 {
			d.mu.Unlock()
			return fmt.Errorf("application has no container")
		}
		cid = a.ContainerIDs[0]
		c, cok := d.Containers[cid]
		if !cok {
			d.mu.Unlock()
			return fmt.Errorf("container missing")
		}
		a.OWASPProtection, a.WAFErrorPages, a.Updated = policy, pages, time.Now().UTC()
		d.Applications[b.TargetID] = a
		c.OWASPProtection, c.WAFErrorPages, c.Updated = policy, pages, time.Now().UTC()
		d.Containers[cid] = c
		_ = d.save()
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports, backendPort = c.Spec.Ports, 80
		}
		spec, workerID, owner = c.Spec, a.WorkerID, a.Owner
	} else {
		c, ok := d.Containers[b.TargetID]
		if !ok {
			d.mu.Unlock()
			return fmt.Errorf("container not found")
		}
		cid = b.TargetID
		c.OWASPProtection, c.WAFErrorPages, c.Updated = policy, pages, time.Now().UTC()
		d.Containers[cid] = c
		_ = d.save()
		ports, backendPort, _ = s.dockerWAFPortsFromSpec(c.Spec)
		if len(ports) == 0 {
			ports, backendPort = c.Spec.Ports, 80
		}
		spec, workerID, owner = c.Spec, c.WorkerID, c.Owner
	}
	d.mu.Unlock()
	_, err := s.dockerQueueWAF(workerID, owner, "waf-update", cid, &spec, ports, backendPort, policy, pages)
	return err
}

func (s *Server) deleteWAFBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	if b.Owner != "" && !canAccessApplication(r, b.Owner) {
		fail(w, 404, "binding not found")
		return
	}
	if b.State == "ACTIVE" || b.State == "ACTIVATING" {
		if err := s.deactivateWAFBindingLocked(&b); err != nil {
			fail(w, 409, err.Error())
			return
		}
		// Drop immediately; deactivate continues on the worker.
	}
	_ = s.waf.drop(id)
	s.Store.Audit(accountID(r), "waf.binding.delete", id, b.TargetKind+":"+b.TargetID)
	writeJSON(w, 200, map[string]any{"deleted": id})
}

func (s *Server) activateWAFBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	if err := s.activateWAFBindingLocked(&b); err != nil {
		b.State, b.Error, b.Updated = "FAILED", err.Error(), time.Now().UTC()
		_ = s.waf.put(b)
		fail(w, 409, err.Error())
		return
	}
	_ = s.waf.put(b)
	writeJSON(w, 202, b)
}

func (s *Server) deactivateWAFBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	if err := s.deactivateWAFBindingLocked(&b); err != nil {
		fail(w, 409, err.Error())
		return
	}
	_ = s.waf.put(b)
	writeJSON(w, 202, b)
}

func (s *Server) wafBindingCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	var in struct {
		Category string `json:"category"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil || wafCategories[in.Category] == "" {
		fail(w, 400, "category and enabled are required")
		return
	}
	if b.OWASPProtection == nil {
		b.OWASPProtection = wafDefaultPolicy()
	}
	b.OWASPProtection[in.Category] = *in.Enabled
	b.Updated = time.Now().UTC()
	if b.State == "ACTIVE" {
		if err := s.reapplyWAFBinding(b); err != nil {
			fail(w, 409, err.Error())
			return
		}
	}
	_ = s.waf.put(b)
	writeJSON(w, 202, map[string]any{"id": id, "category": in.Category, "enabled": *in.Enabled})
}

func (s *Server) wafBindingErrorPages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok {
		fail(w, 404, "binding not found")
		return
	}
	var in struct {
		Status string `json:"status"`
		HTML   string `json:"html"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWAFErrorPageBytes+4096)).Decode(&in); err != nil {
		fail(w, 400, "invalid JSON")
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
	if b.WAFErrorPages == nil {
		b.WAFErrorPages = map[string]string{}
	}
	if strings.TrimSpace(in.HTML) == "" {
		delete(b.WAFErrorPages, in.Status)
	} else {
		b.WAFErrorPages[in.Status] = in.HTML
	}
	b.Updated = time.Now().UTC()
	if b.State == "ACTIVE" {
		if err := s.reapplyWAFBinding(b); err != nil {
			fail(w, 409, err.Error())
			return
		}
	}
	_ = s.waf.put(b)
	writeJSON(w, 202, map[string]any{"id": id, "status": in.Status, "queued": true})
}

func (s *Server) getWAFBindingStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok || !canAccessApplication(r, b.Owner) {
		fail(w, 404, "binding not found")
		return
	}
	if b.Stats == nil {
		writeJSON(w, 200, map[string]any{"id": id, "stats": nil, "message": "No samples yet — open Refresh to pull edge logs."})
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "stats": b.Stats})
}

func (s *Server) refreshWAFBindingStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.waf.get(id)
	if !ok || !canAccessApplication(r, b.Owner) {
		fail(w, 404, "binding not found")
		return
	}
	j, err := s.queueWAFStatsJob(b)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"id": id, "job_id": j.ID, "queued": true})
}

func (s *Server) queueWAFStatsJob(b wafBinding) (dockerJob, error) {
	if s.docker == nil {
		return dockerJob{}, fmt.Errorf("docker product not available")
	}
	workerID, owner, targetID, mode, err := s.wafStatsJobParams(b)
	if err != nil {
		return dockerJob{}, err
	}
	if owner == "" {
		owner = b.Owner
	}
	op := dock.Operation{Action: "waf-stats", WAFTargetID: targetID, WAFMode: mode, LogTail: 4000}
	return s.queueDockerJob(workerID, owner, "waf-stats", op)
}

func (s *Server) wafStatsJobParams(b wafBinding) (workerID, owner, targetID, mode string, err error) {
	switch b.TargetKind {
	case "vps":
		inst, e := s.Store.GetInstance(b.TargetID)
		if e != nil {
			return "", "", "", "", fmt.Errorf("VPS not found")
		}
		return inst.WorkerID, inst.AccountID, b.TargetID, "vps", nil
	case "docker_app", "docker_container":
		if s.docker == nil {
			return "", "", "", "", fmt.Errorf("docker product not available")
		}
		d := s.docker
		d.mu.Lock()
		defer d.mu.Unlock()
		cid := b.TargetID
		if b.TargetKind == "docker_app" {
			a, ok := d.Applications[b.TargetID]
			if !ok {
				return "", "", "", "", fmt.Errorf("application not found")
			}
			if len(a.ContainerIDs) == 0 {
				return "", "", "", "", fmt.Errorf("application has no container")
			}
			cid = a.ContainerIDs[0]
			return a.WorkerID, a.Owner, cid, "", nil
		}
		c, ok := d.Containers[cid]
		if !ok {
			return "", "", "", "", fmt.Errorf("container not found")
		}
		return c.WorkerID, c.Owner, cid, "", nil
	default:
		return "", "", "", "", fmt.Errorf("unsupported target kind")
	}
}

func (s *Server) applyWAFStatsJobResult(targetID, mode string, data json.RawMessage, ok bool, errMsg string) {
	if s.waf == nil || targetID == "" || !ok || len(data) == 0 {
		return
	}
	var stats dock.WAFStats
	if json.Unmarshal(data, &stats) != nil {
		return
	}
	kind := "vps"
	if mode != "vps" {
		// Docker edge: binding may be on app or container id; match either.
		for _, b := range s.waf.list() {
			if b.TargetKind == "vps" {
				continue
			}
			match := b.TargetID == targetID
			if !match && b.TargetKind == "docker_app" && s.docker != nil {
				s.docker.mu.Lock()
				if a, ok := s.docker.Applications[b.TargetID]; ok {
					for _, cid := range a.ContainerIDs {
						if cid == targetID {
							match = true
							break
						}
					}
				}
				s.docker.mu.Unlock()
			}
			if match {
				b.Stats = &stats
				b.Updated = time.Now().UTC()
				_ = s.waf.put(b)
				return
			}
		}
		return
	}
	b, found := s.waf.findByTarget(kind, targetID)
	if !found {
		return
	}
	b.Stats = &stats
	if errMsg != "" && stats.Error == "" {
		stats.Error = errMsg
		b.Stats = &stats
	}
	b.Updated = time.Now().UTC()
	_ = s.waf.put(b)
}
