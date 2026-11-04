package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	accs, err := s.Store.ListAccounts()
	if err != nil {
		fail(w, 500, "could not list accounts")
		return
	}
	users, _ := s.Store.ListIAMUsers()
	allow := s.visibleAccountIDSet(p)
	out := []map[string]any{}
	for _, acc := range accs {
		if p.Kind != "admin" && !allow[acc.ID] {
			continue
		}
		email := ""
		kind := ""
		for _, u := range users {
			if u.AccountID == acc.ID && (u.Kind == "root" || u.Kind == "partner" || u.Kind == "admin") {
				email, kind = u.Email, u.Kind
				break
			}
		}
		out = append(out, map[string]any{"id": acc.ID, "name": acc.Name, "kind": acc.Kind, "parent_id": acc.ParentID, "email": email, "user_kind": kind, "created": acc.Created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	writeJSON(w, 200, out)
}

func (s *Server) createAccountUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid account payload")
		return
	}
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Kind != "partner" && in.Kind != "client" {
		fail(w, 400, "kind must be partner or client")
		return
	}
	if p.Kind == "partner" && in.Kind != "client" {
		fail(w, 403, "partners can only create client accounts")
		return
	}
	if p.Kind != "admin" && p.Kind != "partner" {
		fail(w, 403, "only admin or partner can create accounts")
		return
	}
	if !validAuthEmail(in.Email) || !validAuthPassword(in.Password) || len(in.Name) < 2 || len(in.Name) > 80 {
		fail(w, 400, "name, email, and a 12+ character password are required")
		return
	}
	if _, err := s.Store.GetIAMUserByEmail(in.Email); err == nil {
		fail(w, 409, "an account with this email already exists")
		return
	}
	parent := ""
	accKind := "client"
	userKind := "root"
	if in.Kind == "partner" {
		accKind, userKind = "partner", "partner"
	} else if p.Kind == "partner" {
		parent = p.AccountID
	}
	acc := store.Account{ID: "acc-" + randHex(8), Name: in.Name, Kind: accKind, ParentID: parent, Created: time.Now().UTC()}
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
		ID: "usr-" + randHex(8), AccountID: acc.ID, Kind: userKind, Email: in.Email, Username: usernameFromEmail(in.Email),
		PasswordHash: hash, Role: "Full Administrator", Status: "active", Created: time.Now().UTC(), GroupIDs: []string{}, PolicyIDs: []string{},
	}
	if err := s.Store.SaveIAMUser(user); err != nil {
		fail(w, 409, "could not create user")
		return
	}
	s.Store.Audit(p.Name, "account.create", acc.ID, userKind)
	writeJSON(w, 200, map[string]any{"account": acc, "user": publicUser(user)})
}

func (s *Server) accountPrimaryUser(accountID string) (store.IAMUser, bool) {
	users, _ := s.Store.ListIAMUsers()
	for _, u := range users {
		if u.AccountID == accountID && (u.Kind == "root" || u.Kind == "partner" || u.Kind == "admin") {
			return u, true
		}
	}
	return store.IAMUser{}, false
}

func (s *Server) canManageAccountRecord(p iamPrincipal, acc store.Account) bool {
	if p.Kind != "admin" && p.Kind != "partner" {
		return false
	}
	if acc.Kind == "admin" {
		return false
	}
	if p.Kind == "admin" {
		return true
	}
	return acc.Kind == "client" && acc.ParentID == p.AccountID
}

func (s *Server) updateAccountUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := strings.TrimSpace(r.PathValue("id"))
	acc, err := s.Store.GetAccount(id)
	if err != nil {
		fail(w, 404, "account not found")
		return
	}
	if !s.canManageAccountRecord(p, acc) {
		fail(w, 403, "cannot edit this account")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid account payload")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if len(in.Name) < 2 || len(in.Name) > 80 {
		fail(w, 400, "account name must be 2–80 characters")
		return
	}
	acc.Name = in.Name
	if err := s.Store.SaveAccount(acc); err != nil {
		fail(w, 500, "could not update account")
		return
	}
	user, ok := s.accountPrimaryUser(acc.ID)
	if ok {
		if in.Email != "" && in.Email != user.Email {
			if !validAuthEmail(in.Email) {
				fail(w, 400, "invalid email")
				return
			}
			if existing, err := s.Store.GetIAMUserByEmail(in.Email); err == nil && existing.ID != user.ID {
				fail(w, 409, "an account with this email already exists")
				return
			}
			user.Email = in.Email
			user.Username = usernameFromEmail(in.Email)
		}
		if in.Password != "" {
			if !validAuthPassword(in.Password) {
				fail(w, 400, "password must be at least 12 characters")
				return
			}
			hash, err := hashPassword(in.Password)
			if err != nil {
				fail(w, 500, "could not store password")
				return
			}
			user.PasswordHash = hash
		}
		if err := s.Store.SaveIAMUser(user); err != nil {
			fail(w, 500, "could not update user")
			return
		}
	}
	s.Store.Audit(p.Name, "account.update", acc.ID, acc.Kind)
	writeJSON(w, 200, map[string]any{"account": acc, "user": publicUser(user)})
}

func (s *Server) deleteAccountUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := strings.TrimSpace(r.PathValue("id"))
	acc, err := s.Store.GetAccount(id)
	if err != nil {
		fail(w, 404, "account not found")
		return
	}
	if !s.canManageAccountRecord(p, acc) {
		fail(w, 403, "cannot delete this account")
		return
	}
	if id == p.AccountID {
		fail(w, 400, "cannot delete your own account")
		return
	}
	if acc.Kind == "partner" {
		children, _ := s.Store.ListAccounts()
		for _, child := range children {
			if child.ParentID == id {
				fail(w, 409, "delete client accounts under this partner first")
				return
			}
		}
	}
	instances, _ := s.Store.ListInstances()
	for _, inst := range instances {
		if inst.AccountID == id {
			fail(w, 409, "delete or reassign VPS for this account first")
			return
		}
	}
	users, _ := s.Store.ListIAMUsers()
	for _, u := range users {
		if u.AccountID != id {
			continue
		}
		keys, _ := s.Store.ListIAMAccessKeys(u.ID)
		for _, key := range keys {
			_ = s.Store.DeleteIAMAccessKey(key.AccessKeyID)
		}
		_ = s.Store.DeleteIAMUser(u.ID)
	}
	if err := s.Store.DeleteAccount(id); err != nil {
		fail(w, 500, "could not delete account")
		return
	}
	s.Store.Audit(p.Name, "account.delete", id, acc.Kind)
	writeJSON(w, 200, map[string]any{"deleted": true, "id": id})
}
