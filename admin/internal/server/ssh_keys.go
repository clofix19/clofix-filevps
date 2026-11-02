package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type sshPEMKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	Created   time.Time `json:"created"`
}

type sshKeyStore struct {
	sync.Mutex
	Keys map[string]sshPEMKey `json:"keys"`
	file string
}

func loadSSHKeys(dir string) *sshKeyStore {
	d := &sshKeyStore{file: filepath.Join(dir, "ssh-keys.json"), Keys: map[string]sshPEMKey{}}
	if raw, err := os.ReadFile(d.file); err == nil {
		_ = json.Unmarshal(raw, d)
	}
	if d.Keys == nil {
		d.Keys = map[string]sshPEMKey{}
	}
	return d
}

func (d *sshKeyStore) save() error {
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

func (s *Server) sshKeyRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/ssh-keys", s.auth(s.listSSHKeys))
	m.HandleFunc("POST /api/ssh-keys", s.auth(s.createSSHKey))
	m.HandleFunc("DELETE /api/ssh-keys/{id}", s.auth(s.deleteSSHKey))
}

func (s *Server) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	s.sshKeys.Lock()
	defer s.sshKeys.Unlock()
	out := make([]sshPEMKey, 0, len(s.sshKeys.Keys))
	for _, k := range s.sshKeys.Keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, 200, map[string]any{"keys": out})
}

func (s *Server) createSSHKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		fail(w, 400, "name must be 1–80 characters")
		return
	}
	key := sshPEMKey{ID: "pem-" + randHex(6), Name: name, Created: time.Now()}
	privatePEM := ""
	if pub := strings.TrimSpace(in.PublicKey); pub != "" {
		if !validSSHPublicKey(pub) {
			fail(w, 400, "a valid single-line SSH public key is required")
			return
		}
		key.PublicKey = pub
	} else {
		pub, priv, err := generateEd25519PEM(name)
		if err != nil {
			fail(w, 500, "could not generate PEM key")
			return
		}
		key.PublicKey, privatePEM = pub, priv
	}
	s.sshKeys.Lock()
	s.sshKeys.Keys[key.ID] = key
	err := s.sshKeys.save()
	s.sshKeys.Unlock()
	if err != nil {
		fail(w, 500, "could not save PEM key")
		return
	}
	s.Store.Audit("admin", "ssh-key.create", key.ID, "ok")
	writeJSON(w, 201, map[string]any{"key": key, "pem": privatePEM})
}

func (s *Server) deleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.sshKeys.Lock()
	defer s.sshKeys.Unlock()
	if _, ok := s.sshKeys.Keys[id]; !ok {
		fail(w, 404, "PEM key not found")
		return
	}
	delete(s.sshKeys.Keys, id)
	if err := s.sshKeys.save(); err != nil {
		fail(w, 500, "could not delete PEM key")
		return
	}
	s.Store.Audit("admin", "ssh-key.delete", id, "ok")
	writeJSON(w, 200, map[string]string{"id": id, "deleted": "true"})
}

func (s *Server) lookupSSHKey(id string) (sshPEMKey, bool) {
	s.sshKeys.Lock()
	defer s.sshKeys.Unlock()
	k, ok := s.sshKeys.Keys[id]
	return k, ok
}

func generateEd25519PEM(comment string) (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return sshEd25519Public(pub, comment), string(block), nil
}

func sshEd25519Public(pub ed25519.PublicKey, comment string) string {
	payload := appendSSHString(nil, "ssh-ed25519")
	payload = appendSSHBytes(payload, pub)
	if comment = strings.TrimSpace(comment); comment == "" {
		comment = "clofix"
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(payload) + " " + comment
}

func appendSSHString(dst []byte, s string) []byte {
	return appendSSHBytes(dst, []byte(s))
}

func appendSSHBytes(dst []byte, b []byte) []byte {
	n := len(b)
	dst = append(dst, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	return append(dst, b...)
}

func validRootPassword(pw string) bool {
	if len(pw) < 8 || len(pw) > 128 {
		return false
	}
	for _, r := range pw {
		if r == '\n' || r == '\r' || r == ':' || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
