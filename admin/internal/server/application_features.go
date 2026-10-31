package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func activeApplicationRun(state string) bool { return state == "QUEUED" || state == "RUNNING" }
func cloneApplicationEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	return out
}
func safeApplication(a application) application {
	a.Execution.Artifact = nil
	a.Secrets = nil
	a.EncryptedSecrets = nil
	a.WebhookHash = ""
	versions := make([]applicationVersion, len(a.Versions))
	for i, v := range a.Versions {
		v.Execution.Artifact = nil
		v.EncryptedSecrets = nil
		versions[i] = v
	}
	a.Versions = versions
	return a
}
func validateApplication(a application) error {
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 80 || (a.Kind != "job" && a.Kind != "function") {
		return fmt.Errorf("name and job/function type are required")
	}
	if err := proto.ValidateExecution(a.Execution); err != nil {
		return err
	}
	if err := proto.ValidateExecutionEnv(a.Secrets); err != nil {
		return err
	}
	if len(a.Execution.Env)+len(a.Secrets) > 64 {
		return fmt.Errorf("at most 64 environment variables and secrets combined")
	}
	if a.Schedule != "" {
		if a.Kind != "job" {
			return fmt.Errorf("only jobs can be scheduled")
		}
		if _, err := parseApplicationCron(a.Schedule); err != nil {
			return err
		}
	}
	if a.Execution.InstallDependencies && (a.Execution.Format != "zip" || (a.Execution.Runtime != "python" && a.Execution.Runtime != "nodejs")) {
		return fmt.Errorf("dependency installation requires a Python or Node.js ZIP project")
	}
	return nil
}
func (s *Server) applicationCipher() (cipher.AEAD, error) {
	// Key creation is serialized separately from the application store lock.
	s.applicationKeyMu.Lock()
	defer s.applicationKeyMu.Unlock()
	file := filepath.Join(filepath.Dir(s.applications.file), "application-secrets.key")
	key, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		err = os.WriteFile(file, key, 0600)
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Server) encryptApplicationSecrets(env map[string]string) ([]byte, error) {
	if len(env) == 0 {
		return nil, nil
	}
	aead, err := s.applicationCipher()
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte("clofix-application-secrets-v1")), nil
}
func (s *Server) decryptApplicationSecrets(data []byte) (map[string]string, error) {
	env := map[string]string{}
	if len(data) == 0 {
		return env, nil
	}
	aead, err := s.applicationCipher()
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("invalid secret ciphertext")
	}
	plain, err := aead.Open(nil, data[:n], data[n:], []byte("clofix-application-secrets-v1"))
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(plain, &env)
	return env, err
}
func (s *Server) applicationExtraRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/applications/runtimes", s.auth(s.listApplicationRuntimes))
	mux.HandleFunc("PUT /api/applications/{id}", s.auth(s.updateApplication))
	mux.HandleFunc("POST /api/applications/{id}/rollback/{version}", s.auth(s.rollbackApplication))
	mux.HandleFunc("POST /api/applications/{id}/webhook", s.auth(s.enableApplicationWebhook))
	mux.HandleFunc("DELETE /api/applications/{id}/webhook", s.auth(s.disableApplicationWebhook))
	mux.HandleFunc("POST /hooks/applications/{id}", s.triggerApplicationWebhook)
	mux.HandleFunc("GET /api/application-runs/{id}/output", s.auth(s.downloadApplicationOutput))
}
func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) {
	var update application
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20)).Decode(&update) != nil {
		fail(w, 400, "invalid application JSON")
		return
	}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	old, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, old.Owner) {
		fail(w, 404, "application not found")
		return
	}
	if len(old.Versions) >= 10 {
		fail(w, 409, "maximum 10 versions per application")
		return
	}
	if len(update.Execution.Artifact) == 0 {
		update.Execution.Artifact = old.Execution.Artifact
	}
	if err := validateApplication(update); err != nil {
		fail(w, 400, err.Error())
		return
	}
	encrypted := old.EncryptedSecrets
	if update.Secrets != nil {
		var err error
		encrypted, err = s.encryptApplicationSecrets(update.Secrets)
		if err != nil {
			fail(w, 500, "cannot encrypt secrets")
			return
		}
	}
	retainedSecrets, err := s.decryptApplicationSecrets(encrypted)
	if err != nil {
		fail(w, 500, "cannot decrypt secrets")
		return
	}
	merged := cloneApplicationEnv(update.Execution.Env)
	for k, v := range retainedSecrets {
		merged[k] = v
	}
	if err = proto.ValidateExecutionEnv(merged); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a := old
	a.Name = update.Name
	a.Kind = update.Kind
	a.Schedule = update.Schedule
	a.LastSchedule = ""
	a.ScheduleError = ""
	a.Execution = update.Execution
	a.EncryptedSecrets = encrypted
	a.Version = 1
	for _, v := range old.Versions {
		if v.Number >= a.Version {
			a.Version = v.Number + 1
		}
	}
	a.Versions = append(append([]applicationVersion{}, old.Versions...), applicationVersion{Number: a.Version, Created: time.Now().UTC(), Execution: a.Execution, EncryptedSecrets: encrypted})
	d.Apps[a.ID] = a
	if err := d.save(); err != nil {
		d.Apps[a.ID] = old
		fail(w, 500, "cannot persist version")
		return
	}
	s.Store.Audit(applicationOwner(r), "application.update", a.ID, strconv.Itoa(a.Version))
	writeJSON(w, 200, safeApplication(a))
}
func (s *Server) rollbackApplication(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		fail(w, 400, "invalid version")
		return
	}
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	for _, v := range a.Versions {
		if v.Number == number {
			old := a
			a.Execution = v.Execution
			a.EncryptedSecrets = v.EncryptedSecrets
			a.Version = number
			d.Apps[a.ID] = a
			if d.save() != nil {
				d.Apps[a.ID] = old
				fail(w, 500, "cannot persist rollback")
				return
			}
			writeJSON(w, 200, safeApplication(a))
			return
		}
	}
	fail(w, 404, "version not found")
}
func (s *Server) enableApplicationWebhook(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	secret := randHex(32)
	old := a
	a.WebhookHash = hashToken(secret)
	d.Apps[a.ID] = a
	if d.save() != nil {
		d.Apps[a.ID] = old
		fail(w, 500, "cannot save webhook")
		return
	}
	writeJSON(w, 200, map[string]string{"path": "/hooks/applications/" + a.ID, "secret": secret, "header": "X-CloFix-Webhook-Key"})
}
func (s *Server) disableApplicationWebhook(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	a, ok := d.Apps[r.PathValue("id")]
	if !ok || !canAccessApplication(r, a.Owner) {
		fail(w, 404, "application not found")
		return
	}
	old := a
	a.WebhookHash = ""
	d.Apps[a.ID] = a
	if d.save() != nil {
		d.Apps[a.ID] = old
		fail(w, 500, "cannot disable webhook")
		return
	}
	writeJSON(w, 200, map[string]bool{"disabled": true})
}
func (s *Server) applicationTriggerPrincipal(owner string) (iamPrincipal, bool) {
	if owner == "root" {
		return iamPrincipal{Root: true, Name: "root", Permissions: []string{"*"}}, true
	}
	u, err := s.Store.GetIAMUser(owner)
	if err != nil || u.Status != "active" || u.Role == "Read Only" {
		return iamPrincipal{}, false
	}
	p := iamPrincipal{UserID: u.ID, Name: u.Username, Role: u.Role}
	if u.Role == "Full Administrator" {
		p.Permissions = []string{"*"}
	}
	for _, id := range u.PolicyIDs {
		if policy, err := s.Store.GetIAMPolicy(id); err == nil {
			p.Permissions = append(p.Permissions, policy.Permissions...)
		}
	}
	for _, id := range u.GroupIDs {
		if group, err := s.Store.GetIAMGroup(id); err == nil {
			for _, pid := range group.PolicyIDs {
				if policy, err := s.Store.GetIAMPolicy(pid); err == nil {
					p.Permissions = append(p.Permissions, policy.Permissions...)
				}
			}
		}
	}
	return p, hasIAMPermission(p, "applications:manage")
}
func (s *Server) triggerApplicationWebhook(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	a, ok := d.Apps[r.PathValue("id")]
	d.Unlock()
	candidate := hashToken(r.Header.Get("X-CloFix-Webhook-Key"))
	if !ok || a.WebhookHash == "" || subtle.ConstantTimeCompare([]byte(candidate), []byte(a.WebhookHash)) != 1 {
		fail(w, 401, "invalid webhook credential")
		return
	}
	p, ok := s.applicationTriggerPrincipal(a.Owner)
	if !ok {
		fail(w, 403, "application owner cannot execute applications")
		return
	}
	// Arbitrary webhook JSON/body becomes stdin. This endpoint never fetches URLs.
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		fail(w, 413, "webhook payload exceeds 64 KiB")
		return
	}
	payload, _ := json.Marshal(map[string]string{"input": string(data)})
	r.Body = io.NopCloser(bytes.NewReader(payload))
	r = r.WithContext(context.WithValue(r.Context(), iamPrincipalContextKey{}, p))
	run, err := s.queueApplication(r, a.Kind)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, run)
}
func (s *Server) recordExecutionProgress(workerID string, progress []proto.ExecutionProgress) {
	d := s.applications
	d.Lock()
	defer d.Unlock()
	for _, p := range progress {
		if !strings.HasPrefix(p.OpID, "execution/") {
			continue
		}
		id := strings.TrimPrefix(p.OpID, "execution/")
		run, ok := d.Runs[id]
		if !ok || run.WorkerID != workerID || !activeApplicationRun(run.State) {
			continue
		}
		if len(p.Stdout) > 128<<10 || len(p.Stderr) > 128<<10 {
			continue
		}
		run.State = "RUNNING"
		run.Result = &proto.ExecutionResult{Stdout: p.Stdout, Stderr: p.Stderr, Truncated: p.Truncated, ExitCode: -1}
		d.Runs[id] = run
	}
}
func (s *Server) downloadApplicationOutput(w http.ResponseWriter, r *http.Request) {
	d := s.applications
	d.Lock()
	run, ok := d.Runs[r.PathValue("id")]
	d.Unlock()
	if !ok || !canAccessApplication(r, run.Owner) || run.Result == nil {
		fail(w, 404, "no output files")
		return
	}
	data := run.Result.OutputZIP
	if run.Result.OutputAvailable {
		var err error
		data, err = os.ReadFile(filepath.Join(filepath.Dir(d.file), "application-output", run.ID+".zip"))
		if err != nil {
			fail(w, 404, "output files unavailable")
			return
		}
	}
	if len(data) == 0 {
		fail(w, 404, "no output files")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+run.ID+`-output.zip"`)
	w.Write(data)
}

func (s *Server) listApplicationRuntimes(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Store.ListWorkers()
	if err != nil {
		fail(w, 500, "cannot list runtimes")
		return
	}
	out := []map[string]any{}
	for _, runtime := range proto.ExecutionRuntimes {
		count := 0
		for _, wk := range workers {
			if wk.Status != "active" || time.Since(wk.LastHeartbeat) > time.Minute {
				continue
			}
			for _, ready := range wk.Capacity.ServerlessRuntimes {
				if ready == runtime {
					count++
				}
			}
		}
		out = append(out, map[string]any{"runtime": runtime, "workers": count})
	}
	writeJSON(w, 200, out)
}
