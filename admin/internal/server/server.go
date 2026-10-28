// Package server implements the controller: admin API (bearer token) and worker API (mTLS).
package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/admin/internal/objectstore"
	"github.com/clofix/filevps/admin/internal/pki"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type Server struct {
	Store      store.Store
	CA         *pki.CA
	AdminToken string
	Sched      scheduler.Config
	UIDir      string
	LogDir     string
	S3         *objectstore.Store

	placeMu         sync.Mutex
	failMu          sync.Mutex
	failures        map[string][]time.Time
	usageMu         sync.RWMutex
	usage           map[string]proto.InstanceMetrics
	services        map[string][]proto.ServiceStatus
	serviceInstalls map[string]map[string]string
}

func New(s store.Store, ca *pki.CA, adminToken, uiDir string) *Server {
	dataDir := os.Getenv("CLOFIX_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/clofix-controller"
	}
	return &Server{Store: s, CA: ca, AdminToken: adminToken, Sched: scheduler.DefaultConfig(), UIDir: uiDir, S3: objectstore.New(dataDir), failures: map[string][]time.Time{}, usage: map[string]proto.InstanceMetrics{}, services: map[string][]proto.ServiceStatus{}, serviceInstalls: map[string]map[string]string{}}
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---------------- admin API ----------------

func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(s.CA.CertPEM)
	})
	mux.HandleFunc("POST /api/enrollment-tokens", s.auth(s.createToken))
	mux.HandleFunc("GET /api/workers", s.auth(s.listWorkers))
	mux.HandleFunc("POST /api/workers/{id}/drain", s.auth(s.setStatus("draining")))
	mux.HandleFunc("POST /api/workers/{id}/activate", s.auth(s.setStatus("active")))
	mux.HandleFunc("POST /api/workers/{id}/revoke", s.auth(s.setStatus("revoked")))
	mux.HandleFunc("GET /api/instances", s.auth(s.listInstances))
	mux.HandleFunc("GET /api/storage/s3", s.auth(s.getS3Config))
	mux.HandleFunc("PUT /api/storage/s3", s.auth(s.saveS3Config))
	mux.HandleFunc("POST /api/instances/{id}/s3-mount", s.auth(s.mountS3))
	mux.HandleFunc("DELETE /api/instances/{id}/s3-mount", s.auth(s.unmountS3))
	mux.HandleFunc("POST /api/instances/{id}/ports", s.auth(s.addPortMapping))
	mux.HandleFunc("POST /api/instances/{id}/protection", s.auth(s.setProtectionMode))
	mux.HandleFunc("POST /api/instances/{id}/waf", s.auth(s.setWAFCategory))
	mux.HandleFunc("DELETE /api/instances/{id}/ports/{hostPort}", s.auth(s.deletePortMapping))
	mux.HandleFunc("GET /api/instances/{id}/logs", s.auth(s.instanceLogs))
	mux.HandleFunc("GET /api/instances/{id}/waf/logs", s.auth(s.wafErrorLogs))
	mux.HandleFunc("POST /api/instances", s.auth(s.createInstance))
	mux.HandleFunc("POST /api/instances/{id}/retry", s.auth(s.retryInstance))
	mux.HandleFunc("POST /api/instances/{id}/start", s.auth(s.instanceOp(proto.ActionStart)))
	mux.HandleFunc("POST /api/instances/{id}/stop", s.auth(s.instanceOp(proto.ActionStop)))
	mux.HandleFunc("POST /api/instances/{id}/restart", s.auth(s.instanceOp(proto.ActionRestart)))
	mux.HandleFunc("POST /api/instances/{id}/services/{name}", s.auth(s.serviceControl))
	mux.HandleFunc("GET /api/instances/{id}/services/{name}/error-log", s.auth(s.serviceErrorLog))
	mux.HandleFunc("POST /api/instances/{id}/install-service", s.auth(s.installService))
	mux.HandleFunc("DELETE /api/instances/{id}", s.auth(s.instanceOp(proto.ActionDelete)))
	if s.UIDir != "" {
		mux.Handle("/", s.uiHandler())
	}
	return mux
}

var wafCategories = map[string]string{
	"protocol": "attack-protocol", "lfi": "attack-lfi", "rfi": "attack-rfi",
	"rce": "attack-rce", "php": "attack-php", "generic": "attack-generic",
	"xss": "attack-xss", "sqli": "attack-sqli", "fixation": "attack-fixation",
	"method": "id:911000-911999", "iprep": "id:910000-910999",
	"dos": "id:912000-912999", "scanner": "id:913000-913999",
	"protocol_enforcement": "id:920000-920999", "multipart": "id:922000-922999",
	"disclosure": "attack-disclosure",
}

func (s *Server) setWAFCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	var in struct {
		Category string `json:"category"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil || wafCategories[in.Category] == "" {
		fail(w, http.StatusBadRequest, "category and enabled are required")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	if inst.State != proto.StateRunning {
		fail(w, http.StatusConflict, "VPS must be running to update WAF rules")
		return
	}
	if inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode {
		fail(w, http.StatusConflict, "disable Protection Mode before changing WAF rules; the rule file is on the guest filesystem")
		return
	}
	s.usageMu.RLock()
	wafInstalled := s.serviceInstalls[id]["owasp-waf"] == "INSTALLED"
	s.usageMu.RUnlock()
	if !wafInstalled {
		fail(w, http.StatusConflict, "install OWASP CRS WAF from Services before changing WAF rules")
		return
	}
	currentEnabled := true
	if current, exists := inst.Spec.OWASPProtection[in.Category]; exists {
		currentEnabled = current
	}
	if currentEnabled == *in.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "category": in.Category, "enabled": currentEnabled, "unchanged": true})
		return
	}
	previous := inst
	policy := make(map[string]bool, len(inst.Spec.OWASPProtection)+1)
	for key, enabled := range inst.Spec.OWASPProtection {
		policy[key] = enabled
	}
	policy[in.Category] = *in.Enabled
	inst.Spec.OWASPProtection = policy
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save WAF rule setting")
		return
	}
	actionID := proto.ActionUpdateWAF + "-" + in.Category + "-" + strconv.FormatBool(*in.Enabled)
	op := proto.Operation{ID: id + "/" + actionID + "/" + randHex(4), Action: proto.ActionUpdateWAF, InstanceID: id, Spec: &inst.Spec, WAFCategory: in.Category, WAFEnabled: *in.Enabled}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue WAF rule update")
		return
	}
	s.Store.Audit("admin", "instance.waf."+in.Category, id, fmt.Sprintf("enabled=%t", *in.Enabled))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "category": in.Category, "enabled": *in.Enabled, "queued": true})
}

// uiHandler renders each UI route as a separate HTML document while sharing the
// same navigation, stylesheet, and browser code.
func (s *Server) uiHandler() http.Handler {
	files := http.FileServer(http.Dir(s.UIDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanURL := pathpkg.Clean("/" + r.URL.Path)
		page, title, instanceID, isPage := uiPage(cleanURL)
		if isPage {
			t, err := template.ParseFiles(filepath.Join(s.UIDir, "layout.html"), filepath.Join(s.UIDir, "pages", page+".html"))
			if err != nil {
				http.Error(w, "UI page could not be loaded", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := t.ExecuteTemplate(w, "layout", struct{ Page, Title, InstanceID string }{page, title, instanceID}); err != nil {
				return
			}
			return
		}
		name := strings.TrimPrefix(cleanURL, "/")
		candidate := filepath.Join(s.UIDir, filepath.FromSlash(name))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func uiPage(route string) (page, title, instanceID string, ok bool) {
	switch route {
	case "/", "/instances":
		return "instances", "FileVPS", "", true
	case "/create":
		return "create", "Create FileVPS", "", true
	case "/workers":
		return "workers", "Workers", "", true
	case "/services":
		return "services", "Services", "", true
	case "/security":
		return "security", "CloFix Security", "", true
	case "/storage/s3":
		return "storage", "S3 Filesystem", "", true
	case "/settings":
		return "settings", "Settings", "", true
	}
	parts := strings.Split(strings.TrimPrefix(route, "/"), "/")
	if len(parts) == 2 && parts[0] == "instances" && apiInstanceID.MatchString(parts[1]) {
		return "details", "VPS Details", parts[1], true
	}
	return "", "", "", false
}

func (s *Server) setProtectionMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, http.StatusBadRequest, "enabled must be true or false")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.State == proto.StateDeleting {
		fail(w, http.StatusConflict, "cannot change protection while VPS is deleting")
		return
	}
	wasEnabled := inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode
	if wasEnabled == *in.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "protection_mode": wasEnabled, "unchanged": true})
		return
	}
	previous := inst
	inst.Spec.ProtectionMode = in.Enabled
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save protection setting")
		return
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/" + proto.ActionUpdateProtection + "/" + randHex(4), Action: proto.ActionUpdateProtection, InstanceID: id, Spec: &spec}); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue protection update")
		return
	}
	s.Store.Audit("admin", "instance.protection", id, fmt.Sprintf("enabled=%t", *in.Enabled))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "protection_mode": *in.Enabled, "queued": true, "restart_required": inst.State == proto.StateRunning})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.AdminToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.AdminToken)) != 1 {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TTLMinutes int `json:"ttl_minutes"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if in.TTLMinutes <= 0 || in.TTLMinutes > 24*60 {
		in.TTLMinutes = 60
	}
	tok := "cfe_" + randHex(32)
	exp := time.Now().Add(time.Duration(in.TTLMinutes) * time.Minute)
	if err := s.Store.CreateToken(hashToken(tok), exp, "admin"); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit("admin", "enrollment-token.create", "", "ok")
	// Shown once; only the hash is stored.
	writeJSON(w, 201, map[string]any{"token": tok, "expires": exp})
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	ws, _ := s.Store.ListWorkers()
	if ws == nil {
		ws = []store.Worker{}
	}
	writeJSON(w, 200, ws)
}

func (s *Server) setStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := s.Store.SetWorkerStatus(id, status); err != nil {
			fail(w, 404, "worker not found")
			return
		}
		s.Store.Audit("admin", "worker."+status, id, "ok")
		writeJSON(w, 200, map[string]string{"id": id, "status": status})
	}
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	is, _ := s.Store.ListInstances()
	if is == nil {
		is = []store.Instance{}
	}
	type instanceView struct {
		store.Instance
		Metrics         proto.InstanceMetrics `json:"metrics"`
		Services        []proto.ServiceStatus `json:"services,omitempty"`
		ServiceInstalls map[string]string     `json:"service_installs,omitempty"`
	}
	views := make([]instanceView, 0, len(is))
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	for _, i := range is {
		views = append(views, instanceView{Instance: i, Metrics: s.usage[i.ID], Services: s.services[i.ID], ServiceInstalls: s.serviceInstalls[i.ID]})
	}
	writeJSON(w, 200, views)
}

func (s *Server) getS3Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.S3.Public())
}

func (s *Server) saveS3Config(w http.ResponseWriter, r *http.Request) {
	var c objectstore.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
		fail(w, http.StatusBadRequest, "invalid S3 configuration")
		return
	}
	if err := s.S3.Save(c); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.configure", c.Bucket, "ok")
	writeJSON(w, http.StatusOK, s.S3.Public())
}

func validS3MountPath(p string) bool {
	if !strings.HasPrefix(p, "/mnt/") || pathpkg.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/mnt/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return len(p) <= 256
}

func (s *Server) mountS3(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	var in struct {
		GuestPath string `json:"guest_path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil || !validS3MountPath(in.GuestPath) {
		fail(w, http.StatusBadRequest, "guest_path must be a clean path below /mnt")
		return
	}
	c, err := s.S3.Load()
	if err != nil {
		fail(w, http.StatusConflict, "configure S3 storage first")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.State == proto.StateDeleting {
		fail(w, http.StatusConflict, "cannot mount S3 while VPS is deleting")
		return
	}
	if inst.Spec.S3MountPath != "" && inst.Spec.S3MountPath != in.GuestPath {
		fail(w, http.StatusConflict, "unmount the current S3 path before changing it")
		return
	}
	previous := inst
	inst.Spec.S3MountPath = in.GuestPath
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save S3 mount setting")
		return
	}
	spec := inst.Spec
	mount := &proto.S3Mount{Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, AccessKey: c.AccessKey, SecretKey: c.SecretKey, GuestPath: in.GuestPath}
	op := proto.Operation{ID: id + "/" + proto.ActionUpdateS3Mount + "/" + randHex(4), Action: proto.ActionUpdateS3Mount, InstanceID: id, Spec: &spec, S3Mount: mount}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue S3 mount")
		return
	}
	s.Store.Audit("admin", "instance.s3.mount", id, in.GuestPath)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "guest_path": in.GuestPath, "queued": true, "restart_required": inst.State == proto.StateRunning})
}

func (s *Server) unmountS3(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.Spec.S3MountPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "unchanged": true})
		return
	}
	previous := inst
	inst.Spec.S3MountPath = ""
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save S3 mount setting")
		return
	}
	spec := inst.Spec
	op := proto.Operation{ID: id + "/" + proto.ActionUpdateS3Mount + "/" + randHex(4), Action: proto.ActionUpdateS3Mount, InstanceID: id, Spec: &spec, UnmountS3: true}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue S3 unmount")
		return
	}
	s.Store.Audit("admin", "instance.s3.unmount", id, "queued")
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queued": true, "restart_required": inst.State == proto.StateRunning})
}

var apiInstanceID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (s *Server) instanceLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	f, err := os.Open(filepath.Join(s.LogDir, id+".log"))
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]string{"id": id, "logs": "No logs yet."})
			return
		}
		fail(w, http.StatusInternalServerError, "cannot read instance logs")
		return
	}
	defer f.Close()
	const maxTail = int64(128 << 10)
	info, err := f.Stat()
	if err != nil {
		fail(w, http.StatusInternalServerError, "cannot stat instance logs")
		return
	}
	offset := info.Size() - maxTail
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		fail(w, http.StatusInternalServerError, "cannot seek instance logs")
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTail))
	if err != nil {
		fail(w, http.StatusInternalServerError, "cannot read instance logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": inst.ID, "logs": string(data)})
}

func (s *Server) wafErrorLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	if _, err := s.Store.GetInstance(id); err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	s.usageMu.RLock()
	services := append([]proto.ServiceStatus(nil), s.services[id]...)
	s.usageMu.RUnlock()
	const marker = "# nginx/error.log\n"
	for _, service := range services {
		if service.Name != "nginx" {
			continue
		}
		start := strings.Index(service.Logs, marker)
		if start < 0 {
			writeJSON(w, http.StatusOK, map[string]string{"id": id, "logs": "No Nginx error log entries have been collected yet."})
			return
		}
		start += len(marker)
		end := strings.Index(service.Logs[start:], "\n# ")
		if end >= 0 {
			service.Logs = service.Logs[start : start+end]
		} else {
			service.Logs = service.Logs[start:]
		}
		if strings.TrimSpace(service.Logs) == "" {
			service.Logs = "Nginx error log is empty."
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "logs": service.Logs})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "logs": "Nginx is not currently reporting service logs. Start Nginx and wait for a worker heartbeat."})
}

var apiServiceName = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,128}$`)

func (s *Server) serviceControl(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	if !apiInstanceID.MatchString(id) || !apiServiceName.MatchString(name) || name == "." || name == ".." {
		fail(w, http.StatusBadRequest, "invalid VPS or service name")
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || (in.Action != "start" && in.Action != "stop" && in.Action != "restart") {
		fail(w, http.StatusBadRequest, "action must be start, stop, or restart")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	if inst.State != proto.StateRunning {
		fail(w, http.StatusConflict, "VPS must be running to control a service")
		return
	}
	s.usageMu.RLock()
	services := append([]proto.ServiceStatus(nil), s.services[id]...)
	s.usageMu.RUnlock()
	known := false
	for _, service := range services {
		if service.Name == name {
			known = true
			break
		}
	}
	if !known {
		fail(w, http.StatusNotFound, "enabled application service not found")
		return
	}
	op := proto.Operation{ID: id + "/" + proto.ActionServiceControl + "/" + randHex(4), Action: proto.ActionServiceControl, InstanceID: id, ServiceName: name, ServiceAction: in.Action}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		fail(w, http.StatusInternalServerError, "could not queue service action")
		return
	}
	s.Store.Audit("admin", "instance.service."+in.Action, id, name)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "service": name, "action": in.Action, "queued": true})
}

func (s *Server) serviceErrorLog(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	if !apiInstanceID.MatchString(id) || !apiServiceName.MatchString(name) {
		fail(w, http.StatusBadRequest, "invalid VPS or service name")
		return
	}
	if _, err := s.Store.GetInstance(id); err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	s.usageMu.RLock()
	services := append([]proto.ServiceStatus(nil), s.services[id]...)
	s.usageMu.RUnlock()
	marker := "# " + name + "/error.log\n"
	for _, service := range services {
		if service.Name != name {
			continue
		}
		start := strings.Index(service.Logs, marker)
		if start < 0 {
			writeJSON(w, http.StatusOK, map[string]string{"service": name, "logs": "No error.log entries have been collected for this service."})
			return
		}
		start += len(marker)
		end := strings.Index(service.Logs[start:], "\n# ")
		if end >= 0 {
			service.Logs = service.Logs[start : start+end]
		} else {
			service.Logs = service.Logs[start:]
		}
		if strings.TrimSpace(service.Logs) == "" {
			service.Logs = "The service error log is empty."
		}
		writeJSON(w, http.StatusOK, map[string]string{"service": name, "logs": service.Logs})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"service": name, "logs": "Service is not reporting logs. Start it and wait for a worker heartbeat."})
}

var serviceCatalog = map[string]bool{"nginx": true, "apache": true, "mysql": true, "mariadb": true, "postgresql": true, "redis": true, "php": true, "nodejs": true, "python": true, "wordpress": true, "joomla": true, "owasp-waf": true}

func (s *Server) installService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid VPS ID")
		return
	}
	var in struct {
		ServiceID string `json:"service_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || !serviceCatalog[in.ServiceID] {
		fail(w, http.StatusBadRequest, "unknown service catalog item")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	if inst.State != proto.StateRunning {
		fail(w, http.StatusConflict, "VPS must be running to install a service")
		return
	}
	if inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode {
		fail(w, http.StatusConflict, "disable Protection Mode in VPS Details before installing packages")
		return
	}
	op := proto.Operation{ID: id + "/" + proto.ActionInstallService + "/" + randHex(4), Action: proto.ActionInstallService, InstanceID: id, ServiceID: in.ServiceID}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		fail(w, http.StatusInternalServerError, "could not queue service installation")
		return
	}
	s.Store.Audit("admin", "instance.service.install", id, in.ServiceID)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "service_id": in.ServiceID, "queued": true})
}

type createReq struct {
	Spec     proto.InstanceSpec `json:"spec"`
	WorkerID string             `json:"worker_id"` // empty = automatic placement
}

const (
	sshPortBase         = 22000
	sshPortCount        = 100
	customHostPortBase  = 24000
	customHostPortCount = 100
)

func (s *Server) addPortMapping(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GuestPort int    `json:"guest_port"`
		Protocol  string `json:"protocol"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	if in.GuestPort < 1 || in.GuestPort > 65535 || (in.Protocol != "tcp" && in.Protocol != "udp") {
		fail(w, http.StatusBadRequest, "protocol must be tcp or udp and guest_port must be between 1 and 65535")
		return
	}
	if in.Protocol == "tcp" && (in.GuestPort == 22 || in.GuestPort == 80 || in.GuestPort == 443) {
		fail(w, http.StatusConflict, "guest ports 22, 80, and 443 already have standard mappings")
		return
	}
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	instances, _ := s.Store.ListInstances()
	used := make(map[int]bool)
	for _, existing := range instances {
		for _, mapping := range existing.Spec.PortMappings {
			used[mapping.HostPort] = true
			if existing.ID == id && mapping.GuestPort == in.GuestPort && mapping.Protocol == in.Protocol {
				fail(w, http.StatusConflict, "this guest port is already mapped")
				return
			}
		}
	}
	var hostPort int
	for p := customHostPortBase; p < customHostPortBase+customHostPortCount; p++ {
		if !used[p] {
			hostPort = p
			break
		}
	}
	if hostPort == 0 {
		fail(w, http.StatusServiceUnavailable, "custom host port range is full")
		return
	}
	mapping := proto.PortMapping{Protocol: in.Protocol, GuestPort: in.GuestPort, HostPort: hostPort}
	inst.Spec.PortMappings = append(inst.Spec.PortMappings, mapping)
	if err := s.savePortMappings(inst, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit("admin", "instance.port.add", id, fmt.Sprintf("%s %d:%d", in.Protocol, hostPort, in.GuestPort))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "mapping": mapping, "address": fmt.Sprintf("localhost:%d", hostPort)})
}

func (s *Server) deletePortMapping(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hostPort, err := strconv.Atoi(r.PathValue("hostPort"))
	if !apiInstanceID.MatchString(id) || err != nil || hostPort < customHostPortBase || hostPort >= customHostPortBase+customHostPortCount {
		fail(w, http.StatusBadRequest, "invalid instance or host port")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	filtered := make([]proto.PortMapping, 0, len(inst.Spec.PortMappings))
	removed := false
	for _, mapping := range inst.Spec.PortMappings {
		if mapping.HostPort == hostPort {
			removed = true
			continue
		}
		filtered = append(filtered, mapping)
	}
	if !removed {
		fail(w, http.StatusNotFound, "port mapping not found")
		return
	}
	inst.Spec.PortMappings = filtered
	if err := s.savePortMappings(inst, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit("admin", "instance.port.delete", id, strconv.Itoa(hostPort))
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "removed": strconv.Itoa(hostPort)})
}

func (s *Server) savePortMappings(inst store.Instance, id string) error {
	previous, err := s.Store.GetInstance(id)
	if err != nil {
		return err
	}
	if err := s.Store.SaveInstance(inst); err != nil {
		return err
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/" + proto.ActionUpdatePorts + "/" + randHex(4), Action: proto.ActionUpdatePorts, InstanceID: id, Spec: &spec}); err != nil {
		_ = s.Store.SaveInstance(previous)
		return err
	}
	return nil
}

func validSSHPublicKey(key string) bool {
	if len(key) == 0 || len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
		return false
	}
	fields := strings.Fields(key)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
		_, err := base64.StdEncoding.DecodeString(fields[1])
		return err == nil
	default:
		return false
	}
}

func reserveNetwork(spec *proto.InstanceSpec, instances []store.Instance) bool {
	usedIPs, usedPorts := map[string]bool{}, map[int]bool{}
	for _, instance := range instances {
		usedIPs[instance.Spec.IPAddress] = true
		usedPorts[instance.Spec.SSHPort] = true
	}
	for slot := 0; slot < sshPortCount; slot++ {
		ip, port := fmt.Sprintf("10.231.0.%d", slot+2), sshPortBase+slot
		if !usedIPs[ip] && !usedPorts[port] {
			spec.IPAddress, spec.SSHPort = ip, port
			return true
		}
	}
	return false
}

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var in createReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	sp := in.Spec
	if sp.ProtectionMode == nil {
		enabled := true
		sp.ProtectionMode = &enabled
	}
	if sp.Image.Name == "" {
		sp.Image.Name = "ubuntu"
	}
	if sp.Image.Name == "ubuntu" && sp.Image.Version == "" {
		sp.Image.Version = "24.04"
	}
	if sp.Image.Name == "" || sp.Image.Version == "" || sp.RAMMB <= 0 || sp.CPU <= 0 || sp.DiskMB <= 0 {
		fail(w, 400, "spec requires image.name, ram_mb, cpu, disk_mb")
		return
	}
	if !validSSHPublicKey(sp.SSHAuthorizedKey) {
		fail(w, 400, "a valid single-line SSH public key is required")
		return
	}
	if sp.SwapMB == nil {
		swapMB := proto.DefaultSwapMB
		sp.SwapMB = &swapMB
	}
	if *sp.SwapMB < 0 {
		fail(w, http.StatusBadRequest, "swap_mb cannot be negative")
		return
	}
	req := scheduler.Request{RAM: uint64(sp.RAMMB) << 20, Swap: uint64(proto.SwapLimitMB(sp)) << 20, Disk: uint64(sp.DiskMB) << 20, CPU: sp.CPU}

	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	ws, _ := s.Store.ListWorkers()
	var cands []scheduler.Candidate
	for _, wk := range ws {
		if in.WorkerID != "" && wk.ID != in.WorkerID {
			continue
		}
		cands = append(cands, scheduler.Candidate{ID: wk.ID, Active: wk.Status == "active", Draining: wk.Status == "draining", LastHeartbeat: wk.LastHeartbeat, Capacity: wk.Capacity})
	}
	dec, err := scheduler.Place(cands, req, s.Sched, time.Now())
	method := "automatic"
	if in.WorkerID != "" {
		method = "manual"
	}
	if err != nil {
		s.Store.Audit("admin", "instance.create", "", "rejected: no capacity")
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "placement": dec.Explain})
		return
	}
	instances, _ := s.Store.ListInstances()
	if !reserveNetwork(&sp, instances) {
		fail(w, http.StatusServiceUnavailable, "SSH port range is full (100 VPS limit in this development setup)")
		return
	}
	inst := store.Instance{ID: "vps-" + randHex(6), WorkerID: dec.WorkerID, State: proto.StateProvisioning, PlacementMethod: method, Spec: sp, Created: time.Now()}
	s.Store.SaveInstance(inst)
	// reserve capacity until the worker's next heartbeat reports real numbers
	if wk, err := s.Store.GetWorker(dec.WorkerID); err == nil {
		wk.Capacity.RAMCommitted += req.RAM
		wk.Capacity.SwapCommitted += req.Swap
		wk.Capacity.DiskCommitted += req.Disk
		wk.Capacity.CPUAllocated += req.CPU
		s.Store.UpsertWorker(wk)
	}
	s.Store.QueueOp(dec.WorkerID, proto.Operation{ID: inst.ID + "/create/" + randHex(4), Action: proto.ActionCreate, InstanceID: inst.ID, Spec: &sp})
	s.Store.Audit("admin", "instance.create", inst.ID, "ok")
	writeJSON(w, 201, map[string]any{"instance": inst, "placement": dec.Explain})
}

func (s *Server) instanceOp(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		inst, err := s.Store.GetInstance(id)
		if err != nil {
			fail(w, 404, "instance not found")
			return
		}
		if inst.State == proto.StateDeleting {
			fail(w, http.StatusConflict, "VPS is deleting")
			return
		}
		if inst.State == proto.StateStarting || inst.State == proto.StateStopping || inst.State == proto.StateRestarting {
			fail(w, http.StatusConflict, "VPS is already changing state")
			return
		}
		previousState := inst.State
		if action == proto.ActionDelete {
			inst.State = proto.StateDeleting
		} else if action == proto.ActionStart {
			inst.State = proto.StateStarting
		} else if action == proto.ActionStop {
			inst.State = proto.StateStopping
		} else if action == proto.ActionRestart {
			inst.State = proto.StateRestarting
		}
		if err := s.Store.SaveInstance(inst); err != nil {
			fail(w, http.StatusInternalServerError, "could not update VPS status")
			return
		}
		if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/" + action + "/" + randHex(4), Action: action, InstanceID: id}); err != nil {
			inst.State = previousState
			_ = s.Store.SaveInstance(inst)
			fail(w, http.StatusInternalServerError, "could not queue VPS action")
			return
		}
		s.Store.Audit("admin", "instance."+action, id, "queued")
		writeJSON(w, 202, map[string]string{"id": id, "queued": action})
	}
}

func (s *Server) retryInstance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SSHPublicKey string `json:"ssh_authorized_key"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in)
	}
	id := r.PathValue("id")
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.State != proto.StateFailed {
		fail(w, http.StatusConflict, "only failed instances can be retried")
		return
	}
	wk, err := s.Store.GetWorker(inst.WorkerID)
	if err != nil || wk.Status != "active" {
		fail(w, http.StatusConflict, "assigned worker is not active")
		return
	}
	if inst.Spec.SSHAuthorizedKey == "" {
		if !validSSHPublicKey(in.SSHPublicKey) {
			fail(w, http.StatusBadRequest, "paste a valid SSH public key before retrying")
			return
		}
		inst.Spec.SSHAuthorizedKey = in.SSHPublicKey
	}
	if inst.Spec.ProtectionMode == nil {
		enabled := true
		inst.Spec.ProtectionMode = &enabled
	}
	if inst.Spec.SwapMB == nil {
		swapMB := proto.DefaultSwapMB
		inst.Spec.SwapMB = &swapMB
	}
	if inst.Spec.IPAddress == "" || inst.Spec.SSHPort == 0 {
		instances, _ := s.Store.ListInstances()
		if !reserveNetwork(&inst.Spec, instances) {
			fail(w, http.StatusServiceUnavailable, "SSH port range is full")
			return
		}
	}
	inst.State, inst.Error = proto.StateProvisioning, ""
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "cannot update instance")
		return
	}
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/create/" + randHex(4), Action: proto.ActionCreate, InstanceID: id, Spec: &inst.Spec}); err != nil {
		inst.State, inst.Error = proto.StateFailed, "could not queue retry"
		s.Store.SaveInstance(inst)
		fail(w, http.StatusInternalServerError, "could not queue retry")
		return
	}
	s.Store.Audit("admin", "instance.retry", id, "queued")
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "queued": "retry"})
}

// ---------------- worker API ----------------

func (s *Server) WorkerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/enroll", s.enroll)
	mux.HandleFunc("POST /v1/heartbeat", s.heartbeat)
	return mux
}

// rateLimited allows 5 failed enrollments per source IP per 10 minutes.
func (s *Server) rateLimited(ip string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range s.failures[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	s.failures[ip] = keep
	return len(keep) >= 5
}

func (s *Server) noteFail(ip string) {
	s.failMu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.failMu.Unlock()
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.rateLimited(ip) {
		fail(w, 429, "too many failed attempts")
		return
	}
	var in proto.EnrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	if err := s.Store.ConsumeToken(hashToken(in.Token), time.Now()); err != nil {
		s.noteFail(ip)
		s.Store.Audit("worker", "enroll", ip, "denied")
		fail(w, 403, store.ErrTokenInvalid.Error())
		return
	}
	id := "wrk-" + randHex(6)
	cert, err := s.CA.SignWorkerCSR([]byte(in.CSRPEM), id, 90*24*time.Hour)
	if err != nil {
		fail(w, 400, "bad CSR")
		return
	}
	s.Store.UpsertWorker(store.Worker{ID: id, Hostname: in.Hostname, Status: "pending", Version: in.Version, Capacity: in.Capacity, LastHeartbeat: time.Now()})
	s.Store.Audit("worker", "enroll", id, "ok")
	writeJSON(w, 201, proto.EnrollResponse{WorkerID: id, CertPEM: string(cert), CAPEM: string(s.CA.CertPEM)})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(w, 401, "client certificate required")
		return
	}
	id := r.TLS.PeerCertificates[0].Subject.CommonName // identity from our CA-signed cert only
	wk, err := s.Store.GetWorker(id)
	if err != nil || wk.Status == "revoked" {
		fail(w, 403, "worker not authorized")
		return
	}
	var hb proto.Heartbeat
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&hb); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	wk.Capacity, wk.Version, wk.LastHeartbeat = hb.Capacity, hb.Version, time.Now()
	if wk.Status == "pending" {
		wk.Status = "active" // TODO: gate on policy/health checks and optional admin approval
	}
	s.Store.UpsertWorker(wk)

	for _, st := range hb.Instances {
		if inst, err := s.Store.GetInstance(st.ID); err == nil && inst.WorkerID == id && inst.State != proto.StateDeleting {
			inst.State, inst.Error = st.State, st.Error
			s.Store.SaveInstance(inst)
			s.usageMu.Lock()
			s.usage[st.ID] = st.Metrics
			s.services[st.ID] = st.Services
			s.serviceInstalls[st.ID] = st.ServiceInstalls
			s.usageMu.Unlock()
		}
	}
	for _, res := range hb.Results {
		parts := strings.Split(res.OpID, "/") // <instance>/<action>/<rand>
		if len(parts) != 3 {
			continue
		}
		inst, err := s.Store.GetInstance(parts[0])
		if err != nil || inst.WorkerID != id {
			continue
		}
		switch {
		case res.OK && parts[1] == proto.ActionDelete:
			s.Store.DeleteInstance(inst.ID)
			s.usageMu.Lock()
			delete(s.usage, inst.ID)
			delete(s.services, inst.ID)
			delete(s.serviceInstalls, inst.ID)
			s.usageMu.Unlock()
		case res.OK && parts[1] == proto.ActionServiceControl:
			if strings.HasPrefix(inst.Error, "service action failed:") {
				inst.Error = ""
				_ = s.Store.SaveInstance(inst)
			}
		case res.OK && parts[1] == proto.ActionInstallService:
			if strings.HasPrefix(inst.Error, "service installation failed:") {
				inst.Error = ""
				_ = s.Store.SaveInstance(inst)
			}
		case res.OK && strings.HasPrefix(parts[1], proto.ActionUpdateWAF+"-"):
			if strings.HasPrefix(inst.Error, "WAF update failed:") {
				inst.Error = ""
				_ = s.Store.SaveInstance(inst)
			}
		case !res.OK:
			if parts[1] == proto.ActionServiceControl {
				inst.Error = "service action failed: " + res.Error
			} else if parts[1] == proto.ActionInstallService {
				inst.Error = "service installation failed: " + res.Error
			} else if parts[1] == proto.ActionUpdatePorts {
				inst.Error = "port mapping update failed: " + res.Error
			} else if strings.HasPrefix(parts[1], proto.ActionUpdateWAF+"-") {
				// The operation ID carries the requested category/value so a failed
				// guest config reload can restore the last controller-known setting.
				fields := strings.Split(strings.TrimPrefix(parts[1], proto.ActionUpdateWAF+"-"), "-")
				if len(fields) == 2 {
					requested, parseErr := strconv.ParseBool(fields[1])
					if parseErr == nil {
						if inst.Spec.OWASPProtection == nil {
							inst.Spec.OWASPProtection = map[string]bool{}
						}
						inst.Spec.OWASPProtection[fields[0]] = !requested
					}
				}
				inst.Error = "WAF update failed: " + res.Error
			} else {
				inst.State, inst.Error = proto.StateFailed, res.Error
			}
			s.Store.SaveInstance(inst)
		}
		s.Store.Audit(id, "op."+parts[1], inst.ID, map[bool]string{true: "ok", false: "failed"}[res.OK])
	}
	ops, _ := s.Store.TakeOps(id)
	writeJSON(w, 200, proto.HeartbeatResponse{Operations: ops})
}
