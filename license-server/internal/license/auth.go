package license

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type adminSession struct {
	Expires time.Time
}

func (s *Service) loginAuth(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		problem(w, 400, "username and password required")
		return
	}
	user := strings.TrimSpace(in.Username)
	pass := in.Password
	if user == "" || pass == "" {
		problem(w, 400, "username and password required")
		return
	}
	s.Lock()
	account, ok := s.Admins[user]
	s.Unlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(pass)) != nil {
		problem(w, 401, "invalid username or password")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		problem(w, 500, "could not create session")
		return
	}
	token := hex.EncodeToString(raw)
	s.Lock()
	defer s.Unlock()
	now := time.Now().UTC()
	for id, sess := range s.sessions {
		if now.After(sess.Expires) {
			delete(s.sessions, id)
		}
	}
	s.sessions[token] = adminSession{Expires: now.Add(12 * time.Hour)}
	respond(w, 200, map[string]string{"token": token})
}

func (s *Service) logoutAuth(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.Lock()
	delete(s.sessions, token)
	s.Unlock()
	respond(w, 200, map[string]string{"ok": "logged out"})
}

func (s *Service) sessionOK(token string) bool {
	if token == "" {
		return false
	}
	s.Lock()
	defer s.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().UTC().After(sess.Expires) {
		delete(s.sessions, token)
		return false
	}
	return true
}
