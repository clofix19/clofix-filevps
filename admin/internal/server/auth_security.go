package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

type mfaChallenge struct {
	UserID  string
	Expires time.Time
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func normalizeIPList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			if _, network, err := net.ParseCIDR(raw); err != nil || network == nil {
				return nil, fmt.Errorf("invalid CIDR %q", raw)
			}
		} else if net.ParseIP(raw) == nil {
			return nil, fmt.Errorf("invalid IP %q", raw)
		}
		if seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
		if len(out) > 64 {
			return nil, fmt.Errorf("at most 64 IP rules")
		}
	}
	return out, nil
}

func ipMatches(list []string, ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, rule := range list {
		if strings.Contains(rule, "/") {
			_, network, err := net.ParseCIDR(rule)
			if err == nil && network.Contains(parsed) {
				return true
			}
			continue
		}
		if net.ParseIP(rule) != nil && net.ParseIP(rule).Equal(parsed) {
			return true
		}
	}
	return false
}

func loginIPAllowed(user store.IAMUser, ip string) error {
	if len(user.DenyIPs) > 0 && ipMatches(user.DenyIPs, ip) {
		return fmt.Errorf("login denied from this IP address")
	}
	if len(user.AllowIPs) > 0 && !ipMatches(user.AllowIPs, ip) {
		return fmt.Errorf("login is not allowed from this IP address")
	}
	return nil
}

func generateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func totpCode(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / 30)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (int(sum[offset])&0x7f)<<24 | (int(sum[offset+1])&0xff)<<16 | (int(sum[offset+2])&0xff)<<8 | (int(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", value%1000000), nil
}

func verifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, skew := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		want, err := totpCode(secret, now.Add(skew))
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return true
		}
	}
	return false
}

func (s *Server) rememberMFAChallenge(userID string) (string, error) {
	raw := randHex(24)
	s.mfaMu.Lock()
	defer s.mfaMu.Unlock()
	if s.mfaPending == nil {
		s.mfaPending = map[string]mfaChallenge{}
	}
	now := time.Now().UTC()
	for token, challenge := range s.mfaPending {
		if now.After(challenge.Expires) {
			delete(s.mfaPending, token)
		}
	}
	s.mfaPending[hashToken(raw)] = mfaChallenge{UserID: userID, Expires: now.Add(5 * time.Minute)}
	return raw, nil
}

func (s *Server) takeMFAChallenge(raw string) (string, bool) {
	s.mfaMu.Lock()
	defer s.mfaMu.Unlock()
	challenge, ok := s.mfaPending[hashToken(raw)]
	if !ok || time.Now().UTC().After(challenge.Expires) {
		delete(s.mfaPending, hashToken(raw))
		return "", false
	}
	delete(s.mfaPending, hashToken(raw))
	return challenge.UserID, true
}

func (s *Server) loginMFAAuth(w http.ResponseWriter, r *http.Request) {
	if s.rejectAuthAbuse(w, r, "mfa") {
		return
	}
	var in struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
		Website  string `json:"website"` // honeypot; must stay empty
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || strings.TrimSpace(in.MFAToken) == "" || strings.TrimSpace(in.Code) == "" {
		fail(w, 400, "mfa_token and authenticator code are required")
		return
	}
	ip := clientIP(r)
	if honeypotFilled(in.Website) {
		s.noteMFAFailure(ip)
		fail(w, 401, "invalid authenticator code")
		return
	}
	userID, ok := s.takeMFAChallenge(strings.TrimSpace(in.MFAToken))
	if !ok {
		s.noteMFAFailure(ip)
		fail(w, 401, "MFA challenge expired; sign in again")
		return
	}
	user, err := s.Store.GetIAMUser(userID)
	if err != nil || user.Status != "active" || !user.MFAEnabled || user.MFASecret == "" {
		s.noteMFAFailure(ip)
		fail(w, 401, "invalid MFA challenge")
		return
	}
	if err := loginIPAllowed(user, ip); err != nil {
		s.noteMFAFailure(ip)
		fail(w, 403, err.Error())
		return
	}
	if !verifyTOTP(user.MFASecret, in.Code, time.Now().UTC()) {
		s.noteMFAFailure(ip)
		fail(w, 401, "invalid authenticator code")
		return
	}
	if err := s.issueSession(w, user); err != nil {
		fail(w, 500, "could not start session")
		return
	}
	s.clearLoginFailures(ip, user.Email)
	writeJSON(w, 200, map[string]any{"user": publicUser(user)})
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	user, err := s.Store.GetIAMUser(p.UserID)
	if err != nil {
		fail(w, 404, "user not found")
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": user.ID, "username": user.Username, "display_name": user.DisplayName, "email": user.Email,
		"kind": user.Kind, "role": user.Role, "account_id": user.AccountID, "status": user.Status,
		"mfa_enabled": user.MFAEnabled, "allow_ips": user.AllowIPs, "deny_ips": user.DenyIPs,
		"client_ip": clientIP(r),
	})
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	user, err := s.Store.GetIAMUser(p.UserID)
	if err != nil {
		fail(w, 404, "user not found")
		return
	}
	var in struct {
		Username        string   `json:"username"`
		DisplayName     *string  `json:"display_name"`
		Email           string   `json:"email"`
		CurrentPassword string   `json:"current_password"`
		NewPassword     string   `json:"new_password"`
		AllowIPs        []string `json:"allow_ips"`
		DenyIPs         []string `json:"deny_ips"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid profile payload")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Username != "" {
		if len(in.Username) < 2 || len(in.Username) > 64 {
			fail(w, 400, "username must be 2–64 characters")
			return
		}
		user.Username = in.Username
	}
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if len(name) > 80 {
			fail(w, 400, "display name is too long")
			return
		}
		user.DisplayName = name
	}
	if in.Email != "" && in.Email != user.Email {
		if !validAuthEmail(in.Email) {
			fail(w, 400, "valid email is required")
			return
		}
		if existing, e := s.Store.GetIAMUserByEmail(in.Email); e == nil && existing.ID != user.ID {
			fail(w, 409, "email already in use")
			return
		}
		user.Email = in.Email
	}
	if in.NewPassword != "" {
		if !checkPassword(user.PasswordHash, in.CurrentPassword) {
			fail(w, 401, "current password is incorrect")
			return
		}
		if !validAuthPassword(in.NewPassword) {
			fail(w, 400, "new password must be at least 12 characters without spaces")
			return
		}
		hash, e := hashPassword(in.NewPassword)
		if e != nil {
			fail(w, 500, "could not store password")
			return
		}
		user.PasswordHash = hash
	}
	if in.AllowIPs != nil {
		allow, e := normalizeIPList(in.AllowIPs)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		user.AllowIPs = allow
	}
	if in.DenyIPs != nil {
		deny, e := normalizeIPList(in.DenyIPs)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		user.DenyIPs = deny
	}
	if err := s.Store.SaveIAMUser(user); err != nil {
		fail(w, 500, "could not save profile")
		return
	}
	s.Store.Audit(p.UserID, "profile.update", user.ID, "updated")
	writeJSON(w, 200, map[string]any{
		"id": user.ID, "username": user.Username, "display_name": user.DisplayName, "email": user.Email,
		"kind": user.Kind, "role": user.Role, "mfa_enabled": user.MFAEnabled, "allow_ips": user.AllowIPs, "deny_ips": user.DenyIPs,
	})
}

func (s *Server) setupProfileMFA(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	user, err := s.Store.GetIAMUser(p.UserID)
	if err != nil {
		fail(w, 404, "user not found")
		return
	}
	if user.MFAEnabled {
		fail(w, 409, "MFA is already enabled")
		return
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		fail(w, 500, "could not create authenticator secret")
		return
	}
	user.MFASecret = secret
	user.MFAEnabled = false
	if err := s.Store.SaveIAMUser(user); err != nil {
		fail(w, 500, "could not save authenticator secret")
		return
	}
	label := user.Email
	if label == "" {
		label = user.Username
	}
	otpauth := "otpauth://totp/" + url.PathEscape("CloFix:"+label) + "?secret=" + url.QueryEscape(secret) + "&issuer=CloFix&digits=6&period=30"
	writeJSON(w, 200, map[string]any{"secret": secret, "otpauth_url": otpauth, "issuer": "CloFix"})
}

func (s *Server) enableProfileMFA(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	user, err := s.Store.GetIAMUser(p.UserID)
	if err != nil {
		fail(w, 404, "user not found")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil || user.MFASecret == "" {
		fail(w, 400, "run MFA setup first, then submit an authenticator code")
		return
	}
	if !verifyTOTP(user.MFASecret, in.Code, time.Now().UTC()) {
		fail(w, 401, "invalid authenticator code")
		return
	}
	user.MFAEnabled = true
	if err := s.Store.SaveIAMUser(user); err != nil {
		fail(w, 500, "could not enable MFA")
		return
	}
	s.Store.Audit(p.UserID, "profile.mfa.enable", user.ID, "enabled")
	writeJSON(w, 200, map[string]any{"mfa_enabled": true})
}

func (s *Server) disableProfileMFA(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	user, err := s.Store.GetIAMUser(p.UserID)
	if err != nil {
		fail(w, 404, "user not found")
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !checkPassword(user.PasswordHash, in.Password) {
		fail(w, 401, "password is required to disable MFA")
		return
	}
	if user.MFAEnabled && !verifyTOTP(user.MFASecret, in.Code, time.Now().UTC()) {
		fail(w, 401, "invalid authenticator code")
		return
	}
	user.MFAEnabled = false
	user.MFASecret = ""
	if err := s.Store.SaveIAMUser(user); err != nil {
		fail(w, 500, "could not disable MFA")
		return
	}
	s.Store.Audit(p.UserID, "profile.mfa.disable", user.ID, "disabled")
	writeJSON(w, 200, map[string]any{"mfa_enabled": false})
}
