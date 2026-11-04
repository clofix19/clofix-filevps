package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

// securityCenterStore persists Security Modes, detections, FIM, incidents, and AutoPilot state.
type securityCenterStore struct {
	sync.Mutex
	Policies   map[string]securityPolicy   `json:"policies"`
	Bindings   map[string]securityBinding  `json:"bindings"`
	Detections []securityDetection         `json:"detections"`
	FIM        map[string]fimBaseline      `json:"fim"`
	Exceptions map[string]vulnException    `json:"exceptions"`
	Incidents  map[string]securityIncident `json:"incidents"`
	AutoPilot  autoPilotConfig             `json:"autopilot"`
	Unlocks    map[string]protectionUnlock   `json:"unlocks"`
	Approvals  map[string]protectionApproval `json:"approvals"`
	Rollbacks  map[string]protectionRollback `json:"rollbacks"`
	file       string
}

type securityPolicy struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Template    string         `json:"template"`
	Version     int            `json:"version"`
	Controls    map[string]any `json:"controls"`
	Description string         `json:"description,omitempty"`
	Created     time.Time      `json:"created"`
	Updated     time.Time      `json:"updated"`
}

type securityBinding struct {
	ID       string    `json:"id"`
	PolicyID string    `json:"policy_id"`
	Scope    string    `json:"scope"` // account|project|worker|vps
	TargetID string    `json:"target_id"`
	Schedule string    `json:"schedule,omitempty"`
	Created  time.Time `json:"created"`
}

type securityDetection struct {
	ID         string    `json:"id"`
	Tenant     string    `json:"tenant,omitempty"`
	Asset      string    `json:"asset"`
	SourceIP   string    `json:"source_ip,omitempty"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	Evidence   string    `json:"evidence,omitempty"`
	Confidence float64   `json:"confidence"`
	Rule       string    `json:"rule,omitempty"`
	Response   string    `json:"response,omitempty"`
	At         time.Time `json:"at"`
}

type fimBaseline struct {
	WorkerID string            `json:"worker_id"`
	Paths    map[string]string `json:"paths"` // path -> sha256
	Updated  time.Time         `json:"updated"`
}

type vulnException struct {
	ID       string    `json:"id"`
	CVE      string    `json:"cve"`
	Asset    string    `json:"asset"`
	Reason   string    `json:"reason"`
	Expires  time.Time `json:"expires"`
	Created  time.Time `json:"created"`
	CreatedBy string   `json:"created_by,omitempty"`
}

type securityIncident struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"` // open|investigating|resolved
	Assignee   string    `json:"assignee,omitempty"`
	Assets     []string  `json:"assets,omitempty"`
	Evidence   []string  `json:"evidence,omitempty"`
	Playbook   string    `json:"playbook,omitempty"`
	DetectionIDs []string `json:"detection_ids,omitempty"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

type autoPilotConfig struct {
	Mode      string    `json:"mode"` // monitor|recommend|approval|auto_protect|emergency
	Updated   time.Time `json:"updated"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	Expires   time.Time `json:"expires,omitempty"` // emergency lockdown end
	Notes     string    `json:"notes,omitempty"`
}

func loadSecurityCenter(dir string) *securityCenterStore {
	d := &securityCenterStore{
		file:       filepath.Join(dir, "security-center.json"),
		Policies:   map[string]securityPolicy{},
		Bindings:   map[string]securityBinding{},
		Detections: []securityDetection{},
		FIM:        map[string]fimBaseline{},
		Exceptions: map[string]vulnException{},
		Incidents:  map[string]securityIncident{},
		AutoPilot:  autoPilotConfig{Mode: "monitor"},
		Unlocks:    map[string]protectionUnlock{},
		Approvals:  map[string]protectionApproval{},
		Rollbacks:  map[string]protectionRollback{},
	}
	if raw, err := os.ReadFile(d.file); err == nil {
		_ = json.Unmarshal(raw, d)
	}
	if d.Policies == nil {
		d.Policies = map[string]securityPolicy{}
	}
	if d.Bindings == nil {
		d.Bindings = map[string]securityBinding{}
	}
	if d.FIM == nil {
		d.FIM = map[string]fimBaseline{}
	}
	if d.Exceptions == nil {
		d.Exceptions = map[string]vulnException{}
	}
	if d.Incidents == nil {
		d.Incidents = map[string]securityIncident{}
	}
	if d.Unlocks == nil {
		d.Unlocks = map[string]protectionUnlock{}
	}
	if d.Approvals == nil {
		d.Approvals = map[string]protectionApproval{}
	}
	if d.Rollbacks == nil {
		d.Rollbacks = map[string]protectionRollback{}
	}
	if d.AutoPilot.Mode == "" {
		d.AutoPilot.Mode = "monitor"
	}
	d.seedTemplates()
	return d
}

func (d *securityCenterStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.file + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.file)
}

func securityModeTemplates() []map[string]any {
	return []map[string]any{
		{"id": "standard", "name": "Standard", "description": "Everyday secure baseline: read-only guest root, SSH allowed, VPS can still be deleted from admin.", "controls": map[string]any{"protection_mode": true, "delete_protection": false, "ssh_login_blocked": false}},
		{"id": "hardened", "name": "Hardened", "description": "Stronger posture: read-only root plus delete lock. SSH still allowed for admin work.", "controls": map[string]any{"protection_mode": true, "delete_protection": true, "ssh_login_blocked": false, "firewall_cidr": "planned", "waf": "planned"}},
		{"id": "lockdown", "name": "Lockdown", "description": "Incident lock: read-only root, new SSH logins blocked, VPS delete blocked.", "controls": map[string]any{"protection_mode": true, "delete_protection": true, "ssh_login_blocked": true, "firewall_cidr": "planned"}},
		{"id": "maintenance", "name": "Maintenance", "description": "Change window: Protection off so packages/WAF can be installed; SSH allowed.", "controls": map[string]any{"protection_mode": false, "ssh_login_blocked": false}},
		{"id": "web_protection", "name": "Web Block", "description": "Keeps Protection on for disk integrity. Install OWASP CRS WAF separately to block SQLi/XSS/RCE over HTTP.", "controls": map[string]any{"protection_mode": true, "waf": "planned"}},
	}
}

func (d *securityCenterStore) seedTemplates() {
	// Lockdown covers Quarantine's live controls; Database Protection belongs under Databases.
	changed := false
	for _, id := range []string{"tpl-quarantine", "tpl-database_protection"} {
		if _, ok := d.Policies[id]; ok {
			delete(d.Policies, id)
			changed = true
		}
	}
	if changed {
		_ = d.save()
	}
	for _, t := range securityModeTemplates() {
		id, _ := t["id"].(string)
		name, _ := t["name"].(string)
		controls, _ := t["controls"].(map[string]any)
		key := "tpl-" + id
		desc, _ := t["description"].(string)
		if desc == "" {
			desc = "Built-in Security Mode template"
		}
		if existing, ok := d.Policies[key]; ok {
			if existing.Name != name || existing.Description != desc {
				existing.Name = name
				existing.Description = desc
				existing.Updated = time.Now().UTC()
				d.Policies[key] = existing
				_ = d.save()
			}
			continue
		}
		now := time.Now().UTC()
		d.Policies[key] = securityPolicy{
			ID: key, Name: name, Template: id, Version: 1, Controls: controls,
			Description: desc, Created: now, Updated: now,
		}
	}
}

func (s *Server) securityCenterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/security/modes", s.auth(s.listSecurityModes))
	mux.HandleFunc("POST /api/security/modes", s.auth(s.saveSecurityMode))
	mux.HandleFunc("POST /api/security/modes/{id}/dry-run", s.auth(s.dryRunSecurityMode))
	mux.HandleFunc("POST /api/security/modes/{id}/apply", s.auth(s.applySecurityMode))
	mux.HandleFunc("GET /api/security/bindings", s.auth(s.listSecurityBindings))
	mux.HandleFunc("POST /api/security/bindings", s.auth(s.saveSecurityBinding))
	mux.HandleFunc("DELETE /api/security/bindings/{id}", s.auth(s.deleteSecurityBinding))
	mux.HandleFunc("GET /api/security/detections", s.auth(s.listSecurityDetections))
	mux.HandleFunc("POST /api/security/detections/refresh", s.auth(s.refreshSecurityDetections))
	mux.HandleFunc("GET /api/security/fim", s.auth(s.listFIMBaselines))
	mux.HandleFunc("POST /api/security/fim/{workerID}", s.auth(s.upsertFIMBaseline))
	mux.HandleFunc("GET /api/security/vulnerabilities", s.auth(s.getSecurityVulnerabilities))
	mux.HandleFunc("POST /api/security/vulnerabilities/exceptions", s.auth(s.saveVulnException))
	mux.HandleFunc("GET /api/security/incidents", s.auth(s.listSecurityIncidents))
	mux.HandleFunc("POST /api/security/incidents", s.auth(s.saveSecurityIncident))
	mux.HandleFunc("PUT /api/security/incidents/{id}", s.auth(s.saveSecurityIncident))
	mux.HandleFunc("GET /api/security/autopilot", s.auth(s.getAutoPilot))
	mux.HandleFunc("PUT /api/security/autopilot", s.auth(s.putAutoPilot))
	mux.HandleFunc("GET /api/security/compliance", s.auth(s.getSecurityCompliance))
}

func (s *Server) listSecurityModes(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]securityPolicy, 0, len(d.Policies))
	for _, p := range d.Policies {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, 200, map[string]any{
		"templates": securityModeTemplates(),
		"policies":  list,
		"supported_controls": supportedModeControls(),
		"planned_controls":   []string{"firewall_cidr", "waf"},
	})
}

func (s *Server) saveSecurityMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID          string         `json:"id"`
		Name        string         `json:"name"`
		Template    string         `json:"template"`
		Controls    map[string]any `json:"controls"`
		Description string         `json:"description"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(w, 400, "name and controls are required")
		return
	}
	if in.Controls == nil {
		in.Controls = map[string]any{}
	}
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	now := time.Now().UTC()
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "pol-" + randHex(6)
	}
	existing, ok := d.Policies[id]
	version := 1
	created := now
	if ok {
		version = existing.Version + 1
		created = existing.Created
	}
	pol := securityPolicy{
		ID: id, Name: strings.TrimSpace(in.Name), Template: in.Template, Version: version,
		Controls: in.Controls, Description: in.Description, Created: created, Updated: now,
	}
	d.Policies[id] = pol
	_ = d.save()
	s.Store.Audit(principalFrom(r).Name, "security.mode.save", id, "ok")
	writeJSON(w, 200, pol)
}

func (s *Server) modeChangePlan(policy securityPolicy, targetIDs []string) []map[string]any {
	changes := []map[string]any{}
	for _, id := range targetIDs {
		inst, err := s.Store.GetInstance(id)
		if err != nil {
			changes = append(changes, map[string]any{"instance_id": id, "error": "not found"})
			continue
		}
		for key, raw := range policy.Controls {
			if !isSupportedModeControl(key) {
				changes = append(changes, map[string]any{
					"instance_id": id, "control": key, "status": "planned",
					"note": "Control is labeled planned and will not be applied",
				})
				continue
			}
			want, ok := raw.(bool)
			if !ok {
				changes = append(changes, map[string]any{"instance_id": id, "control": key, "status": "skipped", "note": "expected boolean"})
				continue
			}
			cur := currentControlBool(inst, key)
			changes = append(changes, map[string]any{
				"instance_id": id, "control": key, "from": cur, "to": want,
				"status": "ready", "changed": cur != want,
			})
		}
	}
	return changes
}

func currentControlBool(inst store.Instance, key string) bool {
	switch key {
	case "protection_mode":
		return inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode
	case "ssh_login_blocked":
		return inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked
	case "delete_protection":
		return inst.Spec.DeleteProtection
	}
	return false
}

func (s *Server) dryRunSecurityMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		InstanceIDs []string `json:"instance_ids"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in)
	d := s.securityCenter
	d.Lock()
	pol, ok := d.Policies[id]
	d.Unlock()
	if !ok {
		fail(w, 404, "policy not found")
		return
	}
	if len(in.InstanceIDs) == 0 {
		fail(w, 400, "instance_ids required")
		return
	}
	writeJSON(w, 200, map[string]any{"policy_id": id, "dry_run": true, "changes": s.modeChangePlan(pol, in.InstanceIDs)})
}

func (s *Server) applySecurityMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		InstanceIDs []string `json:"instance_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || len(in.InstanceIDs) == 0 {
		fail(w, 400, "instance_ids required")
		return
	}
	d := s.securityCenter
	d.Lock()
	pol, ok := d.Policies[id]
	d.Unlock()
	if !ok {
		fail(w, 404, "policy not found")
		return
	}
	if s.requiresApproval() && id != "" {
		fail(w, 403, "AutoPilot approval mode: submit type=mode_apply approval")
		return
	}
	actor := principalFrom(r).Name
	for _, instID := range in.InstanceIDs {
		s.saveRollbackSnapshot(instID, id, actor, pol.Version, pol.Controls)
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	plan := s.modeChangePlan(pol, in.InstanceIDs)
	applied := []map[string]any{}
	for _, step := range plan {
		if step["status"] != "ready" || step["changed"] != true {
			applied = append(applied, step)
			continue
		}
		instID, _ := step["instance_id"].(string)
		control, _ := step["control"].(string)
		want, _ := step["to"].(bool)
		inst, err := s.Store.GetInstance(instID)
		if err != nil {
			step["status"] = "error"
			step["error"] = "not found"
			applied = append(applied, step)
			continue
		}
		previous := inst
		switch control {
		case "protection_mode":
			inst.Spec.ProtectionMode = &want
		case "ssh_login_blocked":
			inst.Spec.SSHLoginBlocked = &want
		case "delete_protection":
			inst.Spec.DeleteProtection = want
		}
		if err := s.Store.SaveInstance(inst); err != nil {
			step["status"] = "error"
			step["error"] = err.Error()
			applied = append(applied, step)
			continue
		}
		spec := inst.Spec
		action := ""
		switch control {
		case "protection_mode":
			action = proto.ActionUpdateProtection
		case "ssh_login_blocked":
			action = proto.ActionUpdateSSHLogin
		case "delete_protection":
			action = proto.ActionUpdateDeleteProtection
		}
		if action != "" {
			if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
				ID: inst.ID + "/" + action + "/" + randHex(4), Action: action, InstanceID: inst.ID, Spec: &spec,
			}); err != nil {
				_ = s.Store.SaveInstance(previous)
				step["status"] = "error"
				step["error"] = "queue failed"
				applied = append(applied, step)
				continue
			}
		}
		step["status"] = "applied"
		applied = append(applied, step)
	}
	s.Store.Audit(principalFrom(r).Name, "security.mode.apply", id, "ok")
	writeJSON(w, 200, map[string]any{"policy_id": id, "dry_run": false, "changes": applied})
}

func (s *Server) listSecurityBindings(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]securityBinding, 0, len(d.Bindings))
	for _, b := range d.Bindings {
		list = append(list, b)
	}
	writeJSON(w, 200, map[string]any{"bindings": list})
}

func (s *Server) saveSecurityBinding(w http.ResponseWriter, r *http.Request) {
	var in securityBinding
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil || in.PolicyID == "" || in.TargetID == "" {
		fail(w, 400, "policy_id, scope, and target_id are required")
		return
	}
	if in.Scope == "" {
		in.Scope = "vps"
	}
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	if _, ok := d.Policies[in.PolicyID]; !ok {
		fail(w, 404, "policy not found")
		return
	}
	if in.ID == "" {
		in.ID = "bind-" + randHex(6)
	}
	in.Created = time.Now().UTC()
	d.Bindings[in.ID] = in
	_ = d.save()
	writeJSON(w, 200, in)
}

func (s *Server) deleteSecurityBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	if _, ok := d.Bindings[id]; !ok {
		fail(w, 404, "binding not found")
		return
	}
	delete(d.Bindings, id)
	_ = d.save()
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) listSecurityDetections(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	events := append([]securityDetection(nil), d.Detections...)
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > limit {
		events = events[:limit]
	}
	writeJSON(w, 200, map[string]any{"detections": events})
}

func (s *Server) refreshSecurityDetections(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	fresh := []securityDetection{}
	for _, ev := range s.recentLogAlertEvents(24 * time.Hour) {
		fresh = append(fresh, securityDetection{
			ID: "det-" + ev.ID, Asset: ev.SourceID, Type: "log_alert", Severity: "medium",
			Evidence: ev.Sample, Confidence: 0.7, Rule: ev.Name, Response: "monitor", At: ev.At,
		})
	}
	// Controller login failures are not separately stored; surface inactive workers as signal.
	workers, _ := s.Store.ListWorkers()
	for _, wk := range workers {
		if wk.Status != "active" && wk.Status != "" {
			fresh = append(fresh, securityDetection{
				ID: "det-worker-" + wk.ID, Asset: wk.ID, Type: "worker_inactive", Severity: "low",
				Evidence: "status=" + wk.Status, Confidence: 1, Rule: "worker.status", Response: "monitor", At: now,
			})
		}
	}
	// Brute-force class: log alert names/samples mentioning auth/ssh/fail.
	for _, ev := range s.recentLogAlertEvents(24 * time.Hour) {
		blob := strings.ToLower(ev.Name + " " + ev.Sample)
		if strings.Contains(blob, "ssh") || strings.Contains(blob, "auth") || strings.Contains(blob, "fail") || strings.Contains(blob, "invalid user") {
			fresh = append(fresh, securityDetection{
				ID: "det-bruteforce-" + ev.ID, Asset: ev.SourceID, Type: "ssh_bruteforce", Severity: "high",
				Evidence: ev.Sample, Confidence: 0.75, Rule: "access.ssh_bruteforce", Response: "recommend",
				SourceIP: "", At: ev.At,
			})
		}
	}
	d := s.securityCenter
	d.Lock()
	// Keep FIM / prior high-signal events; replace only refreshable sources.
	kept := make([]securityDetection, 0, len(d.Detections))
	for _, ev := range d.Detections {
		if ev.Type == "fim_change" {
			kept = append(kept, ev)
		}
	}
	d.Detections = append(kept, fresh...)
	if len(d.Detections) > 200 {
		d.Detections = d.Detections[len(d.Detections)-200:]
	}
	_ = d.save()
	out := append([]securityDetection{}, d.Detections...)
	d.Unlock()
	s.Store.Audit(principalFrom(r).Name, "security.detections.refresh", "fleet", "ok")
	writeJSON(w, 200, map[string]any{"detections": out, "refreshed_at": now})
}

func (s *Server) listFIMBaselines(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]fimBaseline, 0, len(d.FIM))
	for _, b := range d.FIM {
		list = append(list, b)
	}
	writeJSON(w, 200, map[string]any{
		"baselines": list,
		"note":      "Worker reports path hashes; write-protection only where mount/LSM already enforces.",
	})
}

func (s *Server) upsertFIMBaseline(w http.ResponseWriter, r *http.Request) {
	workerID := r.PathValue("workerID")
	var in struct {
		Paths map[string]string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || len(in.Paths) == 0 {
		fail(w, 400, "paths map required")
		return
	}
	if _, err := s.Store.GetWorker(workerID); err != nil {
		fail(w, 404, "worker not found")
		return
	}
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	prev := d.FIM[workerID]
	changes := []map[string]string{}
	for path, hash := range in.Paths {
		if prev.Paths != nil {
			if old, ok := prev.Paths[path]; ok && old != hash {
				changes = append(changes, map[string]string{"path": path, "from": old, "to": hash})
			}
		}
	}
	d.FIM[workerID] = fimBaseline{WorkerID: workerID, Paths: in.Paths, Updated: time.Now().UTC()}
	_ = d.save()
	if len(changes) > 0 {
		d.Detections = append(d.Detections, securityDetection{
			ID: "det-fim-" + randHex(6), Asset: workerID, Type: "fim_change", Severity: "high",
			Evidence: strconv.Itoa(len(changes)) + " path(s) changed", Confidence: 0.95,
			Rule: "fim.baseline", Response: "recommend", At: time.Now().UTC(),
		})
		_ = d.save()
	}
	writeJSON(w, 200, map[string]any{"baseline": d.FIM[workerID], "changes": changes})
}

func (s *Server) getSecurityVulnerabilities(w http.ResponseWriter, r *http.Request) {
	dbScore, dbFindings := s.databaseSecurityPanel()
	cksScore, cksFindings := s.cksSecurityPanel()
	d := s.securityCenter
	d.Lock()
	ex := make([]vulnException, 0, len(d.Exceptions))
	now := time.Now()
	for _, e := range d.Exceptions {
		if e.Expires.IsZero() || e.Expires.After(now) {
			ex = append(ex, e)
		}
	}
	d.Unlock()
	writeJSON(w, 200, map[string]any{
		"packages": map[string]any{
			"status":  "planned",
			"note":    "OSV/NVD package and kernel CVE feeds land with worker inventory exporters.",
			"score":   nil,
			"findings": []any{},
		},
		"databases": map[string]any{
			"score":    dbScore,
			"findings": dbFindings,
		},
		"cks": map[string]any{
			"score":               cksScore,
			"findings":            cksFindings,
			"cks_readiness_status": s.cksReadinessStatus(),
		},
		"exceptions": ex,
	})
}

func (s *Server) databaseSecurityPanel() (int, []securityFinding) {
	findings := []securityFinding{}
	score := 100
	if s.databases == nil {
		return score, findings
	}
	s.databases.Lock()
	defer s.databases.Unlock()
	for id, p := range s.databases.Projects {
		if strings.TrimSpace(p.CA) == "" {
			findings = append(findings, securityFinding{ID: "db-tls:" + id, Scope: "database", Asset: id, Delta: -10, Reason: "Managed DB project missing TLS CA"})
			score -= 10
		}
		host := strings.TrimSpace(p.Spec.Host)
		if host != "" && host != "127.0.0.1" && host != "localhost" && !strings.HasPrefix(host, "10.") && !strings.HasPrefix(host, "172.") {
			findings = append(findings, securityFinding{ID: "db-public:" + id, Scope: "database", Asset: id, Delta: -8, Reason: "Managed DB project host looks publicly reachable (" + host + ")"})
			score -= 8
		}
	}
	readyEncrypted := 0
	for id, b := range s.databases.Backups {
		if len(b.EncryptedKey) > 0 && (b.State == "READY" || b.State == "ready") {
			readyEncrypted++
		} else if b.State == "READY" || b.State == "ready" {
			findings = append(findings, securityFinding{ID: "db-backup:" + id, Scope: "database", Asset: id, Delta: -5, Reason: "DB backup READY without encrypted key metadata"})
			score -= 5
		}
	}
	_ = readyEncrypted
	if score < 0 {
		score = 0
	}
	return score, findings
}

func (s *Server) cksSecurityPanel() (int, []securityFinding) {
	findings := []securityFinding{}
	score := 100
	if s.kubernetes == nil {
		return score, findings
	}
	s.kubernetes.Lock()
	defer s.kubernetes.Unlock()
	for id, c := range s.kubernetes.Clusters {
		st := strings.ToLower(c.State)
		if st != "ready" && st != "running" {
			findings = append(findings, securityFinding{ID: "cks-state:" + id, Scope: "cks", Asset: id, Delta: -5, Reason: "CKS cluster state is " + c.State})
			score -= 5
		}
		// RBAC/PSS/NetworkPolicy inventory is not yet exported from workers — mark planned gaps.
		findings = append(findings, securityFinding{ID: "cks-pss:" + id, Scope: "cks", Asset: id, Delta: -3, Reason: "Pod Security Standards inventory not yet reported (planned)"})
		score -= 3
		findings = append(findings, securityFinding{ID: "cks-netpol:" + id, Scope: "cks", Asset: id, Delta: -3, Reason: "NetworkPolicy inventory not yet reported (planned)"})
		score -= 3
	}
	if score < 0 {
		score = 0
	}
	return score, findings
}

func (s *Server) cksReadinessStatus() map[string]any {
	status := map[string]any{
		"ha_validated": false,
		"note":         "CKS Readiness Status: HA not fully validated",
		"clusters":     0,
		"ready":        0,
		"ha_requested": 0,
	}
	if s.kubernetes == nil {
		return status
	}
	s.kubernetes.Lock()
	defer s.kubernetes.Unlock()
	status["clusters"] = len(s.kubernetes.Clusters)
	ready, haReq := 0, 0
	for _, c := range s.kubernetes.Clusters {
		if c.HA {
			haReq++
		}
		st := strings.ToLower(c.State)
		if st == "ready" || st == "running" {
			ready++
		}
	}
	status["ready"] = ready
	status["ha_requested"] = haReq
	// HA topology may be requested, but full multi-control-plane validation is not claimed.
	status["ha_validated"] = false
	return status
}

func (s *Server) saveVulnException(w http.ResponseWriter, r *http.Request) {
	var in vulnException
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil || in.CVE == "" || in.Asset == "" {
		fail(w, 400, "cve and asset are required")
		return
	}
	if in.Expires.IsZero() {
		in.Expires = time.Now().Add(30 * 24 * time.Hour)
	}
	in.ID = "vex-" + randHex(6)
	in.Created = time.Now().UTC()
	in.CreatedBy = principalFrom(r).Name
	d := s.securityCenter
	d.Lock()
	d.Exceptions[in.ID] = in
	_ = d.save()
	d.Unlock()
	writeJSON(w, 200, in)
}

func (s *Server) listSecurityIncidents(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]securityIncident, 0, len(d.Incidents))
	for _, i := range d.Incidents {
		list = append(list, i)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Updated.After(list[j].Updated) })
	writeJSON(w, 200, map[string]any{"incidents": list})
}

func (s *Server) saveSecurityIncident(w http.ResponseWriter, r *http.Request) {
	var in securityIncident
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" {
		fail(w, 400, "title is required")
		return
	}
	pathID := r.PathValue("id")
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	now := time.Now().UTC()
	if pathID != "" {
		in.ID = pathID
	}
	if in.ID == "" {
		in.ID = "inc-" + randHex(6)
		in.Created = now
	} else if existing, ok := d.Incidents[in.ID]; ok {
		in.Created = existing.Created
	} else {
		in.Created = now
	}
	if in.Status == "" {
		in.Status = "open"
	}
	if in.Severity == "" {
		in.Severity = "medium"
	}
	in.Updated = now
	d.Incidents[in.ID] = in
	_ = d.save()
	writeJSON(w, 200, in)
}

func (s *Server) getAutoPilot(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, map[string]any{
		"autopilot": d.AutoPilot,
		"modes": []map[string]string{
			{"id": "monitor", "label": "Monitor Only"},
			{"id": "recommend", "label": "Recommend"},
			{"id": "approval", "label": "Approval Required"},
			{"id": "auto_protect", "label": "Auto Protect (allowlisted low-risk)"},
			{"id": "emergency", "label": "Emergency Lockdown (time-limited)"},
		},
		"guardrails": []string{
			"Never auto-shutdown production on low-confidence detections",
			"Auto Protect only applies allowlisted low-risk controls",
			"Emergency Lockdown requires an expiry timestamp",
		},
	})
}

func (s *Server) putAutoPilot(w http.ResponseWriter, r *http.Request) {
	var in autoPilotConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		fail(w, 400, "invalid autopilot config")
		return
	}
	allowed := map[string]bool{"monitor": true, "recommend": true, "approval": true, "auto_protect": true, "emergency": true}
	if !allowed[in.Mode] {
		fail(w, 400, "invalid mode")
		return
	}
	if in.Mode == "emergency" && (in.Expires.IsZero() || !in.Expires.After(time.Now())) {
		fail(w, 400, "emergency lockdown requires a future expires timestamp")
		return
	}
	in.Updated = time.Now().UTC()
	in.UpdatedBy = principalFrom(r).Name
	d := s.securityCenter
	d.Lock()
	d.AutoPilot = in
	_ = d.save()
	d.Unlock()
	s.Store.Audit(in.UpdatedBy, "security.autopilot", in.Mode, "ok")
	writeJSON(w, 200, in)
}

func (s *Server) getSecurityCompliance(w http.ResponseWriter, r *http.Request) {
	overview := computeSecurityScore
	instances, _ := s.Store.ListInstances()
	workers, _ := s.Store.ListWorkers()
	s.usageMu.RLock()
	installs := map[string]map[string]string{}
	for id, m := range s.serviceInstalls {
		cp := map[string]string{}
		for k, v := range m {
			cp[k] = v
		}
		installs[id] = cp
	}
	s.usageMu.RUnlock()
	score := overview(instances, workers, installs, s.recentLogAlertEvents(24*time.Hour))

	controls := []map[string]any{
		{"framework": "CIS", "id": "cis-protection", "title": "Filesystem Protection Mode", "status": passFail(score.Score >= 80 || len(instances) == 0), "detail": "Protection Mode coverage"},
		{"framework": "CIS", "id": "cis-waf", "title": "HTTP WAF on public web", "status": "N/A", "detail": "Applies when HTTP is exposed; see findings"},
		{"framework": "ISO", "id": "iso-audit", "title": "Audit logging available", "status": "passed", "detail": "GET /api/audit"},
		{"framework": "ISO", "id": "iso-access", "title": "IAM MFA available", "status": "passed", "detail": "Profile MFA endpoints"},
		{"framework": "SOC2", "id": "soc2-change", "title": "Change audit trail", "status": "passed", "detail": "cf_audit writes on mutations"},
		{"framework": "SOC2", "id": "soc2-incident", "title": "Incident tracking", "status": passFail(true), "detail": "Security Center incidents panel"},
		{"framework": "SOC2", "id": "soc2-cert", "title": "Certification claim", "status": "N/A", "detail": "No certification claims are made"},
	}
	// Refine WAF control from live inventory.
	needWAF, haveWAF := 0, 0
	for _, inst := range instances {
		needWAF++
		if installs[inst.ID]["owasp-waf"] == "INSTALLED" {
			haveWAF++
		}
	}
	if needWAF == 0 {
		controls[1]["status"] = "N/A"
	} else if haveWAF == needWAF {
		controls[1]["status"] = "passed"
	} else {
		controls[1]["status"] = "failed"
		controls[1]["detail"] = strconv.Itoa(haveWAF) + "/" + strconv.Itoa(needWAF) + " VPS have WAF"
	}

	format := r.URL.Query().Get("format")
	payload := map[string]any{
		"generated_at": time.Now().UTC(),
		"score":        score.Score,
		"controls":     controls,
		"retention":    "Audit events retained in cf_audit / security-center.json per controller data directory policy",
		"disclaimer":   "This export is a control mapping aid only. It is not a CIS, ISO, or SOC2 certification.",
	}
	if format == "export" {
		w.Header().Set("Content-Disposition", `attachment; filename="clofix-security-compliance.json"`)
	}
	writeJSON(w, 200, payload)
}

func passFail(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}
