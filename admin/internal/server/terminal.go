package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin: func(r *http.Request) bool {
		// Same-origin admin UI only; cookie session already required.
		return true
	},
}

type terminalAuthMsg struct {
	Method     string `json:"method"` // password | pem
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

type terminalControlMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Msg  string `json:"message,omitempty"`
}

func (s *Server) instanceTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	p := principalFrom(r)
	if p.Role == "Read Only" || !hasIAMPermission(p, "instances:manage") {
		fail(w, http.StatusForbidden, "permission denied: instances:manage")
		return
	}
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if p.Kind != "admin" && inst.AccountID != "" && !s.canAccessAccount(p, inst.AccountID) {
		fail(w, http.StatusForbidden, "instance belongs to another account")
		return
	}
	if inst.State != proto.StateRunning {
		fail(w, http.StatusConflict, "VPS must be running")
		return
	}
	if inst.Spec.SSHPort <= 0 {
		fail(w, http.StatusConflict, "SSH port is not assigned")
		return
	}
	if protectionEnabled(inst.Spec) {
		fail(w, http.StatusConflict, "disable Protection Mode before opening a shell")
		return
	}
	if inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked {
		fail(w, http.StatusConflict, "SSH login is blocked for this VPS")
		return
	}

	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	auth, err := readTerminalAuth(conn)
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: err.Error()})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if auth.Cols < 20 {
		auth.Cols = 80
	}
	if auth.Rows < 5 {
		auth.Rows = 24
	}

	clientConfig, err := buildSSHClientConfig(auth, inst.Spec)
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: err.Error()})
		return
	}
	addr := net.JoinHostPort(s.sshWorkerHost(inst.WorkerID), fmt.Sprintf("%d", inst.Spec.SSHPort))
	sshConn, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: mapSSHDialError(err, auth)})
		return
	}
	defer sshConn.Close()

	session, err := sshConn.NewSession()
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "SSH session failed: " + err.Error()})
		return
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "stdin pipe failed"})
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "stdout pipe failed"})
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "stderr pipe failed"})
		return
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", auth.Rows, auth.Cols, modes); err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "PTY request failed: " + err.Error()})
		return
	}
	if err := session.Shell(); err != nil {
		writeTerminalJSON(conn, terminalControlMsg{Type: "error", Msg: "shell start failed: " + err.Error()})
		return
	}
	writeTerminalJSON(conn, terminalControlMsg{Type: "ready"})
	s.audit(r, "instance.terminal", id, "shell opened", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		copySSHToWS(conn, io.MultiReader(stdout, stderr))
	}()
	go func() {
		copyWSToSSH(conn, stdin, session)
		_ = stdin.Close()
	}()
	<-done
	_ = session.Wait()
}

func readTerminalAuth(conn *websocket.Conn) (terminalAuthMsg, error) {
	// Skip early resize/noise frames that the xterm FitAddon may send before auth.
	for i := 0; i < 8; i++ {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return terminalAuthMsg{}, fmt.Errorf("auth message missing")
		}
		var auth terminalAuthMsg
		if err := json.Unmarshal(raw, &auth); err != nil {
			return terminalAuthMsg{}, fmt.Errorf("invalid auth payload")
		}
		if strings.TrimSpace(auth.Method) == "" {
			continue
		}
		return auth, nil
	}
	return terminalAuthMsg{}, fmt.Errorf("auth message missing")
}

func buildSSHClientConfig(auth terminalAuthMsg, spec proto.InstanceSpec) (*ssh.ClientConfig, error) {
	method := strings.ToLower(strings.TrimSpace(auth.Method))
	cfg := &ssh.ClientConfig{
		User:            "root",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         12 * time.Second,
	}
	switch method {
	case "password", "pam":
		if !spec.PasswordAuth {
			return nil, fmt.Errorf("this VPS is PEM/publickey-only; switch Terminal to PEM key (PAM password is disabled)")
		}
		if strings.TrimSpace(auth.Password) == "" {
			return nil, fmt.Errorf("password is required for PAM login")
		}
		pw := auth.Password
		// Guests use sshd PasswordAuthentication (UsePAM no). Keep
		// keyboard-interactive as a fallback for older PAM-enabled drop-ins.
		cfg.Auth = []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				ans := make([]string, len(questions))
				for i := range questions {
					ans[i] = pw
				}
				return ans, nil
			}),
		}
	case "pem", "key":
		if strings.TrimSpace(spec.SSHAuthorizedKey) == "" && !spec.PasswordAuth {
			return nil, fmt.Errorf("this VPS has no SSH authorized key installed")
		}
		key := normalizeSSHPEM(auth.PrivateKey)
		if key == "" {
			return nil, fmt.Errorf("private PEM key is required")
		}
		if looksLikeSSHPublicKey(key) {
			return nil, fmt.Errorf("paste the private PEM key (-----BEGIN … PRIVATE KEY-----), not the public ssh-ed25519 line")
		}
		signer, err := parseSSHPrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
		if authorized := strings.TrimSpace(spec.SSHAuthorizedKey); authorized != "" {
			if !sshSignerMatchesAuthorizedKey(signer, authorized) {
				return nil, fmt.Errorf("this private key does not match the VPS authorized_keys; use the PEM downloaded when the VPS was created")
			}
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	default:
		return nil, fmt.Errorf("method must be password or pem")
	}
	return cfg, nil
}

func mapSSHDialError(err error, auth terminalAuthMsg) string {
	msg := err.Error()
	method := strings.ToLower(strings.TrimSpace(auth.Method))
	switch {
	case strings.Contains(msg, "attempted methods [none]") && !strings.Contains(msg, "publickey") && !strings.Contains(msg, "password"):
		if method == "password" || method == "pam" {
			return "SSH connect failed: PAM/password is not offered by this VPS (publickey-only). Switch Terminal to PEM key."
		}
		return "SSH connect failed: no mutual auth method (server is publickey-only). Use the matching private PEM."
	case strings.Contains(msg, "publickey"):
		return "SSH connect failed: publickey rejected. Use the private PEM that matches this VPS authorized_keys."
	case strings.Contains(msg, "password") || strings.Contains(msg, "keyboard-interactive"):
		return "SSH connect failed: password rejected. Re-enter the root password from Access (wait a few seconds after updating it), or use PEM."
	default:
		return "SSH connect failed: " + msg
	}
}

func normalizeSSHPEM(pemBytes string) string {
	key := strings.ReplaceAll(pemBytes, "\r\n", "\n")
	key = strings.ReplaceAll(key, "\r", "\n")
	key = strings.TrimSpace(key)
	// Strip UTF-8 BOM from browser file picks / editors.
	key = strings.TrimPrefix(key, "\ufeff")
	return strings.TrimSpace(key)
}

func looksLikeSSHPublicKey(s string) bool {
	line := strings.TrimSpace(strings.Split(s, "\n")[0])
	for _, p := range []string{"ssh-ed25519 ", "ssh-rsa ", "ecdsa-sha2-", "sk-ssh-ed25519@", "sk-ecdsa-sha2-"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func sshSignerMatchesAuthorizedKey(signer ssh.Signer, authorized string) bool {
	want := strings.Fields(strings.TrimSpace(authorized))
	if len(want) < 2 {
		return true
	}
	got := strings.Fields(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
	if len(got) < 2 {
		return false
	}
	return got[0] == want[0] && got[1] == want[1]
}

func parseSSHPrivateKey(pemBytes string) (ssh.Signer, error) {
	key := []byte(pemBytes)
	signer, err := ssh.ParsePrivateKey(key)
	if err == nil {
		return signer, nil
	}
	// Reject passphrase-protected keys for the browser console (no interactive prompt).
	if _, ok := err.(*ssh.PassphraseMissingError); ok {
		return nil, fmt.Errorf("encrypted PEM keys are not supported in the web terminal; use an unencrypted key or PAM password")
	}
	return nil, err
}

func (s *Server) sshWorkerHost(workerID string) string {
	if h := strings.TrimSpace(os.Getenv("CLOFIX_SSH_WORKER_HOST")); h != "" {
		return h
	}
	if h := strings.TrimSpace(os.Getenv("CLOFIX_LB_WORKER_HOST")); h != "" {
		return h
	}
	if wk, err := s.Store.GetWorker(workerID); err == nil {
		if host := strings.TrimSpace(wk.Hostname); host != "" && host != "localhost" {
			return host
		}
	}
	return "127.0.0.1"
}

func writeTerminalJSON(conn *websocket.Conn, msg terminalControlMsg) {
	b, _ := json.Marshal(msg)
	_ = conn.WriteMessage(websocket.TextMessage, b)
}

func copySSHToWS(conn *websocket.Conn, r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func copyWSToSSH(conn *websocket.Conn, w io.Writer, session *ssh.Session) {
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.TextMessage {
			var ctrl terminalControlMsg
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" {
				cols, rows := ctrl.Cols, ctrl.Rows
				if cols < 20 {
					cols = 80
				}
				if rows < 5 {
					rows = 24
				}
				_ = session.WindowChange(rows, cols)
				continue
			}
		}
		if len(data) == 0 {
			continue
		}
		if _, err := w.Write(data); err != nil {
			return
		}
	}
}

func protectionEnabled(spec proto.InstanceSpec) bool {
	return spec.ProtectionMode == nil || *spec.ProtectionMode
}
