package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

const (
	auditRetentionClientDays = 90
	auditRetentionAdminDays  = 180
)

var auditLastPurgeUnix atomic.Int64

func auditActorName(p iamPrincipal) string {
	actor := strings.TrimSpace(p.Name)
	if actor == "" {
		actor = strings.TrimSpace(p.Email)
	}
	if actor == "" {
		actor = p.UserID
	}
	if actor == "" {
		actor = "unknown"
	}
	return actor
}

func (s *Server) auditDetailJSON(r *http.Request, detail any) string {
	meta := map[string]any{}
	if r != nil {
		p := principalFrom(r)
		meta["request"] = map[string]any{
			"method":     r.Method,
			"path":       r.URL.Path,
			"query":      r.URL.RawQuery,
			"ip":         clientIP(r),
			"user_agent": r.UserAgent(),
		}
		meta["actor"] = map[string]any{
			"id":         p.UserID,
			"username":   p.Name,
			"email":      p.Email,
			"kind":       p.Kind,
			"role":       p.Role,
			"account_id": p.AccountID,
			"root":       p.Root,
		}
	}
	if detail != nil {
		switch v := detail.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				var parsed any
				if json.Unmarshal([]byte(v), &parsed) == nil {
					meta["payload"] = parsed
				} else {
					meta["payload"] = v
				}
			}
		case map[string]any:
			for k, val := range v {
				meta[k] = val
			}
		default:
			meta["payload"] = v
		}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(b)
}

// audit records an account-scoped activity event for the request principal.
// Root and IAM users under the same account share one trail; platform admin sees all via listAudit.
func (s *Server) audit(r *http.Request, action, target, result string, detail any) {
	p := principalFrom(r)
	_ = s.Store.AuditScoped(s.stampAccount(p), p.UserID, auditActorName(p), action, target, result, s.auditDetailJSON(r, detail))
}

// auditAccount records activity against a resource owner account (jobs, DBs, CKS, apps).
func (s *Server) auditAccount(r *http.Request, accountID, action, target, result string, detail any) {
	p := principalFrom(r)
	if accountID == "" {
		accountID = s.stampAccount(p)
	}
	_ = s.Store.AuditScoped(accountID, p.UserID, auditActorName(p), action, target, result, s.auditDetailJSON(r, detail))
}

func auditRetentionDays(p iamPrincipal) int {
	if p.Kind == "admin" {
		return auditRetentionAdminDays
	}
	return auditRetentionClientDays
}

func (s *Server) auditRetentionCutoff(p iamPrincipal) time.Time {
	days := auditRetentionDays(p)
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
}

func (s *Server) maybePurgeAudit() {
	now := time.Now().Unix()
	prev := auditLastPurgeUnix.Load()
	if now-prev < 3600 {
		return
	}
	if !auditLastPurgeUnix.CompareAndSwap(prev, now) {
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(auditRetentionAdminDays) * 24 * time.Hour)
	_, _ = s.Store.PurgeAuditBefore(cutoff)
}

func parseAuditTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02T15:04", raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

const auditPageSize = 100

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	s.maybePurgeAudit()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if limit <= 0 {
		limit = auditPageSize
	}
	if limit > auditPageSize {
		limit = auditPageSize
	}
	if page < 1 {
		page = 1
	}
	p := principalFrom(r)
	retentionDays := auditRetentionDays(p)
	cutoff := s.auditRetentionCutoff(p)

	q := store.AuditQuery{
		Limit:  limit,
		Offset: (page - 1) * limit,
		Since:  cutoff,
		Q:      strings.TrimSpace(r.URL.Query().Get("q")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Actor:  strings.TrimSpace(r.URL.Query().Get("actor")),
	}
	if from, ok := parseAuditTime(r.URL.Query().Get("from")); !ok {
		fail(w, http.StatusBadRequest, "invalid from time")
		return
	} else if !from.IsZero() {
		if from.Before(cutoff) {
			from = cutoff
		}
		q.Since = from
	}
	if to, ok := parseAuditTime(r.URL.Query().Get("to")); !ok {
		fail(w, http.StatusBadRequest, "invalid to time")
		return
	} else if !to.IsZero() {
		q.Until = to
	}

	var (
		entries []store.AuditEntry
		total   int
		err     error
	)
	if p.Kind == "admin" {
		q.AccountID = strings.TrimSpace(r.URL.Query().Get("account_id"))
		total, err = s.Store.CountAuditQuery(q)
		if err == nil {
			entries, err = s.Store.ListAuditQuery(q)
		}
	} else {
		ids := make([]string, 0, len(p.Visible)+1)
		seen := map[string]bool{}
		for id := range s.visibleAccountIDSet(p) {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if p.AccountID != "" && !seen[p.AccountID] {
			ids = append(ids, p.AccountID)
		}
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"events": []store.AuditEntry{}, "retention_days": retentionDays, "since": cutoff,
				"page": 1, "page_size": limit, "total": 0, "pages": 0,
			})
			return
		}
		wantAccount := strings.TrimSpace(r.URL.Query().Get("account_id"))
		if wantAccount != "" {
			if !seen[wantAccount] && wantAccount != p.AccountID {
				fail(w, http.StatusForbidden, "cannot filter another account")
				return
			}
			q.AccountIDs = []string{wantAccount}
		} else {
			q.AccountIDs = ids
		}
		total, err = s.Store.CountAuditQuery(q)
		if err == nil {
			entries, err = s.Store.ListAuditQuery(q)
		}
		// Legacy unscoped rows only on first page to keep pagination stable.
		if err == nil && page == 1 {
			entries = s.mergeLegacyAuditActors(entries, q.AccountIDs, q.Limit, q)
		}
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not list audit events")
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	pages := 0
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	if page > pages && pages > 0 {
		page = pages
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": entries, "retention_days": retentionDays, "since": cutoff,
		"page": page, "page_size": limit, "total": total, "pages": pages,
		"filters": map[string]any{
			"q": q.Q, "action": q.Action, "actor": q.Actor, "account_id": q.AccountID,
			"from": q.Since, "to": q.Until,
		},
	})
}

func (s *Server) mergeLegacyAuditActors(scoped []store.AuditEntry, accountIDs []string, limit int, filter store.AuditQuery) []store.AuditEntry {
	if limit <= 0 {
		limit = 50
	}
	actors := map[string]bool{}
	users, err := s.Store.ListIAMUsers()
	if err != nil {
		return scoped
	}
	allow := map[string]bool{}
	for _, id := range accountIDs {
		allow[id] = true
	}
	for _, u := range users {
		if !allow[u.AccountID] {
			continue
		}
		if u.Username != "" {
			actors[strings.ToLower(u.Username)] = true
		}
		if u.Email != "" {
			actors[strings.ToLower(u.Email)] = true
		}
		if u.ID != "" {
			actors[u.ID] = true
		}
	}
	if len(actors) == 0 {
		return scoped
	}
	legacyQ := filter
	legacyQ.AccountIDs = nil
	legacyQ.AccountID = ""
	legacyQ.Limit = limit * 4
	all, err := s.Store.ListAuditQuery(legacyQ)
	if err != nil {
		return scoped
	}
	have := map[int64]bool{}
	for _, e := range scoped {
		have[e.ID] = true
	}
	out := append([]store.AuditEntry{}, scoped...)
	for _, e := range all {
		if e.AccountID != "" || have[e.ID] {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(e.Actor))
		if !actors[key] && !actors[e.ActorID] {
			continue
		}
		out = append(out, e)
		have[e.ID] = true
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].ID > out[i].ID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) getAudit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, http.StatusBadRequest, "invalid audit id")
		return
	}
	entry, err := s.Store.GetAudit(id)
	if err != nil {
		fail(w, http.StatusNotFound, "audit event not found")
		return
	}
	p := principalFrom(r)
	if entry.At.Before(s.auditRetentionCutoff(p)) {
		fail(w, http.StatusNotFound, "audit event not found")
		return
	}
	if p.Kind != "admin" {
		if entry.AccountID != "" {
			if !s.canAccessAccount(p, entry.AccountID) {
				fail(w, http.StatusNotFound, "audit event not found")
				return
			}
		} else if !s.legacyAuditVisible(p, entry) {
			fail(w, http.StatusNotFound, "audit event not found")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.auditEventView(entry))
}

func (s *Server) legacyAuditVisible(p iamPrincipal, entry store.AuditEntry) bool {
	if p.AccountID == "" {
		return false
	}
	users, err := s.Store.ListIAMUsers()
	if err != nil {
		return false
	}
	actor := strings.ToLower(strings.TrimSpace(entry.Actor))
	for _, u := range users {
		if u.AccountID != p.AccountID {
			continue
		}
		if entry.ActorID != "" && entry.ActorID == u.ID {
			return true
		}
		if actor != "" && (strings.EqualFold(actor, u.Username) || strings.EqualFold(actor, u.Email) || actor == u.ID) {
			return true
		}
	}
	return false
}

func (s *Server) auditEventView(entry store.AuditEntry) map[string]any {
	view := map[string]any{
		"id":         entry.ID,
		"at":         entry.At,
		"account_id": entry.AccountID,
		"actor_id":   entry.ActorID,
		"actor":      entry.Actor,
		"action":     entry.Action,
		"target":     entry.Target,
		"result":     entry.Result,
		"summary": map[string]any{
			"when":   entry.At.UTC().Format(time.RFC3339Nano),
			"who":    entry.Actor,
			"did":    entry.Action,
			"on":     entry.Target,
			"outcome": entry.Result,
		},
	}
	if entry.Detail != "" {
		var parsed any
		if json.Unmarshal([]byte(entry.Detail), &parsed) == nil {
			view["detail"] = parsed
		} else {
			view["detail"] = entry.Detail
		}
	}
	if entry.AccountID != "" {
		if acc, err := s.Store.GetAccount(entry.AccountID); err == nil {
			view["account"] = map[string]any{
				"id": acc.ID, "name": acc.Name, "kind": acc.Kind, "status": acc.Status, "parent_id": acc.ParentID,
			}
		}
	}
	if entry.ActorID != "" {
		if u, err := s.Store.GetIAMUser(entry.ActorID); err == nil {
			view["actor_user"] = map[string]any{
				"id": u.ID, "username": u.Username, "email": u.Email, "kind": u.Kind, "role": u.Role,
				"status": u.Status, "account_id": u.AccountID,
			}
		}
	}
	if res := s.auditRelatedResource(entry.Action, entry.Target); res != nil {
		view["resource"] = res
	}
	return view
}

func (s *Server) auditRelatedResource(action, target string) map[string]any {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	// Prefer explicit id-looking targets; ignore compound result strings.
	id := target
	if i := strings.IndexAny(target, " :"); i > 0 {
		id = target[:i]
	}
	switch {
	case strings.HasPrefix(action, "instance.") || strings.HasPrefix(action, "op."):
		if inst, err := s.Store.GetInstance(id); err == nil {
			return map[string]any{
				"type": "instance", "id": inst.ID, "account_id": inst.AccountID, "worker_id": inst.WorkerID,
				"state": inst.State, "name": inst.Spec.Hostname, "created": inst.Created,
			}
		}
	case strings.HasPrefix(action, "volume."):
		if vol, err := s.Store.GetVolume(id); err == nil {
			return map[string]any{
				"type": "volume", "id": vol.ID, "account_id": vol.AccountID, "name": vol.Name,
				"state": vol.State, "worker_id": vol.WorkerID, "instance_id": vol.InstanceID,
			}
		}
	case strings.HasPrefix(action, "iam.user"):
		if u, err := s.Store.GetIAMUser(id); err == nil {
			return map[string]any{
				"type": "iam_user", "id": u.ID, "username": u.Username, "email": u.Email,
				"kind": u.Kind, "role": u.Role, "status": u.Status, "account_id": u.AccountID,
			}
		}
	case strings.HasPrefix(action, "iam.group"):
		if g, err := s.Store.GetIAMGroup(id); err == nil {
			return map[string]any{
				"type": "iam_group", "id": g.ID, "name": g.Name, "description": g.Description,
				"account_id": g.AccountID, "policy_ids": g.PolicyIDs,
			}
		}
	case strings.HasPrefix(action, "iam.policy"):
		if pol, err := s.Store.GetIAMPolicy(id); err == nil {
			return map[string]any{
				"type": "iam_policy", "id": pol.ID, "name": pol.Name, "description": pol.Description,
				"account_id": pol.AccountID, "permissions": pol.Permissions,
			}
		}
	case strings.HasPrefix(action, "iam.key"):
		if key, err := s.Store.GetIAMAccessKey(id); err == nil {
			return map[string]any{
				"type": "iam_access_key", "access_key_id": key.AccessKeyID, "user_id": key.UserID,
				"active": key.Active, "created": key.Created,
			}
		}
	}
	return nil
}
