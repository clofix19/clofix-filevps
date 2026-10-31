package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

type iamPrincipal struct {
	UserID      string
	Name        string
	Role        string
	Root        bool
	Permissions []string
}
type iamPrincipalContextKey struct{}

func principalFrom(r *http.Request) iamPrincipal {
	p, _ := r.Context().Value(iamPrincipalContextKey{}).(iamPrincipal)
	return p
}

var iamIDPattern = regexp.MustCompile(`^(usr|grp|pol)-[0-9a-f]{16}$`)
var iamUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{2,64}$`)
var iamPermissionSet = map[string]bool{
	"databases:read": true, "databases:manage": true,
	"license:read": true, "license:manage": true,
	"health-checks:read": true, "health-checks:manage": true,
	"load-balancers:read": true, "load-balancers:manage": true,
	"managed-apps:read": true, "managed-apps:manage": true,
	"*": true, "read:*": true, "backups:read": true, "backups:manage": true, "platform:read": true, "platform:manage": true,
	"applications:read": true, "applications:manage": true,
	"instances:read": true, "instances:manage": true, "workers:read": true, "workers:manage": true,
	"volumes:read": true, "volumes:manage": true, "storage:read": true, "storage:manage": true,
	"security:read": true, "security:manage": true, "services:read": true, "services:manage": true,
	"iam:read": true, "iam:manage": true,
}

func iamPermissionCatalog() []string {
	out := make([]string, 0, len(iamPermissionSet))
	for v := range iamPermissionSet {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (s *Server) authenticate(r *http.Request) (iamPrincipal, bool) {
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.AdminToken != "" && subtle.ConstantTimeCompare([]byte(credential), []byte(s.AdminToken)) == 1 {
		return iamPrincipal{Name: "root", Role: "Root user", Root: true, Permissions: []string{"*"}}, true
	}
	accessID, secret, ok := strings.Cut(credential, ".")
	if !ok || !strings.HasPrefix(accessID, "CFX") || secret == "" {
		return iamPrincipal{}, false
	}
	key, err := s.Store.GetIAMAccessKey(accessID)
	if err != nil || !key.Active || subtle.ConstantTimeCompare([]byte(key.SecretHash), []byte(hashToken(secret))) != 1 {
		return iamPrincipal{}, false
	}
	user, err := s.Store.GetIAMUser(key.UserID)
	if err != nil || user.Status != "active" {
		return iamPrincipal{}, false
	}
	p := iamPrincipal{UserID: user.ID, Name: user.Username, Role: user.Role}
	switch user.Role {
	case "Full Administrator":
		p.Permissions = append(p.Permissions, "*")
	case "Read Only":
		p.Permissions = append(p.Permissions, "read:*")
	}
	for _, id := range user.PolicyIDs {
		policy, e := s.Store.GetIAMPolicy(id)
		if e == nil {
			p.Permissions = append(p.Permissions, policy.Permissions...)
		}
	}
	for _, groupID := range user.GroupIDs {
		group, e := s.Store.GetIAMGroup(groupID)
		if e != nil {
			continue
		}
		for _, policyID := range group.PolicyIDs {
			policy, e := s.Store.GetIAMPolicy(policyID)
			if e == nil {
				p.Permissions = append(p.Permissions, policy.Permissions...)
			}
		}
	}
	return p, true
}

func permissionResource(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/databases"):
		return "databases"
	case strings.HasPrefix(path, "/api/license"):
		return "license"
	case strings.HasPrefix(path, "/api/monitoring"):
		return "health-checks"
	case strings.HasPrefix(path, "/api/load-balancers"):
		return "load-balancers"
	case strings.HasPrefix(path, "/api/managed-apps"):
		return "managed-apps"
	case strings.HasPrefix(path, "/api/backups"), strings.HasPrefix(path, "/api/instances/") && strings.HasSuffix(path, "/backups"):
		return "backups"
	case strings.HasPrefix(path, "/api/applications"), strings.HasPrefix(path, "/api/application-runs"), strings.HasPrefix(path, "/api/functions"):
		return "applications"
	case strings.HasPrefix(path, "/api/iam"):
		return "iam"
	case path == "/api/ca.pem":
		return "platform"
	case strings.HasPrefix(path, "/api/workers"), strings.HasPrefix(path, "/api/enrollment-tokens"):
		return "workers"
	case strings.HasPrefix(path, "/api/volumes"):
		return "volumes"
	case strings.HasPrefix(path, "/api/storage"):
		return "storage"
	case strings.HasPrefix(path, "/api/instances"):
		for _, part := range []string{"/services", "/install-service", "/uninstall-service"} {
			if strings.Contains(path, part) {
				return "services"
			}
		}
		for _, part := range []string{"/protection", "/ssh-login", "/delete-protection", "/waf", "/ports"} {
			if strings.Contains(path, part) {
				return "security"
			}
		}
		return "instances"
	default:
		return "platform"
	}
}
func hasIAMPermission(p iamPrincipal, permission string) bool {
	// Existing instance policies retain their historical backup access.
	if strings.HasPrefix(permission, "backups:") && hasIAMPermission(p, "instances:"+strings.TrimPrefix(permission, "backups:")) {
		return true
	}
	for _, v := range p.Permissions {
		if v == "*" || v == permission {
			return true
		}
		if strings.HasSuffix(v, ":manage") && permission == strings.TrimSuffix(v, ":manage")+":read" {
			return true
		}
		if strings.HasSuffix(v, ":*") {
			resource := strings.TrimSuffix(v, ":*")
			if permission == resource+":read" || permission == resource+":manage" {
				return true
			}
		}
		if strings.HasSuffix(permission, ":read") && v == "read:*" {
			return true
		}
	}
	return false
}

func isIAMConflict(err error) bool {
	if errors.Is(err, store.ErrConflict) {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "23505") || strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate key")
}

func (s *Server) iamMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"username": p.Name, "role": p.Role, "root": p.Root, "permissions": p.Permissions})
}
func (s *Server) listIAMUsers(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.ListIAMUsers()
	if e != nil {
		fail(w, 500, "could not list IAM users")
		return
	}
	if v == nil {
		v = []store.IAMUser{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) listIAMGroups(w http.ResponseWriter, r *http.Request) {
	groups, e := s.Store.ListIAMGroups()
	if e != nil {
		fail(w, 500, "could not list IAM groups")
		return
	}
	users, e := s.Store.ListIAMUsers()
	if e != nil {
		fail(w, 500, "could not list IAM users")
		return
	}
	type groupView struct {
		store.IAMGroup
		UserIDs []string `json:"user_ids"`
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		view := groupView{IAMGroup: g, UserIDs: []string{}}
		for _, u := range users {
			for _, id := range u.GroupIDs {
				if id == g.ID {
					view.UserIDs = append(view.UserIDs, u.ID)
				}
			}
		}
		out = append(out, view)
	}
	writeJSON(w, 200, out)
}
func (s *Server) listIAMPolicies(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ListIAMPolicies()
	if err != nil {
		fail(w, 500, "could not list IAM policies")
		return
	}
	if v == nil {
		v = []store.IAMPolicy{}
	}
	builtIn := []map[string]any{
		{"name": "CloFixFullAdministrator", "permissions": []string{"*"}},
		{"name": "CloFixReadOnly", "permissions": []string{"read:*"}},
	}
	writeJSON(w, 200, map[string]any{"policies": v, "permissions": iamPermissionCatalog(), "permission_details": iamPermissionDetails(), "built_in": builtIn})
}
func decodeIAM(w http.ResponseWriter, r *http.Request, dst any) bool {
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst); e != nil {
		fail(w, 400, "invalid IAM request body")
		return false
	}
	return true
}
func validateIAMIDs(ids []string, kind string, exists func(string) bool) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, kind+"-") || seen[id] || !exists(id) {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *Server) saveIAMUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username  string   `json:"username"`
		Role      string   `json:"role"`
		Status    string   `json:"status"`
		GroupIDs  []string `json:"group_ids"`
		PolicyIDs []string `json:"policy_ids"`
	}
	if !decodeIAM(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if !iamUsernamePattern.MatchString(in.Username) {
		fail(w, 400, "username must be 2–64 letters, numbers, dots, dashes, or underscores")
		return
	}
	if in.Role != "IAM User" && in.Role != "Read Only" && in.Role != "Full Administrator" {
		fail(w, 400, "role must be IAM User, Read Only, or Full Administrator")
		return
	}
	if in.Status != "active" && in.Status != "inactive" {
		fail(w, 400, "status must be active or inactive")
		return
	}
	if in.GroupIDs == nil {
		in.GroupIDs = []string{}
	}
	if in.PolicyIDs == nil {
		in.PolicyIDs = []string{}
	}
	if !validateIAMIDs(in.GroupIDs, "grp", func(id string) bool { _, e := s.Store.GetIAMGroup(id); return e == nil }) {
		fail(w, 400, "one or more groups are invalid")
		return
	}
	if !validateIAMIDs(in.PolicyIDs, "pol", func(id string) bool { _, e := s.Store.GetIAMPolicy(id); return e == nil }) {
		fail(w, 400, "one or more policies are invalid")
		return
	}
	id := r.PathValue("id")
	var user store.IAMUser
	var err error
	if id == "" {
		user = store.IAMUser{ID: "usr-" + randHex(8), Created: time.Now()}
	} else {
		if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "usr-") {
			fail(w, 400, "invalid IAM user id")
			return
		}
		user, err = s.Store.GetIAMUser(id)
		if err != nil {
			fail(w, 404, "IAM user not found")
			return
		}
	}
	existingUsers, listErr := s.Store.ListIAMUsers()
	if listErr != nil {
		fail(w, 500, "could not validate IAM username")
		return
	}
	for _, existing := range existingUsers {
		if existing.ID != user.ID && strings.EqualFold(existing.Username, in.Username) {
			fail(w, 409, "username already exists")
			return
		}
	}
	user.Username, user.Role, user.Status, user.GroupIDs, user.PolicyIDs = in.Username, in.Role, in.Status, in.GroupIDs, in.PolicyIDs
	if err = s.Store.SaveIAMUser(user); err != nil {
		if isIAMConflict(err) {
			fail(w, 409, "username already exists")
		} else {
			fail(w, 500, "could not save IAM user")
		}
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.user.save", user.ID, user.Role)
	writeJSON(w, 200, user)
}
func (s *Server) deleteIAMUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "usr-") {
		fail(w, 400, "invalid IAM user id")
		return
	}
	if principalFrom(r).UserID == id {
		fail(w, 409, "you cannot delete the IAM user currently in use")
		return
	}
	if e := s.Store.DeleteIAMUser(id); e != nil {
		fail(w, 404, "IAM user not found")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.user.delete", id, "deleted")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) saveIAMGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		PolicyIDs   []string `json:"policy_ids"`
	}
	if !decodeIAM(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 2 || len(in.Name) > 80 {
		fail(w, 400, "group name must be 2–80 characters")
		return
	}
	if in.PolicyIDs == nil {
		in.PolicyIDs = []string{}
	}
	if !validateIAMIDs(in.PolicyIDs, "pol", func(id string) bool { _, e := s.Store.GetIAMPolicy(id); return e == nil }) {
		fail(w, 400, "one or more policies are invalid")
		return
	}
	id := r.PathValue("id")
	g := store.IAMGroup{ID: "grp-" + randHex(8), Created: time.Now()}
	if id != "" {
		if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "grp-") {
			fail(w, 400, "invalid group id")
			return
		}
		old, e := s.Store.GetIAMGroup(id)
		if e != nil {
			fail(w, 404, "group not found")
			return
		}
		g = old
	}
	existingGroups, listErr := s.Store.ListIAMGroups()
	if listErr != nil {
		fail(w, 500, "could not validate group name")
		return
	}
	for _, existing := range existingGroups {
		if existing.ID != g.ID && strings.EqualFold(existing.Name, in.Name) {
			fail(w, 409, "group name already exists")
			return
		}
	}
	g.Name, g.Description, g.PolicyIDs = in.Name, strings.TrimSpace(in.Description), in.PolicyIDs
	if e := s.Store.SaveIAMGroup(g); e != nil {
		if isIAMConflict(e) {
			fail(w, 409, "group name already exists")
		} else {
			fail(w, 500, "could not save group")
		}
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.group.save", g.ID, g.Name)
	writeJSON(w, 200, g)
}
func (s *Server) deleteIAMGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "grp-") {
		fail(w, 400, "invalid group id")
		return
	}
	users, e := s.Store.ListIAMUsers()
	if e != nil {
		fail(w, 500, "could not list users")
		return
	}
	for _, u := range users {
		u.GroupIDs = removeIAMString(u.GroupIDs, id)
		if e = s.Store.SaveIAMUser(u); e != nil {
			fail(w, 500, "could not update group memberships")
			return
		}
	}
	if e = s.Store.DeleteIAMGroup(id); e != nil {
		fail(w, 404, "group not found")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.group.delete", id, "deleted")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) saveIAMPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if !decodeIAM(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 2 || len(in.Name) > 80 {
		fail(w, 400, "policy name must be 2–80 characters")
		return
	}
	if len(in.Permissions) == 0 || len(in.Permissions) > len(iamPermissionSet) {
		fail(w, 400, "select at least one valid permission")
		return
	}
	seen := map[string]bool{}
	for _, v := range in.Permissions {
		if !iamPermissionSet[v] || seen[v] {
			fail(w, 400, "policy contains an invalid or duplicate permission")
			return
		}
		seen[v] = true
	}
	id := r.PathValue("id")
	p := store.IAMPolicy{ID: "pol-" + randHex(8), Created: time.Now()}
	if id != "" {
		if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "pol-") {
			fail(w, 400, "invalid policy id")
			return
		}
		old, e := s.Store.GetIAMPolicy(id)
		if e != nil {
			fail(w, 404, "policy not found")
			return
		}
		p = old
	}
	existingPolicies, listErr := s.Store.ListIAMPolicies()
	if listErr != nil {
		fail(w, 500, "could not validate policy name")
		return
	}
	for _, existing := range existingPolicies {
		if existing.ID != p.ID && strings.EqualFold(existing.Name, in.Name) {
			fail(w, 409, "policy name already exists")
			return
		}
	}
	p.Name, p.Description, p.Permissions = in.Name, strings.TrimSpace(in.Description), in.Permissions
	if e := s.Store.SaveIAMPolicy(p); e != nil {
		if isIAMConflict(e) {
			fail(w, 409, "policy name already exists")
		} else {
			fail(w, 500, "could not save policy")
		}
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.policy.save", p.ID, p.Name)
	writeJSON(w, 200, p)
}
func removeIAMString(list []string, target string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != target {
			out = append(out, v)
		}
	}
	return out
}
func (s *Server) deleteIAMPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !iamIDPattern.MatchString(id) || !strings.HasPrefix(id, "pol-") {
		fail(w, 400, "invalid policy id")
		return
	}
	users, e := s.Store.ListIAMUsers()
	if e != nil {
		fail(w, 500, "could not list IAM users")
		return
	}
	for _, u := range users {
		u.PolicyIDs = removeIAMString(u.PolicyIDs, id)
		if e = s.Store.SaveIAMUser(u); e != nil {
			fail(w, 500, "could not update user policies")
			return
		}
	}
	groups, e := s.Store.ListIAMGroups()
	if e != nil {
		fail(w, 500, "could not list IAM groups")
		return
	}
	for _, g := range groups {
		g.PolicyIDs = removeIAMString(g.PolicyIDs, id)
		if e = s.Store.SaveIAMGroup(g); e != nil {
			fail(w, 500, "could not update group policies")
			return
		}
	}
	if e = s.Store.DeleteIAMPolicy(id); e != nil {
		fail(w, 404, "policy not found")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.policy.delete", id, "deleted")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) listIAMUserKeys(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := s.Store.GetIAMUser(id); e != nil {
		fail(w, 404, "IAM user not found")
		return
	}
	keys, e := s.Store.ListIAMAccessKeys(id)
	if e != nil {
		fail(w, 500, "could not list access keys")
		return
	}
	if keys == nil {
		keys = []store.IAMAccessKey{}
	}
	writeJSON(w, 200, keys)
}
func (s *Server) createIAMUserKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, e := s.Store.GetIAMUser(id)
	if e != nil {
		fail(w, 404, "IAM user not found")
		return
	}
	if user.Status != "active" {
		fail(w, 409, "activate the IAM user before creating an access key")
		return
	}
	keys, e := s.Store.ListIAMAccessKeys(id)
	if e != nil {
		fail(w, 500, "could not list access keys")
		return
	}
	active := 0
	for _, k := range keys {
		if k.Active {
			active++
		}
	}
	if active >= 2 {
		fail(w, 409, "an IAM user may have at most two active access keys")
		return
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		fail(w, 500, "could not generate access key")
		return
	}
	accessIDBytes := make([]byte, 10)
	if _, e = rand.Read(accessIDBytes); e != nil {
		fail(w, 500, "could not generate access key")
		return
	}
	accessID := "CFX" + strings.ToUpper(hex.EncodeToString(accessIDBytes))
	secret := base64.RawURLEncoding.EncodeToString(raw)
	key := store.IAMAccessKey{AccessKeyID: accessID, UserID: id, SecretHash: hashToken(secret), Active: true, Created: time.Now()}
	if e = s.Store.SaveIAMAccessKey(key); e != nil {
		fail(w, 500, "could not save access key")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.key.create", accessID, id)
	writeJSON(w, 201, map[string]string{"access_key_id": accessID, "secret_access_key": secret, "bearer_token": accessID + "." + secret, "warning": "The secret is shown once. Copy it now; CloFix stores only its hash."})
}
func (s *Server) revokeIAMUserKey(w http.ResponseWriter, r *http.Request) {
	id, keyID := r.PathValue("id"), r.PathValue("key")
	key, e := s.Store.GetIAMAccessKey(keyID)
	if e != nil || key.UserID != id {
		fail(w, 404, "access key not found")
		return
	}
	if e = s.Store.DeleteIAMAccessKey(keyID); e != nil {
		fail(w, 500, "could not revoke access key")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "iam.key.revoke", keyID, id)
	writeJSON(w, 200, map[string]bool{"revoked": true})
}
