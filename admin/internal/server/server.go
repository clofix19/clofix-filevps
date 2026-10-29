// Package server implements the controller: admin API (bearer token) and worker API (mTLS).
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
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
	"unicode/utf8"

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
	volumeFileMu    sync.Mutex
	volumeFileWait  map[string]volumeFileWaiter
}

type volumeFileWaiter struct {
	workerID string
	result   chan proto.OpResult
}

func New(s store.Store, ca *pki.CA, adminToken, uiDir string) *Server {
	dataDir := os.Getenv("CLOFIX_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/clofix-controller"
	}
	return &Server{Store: s, CA: ca, AdminToken: adminToken, Sched: scheduler.DefaultConfig(), UIDir: uiDir, S3: objectstore.New(dataDir), failures: map[string][]time.Time{}, usage: map[string]proto.InstanceMetrics{}, services: map[string][]proto.ServiceStatus{}, serviceInstalls: map[string]map[string]string{}, volumeFileWait: map[string]volumeFileWaiter{}}
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
	mux.HandleFunc("GET /api/iam/me", s.auth(s.iamMe))
	mux.HandleFunc("GET /api/iam/users", s.auth(s.listIAMUsers))
	mux.HandleFunc("POST /api/iam/users", s.auth(s.saveIAMUser))
	mux.HandleFunc("PUT /api/iam/users/{id}", s.auth(s.saveIAMUser))
	mux.HandleFunc("DELETE /api/iam/users/{id}", s.auth(s.deleteIAMUser))
	mux.HandleFunc("GET /api/iam/users/{id}/keys", s.auth(s.listIAMUserKeys))
	mux.HandleFunc("POST /api/iam/users/{id}/keys", s.auth(s.createIAMUserKey))
	mux.HandleFunc("DELETE /api/iam/users/{id}/keys/{key}", s.auth(s.revokeIAMUserKey))
	mux.HandleFunc("GET /api/iam/groups", s.auth(s.listIAMGroups))
	mux.HandleFunc("POST /api/iam/groups", s.auth(s.saveIAMGroup))
	mux.HandleFunc("PUT /api/iam/groups/{id}", s.auth(s.saveIAMGroup))
	mux.HandleFunc("DELETE /api/iam/groups/{id}", s.auth(s.deleteIAMGroup))
	mux.HandleFunc("GET /api/iam/policies", s.auth(s.listIAMPolicies))
	mux.HandleFunc("POST /api/iam/policies", s.auth(s.saveIAMPolicy))
	mux.HandleFunc("PUT /api/iam/policies/{id}", s.auth(s.saveIAMPolicy))
	mux.HandleFunc("DELETE /api/iam/policies/{id}", s.auth(s.deleteIAMPolicy))
	mux.HandleFunc("GET /api/workers", s.auth(s.listWorkers))
	mux.HandleFunc("POST /api/workers/{id}/drain", s.auth(s.setStatus("draining")))
	mux.HandleFunc("POST /api/workers/{id}/activate", s.auth(s.setStatus("active")))
	mux.HandleFunc("POST /api/workers/{id}/revoke", s.auth(s.setStatus("revoked")))
	mux.HandleFunc("GET /api/instances", s.auth(s.listInstances))
	mux.HandleFunc("GET /api/volumes", s.auth(s.listVolumes))
	mux.HandleFunc("POST /api/volumes", s.auth(s.createVolume))
	mux.HandleFunc("PUT /api/volumes/{id}", s.auth(s.editVolume))
	mux.HandleFunc("DELETE /api/volumes/{id}", s.auth(s.deleteVolume))
	mux.HandleFunc("POST /api/volumes/{id}/attach", s.auth(s.attachVolume))
	mux.HandleFunc("POST /api/volumes/{id}/detach", s.auth(s.detachVolume))
	mux.HandleFunc("GET /api/volumes/{id}/files", s.auth(s.listVolumeFiles))
	mux.HandleFunc("GET /api/volumes/{id}/file", s.auth(s.readVolumeFile))
	mux.HandleFunc("PUT /api/volumes/{id}/file", s.auth(s.writeVolumeFile))
	mux.HandleFunc("DELETE /api/volumes/{id}/file", s.auth(s.deleteVolumeFile))
	mux.HandleFunc("POST /api/volumes/{id}/folder", s.auth(s.createVolumeFolder))
	mux.HandleFunc("POST /api/instances/{id}/resources", s.auth(s.updateInstanceResources))
	mux.HandleFunc("GET /api/storage/s3", s.auth(s.getS3Config))
	mux.HandleFunc("PUT /api/storage/s3", s.auth(s.saveS3Config))
	mux.HandleFunc("GET /api/storage/connections", s.auth(s.listS3Connections))
	mux.HandleFunc("POST /api/storage/connections", s.auth(s.createS3Connection))
	mux.HandleFunc("PUT /api/storage/connections/{id}", s.auth(s.updateS3Connection))
	mux.HandleFunc("DELETE /api/storage/connections/{id}", s.auth(s.deleteS3Connection))
	mux.HandleFunc("GET /api/storage/connections/{id}/objects", s.auth(s.listS3Objects))
	mux.HandleFunc("GET /api/storage/connections/{id}/object", s.auth(s.getS3Object))
	mux.HandleFunc("PUT /api/storage/connections/{id}/object", s.auth(s.putS3Object))
	mux.HandleFunc("DELETE /api/storage/connections/{id}/object", s.auth(s.deleteS3Object))
	mux.HandleFunc("POST /api/storage/connections/{id}/folders", s.auth(s.createS3Folder))
	mux.HandleFunc("POST /api/instances/{id}/s3-mount", s.auth(s.mountS3))
	mux.HandleFunc("DELETE /api/instances/{id}/s3-mount", s.auth(s.unmountS3))
	mux.HandleFunc("POST /api/instances/{id}/ports", s.auth(s.addPortMapping))
	mux.HandleFunc("POST /api/instances/{id}/protection", s.auth(s.setProtectionMode))
	mux.HandleFunc("POST /api/instances/{id}/ssh-login", s.auth(s.setSSHLoginBlocked))
	mux.HandleFunc("POST /api/instances/{id}/delete-protection", s.auth(s.setDeleteProtection))
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
	mux.HandleFunc("POST /api/instances/{id}/uninstall-service", s.auth(s.uninstallService))
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
	case "/":
		return "home", "Overview", "", true
	case "/instances":
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
	case "/volumes":
		return "volumes", "Persistent Volumes", "", true
	case "/settings":
		return "settings", "Settings", "", true
	case "/iam":
		return "iam", "Identity and Access Management", "", true
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

func (s *Server) setSSHLoginBlocked(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, http.StatusBadRequest, "enabled must be true or false")
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
	if inst.State == proto.StateDeleting {
		fail(w, http.StatusConflict, "cannot change SSH login policy while VPS is deleting")
		return
	}
	wasBlocked := inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked
	if wasBlocked == *in.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "ssh_login_blocked": wasBlocked, "unchanged": true})
		return
	}
	previous := inst
	inst.Spec.SSHLoginBlocked = in.Enabled
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save SSH login policy")
		return
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/" + proto.ActionUpdateSSHLogin + "/" + randHex(4), Action: proto.ActionUpdateSSHLogin, InstanceID: id, Spec: &spec}); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue SSH login policy update")
		return
	}
	s.Store.Audit("admin", "instance.ssh_login", id, fmt.Sprintf("blocked=%t", *in.Enabled))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "ssh_login_blocked": *in.Enabled, "queued": true, "restart_required": inst.State == proto.StateRunning})
}

func (s *Server) setDeleteProtection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, http.StatusBadRequest, "enabled must be true or false")
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
	if inst.State == proto.StateDeleting {
		fail(w, http.StatusConflict, "cannot change delete protection while VPS is deleting")
		return
	}
	if inst.Spec.DeleteProtection == *in.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "delete_protection": inst.Spec.DeleteProtection, "unchanged": true})
		return
	}
	previous := inst
	inst.Spec.DeleteProtection = *in.Enabled
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save delete protection")
		return
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{ID: id + "/" + proto.ActionUpdateDeleteProtection + "/" + randHex(4), Action: proto.ActionUpdateDeleteProtection, InstanceID: id, Spec: &spec}); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue delete protection update")
		return
	}
	s.Store.Audit("admin", "instance.delete_protection", id, fmt.Sprintf("enabled=%t", *in.Enabled))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "delete_protection": *in.Enabled, "queued": true})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.authenticate(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		resource := permissionResource(r.URL.Path)
		action := "manage"
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			action = "read"
		}
		required := resource + ":" + action
		if p.Role == "Read Only" && action == "manage" {
			fail(w, http.StatusForbidden, "Read Only role cannot change resources")
			return
		}
		if resource == "iam" && action == "manage" && p.Role != "Full Administrator" && !p.Root {
			fail(w, http.StatusForbidden, "IAM administration requires the Full Administrator role")
			return
		}
		if !hasIAMPermission(p, required) {
			fail(w, http.StatusForbidden, "permission denied: "+required)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), iamPrincipalContextKey{}, p))
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

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	volumes, err := s.Store.ListVolumes()
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not list volumes")
		return
	}
	if volumes == nil {
		volumes = []store.Volume{}
	}
	writeJSON(w, http.StatusOK, volumes)
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		WorkerID string `json:"worker_id"`
		LimitMB  int64  `json:"limit_mb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid volume settings")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 80 || !apiInstanceID.MatchString(in.WorkerID) || in.LimitMB < 0 || (in.LimitMB > 0 && in.LimitMB < 64) || in.LimitMB > 1_048_576 {
		fail(w, http.StatusBadRequest, "volume name and worker are required; a hard limit must be 0 (unlimited) or 64–1,048,576 MB")
		return
	}
	wk, err := s.Store.GetWorker(in.WorkerID)
	if err != nil || wk.Status != "active" {
		fail(w, http.StatusConflict, "select an active worker")
		return
	}
	v := store.Volume{ID: "vol-" + randHex(8), Name: in.Name, WorkerID: in.WorkerID, State: "CREATING", LimitMB: in.LimitMB, Created: time.Now()}
	if err := s.Store.SaveVolume(v); err != nil {
		fail(w, http.StatusInternalServerError, "could not save volume")
		return
	}
	op := proto.Operation{ID: "volume/" + v.ID + "/create/" + randHex(4), Action: proto.ActionCreateVolume, VolumeID: v.ID, VolumeName: v.Name, VolumeLimitMB: v.LimitMB}
	if err := s.Store.QueueOp(v.WorkerID, op); err != nil {
		_ = s.Store.DeleteVolume(v.ID)
		fail(w, http.StatusInternalServerError, "could not queue volume creation")
		return
	}
	s.Store.Audit("admin", "volume.create", v.ID, v.WorkerID)
	writeJSON(w, http.StatusAccepted, v)
}

func (s *Server) editVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name string `json:"name"`
	}
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid volume id")
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid volume settings")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 80 {
		fail(w, http.StatusBadRequest, "volume name must be 1–80 characters")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	v, err := s.Store.GetVolume(id)
	if err != nil {
		fail(w, http.StatusNotFound, "volume not found")
		return
	}
	if v.State == "CREATING" || v.State == "ATTACHING" || v.State == "DETACHING" || v.State == "DELETING" {
		fail(w, http.StatusConflict, "wait for the current volume action to finish")
		return
	}
	v.Name = in.Name
	if err := s.Store.SaveVolume(v); err != nil {
		fail(w, http.StatusInternalServerError, "could not update volume")
		return
	}
	s.Store.Audit("admin", "volume.edit", id, in.Name)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid volume id")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	v, err := s.Store.GetVolume(id)
	if err != nil {
		fail(w, http.StatusNotFound, "volume not found")
		return
	}
	if v.InstanceID != "" || v.State == "ATTACHED" || v.State == "ATTACHING" || v.State == "DETACHING" {
		fail(w, http.StatusConflict, "detach the volume before deleting it")
		return
	}
	if v.State != "READY" && v.State != "FAILED" {
		fail(w, http.StatusConflict, "volume is not ready to delete")
		return
	}
	previous := v
	v.State = "DELETING"
	v.Error = ""
	if err := s.Store.SaveVolume(v); err != nil {
		fail(w, http.StatusInternalServerError, "could not update volume")
		return
	}
	op := proto.Operation{ID: "volume/" + id + "/delete/" + randHex(4), Action: proto.ActionDeleteVolume, VolumeID: id}
	if err := s.Store.QueueOp(v.WorkerID, op); err != nil {
		_ = s.Store.SaveVolume(previous)
		fail(w, http.StatusInternalServerError, "could not queue volume deletion")
		return
	}
	s.Store.Audit("admin", "volume.delete", id, "queued; data removal requested")
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queued": true, "data_deleted": true})
}

func (s *Server) attachVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		InstanceID string `json:"instance_id"`
		GuestPath  string `json:"guest_path"`
	}
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid volume id")
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil || !apiInstanceID.MatchString(in.InstanceID) || !validS3MountPath(in.GuestPath) {
		fail(w, http.StatusBadRequest, "existing VPS and a clean guest path outside protected system directories are required")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	v, err := s.Store.GetVolume(id)
	if err != nil {
		fail(w, http.StatusNotFound, "volume not found")
		return
	}
	if v.State != "READY" {
		fail(w, http.StatusConflict, "volume must be ready and detached before attaching")
		return
	}
	inst, err := s.Store.GetInstance(in.InstanceID)
	if err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	if inst.WorkerID != v.WorkerID {
		fail(w, http.StatusConflict, "volume and VPS must be on the same worker")
		return
	}
	if inst.State != proto.StateRunning && inst.State != proto.StateStopped {
		fail(w, http.StatusConflict, "VPS must be running or stopped")
		return
	}
	if storagePathsConflict(in.GuestPath, inst.Spec.S3MountPath) {
		fail(w, http.StatusConflict, "storage mount paths cannot overlap")
		return
	}
	for _, m := range inst.Spec.StorageMounts {
		if m.GuestPath != "" && storagePathsConflict(m.GuestPath, in.GuestPath) {
			fail(w, http.StatusConflict, "storage mount paths cannot overlap")
			return
		}
	}
	prevInst, prevVol := inst, v
	inst.Spec.StorageMounts = append(inst.Spec.StorageMounts, proto.StorageMount{ID: v.ID, GuestPath: in.GuestPath})
	v.State = "ATTACHING"
	v.InstanceID = inst.ID
	v.GuestPath = in.GuestPath
	v.Error = ""
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save VPS mount")
		return
	}
	if err := s.Store.SaveVolume(v); err != nil {
		_ = s.Store.SaveInstance(prevInst)
		fail(w, http.StatusInternalServerError, "could not save volume attachment")
		return
	}
	op := proto.Operation{ID: inst.ID + "/" + proto.ActionUpdateStorageMounts + "/" + v.ID + "/" + randHex(4), Action: proto.ActionUpdateStorageMounts, InstanceID: inst.ID, Spec: &inst.Spec, VolumeID: v.ID}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(prevInst)
		_ = s.Store.SaveVolume(prevVol)
		fail(w, http.StatusInternalServerError, "could not queue volume attachment")
		return
	}
	s.Store.Audit("admin", "volume.attach", id, inst.ID+":"+in.GuestPath)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "state": "ATTACHING", "restart_required": inst.State == proto.StateRunning})
}

func (s *Server) detachVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid volume id")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	v, err := s.Store.GetVolume(id)
	if err != nil {
		fail(w, http.StatusNotFound, "volume not found")
		return
	}
	if v.State != "ATTACHED" || v.InstanceID == "" {
		fail(w, http.StatusConflict, "volume is not attached")
		return
	}
	inst, err := s.Store.GetInstance(v.InstanceID)
	if err != nil {
		fail(w, http.StatusConflict, "attached VPS is unavailable")
		return
	}
	if inst.State != proto.StateRunning && inst.State != proto.StateStopped {
		fail(w, http.StatusConflict, "VPS must be running or stopped")
		return
	}
	prevInst, prevVol := inst, v
	mounts := make([]proto.StorageMount, 0, len(inst.Spec.StorageMounts))
	found := false
	for _, m := range inst.Spec.StorageMounts {
		if m.ID == id {
			found = true
			continue
		}
		mounts = append(mounts, m)
	}
	if !found {
		fail(w, http.StatusConflict, "VPS metadata does not contain this volume mount")
		return
	}
	inst.Spec.StorageMounts = mounts
	v.State = "DETACHING"
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save VPS mount change")
		return
	}
	if err := s.Store.SaveVolume(v); err != nil {
		_ = s.Store.SaveInstance(prevInst)
		fail(w, http.StatusInternalServerError, "could not save volume state")
		return
	}
	op := proto.Operation{ID: inst.ID + "/" + proto.ActionUpdateStorageMounts + "/" + id + "/" + randHex(4), Action: proto.ActionUpdateStorageMounts, InstanceID: inst.ID, Spec: &inst.Spec, VolumeID: id}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(prevInst)
		_ = s.Store.SaveVolume(prevVol)
		fail(w, http.StatusInternalServerError, "could not queue volume detach")
		return
	}
	s.Store.Audit("admin", "volume.detach", id, inst.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "state": "DETACHING", "restart_required": inst.State == proto.StateRunning, "data_retained": true})
}

func validVolumeRelativePath(name string, allowRoot bool) bool {
	if name == "" {
		return allowRoot
	}
	if pathpkg.IsAbs(name) || pathpkg.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || len(name) > 1024 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (s *Server) requestVolumeFile(r *http.Request, volumeID string, request proto.VolumeFileRequest) (proto.VolumeFileResult, int, string) {
	if !apiInstanceID.MatchString(volumeID) || !validVolumeRelativePath(request.Path, request.Action == "list") {
		return proto.VolumeFileResult{}, http.StatusBadRequest, "invalid volume path"
	}
	volume, err := s.Store.GetVolume(volumeID)
	if err != nil {
		return proto.VolumeFileResult{}, http.StatusNotFound, "volume not found"
	}
	if volume.State != "READY" && volume.State != "ATTACHED" {
		return proto.VolumeFileResult{}, http.StatusConflict, "volume must be ready or attached to browse files"
	}
	worker, err := s.Store.GetWorker(volume.WorkerID)
	if err != nil || worker.Status != "active" {
		return proto.VolumeFileResult{}, http.StatusConflict, "volume worker is not active"
	}
	opID := "fileop/" + randHex(12)
	resultCh := make(chan proto.OpResult, 1)
	s.volumeFileMu.Lock()
	s.volumeFileWait[opID] = volumeFileWaiter{workerID: volume.WorkerID, result: resultCh}
	s.volumeFileMu.Unlock()
	op := proto.Operation{ID: opID, Action: "volume-file", VolumeID: volumeID, VolumeFile: &request}
	if err := s.Store.QueueOp(volume.WorkerID, op); err != nil {
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, opID)
		s.volumeFileMu.Unlock()
		return proto.VolumeFileResult{}, http.StatusInternalServerError, "could not queue worker file operation"
	}
	select {
	case result := <-resultCh:
		if !result.OK {
			return proto.VolumeFileResult{}, http.StatusBadRequest, result.Error
		}
		if result.VolumeFile == nil {
			return proto.VolumeFileResult{}, http.StatusBadGateway, "worker returned no file result"
		}
		return *result.VolumeFile, http.StatusOK, ""
	case <-time.After(30 * time.Second):
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, opID)
		s.volumeFileMu.Unlock()
		return proto.VolumeFileResult{}, http.StatusGatewayTimeout, "worker file operation timed out; refresh and try again"
	case <-r.Context().Done():
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, opID)
		s.volumeFileMu.Unlock()
		return proto.VolumeFileResult{}, http.StatusRequestTimeout, "request cancelled"
	}
}

func (s *Server) listVolumeFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	result, code, message := s.requestVolumeFile(r, r.PathValue("id"), proto.VolumeFileRequest{VolumeID: r.PathValue("id"), Action: "list", Path: r.URL.Query().Get("path")})
	if code != http.StatusOK {
		fail(w, code, message)
		return
	}
	if result.Entries == nil {
		result.Entries = []proto.VolumeFileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": r.URL.Query().Get("path"), "entries": result.Entries})
}

func (s *Server) readVolumeFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	result, code, message := s.requestVolumeFile(r, r.PathValue("id"), proto.VolumeFileRequest{VolumeID: r.PathValue("id"), Action: "read", Path: name})
	if code != http.StatusOK {
		fail(w, code, message)
		return
	}
	if r.URL.Query().Get("text") == "1" {
		if len(result.Content) > 2<<20 {
			fail(w, http.StatusRequestEntityTooLarge, "file is larger than the 2 MB edit limit; download it instead")
			return
		}
		if !utf8.Valid(result.Content) {
			fail(w, http.StatusUnsupportedMediaType, "binary files cannot be edited as text")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"content": string(result.Content)})
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(pathpkg.Base(name), `"`, "")+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(result.Content)
}

func (s *Server) writeVolumeFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !validVolumeRelativePath(name, false) {
		fail(w, http.StatusBadRequest, "invalid volume file path")
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "file upload limit is 4 MB")
		return
	}
	_, code, message := s.requestVolumeFile(r, r.PathValue("id"), proto.VolumeFileRequest{VolumeID: r.PathValue("id"), Action: "write", Path: name, Content: content})
	if code != http.StatusOK {
		fail(w, code, message)
		return
	}
	s.Store.Audit("admin", "volume.file.write", r.PathValue("id"), name)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "path": name, "size": len(content)})
}

func (s *Server) createVolumeFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || !validVolumeRelativePath(input.Path, false) {
		fail(w, http.StatusBadRequest, "invalid volume folder path")
		return
	}
	_, code, message := s.requestVolumeFile(r, r.PathValue("id"), proto.VolumeFileRequest{VolumeID: r.PathValue("id"), Action: "mkdir", Path: input.Path})
	if code != http.StatusOK {
		fail(w, code, message)
		return
	}
	s.Store.Audit("admin", "volume.folder.create", r.PathValue("id"), input.Path)
	writeJSON(w, http.StatusCreated, map[string]any{"created": true, "path": input.Path})
}

func (s *Server) deleteVolumeFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !validVolumeRelativePath(name, false) {
		fail(w, http.StatusBadRequest, "invalid volume path")
		return
	}
	_, code, message := s.requestVolumeFile(r, r.PathValue("id"), proto.VolumeFileRequest{VolumeID: r.PathValue("id"), Action: "delete", Path: name, Recursive: r.URL.Query().Get("recursive") == "1"})
	if code != http.StatusOK {
		fail(w, code, message)
		return
	}
	s.Store.Audit("admin", "volume.file.delete", r.PathValue("id"), name)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "path": name})
}

func (s *Server) completeVolumeFileResult(workerID string, result proto.OpResult) {
	s.volumeFileMu.Lock()
	waiter, ok := s.volumeFileWait[result.OpID]
	if ok && waiter.workerID == workerID {
		delete(s.volumeFileWait, result.OpID)
	}
	s.volumeFileMu.Unlock()
	if ok && waiter.workerID == workerID {
		select {
		case waiter.result <- result:
		default:
		}
	}
}

func (s *Server) updateInstanceResources(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	var in struct {
		RAMMB  int64   `json:"ram_mb"`
		CPU    float64 `json:"cpu"`
		DiskMB int64   `json:"disk_mb"`
		SwapMB int64   `json:"swap_mb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid resource settings")
		return
	}
	if in.RAMMB < 128 || in.RAMMB > 1<<20 || in.CPU < .1 || in.CPU > 1024 || in.DiskMB < 128 || in.DiskMB > 1<<30 || in.SwapMB < 0 || in.SwapMB > 1<<20 {
		fail(w, http.StatusBadRequest, "RAM, CPU, disk allocation, or swap is outside supported limits")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.State != proto.StateRunning && inst.State != proto.StateStopped {
		fail(w, http.StatusConflict, "VPS resources can only be edited while running or stopped")
		return
	}
	wk, err := s.Store.GetWorker(inst.WorkerID)
	if err != nil || wk.Status != "active" {
		fail(w, http.StatusConflict, "assigned worker is unavailable")
		return
	}
	oldRAM, oldSwap, oldDisk, oldCPU := uint64(inst.Spec.RAMMB)<<20, uint64(proto.SwapLimitMB(inst.Spec))<<20, uint64(inst.Spec.DiskMB)<<20, inst.Spec.CPU
	if wk.Capacity.RAMCommitted >= oldRAM {
		wk.Capacity.RAMCommitted -= oldRAM
	}
	if wk.Capacity.SwapCommitted >= oldSwap {
		wk.Capacity.SwapCommitted -= oldSwap
	}
	if wk.Capacity.DiskCommitted >= oldDisk {
		wk.Capacity.DiskCommitted -= oldDisk
	}
	if wk.Capacity.CPUAllocated >= oldCPU {
		wk.Capacity.CPUAllocated -= oldCPU
	}
	_, placeErr := scheduler.Place([]scheduler.Candidate{{ID: wk.ID, Active: true, LastHeartbeat: wk.LastHeartbeat, Capacity: wk.Capacity}}, scheduler.Request{RAM: uint64(in.RAMMB) << 20, Swap: uint64(in.SwapMB) << 20, Disk: uint64(in.DiskMB) << 20, CPU: in.CPU}, s.Sched, time.Now())
	if placeErr != nil {
		fail(w, http.StatusConflict, "worker cannot fit the requested resource limits: "+placeErr.Error())
		return
	}
	previous := inst
	wasRunning := inst.State == proto.StateRunning
	inst.Spec.RAMMB, inst.Spec.CPU, inst.Spec.DiskMB = in.RAMMB, in.CPU, in.DiskMB
	inst.Spec.SwapMB = &in.SwapMB
	if wasRunning {
		inst.State = proto.StateRestarting
	}
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save VPS resources")
		return
	}
	spec := inst.Spec
	op := proto.Operation{ID: id + "/" + proto.ActionUpdateResources + "/" + randHex(4), Action: proto.ActionUpdateResources, InstanceID: id, Spec: &spec}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, "could not queue VPS resource update")
		return
	}
	s.Store.Audit("admin", "instance.resources.update", id, "queued")
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queued": true, "restart_required": wasRunning, "disk_quota_enforced": false})
}

func (s *Server) listS3Connections(w http.ResponseWriter, r *http.Request) {
	items, err := s.S3.PublicConnections()
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not read S3 connections")
		return
	}
	if items == nil {
		items = []objectstore.PublicConfig{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createS3Connection(w http.ResponseWriter, r *http.Request) {
	var c objectstore.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&c); err != nil {
		fail(w, http.StatusBadRequest, "invalid S3 connection")
		return
	}
	c.ID = "s3-" + randHex(8)
	c.Name = strings.TrimSpace(c.Name)
	if err := s.S3.SaveConnection(c); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.create", c.ID, c.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"connection": objectstore.PublicConfig{ID: c.ID, Name: c.Name, Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, Configured: true}})
}

func (s *Server) updateS3Connection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid connection id")
		return
	}
	old, err := s.S3.GetConnection(id)
	if err != nil {
		fail(w, http.StatusNotFound, "S3 connection not found")
		return
	}
	var c objectstore.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&c); err != nil {
		fail(w, http.StatusBadRequest, "invalid S3 connection")
		return
	}
	c.ID = id
	if c.AccessKey == "" {
		c.AccessKey = old.AccessKey
	}
	if c.SecretKey == "" {
		c.SecretKey = old.SecretKey
	}
	if strings.TrimSpace(c.Name) == "" {
		c.Name = old.Name
	}
	if err := s.S3.SaveConnection(c); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.edit", id, c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"connection": objectstore.PublicConfig{ID: c.ID, Name: c.Name, Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, Configured: true}})
}

func (s *Server) deleteS3Connection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid connection id")
		return
	}
	instances, _ := s.Store.ListInstances()
	for _, i := range instances {
		if i.Spec.S3MountPath != "" && (i.Spec.S3ConnectionID == id || (i.Spec.S3ConnectionID == "" && id == "s3-default")) {
			fail(w, http.StatusConflict, "unmount this connection from all VPSs before deleting it")
			return
		}
	}
	if err := s.S3.DeleteConnection(id); err != nil {
		if os.IsNotExist(err) {
			fail(w, http.StatusNotFound, "S3 connection not found")
		} else {
			fail(w, http.StatusInternalServerError, "could not delete S3 connection")
		}
		return
	}
	s.Store.Audit("admin", "storage.s3.delete", id, "ok")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) s3Connection(w http.ResponseWriter, r *http.Request) objectstore.Config {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid connection id")
		return objectstore.Config{}
	}
	c, err := s.S3.GetConnection(id)
	if err != nil {
		fail(w, http.StatusNotFound, "S3 connection not found")
	}
	return c
}

func validS3ObjectKey(key string, allowEmpty bool) bool {
	if key == "" {
		return allowEmpty
	}
	if strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00\r\n") || len(key) > 1024 {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(key, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (s *Server) listS3Objects(w http.ResponseWriter, r *http.Request) {
	c := s.s3Connection(w, r)
	if c.ID == "" {
		return
	}
	prefix, cursor := r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor")
	if !validS3ObjectKey(prefix, true) {
		fail(w, http.StatusBadRequest, "invalid bucket prefix")
		return
	}
	listing, err := objectstore.List(c, prefix, cursor)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) getS3Object(w http.ResponseWriter, r *http.Request) {
	c := s.s3Connection(w, r)
	if c.ID == "" {
		return
	}
	key := r.URL.Query().Get("key")
	if !validS3ObjectKey(key, false) {
		fail(w, http.StatusBadRequest, "invalid object key")
		return
	}
	if r.URL.Query().Get("text") == "1" {
		b, contentType, err := objectstore.Get(c, key)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if !utf8.Valid(b) || !(strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "json") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "xml")) {
			fail(w, http.StatusUnsupportedMediaType, "object is not editable text")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"content": string(b), "content_type": contentType})
		return
	}
	resp, err := objectstore.Open(c, key)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(pathpkg.Base(key), `"`, "")+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) putS3Object(w http.ResponseWriter, r *http.Request) {
	c := s.s3Connection(w, r)
	if c.ID == "" {
		return
	}
	key := r.URL.Query().Get("key")
	if !validS3ObjectKey(key, false) {
		fail(w, http.StatusBadRequest, "invalid object key")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<20))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "file exceeds the 128 MB upload limit")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := objectstore.Put(c, key, contentType, body); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.object.put", c.ID, key)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "key": key, "size": len(body)})
}

func (s *Server) createS3Folder(w http.ResponseWriter, r *http.Request) {
	c := s.s3Connection(w, r)
	if c.ID == "" {
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || !validS3ObjectKey(in.Key, false) {
		fail(w, http.StatusBadRequest, "invalid folder name")
		return
	}
	if !strings.HasSuffix(in.Key, "/") {
		in.Key += "/"
	}
	if err := objectstore.Put(c, in.Key, "application/x-directory", nil); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.folder.create", c.ID, in.Key)
	writeJSON(w, http.StatusCreated, map[string]any{"created": true, "key": in.Key})
}

func (s *Server) deleteS3Object(w http.ResponseWriter, r *http.Request) {
	c := s.s3Connection(w, r)
	if c.ID == "" {
		return
	}
	key := r.URL.Query().Get("key")
	if !validS3ObjectKey(key, false) {
		fail(w, http.StatusBadRequest, "invalid object key")
		return
	}
	folder := strings.HasSuffix(key, "/")
	if err := objectstore.Delete(c, key, folder); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Store.Audit("admin", "storage.s3.object.delete", c.ID, key)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "key": key})
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
	if !strings.HasPrefix(p, "/") || p == "/" || pathpkg.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") || len(p) > 256 {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, root := range []string{"/proc", "/sys", "/dev", "/run", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/root", "/var/lib", "/var/log", "/var/cache", "/var/run"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return false
		}
	}
	return true
}

func storagePathsConflict(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (s *Server) mountS3(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	var in struct {
		GuestPath    string `json:"guest_path"`
		ConnectionID string `json:"connection_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil || !validS3MountPath(in.GuestPath) {
		fail(w, http.StatusBadRequest, "guest_path must be a clean absolute guest path outside protected system directories")
		return
	}
	c, err := s.S3.GetConnection(in.ConnectionID)
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
	for _, mount := range inst.Spec.StorageMounts {
		if mount.GuestPath != "" && storagePathsConflict(mount.GuestPath, in.GuestPath) {
			fail(w, http.StatusConflict, "S3 and local storage mount paths cannot overlap")
			return
		}
	}
	previous := inst
	inst.Spec.S3MountPath = in.GuestPath
	inst.Spec.S3ConnectionID = c.ID
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, "could not save S3 mount setting")
		return
	}
	spec := inst.Spec
	mount := &proto.S3Mount{ConnectionID: c.ID, Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, AccessKey: c.AccessKey, SecretKey: c.SecretKey, GuestPath: in.GuestPath}
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
	inst.Spec.S3ConnectionID = ""
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
	s.usageMu.RLock()
	state := s.serviceInstalls[id][in.ServiceID]
	s.usageMu.RUnlock()
	if state == "INSTALLING" || state == "UNINSTALLING" || state == "UNINSTALL_FAILED" {
		fail(w, http.StatusConflict, "wait for the current service change to finish")
		return
	}
	if state == "INSTALLED" {
		fail(w, http.StatusConflict, "service is already installed")
		return
	}
	op := proto.Operation{ID: id + "/" + proto.ActionInstallService + "/" + randHex(4), Action: proto.ActionInstallService, InstanceID: id, ServiceID: in.ServiceID}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		fail(w, http.StatusInternalServerError, "could not queue service installation")
		return
	}
	s.usageMu.Lock()
	if s.serviceInstalls[id] == nil {
		s.serviceInstalls[id] = map[string]string{}
	}
	s.serviceInstalls[id][in.ServiceID] = "INSTALLING"
	s.usageMu.Unlock()
	s.Store.Audit("admin", "instance.service.install", id, in.ServiceID)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "service_id": in.ServiceID, "queued": true})
}

func (s *Server) uninstallService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid VPS ID")
		return
	}
	var in struct {
		ServiceID string `json:"service_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || !serviceCatalog[in.ServiceID] || in.ServiceID == "python" {
		fail(w, http.StatusBadRequest, "unknown service or service cannot be safely uninstalled from CloFix")
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
		fail(w, http.StatusConflict, "VPS must be running to uninstall a service")
		return
	}
	if inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode {
		fail(w, http.StatusConflict, "disable Protection Mode in VPS Details before uninstalling packages")
		return
	}
	s.usageMu.RLock()
	state := s.serviceInstalls[id][in.ServiceID]
	s.usageMu.RUnlock()
	if state != "INSTALLED" && state != "FAILED" && state != "UNINSTALL_FAILED" {
		fail(w, http.StatusConflict, "service is not installed or is already changing state")
		return
	}
	op := proto.Operation{ID: id + "/" + proto.ActionUninstallService + "/" + randHex(4), Action: proto.ActionUninstallService, InstanceID: id, ServiceID: in.ServiceID}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		fail(w, http.StatusInternalServerError, "could not queue service uninstall")
		return
	}
	s.usageMu.Lock()
	if s.serviceInstalls[id] == nil {
		s.serviceInstalls[id] = map[string]string{}
	}
	s.serviceInstalls[id][in.ServiceID] = "UNINSTALLING"
	s.usageMu.Unlock()
	s.Store.Audit("admin", "instance.service.uninstall", id, in.ServiceID)
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
		if action == proto.ActionDelete {
			s.placeMu.Lock()
			defer s.placeMu.Unlock()
		}
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
			if inst.Spec.DeleteProtection {
				fail(w, http.StatusConflict, "VPS Delete Protection is enabled; disable it in VPS Details first")
				return
			}
			volumes, _ := s.Store.ListVolumes()
			for _, volume := range volumes {
				if volume.InstanceID == inst.ID {
					fail(w, http.StatusConflict, "detach persistent volumes before deleting this VPS")
					return
				}
			}
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

func (s *Server) completeVolumeOperation(workerID, volumeID, action string, result proto.OpResult) {
	v, err := s.Store.GetVolume(volumeID)
	if err != nil || v.WorkerID != workerID {
		return
	}
	if action == "create" {
		if result.OK {
			v.State, v.Error = "READY", ""
		} else {
			v.State, v.Error = "FAILED", result.Error
		}
		_ = s.Store.SaveVolume(v)
		s.Store.Audit(workerID, "volume.create.result", volumeID, map[bool]string{true: "ready", false: result.Error}[result.OK])
	} else if action == "delete" {
		if result.OK {
			_ = s.Store.DeleteVolume(volumeID)
		} else {
			v.State, v.Error = "READY", result.Error
			_ = s.Store.SaveVolume(v)
		}
		s.Store.Audit(workerID, "volume.delete.result", volumeID, map[bool]string{true: "deleted", false: result.Error}[result.OK])
	}
}

func (s *Server) completeVolumeAttachment(workerID, instanceID, volumeID string, result proto.OpResult) {
	v, err := s.Store.GetVolume(volumeID)
	if err != nil || v.WorkerID != workerID {
		return
	}
	inst, err := s.Store.GetInstance(instanceID)
	if err != nil || inst.WorkerID != workerID {
		return
	}
	if result.OK {
		if v.State == "ATTACHING" {
			v.State = "ATTACHED"
		} else if v.State == "DETACHING" {
			v.State, v.InstanceID, v.GuestPath = "READY", "", ""
		}
		v.Error = ""
	} else {
		v.Error = result.Error
		if v.State == "ATTACHING" {
			v.State, v.InstanceID, v.GuestPath = "READY", "", ""
			mounts := make([]proto.StorageMount, 0, len(inst.Spec.StorageMounts))
			for _, m := range inst.Spec.StorageMounts {
				if m.ID != volumeID {
					mounts = append(mounts, m)
				}
			}
			inst.Spec.StorageMounts = mounts
			_ = s.Store.SaveInstance(inst)
			_ = s.Store.QueueOp(workerID, proto.Operation{ID: instanceID + "/" + proto.ActionUpdateStorageMounts + "/rollback/" + randHex(4), Action: proto.ActionUpdateStorageMounts, InstanceID: instanceID, Spec: &inst.Spec, VolumeID: volumeID})
		} else if v.State == "DETACHING" {
			v.State = "ATTACHED"
			found := false
			for _, m := range inst.Spec.StorageMounts {
				if m.ID == volumeID {
					found = true
				}
			}
			if !found {
				inst.Spec.StorageMounts = append(inst.Spec.StorageMounts, proto.StorageMount{ID: volumeID, GuestPath: v.GuestPath})
				_ = s.Store.SaveInstance(inst)
			}
			_ = s.Store.QueueOp(workerID, proto.Operation{ID: instanceID + "/" + proto.ActionUpdateStorageMounts + "/rollback/" + randHex(4), Action: proto.ActionUpdateStorageMounts, InstanceID: instanceID, Spec: &inst.Spec, VolumeID: volumeID})
		}
	}
	_ = s.Store.SaveVolume(v)
	s.Store.Audit(workerID, "volume.mount.result", volumeID, map[bool]string{true: "ok", false: result.Error}[result.OK])
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<20)).Decode(&hb); err != nil {
		fail(w, 400, "invalid json")
		return
	}
	wk.Capacity, wk.Version, wk.LastHeartbeat = hb.Capacity, hb.Version, time.Now()
	if wk.Status == "pending" {
		wk.Status = "active" // TODO: gate on policy/health checks and optional admin approval
	}
	s.Store.UpsertWorker(wk)
	for _, reported := range hb.Volumes {
		v, err := s.Store.GetVolume(reported.ID)
		if err != nil || v.WorkerID != id || v.State == "DELETING" {
			continue
		}
		v.UsageBytes = reported.UsageBytes
		if v.State == "CREATING" {
			v.State = "READY"
		}
		_ = s.Store.SaveVolume(v)
	}

	for _, st := range hb.Instances {
		if inst, err := s.Store.GetInstance(st.ID); err == nil && inst.WorkerID == id && inst.State != proto.StateDeleting {
			inst.State, inst.Error = st.State, st.Error
			s.Store.SaveInstance(inst)
			s.usageMu.Lock()
			s.usage[st.ID] = st.Metrics
			s.services[st.ID] = st.Services
			reported := st.ServiceInstalls
			if reported == nil {
				reported = map[string]string{}
			}
			for serviceID, pending := range s.serviceInstalls[st.ID] {
				reportedState := reported[serviceID]
				if pending == "INSTALLING" && reportedState != "INSTALLING" && reportedState != "INSTALLED" && reportedState != "FAILED" {
					reported[serviceID] = pending
				}
				if pending == "UNINSTALLING" && reportedState == "INSTALLED" {
					reported[serviceID] = pending
				}
			}
			s.serviceInstalls[st.ID] = reported
			s.usageMu.Unlock()
		}
	}
	for _, res := range hb.Results {
		if strings.HasPrefix(res.OpID, "fileop/") {
			s.completeVolumeFileResult(id, res)
			continue
		}
		parts := strings.Split(res.OpID, "/") // instance or volume operation ID
		if len(parts) == 4 && parts[0] == "volume" {
			s.completeVolumeOperation(id, parts[1], parts[2], res)
			continue
		}
		if len(parts) == 4 && parts[1] == proto.ActionUpdateStorageMounts {
			s.completeVolumeAttachment(id, parts[0], parts[2], res)
			continue
		}
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
		case res.OK && parts[1] == proto.ActionUninstallService:
			if strings.HasPrefix(inst.Error, "service uninstall failed:") {
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
			} else if parts[1] == proto.ActionUninstallService {
				inst.Error = "service uninstall failed: " + res.Error
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
