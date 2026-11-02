package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

var (
	logCustomID = regexp.MustCompile(`^log-[0-9a-f]{16}$`)
	logAlertID  = regexp.MustCompile(`^lga-[0-9a-f]{16}$`)
	logKinds    = map[string]bool{"vps": true, "lb": true, "database": true, "managed-app": true, "worker": true}
)

type logSource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Path       string `json:"path"`
}
type logAlert struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	SourceID        string    `json:"source_id"`
	Match           string    `json:"match"`
	Level           string    `json:"level,omitempty"`
	Enabled         bool      `json:"enabled"`
	Threshold       int       `json:"threshold"`
	CooldownSeconds int       `json:"cooldown_seconds"`
	Hits            int       `json:"hits,omitempty"`
	LastFired       time.Time `json:"last_fired,omitempty"`
}
type logAlertEvent struct {
	ID         string                   `json:"id"`
	AlertID    string                   `json:"alert_id"`
	SourceID   string                   `json:"source_id"`
	Name       string                   `json:"name"`
	Sample     string                   `json:"sample"`
	Status     string                   `json:"status"`
	At         time.Time                `json:"at"`
	Deliveries map[string]alertDelivery `json:"deliveries"`
}
type logTailCache struct {
	Text    string
	Error   string
	Updated time.Time
}
type logStore struct {
	sync.Mutex
	Sources map[string]logSource `json:"sources"`
	Alerts  map[string]logAlert  `json:"alerts"`
	Events  []logAlertEvent      `json:"events"`
	seen    map[string]string
	tails   map[string]logTailCache
	file    string
}

func loadLogs(dir string) *logStore {
	d := &logStore{file: filepath.Join(dir, "logs.json"), seen: map[string]string{}, tails: map[string]logTailCache{}}
	if raw, e := os.ReadFile(d.file); e == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid logs.json")
		}
	}
	if d.Sources == nil {
		d.Sources = map[string]logSource{}
	}
	if d.Alerts == nil {
		d.Alerts = map[string]logAlert{}
	}
	if d.seen == nil {
		d.seen = map[string]string{}
	}
	if d.tails == nil {
		d.tails = map[string]logTailCache{}
	}
	return d
}
func (d *logStore) save() error {
	if e := os.MkdirAll(filepath.Dir(d.file), 0700); e != nil {
		return e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if e = os.WriteFile(d.file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) logRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/logs", s.auth(s.getLogs))
	m.HandleFunc("GET /api/logs/analysis", s.auth(s.getLogAnalysis))
	m.HandleFunc("GET /api/logs/alert-events", s.auth(s.getLogAlertEvents))
	m.HandleFunc("GET /api/logs/{id}/tail", s.auth(s.getLogTail))
	m.HandleFunc("GET /api/logs/{id}/download", s.auth(s.downloadLogTail))
	m.HandleFunc("POST /api/logs/sources", s.auth(s.saveLogSource))
	m.HandleFunc("DELETE /api/logs/sources/{id}", s.auth(s.deleteLogSource))
	m.HandleFunc("POST /api/logs/alerts", s.auth(s.saveLogAlert))
	m.HandleFunc("PUT /api/logs/alerts/{id}", s.auth(s.saveLogAlert))
	m.HandleFunc("DELETE /api/logs/alerts/{id}", s.auth(s.deleteLogAlert))
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	sources := s.logCatalog()
	d := s.logs
	d.Lock()
	alerts := make([]logAlert, 0, len(d.Alerts))
	for _, a := range d.Alerts {
		alerts = append(alerts, a)
	}
	d.Unlock()
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Name < alerts[j].Name })
	writeJSON(w, 200, map[string]any{
		"sources":   sources,
		"alerts":    alerts,
		"resources": s.logResources(),
		"channels":  s.enabledLogChannels(),
	})
}

func (s *Server) enabledLogChannels() []map[string]any {
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	out := []map[string]any{}
	for _, c := range d.Channels {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name, "type": c.Type, "enabled": c.Enabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

func (s *Server) logCatalog() []map[string]any {
	out := []map[string]any{}
	instances, _ := s.Store.ListInstances()
	s.usageMu.RLock()
	for _, inst := range instances {
		out = append(out, logCatalogItem("vps:"+inst.ID, inst.ID+" instance log", "vps", inst.ID, "instance.log", "auto"))
		for _, svc := range s.services[inst.ID] {
			out = append(out, logCatalogItem("vps:"+inst.ID+":svc:"+svc.Name, inst.ID+" · "+svc.Name, "vps", inst.ID, svc.Name, "auto"))
		}
		out = append(out, logCatalogItem("vps:"+inst.ID+":waf", inst.ID+" · WAF", "vps", inst.ID, "waf", "auto"))
	}
	s.usageMu.RUnlock()
	if s.applications != nil {
		s.applications.Lock()
		for _, a := range s.applications.Managed {
			out = append(out, logCatalogItem("app:"+a.ID, a.Name, "managed-app", a.ID, "app.log", "auto"))
		}
		runs := make([]applicationRun, 0, len(s.applications.Runs))
		for _, run := range s.applications.Runs {
			runs = append(runs, run)
		}
		s.applications.Unlock()
		sort.Slice(runs, func(i, j int) bool { return runs[i].Created.After(runs[j].Created) })
		if len(runs) > 20 {
			runs = runs[:20]
		}
		for _, run := range runs {
			out = append(out, logCatalogItem("run:"+run.ID, run.AppID+" run", "run", run.ID, "stdout", "auto"))
		}
	}
	d := s.logs
	d.Lock()
	for _, src := range d.Sources {
		item := logCatalogItem(src.ID, src.Name, src.Kind, src.ResourceID, src.Path, "custom")
		if cached, ok := d.tails[src.ID]; ok {
			item["state"] = "ready"
			item["updated"] = cached.Updated
			if cached.Error != "" {
				item["error"] = cached.Error
				item["state"] = "error"
			}
		}
		out = append(out, item)
	}
	d.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

func logCatalogItem(id, name, kind, resource, path, origin string) map[string]any {
	return map[string]any{"id": id, "name": name, "kind": kind, "resource_id": resource, "path": path, "origin": origin, "state": "ready"}
}

func (s *Server) logResources() map[string][]map[string]string {
	out := map[string][]map[string]string{"vps": {}, "lb": {}, "database": {}, "managed-app": {}, "worker": {}}
	if instances, err := s.Store.ListInstances(); err == nil {
		for _, inst := range instances {
			out["vps"] = append(out["vps"], map[string]string{"id": inst.ID, "name": inst.ID})
		}
	}
	if s.balancers != nil {
		s.balancers.Lock()
		for _, lb := range s.balancers.Items {
			out["lb"] = append(out["lb"], map[string]string{"id": lb.ID, "name": lb.Name})
		}
		s.balancers.Unlock()
	}
	if s.databases != nil {
		s.databases.Lock()
		for _, p := range s.databases.Projects {
			out["database"] = append(out["database"], map[string]string{"id": p.Spec.ID, "name": p.Name})
		}
		s.databases.Unlock()
	}
	if s.applications != nil {
		s.applications.Lock()
		for _, a := range s.applications.Managed {
			out["managed-app"] = append(out["managed-app"], map[string]string{"id": a.ID, "name": a.Name})
		}
		s.applications.Unlock()
	}
	if workers, err := s.Store.ListWorkers(); err == nil {
		for _, wk := range workers {
			out["worker"] = append(out["worker"], map[string]string{"id": wk.ID, "name": wk.Hostname})
			if out["worker"][len(out["worker"])-1]["name"] == "" {
				out["worker"][len(out["worker"])-1]["name"] = wk.ID
			}
		}
	}
	return out
}

func (s *Server) saveLogSource(w http.ResponseWriter, r *http.Request) {
	var in logSource
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	in.Name, in.Kind, in.ResourceID, in.Path = strings.TrimSpace(in.Name), strings.TrimSpace(in.Kind), strings.TrimSpace(in.ResourceID), strings.TrimSpace(in.Path)
	if in.Name == "" || len(in.Name) > 80 || !logKinds[in.Kind] || in.ResourceID == "" {
		fail(w, 400, "name, kind and resource are required")
		return
	}
	if err := validateLogPath(in.Kind, in.Path); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if cleaned, err := cleanLogPath(in.Path); err == nil {
		in.Path = cleaned
	}
	if !s.logResourceExists(in.Kind, in.ResourceID) {
		fail(w, 400, "resource not found")
		return
	}
	in.ID = "log-" + randHex(8)
	d := s.logs
	d.Lock()
	d.Sources[in.ID] = in
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, "could not save log source")
		return
	}
	s.Store.Audit("admin", "logs.source.create", in.ID, in.Name)
	writeJSON(w, 200, in)
}

func validateLogPath(kind, path string) error {
	if kind == "database" && (path == "" || path == "docker") {
		return nil
	}
	cleaned, err := cleanLogPath(path)
	if err != nil || cleaned == "" {
		return errInvalidLogPath
	}
	if kind == "worker" {
		return allowWorkerHostLogPath(cleaned)
	}
	if !filepath.IsAbs(cleaned) {
		return errInvalidLogPath
	}
	return nil
}

func cleanLogPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "docker" {
		return path, nil
	}
	if strings.Contains(path, "\x00") || strings.Contains(path, "..") {
		return "", errInvalidLogPath
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "..") {
		return "", errInvalidLogPath
	}
	return cleaned, nil
}

func allowWorkerHostLogPath(path string) error {
	cleaned, err := cleanLogPath(path)
	if err != nil {
		return err
	}
	if cleaned == "" || cleaned == "docker" || !filepath.IsAbs(cleaned) {
		return errInvalidLogPath
	}
	rel, err := filepath.Rel("/var/log", cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errInvalidLogPath
	}
	return nil
}

var errInvalidLogPath = errString("invalid log path")

type errString string

func (e errString) Error() string { return string(e) }

func (s *Server) logResourceExists(kind, id string) bool {
	switch kind {
	case "vps":
		_, err := s.Store.GetInstance(id)
		return err == nil
	case "lb":
		if s.balancers == nil {
			return false
		}
		s.balancers.Lock()
		_, ok := s.balancers.Items[id]
		s.balancers.Unlock()
		return ok
	case "database":
		if s.databases == nil {
			return false
		}
		s.databases.Lock()
		_, ok := s.databases.Projects[id]
		s.databases.Unlock()
		return ok
	case "managed-app":
		if s.applications == nil {
			return false
		}
		s.applications.Lock()
		_, ok := s.applications.Managed[id]
		s.applications.Unlock()
		return ok
	case "worker":
		_, err := s.Store.GetWorker(id)
		return err == nil
	}
	return false
}

func (s *Server) deleteLogSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !logCustomID.MatchString(id) {
		fail(w, 400, "invalid source id")
		return
	}
	d := s.logs
	d.Lock()
	if _, ok := d.Sources[id]; !ok {
		d.Unlock()
		fail(w, 404, "source not found")
		return
	}
	delete(d.Sources, id)
	delete(d.tails, id)
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, "could not delete log source")
		return
	}
	s.Store.Audit("admin", "logs.source.delete", id, "ok")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) saveLogAlert(w http.ResponseWriter, r *http.Request) {
	var in logAlert
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	in.Name, in.SourceID, in.Match, in.Level = strings.TrimSpace(in.Name), strings.TrimSpace(in.SourceID), strings.TrimSpace(in.Match), strings.ToLower(strings.TrimSpace(in.Level))
	if in.Name == "" || len(in.Name) > 80 || in.SourceID == "" {
		fail(w, 400, "name and source are required")
		return
	}
	if in.Match == "" && in.Level == "" {
		fail(w, 400, "keyword or level is required")
		return
	}
	if in.Level != "" && in.Level != "error" && in.Level != "warn" && in.Level != "info" {
		fail(w, 400, "level must be error, warn or info")
		return
	}
	if in.Threshold <= 0 {
		in.Threshold = 1
	}
	if in.CooldownSeconds <= 0 {
		in.CooldownSeconds = 300
	}
	id := r.PathValue("id")
	d := s.logs
	d.Lock()
	if id == "" {
		in.ID = "lga-" + randHex(8)
	} else {
		if !logAlertID.MatchString(id) {
			d.Unlock()
			fail(w, 400, "invalid alert id")
			return
		}
		prev, ok := d.Alerts[id]
		if !ok {
			d.Unlock()
			fail(w, 404, "alert not found")
			return
		}
		in.ID, in.Hits, in.LastFired = id, prev.Hits, prev.LastFired
	}
	d.Alerts[in.ID] = in
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, "could not save alert")
		return
	}
	s.Store.Audit("admin", "logs.alert.save", in.ID, in.Name)
	writeJSON(w, 200, in)
}

func (s *Server) deleteLogAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.logs
	d.Lock()
	if _, ok := d.Alerts[id]; !ok {
		d.Unlock()
		fail(w, 404, "alert not found")
		return
	}
	delete(d.Alerts, id)
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, "could not delete alert")
		return
	}
	s.Store.Audit("admin", "logs.alert.delete", id, "ok")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) getLogAlertEvents(w http.ResponseWriter, r *http.Request) {
	d := s.logs
	d.Lock()
	events := append([]logAlertEvent(nil), d.Events...)
	d.Unlock()
	writeJSON(w, 200, map[string]any{"events": events})
}

func (s *Server) getLogTail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text, errMsg, ok := s.readLogTail(id)
	if !ok {
		fail(w, 404, "source not found")
		return
	}
	s.ingestLogTail(id, text)
	text = filterLogText(text, r.URL.Query().Get("q"), r.URL.Query().Get("level"))
	writeJSON(w, 200, map[string]string{"id": id, "logs": text, "error": errMsg})
}

func (s *Server) downloadLogTail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text, _, ok := s.readLogTail(id)
	if !ok {
		fail(w, 404, "source not found")
		return
	}
	s.ingestLogTail(id, text)
	name := strings.Map(func(r rune) rune {
		if r == ':' || r == '/' {
			return '-'
		}
		return r
	}, id) + ".log"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write([]byte(text))
}

func (s *Server) getLogAnalysis(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	rows := []map[string]any{}
	totals := map[string]int{"lines": 0, "errors": 0, "warns": 0, "matches": 0}
	for _, src := range s.logCatalog() {
		id, _ := src["id"].(string)
		text, _, ok := s.readLogTail(id)
		if !ok {
			continue
		}
		s.ingestLogTail(id, text)
		lines, errors, warns, matches := analyzeLogText(text, q)
		rows = append(rows, map[string]any{"id": id, "name": src["name"], "lines": lines, "errors": errors, "warns": warns, "matches": matches})
		totals["lines"] += lines
		totals["errors"] += errors
		totals["warns"] += warns
		totals["matches"] += matches
	}
	writeJSON(w, 200, map[string]any{"sources": rows, "totals": totals, "q": q})
}

func analyzeLogText(text, q string) (lines, errors, warns, matches int) {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "crit") {
			errors++
		}
		if strings.Contains(lower, "warn") {
			warns++
		}
		if q != "" && strings.Contains(lower, q) {
			matches++
		}
	}
	return
}

func filterLogText(text, q, level string) string {
	q, level = strings.ToLower(strings.TrimSpace(q)), strings.ToLower(strings.TrimSpace(level))
	if q == "" && level == "" {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if logLineMatches(line, q, level) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func logLineMatches(line, keyword, level string) bool {
	lower := strings.ToLower(line)
	if keyword != "" && !strings.Contains(lower, strings.ToLower(keyword)) {
		return false
	}
	switch level {
	case "error":
		return strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "crit")
	case "warn":
		return strings.Contains(lower, "warn")
	case "info":
		return strings.Contains(lower, "info")
	default:
		return keyword != ""
	}
}

func (s *Server) readLogTail(id string) (string, string, bool) {
	if strings.HasPrefix(id, "log-") {
		d := s.logs
		d.Lock()
		if _, ok := d.Sources[id]; !ok {
			d.Unlock()
			return "", "", false
		}
		cached := d.tails[id]
		d.Unlock()
		return cached.Text, cached.Error, true
	}
	if strings.HasPrefix(id, "vps:") && strings.HasSuffix(id, ":waf") {
		instID := strings.TrimSuffix(strings.TrimPrefix(id, "vps:"), ":waf")
		return s.autoWAFLogs(instID), "", true
	}
	if strings.HasPrefix(id, "vps:") && strings.Contains(id, ":svc:") {
		parts := strings.SplitN(strings.TrimPrefix(id, "vps:"), ":svc:", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		return s.autoServiceLogs(parts[0], parts[1]), "", true
	}
	if strings.HasPrefix(id, "vps:") {
		return s.autoInstanceLogs(strings.TrimPrefix(id, "vps:")), "", true
	}
	if strings.HasPrefix(id, "app:") {
		return s.autoManagedLogs(strings.TrimPrefix(id, "app:")), "", true
	}
	if strings.HasPrefix(id, "run:") {
		return s.autoRunLogs(strings.TrimPrefix(id, "run:")), "", true
	}
	return "", "", false
}

func (s *Server) autoInstanceLogs(id string) string {
	if s.LogDir == "" {
		return ""
	}
	data := tailLocalFile(filepath.Join(s.LogDir, id+".log"), 128<<10)
	if len(data) == 0 {
		return "No logs yet."
	}
	return string(data)
}

func tailLocalFile(path string, limit int64) []byte {
	if limit <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() > limit {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return nil
		}
	}
	data, _ := io.ReadAll(io.LimitReader(f, limit))
	return data
}

func (s *Server) autoServiceLogs(id, name string) string {
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	for _, svc := range s.services[id] {
		if svc.Name == name {
			return svc.Logs
		}
	}
	return ""
}

func (s *Server) autoWAFLogs(id string) string {
	s.usageMu.RLock()
	services := append([]proto.ServiceStatus(nil), s.services[id]...)
	s.usageMu.RUnlock()
	var out []string
	for _, svc := range services {
		if svc.Name != "nginx" && svc.Name != "openresty" {
			continue
		}
		if text, ok := extractTaggedLog(svc.Logs, svc.Name, "error.log"); ok {
			out = append(out, text)
		}
		if text, ok := extractTaggedLog(svc.Logs, svc.Name, "modsec_audit.log"); ok {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

func (s *Server) autoManagedLogs(id string) string {
	if s.applications == nil {
		return ""
	}
	s.applications.Lock()
	defer s.applications.Unlock()
	return s.applications.Managed[id].Logs
}

func (s *Server) autoRunLogs(id string) string {
	if s.applications == nil {
		return ""
	}
	s.applications.Lock()
	defer s.applications.Unlock()
	run, ok := s.applications.Runs[id]
	if !ok || run.Result == nil {
		return ""
	}
	return strings.TrimSpace(run.Result.Stdout + "\n" + run.Result.Stderr)
}

func (s *Server) logSourcesForWorker(workerID string) []proto.LogSourceSpec {
	d := s.logs
	d.Lock()
	sources := make([]logSource, 0, len(d.Sources))
	for _, src := range d.Sources {
		sources = append(sources, src)
	}
	d.Unlock()
	out := []proto.LogSourceSpec{}
	for _, src := range sources {
		switch src.Kind {
		case "vps":
			if inst, err := s.Store.GetInstance(src.ResourceID); err == nil && inst.WorkerID == workerID {
				out = append(out, proto.LogSourceSpec{ID: src.ID, Kind: "vps", ResourceID: src.ResourceID, Path: src.Path})
			}
		case "lb":
			ids := s.lbInstancesOnWorker(src.ResourceID, workerID)
			if len(ids) > 0 {
				out = append(out, proto.LogSourceSpec{ID: src.ID, Kind: "lb", ResourceID: strings.Join(ids, ","), Path: src.Path})
			}
		case "database":
			if s.databases != nil {
				s.databases.Lock()
				p, ok := s.databases.Projects[src.ResourceID]
				s.databases.Unlock()
				if ok && p.WorkerID == workerID {
					out = append(out, proto.LogSourceSpec{ID: src.ID, Kind: src.Kind, ResourceID: src.ResourceID, Path: src.Path})
				}
			}
		case "managed-app":
			if s.applications != nil {
				s.applications.Lock()
				a, ok := s.applications.Managed[src.ResourceID]
				s.applications.Unlock()
				if ok && a.WorkerID == workerID {
					out = append(out, proto.LogSourceSpec{ID: src.ID, Kind: src.Kind, ResourceID: src.ResourceID, Path: src.Path})
				}
			}
		case "worker":
			if src.ResourceID == workerID {
				out = append(out, proto.LogSourceSpec{ID: src.ID, Kind: src.Kind, ResourceID: src.ResourceID, Path: src.Path})
			}
		}
	}
	return out
}

func (s *Server) lbInstancesOnWorker(lbID, workerID string) []string {
	if s.balancers == nil {
		return nil
	}
	s.balancers.Lock()
	lb, ok := s.balancers.Items[lbID]
	var ids []string
	if ok {
		ids = append([]string{}, lb.Instances...)
	}
	s.balancers.Unlock()
	out := []string{}
	for _, id := range ids {
		if inst, err := s.Store.GetInstance(id); err == nil && inst.WorkerID == workerID {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) cacheWorkerLogTails(items []proto.LogTail) {
	for _, item := range items {
		s.ingestLogTail(item.ID, item.Text)
		if item.Error != "" {
			d := s.logs
			d.Lock()
			cached := d.tails[item.ID]
			cached.Error = item.Error
			d.tails[item.ID] = cached
			d.Unlock()
		}
	}
}

func appendedLogText(prev, next string) string {
	if next == "" || next == prev {
		return ""
	}
	if prev == "" {
		return ""
	}
	marker := prev
	if len(marker) > 512 {
		marker = marker[len(marker)-512:]
	}
	if i := strings.LastIndex(next, marker); i >= 0 {
		return next[i+len(marker):]
	}
	return ""
}

func (s *Server) ingestLogTail(id, text string) {
	if id == "" {
		return
	}
	d := s.logs
	d.Lock()
	prev := d.seen[id]
	d.tails[id] = logTailCache{Text: text, Updated: time.Now().UTC()}
	d.seen[id] = text
	fresh := appendedLogText(prev, text)
	alerts := make([]logAlert, 0, len(d.Alerts))
	for _, a := range d.Alerts {
		if a.Enabled && (a.SourceID == "*" || a.SourceID == id) {
			alerts = append(alerts, a)
		}
	}
	d.Unlock()
	if fresh == "" || len(alerts) == 0 {
		return
	}
	now := time.Now().UTC()
	for _, rule := range alerts {
		count := 0
		sample := ""
		for _, line := range strings.Split(fresh, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if logLineMatches(line, rule.Match, rule.Level) {
				count++
				if sample == "" {
					sample = strings.TrimSpace(line)
					if len(sample) > 240 {
						sample = sample[:240]
					}
				}
			}
		}
		if count == 0 {
			continue
		}
		d.Lock()
		cur, ok := d.Alerts[rule.ID]
		if !ok || !cur.Enabled {
			d.Unlock()
			continue
		}
		cur.Hits += count
		ready := cur.Hits >= cur.Threshold && (cur.LastFired.IsZero() || now.Sub(cur.LastFired) >= time.Duration(cur.CooldownSeconds)*time.Second)
		if ready {
			cur.Hits = 0
			cur.LastFired = now
		}
		d.Alerts[cur.ID] = cur
		if ready {
			s.recordLogAlertEventLocked(cur, id, sample, now)
		}
		_ = d.save()
		d.Unlock()
	}
	s.deliverLogEvents(context.Background())
}

func (s *Server) recordLogAlertEventLocked(rule logAlert, sourceID, sample string, at time.Time) {
	d := s.logs
	event := logAlertEvent{ID: "lge-" + randHex(8), AlertID: rule.ID, SourceID: sourceID, Name: rule.Name, Sample: sample, Status: "LOG", At: at, Deliveries: map[string]alertDelivery{}}
	s.monitoring.Lock()
	for _, c := range s.monitoring.Channels {
		if c.Enabled {
			event.Deliveries[c.ID] = alertDelivery{Status: "pending"}
		}
	}
	s.monitoring.Unlock()
	d.Events = append(d.Events, event)
	if len(d.Events) > 100 {
		d.Events = d.Events[len(d.Events)-100:]
	}
}

func (s *Server) deliverLogEvents(ctx context.Context) {
	if e := s.checkQuota("notification-channels", 0); e != nil {
		return
	}
	type job struct {
		event   logAlertEvent
		channel alertChannel
	}
	jobs := []job{}
	s.logs.Lock()
	s.monitoring.Lock()
	for _, event := range s.logs.Events {
		for id, delivery := range event.Deliveries {
			channel, ok := s.monitoring.Channels[id]
			if ok && channel.Enabled && delivery.Status != "delivered" && delivery.Attempts < 5 && !time.Now().Before(delivery.Next) {
				jobs = append(jobs, job{event, channel})
			}
		}
	}
	s.monitoring.Unlock()
	s.logs.Unlock()
	if len(jobs) > 8 {
		jobs = jobs[:8]
	}
	for _, job := range jobs {
		text := "CloFix log alert: " + job.event.Name + " matched on " + job.event.SourceID
		if job.event.Sample != "" {
			text += " (" + job.event.Sample + ")"
		}
		result := sendWebhookAlert(ctx, job.channel, text, map[string]any{"event": "LOG", "alert_id": job.event.AlertID, "source_id": job.event.SourceID, "name": job.event.Name, "sample": job.event.Sample, "timestamp": job.event.At, "event_id": job.event.ID})
		s.logs.Lock()
		for i := range s.logs.Events {
			if s.logs.Events[i].ID == job.event.ID {
				delivery := s.logs.Events[i].Deliveries[job.channel.ID]
				delivery.Attempts++
				delivery.Status = result
				delivery.Next = time.Now().Add(time.Duration(1<<delivery.Attempts) * 30 * time.Second)
				s.logs.Events[i].Deliveries[job.channel.ID] = delivery
				break
			}
		}
		_ = s.logs.save()
		s.logs.Unlock()
	}
}

func (s *Server) evaluateAutoLogTails(workerID string) {
	instances, err := s.Store.ListInstances()
	if err != nil {
		return
	}
	for _, inst := range instances {
		if inst.WorkerID != workerID {
			continue
		}
		s.ingestLogTail("vps:"+inst.ID, s.autoInstanceLogs(inst.ID))
		s.usageMu.RLock()
		services := append([]proto.ServiceStatus(nil), s.services[inst.ID]...)
		s.usageMu.RUnlock()
		for _, svc := range services {
			s.ingestLogTail("vps:"+inst.ID+":svc:"+svc.Name, svc.Logs)
		}
		s.ingestLogTail("vps:"+inst.ID+":waf", s.autoWAFLogs(inst.ID))
	}
	if s.applications == nil {
		return
	}
	s.applications.Lock()
	for _, a := range s.applications.Managed {
		if a.WorkerID == workerID {
			s.ingestLogTail("app:"+a.ID, a.Logs)
		}
	}
	s.applications.Unlock()
}
