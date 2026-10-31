package license

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) initKeyCipher() error {
	file := filepath.Join(filepath.Dir(s.file), "license-key-encryption.key")
	key, e := os.ReadFile(file)
	if os.IsNotExist(e) {
		for _, l := range s.Licenses {
			if len(l.EncryptedKey) > 0 {
				return fmt.Errorf("license key encryption file missing; restore it from backup")
			}
		}
		if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
			return e
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		if e = os.WriteFile(file, key, 0600); e != nil {
			return e
		}
	}
	if e != nil {
		return e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return fmt.Errorf("invalid license key encryption file")
	}
	s.keyCipher, e = cipher.NewGCM(block)
	return e
}
func (s *Service) encryptKey(id, key string) ([]byte, error) {
	nonce := make([]byte, s.keyCipher.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return s.keyCipher.Seal(nonce, nonce, []byte(key), []byte(id)), nil
}
func (s *Service) revealKey(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	l, ok := s.Licenses[r.PathValue("id")]
	if !ok || l.Deleted {
		problem(w, 404, "license not found")
		return
	}
	n := s.keyCipher.NonceSize()
	if len(l.EncryptedKey) < n {
		problem(w, 409, "legacy key is hash-only; restore the original CFL key or issue a replacement license")
		return
	}
	raw, e := s.keyCipher.Open(nil, l.EncryptedKey[:n], l.EncryptedKey[n:], []byte(l.ID))
	if e != nil {
		problem(w, 500, "could not decrypt key; check the license-server encryption key backup")
		return
	}
	respond(w, 200, map[string]string{"key": string(raw)})
}
func (s *Service) restoreKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		problem(w, 400, "original CFL key required")
		return
	}
	in.Key = strings.TrimSpace(in.Key)
	s.Lock()
	defer s.Unlock()
	l, ok := s.Licenses[r.PathValue("id")]
	if !ok || l.Deleted {
		problem(w, 404, "license not found")
		return
	}
	if subtle.ConstantTimeCompare([]byte(hash(in.Key)), []byte(l.KeyHash)) != 1 {
		problem(w, 400, "key does not match this license; no changes saved")
		return
	}
	encrypted, e := s.encryptKey(l.ID, in.Key)
	if e != nil {
		problem(w, 500, "could not encrypt key")
		return
	}
	old := l
	l.EncryptedKey = encrypted
	s.Licenses[l.ID] = l
	if e = s.save(); e != nil {
		s.Licenses[l.ID] = old
		problem(w, 500, "could not restore key")
		return
	}
	respond(w, 200, map[string]bool{"restored": true})
}
