package server

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/clofix/filevps/admin/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie   = "cf_session"
	sessionTTL      = 12 * time.Hour
	platformAccount = "acc-platform"
	minPasswordLen  = 12
)

var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
func checkPassword(hash, pw string) bool {
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
func validAuthEmail(v string) bool {
	v = strings.TrimSpace(v)
	return len(v) >= 5 && len(v) <= 120 && emailPattern.MatchString(v)
}
func validAuthPassword(v string) bool {
	if len(v) < minPasswordLen || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func usernameFromEmail(email string) string {
	local := strings.Split(email, "@")[0]
	var b strings.Builder
	for _, r := range local {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) < 2 {
		return "user" + randHex(4)
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func (s *Server) EnsureBootstrapAdmin() error {
	email := strings.ToLower(strings.TrimSpace(os.Getenv("CLOFIX_ADMIN_EMAIL")))
	pass := os.Getenv("CLOFIX_ADMIN_PASSWORD")
	if email == "" || pass == "" {
		return nil
	}
	if !validAuthEmail(email) || !validAuthPassword(pass) {
		return errInvalidBootstrap
	}
	if _, err := s.Store.GetIAMUserByEmail(email); err == nil {
		return nil
	}
	if _, err := s.Store.GetAccount(platformAccount); err != nil {
		acc := store.Account{ID: platformAccount, Name: "CloFix", Kind: "platform", Created: time.Now().UTC()}
		if err := s.Store.SaveAccount(acc); err != nil {
			return err
		}
	}
	hash, err := hashPassword(pass)
	if err != nil {
		return err
	}
	user := store.IAMUser{
		ID: "usr-" + randHex(8), AccountID: platformAccount, Kind: "admin", Email: email,
		Username: "admin", PasswordHash: hash, Role: "Full Administrator", Status: "active", Created: time.Now().UTC(),
		GroupIDs: []string{}, PolicyIDs: []string{},
	}
	return s.Store.SaveIAMUser(user)
}

var errInvalidBootstrap = errString("CLOFIX_ADMIN_EMAIL and CLOFIX_ADMIN_PASSWORD must be a valid email and a password of at least 12 characters")

func (s *Server) issueSession(w http.ResponseWriter, user store.IAMUser) error {
	raw := randHex(32)
	sess := store.Session{ID: "ses-" + randHex(8), UserID: user.ID, TokenHash: hashToken(raw), Expires: time.Now().UTC().Add(sessionTTL)}
	if err := s.Store.SaveSession(sess); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: sess.Expires, MaxAge: int(sessionTTL.Seconds())})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func (s *Server) principalFromUser(user store.IAMUser) iamPrincipal {
	p := iamPrincipal{UserID: user.ID, Name: user.Username, Email: user.Email, Role: user.Role, Kind: user.Kind, AccountID: user.AccountID, Root: user.Kind == "admin" || user.Kind == "root"}
	if acc, err := s.Store.GetAccount(user.AccountID); err == nil {
		p.ParentID = acc.ParentID
	}
	switch user.Kind {
	case "admin":
		p.Permissions = []string{"*"}
		p.Root = true
	case "partner":
		p.Permissions = []string{"accounts:manage", "billing:manage", "billing:read", "iam:read", "read:*"}
	case "root":
		p.Permissions = []string{"*"}
		p.Root = true
	default:
		switch user.Role {
		case "Full Administrator":
			p.Permissions = append(p.Permissions, "*")
		case "Read Only":
			p.Permissions = append(p.Permissions, "read:*")
		}
		for _, id := range user.PolicyIDs {
			if policy, e := s.Store.GetIAMPolicy(id); e == nil {
				p.Permissions = append(p.Permissions, policy.Permissions...)
			}
		}
		for _, groupID := range user.GroupIDs {
			group, e := s.Store.GetIAMGroup(groupID)
			if e != nil {
				continue
			}
			for _, policyID := range group.PolicyIDs {
				if policy, e := s.Store.GetIAMPolicy(policyID); e == nil {
					p.Permissions = append(p.Permissions, policy.Permissions...)
				}
			}
		}
	}
	if p.Kind != "admin" {
		p.Visible = s.visibleAccountIDSet(p)
	}
	return p
}

func (s *Server) sessionPrincipal(r *http.Request) (iamPrincipal, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if auth != "" && !strings.HasPrefix(auth, "CFX") {
			c = &http.Cookie{Value: auth}
		} else {
			return iamPrincipal{}, false
		}
	}
	sess, err := s.Store.GetSessionByHash(hashToken(c.Value))
	if err != nil {
		return iamPrincipal{}, false
	}
	user, err := s.Store.GetIAMUser(sess.UserID)
	if err != nil || user.Status != "active" {
		return iamPrincipal{}, false
	}
	return s.principalFromUser(user), true
}

func (s *Server) registerAuth(w http.ResponseWriter, r *http.Request) {
	if s.rejectAuthAbuse(w, r, "register") {
		return
	}
	var in struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		AccountName string `json:"account_name"`
		Website     string `json:"website"` // honeypot; must stay empty
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid registration payload")
		return
	}
	if honeypotFilled(in.Website) {
		writeJSON(w, 200, map[string]any{"user": map[string]any{"email": strings.ToLower(strings.TrimSpace(in.Email))}, "account": map[string]any{}})
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.AccountName = strings.TrimSpace(in.AccountName)
	if !validAuthEmail(in.Email) || !validAuthPassword(in.Password) || len(in.AccountName) < 2 || len(in.AccountName) > 80 {
		fail(w, 400, "email, a 12+ character password, and an account name are required")
		return
	}
	if _, err := s.Store.GetIAMUserByEmail(in.Email); err == nil {
		fail(w, 409, "an account with this email already exists")
		return
	}
	acc := store.Account{ID: "acc-" + randHex(8), Name: in.AccountName, Kind: "client", Created: time.Now().UTC()}
	if err := s.Store.SaveAccount(acc); err != nil {
		fail(w, 500, "could not create account")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		fail(w, 500, "could not store password")
		return
	}
	user := store.IAMUser{
		ID: "usr-" + randHex(8), AccountID: acc.ID, Kind: "root", Email: in.Email, Username: usernameFromEmail(in.Email),
		PasswordHash: hash, Role: "Full Administrator", Status: "active", Created: time.Now().UTC(), GroupIDs: []string{}, PolicyIDs: []string{},
	}
	if err := s.Store.SaveIAMUser(user); err != nil {
		if isIAMConflict(err) {
			user.Username = user.Username + randHex(2)
			err = s.Store.SaveIAMUser(user)
		}
		if err != nil {
			fail(w, 409, "could not create user")
			return
		}
	}
	if err := s.issueSession(w, user); err != nil {
		fail(w, 500, "could not start session")
		return
	}
	writeJSON(w, 200, map[string]any{"user": publicUser(user), "account": acc})
}

func (s *Server) loginAuth(w http.ResponseWriter, r *http.Request) {
	if s.rejectAuthAbuse(w, r, "login") {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Website  string `json:"website"` // honeypot; must stay empty
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid login payload")
		return
	}
	ip := clientIP(r)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if honeypotFilled(in.Website) {
		s.noteLoginFailure(ip, email)
		fail(w, 401, "invalid email or password")
		return
	}
	if email != "" && s.loginEmailLocked(email) {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "too many failed sign-in attempts for this account; try again later")
		return
	}
	user, err := s.Store.GetIAMUserByEmail(email)
	if err != nil || user.Status != "active" || !checkPassword(user.PasswordHash, in.Password) {
		s.noteLoginFailure(ip, email)
		fail(w, 401, "invalid email or password")
		return
	}
	if err := loginIPAllowed(user, ip); err != nil {
		s.noteLoginFailure(ip, email)
		fail(w, 403, err.Error())
		return
	}
	if user.MFAEnabled && user.MFASecret != "" {
		token, err := s.rememberMFAChallenge(user.ID)
		if err != nil {
			fail(w, 500, "could not start MFA challenge")
			return
		}
		writeJSON(w, 200, map[string]any{"mfa_required": true, "mfa_token": token, "user": map[string]any{"email": user.Email, "kind": user.Kind}})
		return
	}
	if err := s.issueSession(w, user); err != nil {
		fail(w, 500, "could not start session")
		return
	}
	s.clearLoginFailures(ip, email)
	writeJSON(w, 200, map[string]any{"user": publicUser(user)})
}

func (s *Server) logoutAuth(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if sess, e := s.Store.GetSessionByHash(hashToken(c.Value)); e == nil {
			_ = s.Store.DeleteSession(sess.ID)
		}
	}
	s.clearSessionCookie(w)
	writeJSON(w, 200, map[string]bool{"logged_out": true})
}

func (s *Server) meAuth(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	acc, _ := s.Store.GetAccount(p.AccountID)
	user, _ := s.Store.GetIAMUser(p.UserID)
	writeJSON(w, 200, map[string]any{
		"id": p.UserID, "username": p.Name, "display_name": user.DisplayName, "email": p.Email, "role": p.Role, "kind": p.Kind,
		"account_id": p.AccountID, "account": acc, "root": p.Root, "permissions": p.Permissions,
		"mfa_enabled": user.MFAEnabled,
	})
}

func publicUser(u store.IAMUser) map[string]any {
	return map[string]any{"id": u.ID, "username": u.Username, "display_name": u.DisplayName, "email": u.Email, "kind": u.Kind, "role": u.Role, "account_id": u.AccountID, "status": u.Status, "mfa_enabled": u.MFAEnabled}
}
