// Package hostworker defines the trusted host-side security control API
// for CloFix shared-kernel VPS. Integrate with FileVPS worker separately.
package hostworker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type AuditEntry struct {
	Time     time.Time      `json:"time"`
	Tenant   string         `json:"tenant"`
	Action   string         `json:"action"`
	OK       bool           `json:"ok"`
	Detail   string         `json:"detail"`
	Remote   string         `json:"remote"`
}

type Server struct {
	Tokens map[string]string // tenant -> shared secret
	mu     sync.Mutex
	Audit  []AuditEntry
	Nonces map[string]int64
}

func NewServer() *Server {
	return &Server{
		Tokens: map[string]string{},
		Nonces: map[string]int64{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/security/", s.handle)
	return mux
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", 400)
		return
	}
	var req struct {
		Action    string         `json:"action"`
		TenantID  string         `json:"tenant_id"`
		Nonce     string         `json:"nonce"`
		Timestamp int64          `json:"ts"`
		Payload   map[string]any `json:"payload"`
	}
	if json.Unmarshal(body, &req) != nil {
		http.Error(w, "json", 400)
		return
	}
	tok := s.Tokens[req.TenantID]
	if tok == "" {
		http.Error(w, "unauthorized", 401)
		return
	}
	sig := r.Header.Get("X-Clofix-Signature")
	mac := hmac.New(sha256.New, []byte(tok))
	mac.Write(body)
	expect := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expect)) {
		http.Error(w, "bad signature", 401)
		return
	}
	now := time.Now().UTC().Unix()
	if abs(now-req.Timestamp) > 60 {
		http.Error(w, "ts skew", 401)
		return
	}
	s.mu.Lock()
	if _, seen := s.Nonces[req.Nonce]; seen {
		s.mu.Unlock()
		http.Error(w, "replay", 401)
		return
	}
	s.Nonces[req.Nonce] = now
	s.mu.Unlock()

	ok, msg := s.dispatch(req.Action, req.TenantID, req.Payload)
	s.mu.Lock()
	s.Audit = append(s.Audit, AuditEntry{
		Time: time.Now().UTC(), Tenant: req.TenantID, Action: req.Action,
		OK: ok, Detail: msg, Remote: r.RemoteAddr,
	})
	s.mu.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "message": msg})
}

func (s *Server) dispatch(action, tenant string, payload map[string]any) (bool, string) {
	switch action {
	case "snapshot_create", "snapshot_rollback", "immutable_enable", "immutable_disable",
		"seccomp_apply", "cgroup_constrain", "netns_policy":
		return true, fmt.Sprintf("accepted %s for tenant %s (wire into FileVPS worker execution engine)", action, tenant)
	default:
		return false, "unknown action"
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
