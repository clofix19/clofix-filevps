package server

import (
	"net/http"
	"strings"

	"github.com/clofix/filevps/admin/internal/store"
)

func (s *Server) canAccessAccount(p iamPrincipal, accountID string) bool {
	if p.Kind == "admin" {
		return true
	}
	if accountID == "" {
		return false
	}
	if p.AccountID != "" && p.AccountID == accountID {
		return true
	}
	if p.Visible[accountID] {
		return true
	}
	if p.Kind == "partner" {
		acc, err := s.Store.GetAccount(accountID)
		return err == nil && acc.ParentID == p.AccountID
	}
	return false
}

func (s *Server) visibleAccountIDSet(p iamPrincipal) map[string]bool {
	out := map[string]bool{}
	if p.Kind == "admin" {
		accs, _ := s.Store.ListAccounts()
		for _, acc := range accs {
			out[acc.ID] = true
		}
		return out
	}
	if p.AccountID != "" {
		out[p.AccountID] = true
	}
	if p.Kind == "partner" {
		accs, _ := s.Store.ListAccounts()
		for _, acc := range accs {
			if acc.ParentID == p.AccountID {
				out[acc.ID] = true
			}
		}
	}
	return out
}

func (s *Server) stampAccount(p iamPrincipal) string {
	if p.AccountID != "" {
		return p.AccountID
	}
	return ""
}

func (s *Server) getVisibleInstance(r *http.Request, id string) (store.Instance, error) {
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		return inst, err
	}
	if !s.canAccessAccount(principalFrom(r), inst.AccountID) {
		return store.Instance{}, store.ErrNotFound
	}
	return inst, nil
}

func (s *Server) getVisibleVolume(r *http.Request, id string) (store.Volume, error) {
	v, err := s.Store.GetVolume(id)
	if err != nil {
		return v, err
	}
	if !s.canAccessAccount(principalFrom(r), v.AccountID) {
		return store.Volume{}, store.ErrNotFound
	}
	return v, nil
}

func filterInstances(items []store.Instance, allow map[string]bool, admin bool) []store.Instance {
	if admin {
		return items
	}
	out := make([]store.Instance, 0, len(items))
	for _, item := range items {
		if allow[item.AccountID] {
			out = append(out, item)
		}
	}
	return out
}

func filterVolumes(items []store.Volume, allow map[string]bool, admin bool) []store.Volume {
	if admin {
		return items
	}
	out := make([]store.Volume, 0, len(items))
	for _, item := range items {
		if allow[item.AccountID] {
			out = append(out, item)
		}
	}
	return out
}

func platformAdminResource(resource string) bool {
	switch resource {
	case "workers", "license", "pricing", "platform":
		return true
	default:
		return false
	}
}

func (s *Server) enforceTenant(w http.ResponseWriter, r *http.Request, p iamPrincipal) bool {
	if p.Kind == "admin" {
		return true
	}
	path := r.URL.Path
	id := r.PathValue("id")
	if id == "" {
		return true
	}
	switch {
	case strings.HasPrefix(path, "/api/instances/"):
		inst, err := s.Store.GetInstance(id)
		if err != nil {
			return true
		}
		if !s.canAccessAccount(p, inst.AccountID) {
			fail(w, http.StatusNotFound, "VPS not found")
			return false
		}
	case strings.HasPrefix(path, "/api/volumes/"):
		vol, err := s.Store.GetVolume(id)
		if err != nil {
			return true
		}
		if !s.canAccessAccount(p, vol.AccountID) {
			fail(w, http.StatusNotFound, "volume not found")
			return false
		}
	case strings.HasPrefix(path, "/api/efs/"):
		s.efs.Lock()
		share, ok := s.efs.Shares[id]
		s.efs.Unlock()
		if !ok {
			return true
		}
		if !s.canAccessAccount(p, share.AccountID) {
			fail(w, http.StatusNotFound, "EFS share not found")
			return false
		}
	case strings.HasPrefix(path, "/api/load-balancers/") && !strings.Contains(path, "/settings"):
		s.balancers.Lock()
		b := s.balancers.Items[id]
		s.balancers.Unlock()
		if b == nil {
			return true
		}
		if !s.canAccessAccount(p, b.AccountID) {
			fail(w, http.StatusNotFound, "load balancer not found")
			return false
		}
	case strings.HasPrefix(path, "/api/vpcs/"):
		v, err := s.lookupVPC(id)
		if err != nil {
			return true
		}
		if !v.Default && !s.canAccessAccount(p, v.AccountID) {
			fail(w, http.StatusNotFound, "VPC not found")
			return false
		}
	case strings.HasPrefix(path, "/api/backups/"):
		s.usageMu.RLock()
		b, ok := s.backups[id]
		s.usageMu.RUnlock()
		if !ok {
			return true
		}
		inst, err := s.Store.GetInstance(b.InstanceID)
		if err != nil || !s.canAccessAccount(p, inst.AccountID) {
			fail(w, http.StatusNotFound, "backup not found")
			return false
		}
	}
	return true
}
