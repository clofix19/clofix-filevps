package server

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
)

const secretsAAD = "clofix-secrets-v1"

var (
	secretRefID   = regexp.MustCompile(`^sec:[A-Za-z0-9._:-]+$`)
	secretRefTmpl = regexp.MustCompile(`^\{\{\s*secret:([^}]+)\s*\}\}$`)
)

type secretSyncTarget struct {
	ClusterID string    `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	At        time.Time `json:"at"`
}

type vaultSecret struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Type        string             `json:"type"` // string | key_value
	Ciphertext  []byte             `json:"ciphertext"`
	Owner       string             `json:"owner"`
	Created     time.Time          `json:"created"`
	Updated     time.Time          `json:"updated"`
	Version     int                `json:"version"`
	SyncTargets []secretSyncTarget `json:"sync_targets,omitempty"`
}

type secretsStore struct {
	sync.Mutex
	Secrets map[string]vaultSecret `json:"secrets"`
	file    string
}

func loadSecrets(dir string) *secretsStore {
	d := &secretsStore{file: filepath.Join(dir, "secrets.json"), Secrets: map[string]vaultSecret{}}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid secrets.json")
		}
	}
	if d.Secrets == nil {
		d.Secrets = map[string]vaultSecret{}
	}
	return d
}

func (d *secretsStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) secretsCipher() (cipher.AEAD, error) {
	return s.applicationCipher()
}

func (s *Server) encryptVaultSecret(plain []byte) ([]byte, error) {
	aead, err := s.secretsCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte(secretsAAD)), nil
}

func (s *Server) decryptVaultSecret(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty ciphertext")
	}
	aead, err := s.secretsCipher()
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("invalid secret ciphertext")
	}
	return aead.Open(nil, data[:n], data[n:], []byte(secretsAAD))
}

func (s *Server) secretsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/secrets", s.auth(s.listSecrets))
	m.HandleFunc("POST /api/secrets", s.auth(s.createSecret))
	m.HandleFunc("PUT /api/secrets/{id}", s.auth(s.updateSecret))
	m.HandleFunc("DELETE /api/secrets/{id}", s.auth(s.deleteSecret))
	m.HandleFunc("POST /api/secrets/{id}/reveal", s.auth(s.revealSecret))
	m.HandleFunc("GET /api/secrets/{id}/value", s.auth(s.getSecretValueByID))
	m.HandleFunc("GET /api/secrets/by-name/{name}/value", s.auth(s.getSecretValueByName))
	m.HandleFunc("POST /api/secrets/{id}/sync-cks", s.auth(s.syncSecretToCKS))
}

func canAccessSecret(r *http.Request, owner string) bool {
	return canAccessApplication(r, owner)
}

func publicSecret(sec vaultSecret) map[string]any {
	out := map[string]any{
		"id": sec.ID, "name": sec.Name, "description": sec.Description, "type": sec.Type,
		"owner": sec.Owner, "created": sec.Created, "updated": sec.Updated, "version": sec.Version,
	}
	if len(sec.SyncTargets) > 0 {
		out["sync_targets"] = sec.SyncTargets
	}
	return out
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	d := s.secrets
	d.Lock()
	defer d.Unlock()
	out := make([]map[string]any, 0, len(d.Secrets))
	for _, sec := range d.Secrets {
		if !canAccessSecret(r, sec.Owner) {
			continue
		}
		out = append(out, publicSecret(sec))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})
	writeJSON(w, 200, map[string]any{"secrets": out})
}

func normalizeSecretType(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	if t == "" {
		return "string"
	}
	if t == "string" || t == "key_value" {
		return t
	}
	return ""
}

func encodeSecretPlain(secretType string, value string, values map[string]string) ([]byte, error) {
	switch secretType {
	case "string":
		if strings.TrimSpace(value) == "" && len(values) == 0 {
			return nil, fmt.Errorf("value is required")
		}
		if value == "" && len(values) == 1 {
			for _, v := range values {
				value = v
			}
		}
		return []byte(value), nil
	case "key_value":
		if len(values) == 0 {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("key_value secrets require values")
			}
			var parsed map[string]string
			if err := json.Unmarshal([]byte(value), &parsed); err != nil || len(parsed) == 0 {
				return nil, fmt.Errorf("key_value must be a non-empty JSON object")
			}
			values = parsed
		}
		return json.Marshal(values)
	default:
		return nil, fmt.Errorf("type must be string or key_value")
	}
}

func (s *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Type        string            `json:"type"`
		Value       string            `json:"value"`
		Values      map[string]string `json:"values"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		fail(w, 400, "invalid request")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		fail(w, 400, "name must be 1–80 characters")
		return
	}
	secretType := normalizeSecretType(in.Type)
	if secretType == "" {
		fail(w, 400, "type must be string or key_value")
		return
	}
	plain, err := encodeSecretPlain(secretType, in.Value, in.Values)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	cipherText, err := s.encryptVaultSecret(plain)
	if err != nil {
		fail(w, 500, "cannot encrypt secret")
		return
	}
	owner := applicationOwner(r)
	now := time.Now().UTC()
	id := "sec-" + randHex(8)
	sec := vaultSecret{
		ID: id, Name: name, Description: strings.TrimSpace(in.Description), Type: secretType,
		Ciphertext: cipherText, Owner: owner, Created: now, Updated: now, Version: 1,
	}
	d := s.secrets
	d.Lock()
	for _, existing := range d.Secrets {
		if existing.Owner == owner && strings.EqualFold(existing.Name, name) {
			d.Unlock()
			fail(w, 409, "secret name already exists in this account")
			return
		}
	}
	d.Secrets[id] = sec
	err = d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.auditAccount(r, owner, "secrets.create", id, name, nil)
	out := publicSecret(sec)
	out["value"] = string(plain)
	if secretType == "key_value" {
		var values map[string]string
		_ = json.Unmarshal(plain, &values)
		out["values"] = values
		delete(out, "value")
	}
	out["warning"] = "The secret value is shown once. Copy it now; use Reveal later if needed."
	writeJSON(w, 201, out)
}

func (s *Server) updateSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name        *string           `json:"name"`
		Description *string           `json:"description"`
		Value       *string           `json:"value"`
		Values      map[string]string `json:"values"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		fail(w, 400, "invalid request")
		return
	}
	d := s.secrets
	d.Lock()
	sec, ok := d.Secrets[id]
	if !ok || !canAccessSecret(r, sec.Owner) {
		d.Unlock()
		fail(w, 404, "secret not found")
		return
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 80 {
			d.Unlock()
			fail(w, 400, "name must be 1–80 characters")
			return
		}
		for _, existing := range d.Secrets {
			if existing.ID != id && existing.Owner == sec.Owner && strings.EqualFold(existing.Name, name) {
				d.Unlock()
				fail(w, 409, "secret name already exists in this account")
				return
			}
		}
		sec.Name = name
	}
	if in.Description != nil {
		sec.Description = strings.TrimSpace(*in.Description)
	}
	changedValue := false
	if in.Value != nil || in.Values != nil {
		plain, err := encodeSecretPlain(sec.Type, derefString(in.Value), in.Values)
		if err != nil {
			d.Unlock()
			fail(w, 400, err.Error())
			return
		}
		cipherText, err := s.encryptVaultSecret(plain)
		if err != nil {
			d.Unlock()
			fail(w, 500, "cannot encrypt secret")
			return
		}
		sec.Ciphertext = cipherText
		sec.Version++
		changedValue = true
	}
	sec.Updated = time.Now().UTC()
	d.Secrets[id] = sec
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	detail := sec.Name
	if changedValue {
		detail += " value"
	}
	s.audit(r, "secrets.update", id, detail, nil)
	writeJSON(w, 200, publicSecret(sec))
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.secrets
	d.Lock()
	sec, ok := d.Secrets[id]
	if !ok || !canAccessSecret(r, sec.Owner) {
		d.Unlock()
		fail(w, 404, "secret not found")
		return
	}
	delete(d.Secrets, id)
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.audit(r, "secrets.delete", id, sec.Name, nil)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) revealSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.secrets
	d.Lock()
	sec, ok := d.Secrets[id]
	if !ok || !canAccessSecret(r, sec.Owner) {
		d.Unlock()
		fail(w, 404, "secret not found")
		return
	}
	cipherText := append([]byte(nil), sec.Ciphertext...)
	secretType := sec.Type
	name := sec.Name
	d.Unlock()
	plain, err := s.decryptVaultSecret(cipherText)
	if err != nil {
		fail(w, 500, "cannot decrypt secret")
		return
	}
	s.audit(r, "secrets.reveal", id, name, nil)
	out := map[string]any{"id": id, "name": name, "type": secretType}
	if secretType == "key_value" {
		var values map[string]string
		if err := json.Unmarshal(plain, &values); err != nil {
			fail(w, 500, "invalid key_value payload")
			return
		}
		out["values"] = values
	} else {
		out["value"] = string(plain)
	}
	writeJSON(w, 200, out)
}

func secretOwnedBy(sec vaultSecret, accountID string) bool {
	if accountID == "" || sec.Owner == accountID {
		return true
	}
	// Admin/platform resources may stamp owner as root or acc-platform.
	platform := sec.Owner == "" || sec.Owner == "root" || sec.Owner == "acc-platform"
	caller := accountID == "root" || accountID == "acc-platform"
	return platform && caller
}

func (s *Server) findSecretLocked(accountID, idOrName string) (vaultSecret, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if sec, ok := s.secrets.Secrets[idOrName]; ok {
		if secretOwnedBy(sec, accountID) {
			return sec, true
		}
		return vaultSecret{}, false
	}
	for _, sec := range s.secrets.Secrets {
		if !strings.EqualFold(sec.Name, idOrName) {
			continue
		}
		if secretOwnedBy(sec, accountID) {
			return sec, true
		}
	}
	return vaultSecret{}, false
}

func (s *Server) getSecretValueByID(w http.ResponseWriter, r *http.Request) {
	s.writeSecretRuntimeValue(w, r, r.PathValue("id"))
}

func (s *Server) getSecretValueByName(w http.ResponseWriter, r *http.Request) {
	s.writeSecretRuntimeValue(w, r, r.PathValue("name"))
}

func (s *Server) writeSecretRuntimeValue(w http.ResponseWriter, r *http.Request, idOrName string) {
	accountID := applicationOwner(r)
	d := s.secrets
	d.Lock()
	sec, ok := s.findSecretLocked(accountID, idOrName)
	if !ok {
		// Admin may fetch by id across accounts when they can access the owner.
		if sec2, ok2 := d.Secrets[idOrName]; ok2 && canAccessSecret(r, sec2.Owner) {
			sec, ok = sec2, true
		} else {
			for _, candidate := range d.Secrets {
				if strings.EqualFold(candidate.Name, idOrName) && canAccessSecret(r, candidate.Owner) {
					sec, ok = candidate, true
					break
				}
			}
		}
	}
	if !ok {
		d.Unlock()
		fail(w, 404, "secret not found")
		return
	}
	cipherText := append([]byte(nil), sec.Ciphertext...)
	secretType := sec.Type
	id, name := sec.ID, sec.Name
	d.Unlock()
	plain, err := s.decryptVaultSecret(cipherText)
	if err != nil {
		fail(w, 500, "cannot decrypt secret")
		return
	}
	s.auditAccount(r, accountID, "secrets.runtime_get", id, name, nil)
	out := map[string]any{"id": id, "name": name, "type": secretType, "version": sec.Version}
	if secretType == "key_value" {
		var values map[string]string
		if err := json.Unmarshal(plain, &values); err != nil {
			fail(w, 500, "invalid key_value payload")
			return
		}
		out["values"] = values
	} else {
		out["value"] = string(plain)
	}
	writeJSON(w, 200, out)
}

func parseSecretRef(value string) (ref string, ok bool) {
	value = strings.TrimSpace(value)
	if secretRefID.MatchString(value) {
		return strings.TrimPrefix(value, "sec:"), true
	}
	if m := secretRefTmpl.FindStringSubmatch(value); len(m) == 2 {
		return strings.TrimSpace(m[1]), true
	}
	return "", false
}

// resolveSecretRefs replaces sec:… and {{secret:…}} env values for the given account.
// Missing or cross-account refs return an error (do not start with empty secret).
func (s *Server) resolveSecretRefs(accountID string, env map[string]string) (map[string]string, error) {
	if len(env) == 0 {
		return env, nil
	}
	out := make(map[string]string, len(env))
	d := s.secrets
	d.Lock()
	defer d.Unlock()
	for k, v := range env {
		ref, isRef := parseSecretRef(v)
		if !isRef {
			out[k] = v
			continue
		}
		sec, ok := s.findSecretLocked(accountID, ref)
		if !ok {
			return nil, fmt.Errorf("secret ref %q not found for this account", v)
		}
		plain, err := s.decryptVaultSecret(sec.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("cannot decrypt secret %s", sec.ID)
		}
		if sec.Type == "key_value" {
			out[k] = string(plain)
		} else {
			out[k] = string(plain)
		}
	}
	return out, nil
}

func (s *Server) secretDataMap(sec vaultSecret) (map[string]string, error) {
	plain, err := s.decryptVaultSecret(sec.Ciphertext)
	if err != nil {
		return nil, err
	}
	if sec.Type == "key_value" {
		values := map[string]string{}
		if err := json.Unmarshal(plain, &values); err != nil {
			return nil, fmt.Errorf("invalid key_value payload")
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("key_value secret has no keys")
		}
		return values, nil
	}
	key := sec.Name
	if key == "" {
		key = "value"
	}
	// Prefer a stable key "value" for string secrets so pods can use secretKeyRef.
	return map[string]string{"value": string(plain)}, nil
}

func (s *Server) syncSecretToCKS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		ClusterID string `json:"cluster_id"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		fail(w, 400, "invalid request")
		return
	}
	in.ClusterID = strings.TrimSpace(in.ClusterID)
	in.Namespace = strings.TrimSpace(in.Namespace)
	in.Name = strings.TrimSpace(strings.ToLower(in.Name))
	if in.ClusterID == "" || in.Name == "" {
		fail(w, 400, "cluster_id and name are required")
		return
	}
	if in.Namespace == "" {
		in.Namespace = "default"
	}
	if !k8s.Name.MatchString(in.Name) {
		fail(w, 400, "Kubernetes secret name must be a DNS label")
		return
	}
	d := s.secrets
	d.Lock()
	sec, ok := d.Secrets[id]
	if !ok || !canAccessSecret(r, sec.Owner) {
		d.Unlock()
		fail(w, 404, "secret not found")
		return
	}
	secCopy := sec
	d.Unlock()

	data, err := s.secretDataMap(secCopy)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	jobID, err := s.applyCKSSecret(r, in.ClusterID, in.Namespace, in.Name, data)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	now := time.Now().UTC()
	d.Lock()
	if current, ok := d.Secrets[id]; ok {
		updated := false
		for i := range current.SyncTargets {
			t := &current.SyncTargets[i]
			if t.ClusterID == in.ClusterID && t.Namespace == in.Namespace && t.Name == in.Name {
				t.At = now
				updated = true
				break
			}
		}
		if !updated {
			current.SyncTargets = append(current.SyncTargets, secretSyncTarget{
				ClusterID: in.ClusterID, Namespace: in.Namespace, Name: in.Name, At: now,
			})
		}
		current.Updated = now
		d.Secrets[id] = current
		_ = d.save()
		sec = current
	}
	d.Unlock()
	s.audit(r, "secrets.cks_sync", id, in.ClusterID+"/"+in.Namespace+"/"+in.Name, nil)
	writeJSON(w, 202, map[string]any{
		"status": "queued", "job_id": jobID, "secret": publicSecret(sec),
		"cluster_id": in.ClusterID, "namespace": in.Namespace, "name": in.Name,
	})
}

func (s *Server) applyCKSSecret(r *http.Request, clusterID, namespace, name string, data map[string]string) (string, error) {
	if s.kubernetes == nil {
		return "", fmt.Errorf("kubernetes is unavailable")
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	cluster, ok := d.Clusters[clusterID]
	if !ok || cluster.State == k8s.StateDeleted || !canAccessApplication(r, cluster.Owner) {
		return "", fmt.Errorf("cluster not found")
	}
	if cluster.State != k8s.StateRunning && cluster.State != k8s.StateDegraded {
		return "", fmt.Errorf("workloads can be changed after the cluster is running")
	}
	if s.kubernetesClusterBusyLocked(cluster.ID) {
		return "", fmt.Errorf("cluster has an active operation")
	}
	yaml, err := buildKubernetesResourceYAML(kubernetesResourceRequest{
		Kind: "secret", Name: name, Namespace: namespace, Data: data,
	})
	if err != nil {
		return "", err
	}
	control, ok := kubernetesControlNode(cluster)
	if !ok {
		return "", fmt.Errorf("control plane node is missing")
	}
	before := s.kubernetesCheckpoint()
	op := k8s.Operation{
		Spec: k8s.Spec{
			ID: cluster.ID, Version: cluster.Version, InfrastructureMode: cluster.InfrastructureMode,
			Role: k8s.RoleControlPlane, CPU: cluster.NodePool.CPU, RAMMB: cluster.NodePool.RAMMB, DiskMB: cluster.NodePool.DiskMB,
		},
		YAML: yaml, Namespace: namespace, Name: name, Kind: "Secret", Action: k8s.ActionApply,
	}
	job, err := s.queueKubernetesLocked(cluster, control, k8s.ActionApply, op)
	if err != nil {
		return "", err
	}
	if err = s.saveKubernetes(before); err != nil {
		return "", err
	}
	return job.ID, nil
}
