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
