package store

import (
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

type tokenRec struct {
	expires time.Time
	used    bool
}

type AuditRec struct {
	TS                                                       time.Time
	AccountID, ActorID, Actor, Action, Target, Result, Detail string
}

type MemoryStore struct {
	mu          sync.Mutex
	tokens      map[string]*tokenRec
	workers     map[string]Worker
	instances   map[string]Instance
	volumes     map[string]Volume
	accounts    map[string]Account
	sessions    map[string]Session
	iamUsers    map[string]IAMUser
	iamGroups   map[string]IAMGroup
	iamPolicies map[string]IAMPolicy
	iamKeys     map[string]IAMAccessKey
	ops         map[string][]proto.Operation
	audit       []AuditRec
}

func NewMemory() *MemoryStore {
	return &MemoryStore{tokens: map[string]*tokenRec{}, workers: map[string]Worker{}, instances: map[string]Instance{}, volumes: map[string]Volume{}, accounts: map[string]Account{}, sessions: map[string]Session{}, iamUsers: map[string]IAMUser{}, iamGroups: map[string]IAMGroup{}, iamPolicies: map[string]IAMPolicy{}, iamKeys: map[string]IAMAccessKey{}, ops: map[string][]proto.Operation{}}
}

func copyIAMUser(v IAMUser) IAMUser {
	v.GroupIDs = append([]string(nil), v.GroupIDs...)
	v.PolicyIDs = append([]string(nil), v.PolicyIDs...)
	return v
}
func copyIAMGroup(v IAMGroup) IAMGroup { v.PolicyIDs = append([]string(nil), v.PolicyIDs...); return v }
func copyIAMPolicy(v IAMPolicy) IAMPolicy {
	v.Permissions = append([]string(nil), v.Permissions...)
	return v
}

func (m *MemoryStore) ListIAMUsers() ([]IAMUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IAMUser, 0, len(m.iamUsers))
	for _, v := range m.iamUsers {
		out = append(out, copyIAMUser(v))
	}
	return out, nil
}
func (m *MemoryStore) GetIAMUser(id string) (IAMUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.iamUsers[id]
	if !ok {
		return v, ErrNotFound
	}
	return copyIAMUser(v), nil
}
func (m *MemoryStore) GetIAMUserByEmail(email string) (IAMUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	for _, v := range m.iamUsers {
		if v.Email != "" && strings.EqualFold(v.Email, email) {
			return copyIAMUser(v), nil
		}
	}
	return IAMUser{}, ErrNotFound
}
func (m *MemoryStore) SaveIAMUser(v IAMUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, old := range m.iamUsers {
		if id != v.ID && strings.EqualFold(old.Username, v.Username) {
			return ErrConflict
		}
		if id != v.ID && v.Email != "" && old.Email != "" && strings.EqualFold(old.Email, v.Email) {
			return ErrConflict
		}
	}
	m.iamUsers[v.ID] = copyIAMUser(v)
	return nil
}
func (m *MemoryStore) ListAccounts() ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Account, 0, len(m.accounts))
	for _, v := range m.accounts {
		out = append(out, v)
	}
	return out, nil
}
func (m *MemoryStore) GetAccount(id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.accounts[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (m *MemoryStore) SaveAccount(v Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[v.ID] = v
	return nil
}
func (m *MemoryStore) DeleteAccount(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(m.accounts, id)
	return nil
}
func (m *MemoryStore) SaveSession(v Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[v.ID] = v
	return nil
}
func (m *MemoryStore) GetSessionByHash(hash string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.sessions {
		if v.TokenHash == hash {
			if time.Now().After(v.Expires) {
				return Session{}, ErrNotFound
			}
			return v, nil
		}
	}
	return Session{}, ErrNotFound
}
func (m *MemoryStore) DeleteSession(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}
func (m *MemoryStore) DeleteSessionsForUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, sess := range m.sessions {
		if sess.UserID == userID {
			delete(m.sessions, id)
		}
	}
	return nil
}
func (m *MemoryStore) DeleteIAMUser(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.iamUsers[id]; !ok {
		return ErrNotFound
	}
	delete(m.iamUsers, id)
	for k, v := range m.iamKeys {
		if v.UserID == id {
			delete(m.iamKeys, k)
		}
	}
	return nil
}
func (m *MemoryStore) ListIAMGroups() ([]IAMGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IAMGroup, 0, len(m.iamGroups))
	for _, v := range m.iamGroups {
		out = append(out, copyIAMGroup(v))
	}
	return out, nil
}
func (m *MemoryStore) GetIAMGroup(id string) (IAMGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.iamGroups[id]
	if !ok {
		return v, ErrNotFound
	}
	return copyIAMGroup(v), nil
}
func (m *MemoryStore) SaveIAMGroup(v IAMGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, old := range m.iamGroups {
		if id != v.ID && strings.EqualFold(old.Name, v.Name) {
			return ErrConflict
		}
	}
	m.iamGroups[v.ID] = copyIAMGroup(v)
	return nil
}
func (m *MemoryStore) DeleteIAMGroup(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.iamGroups[id]; !ok {
		return ErrNotFound
	}
	delete(m.iamGroups, id)
	for uid, u := range m.iamUsers {
		u.GroupIDs = removeString(u.GroupIDs, id)
		m.iamUsers[uid] = u
	}
	return nil
}
func (m *MemoryStore) ListIAMPolicies() ([]IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IAMPolicy, 0, len(m.iamPolicies))
	for _, v := range m.iamPolicies {
		out = append(out, copyIAMPolicy(v))
	}
	return out, nil
}
func (m *MemoryStore) GetIAMPolicy(id string) (IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.iamPolicies[id]
	if !ok {
		return v, ErrNotFound
	}
	return copyIAMPolicy(v), nil
}
func (m *MemoryStore) SaveIAMPolicy(v IAMPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, old := range m.iamPolicies {
		if id != v.ID && strings.EqualFold(old.Name, v.Name) {
			return ErrConflict
		}
	}
	m.iamPolicies[v.ID] = copyIAMPolicy(v)
	return nil
}
func (m *MemoryStore) DeleteIAMPolicy(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.iamPolicies[id]; !ok {
		return ErrNotFound
	}
	delete(m.iamPolicies, id)
	for gid, g := range m.iamGroups {
		g.PolicyIDs = removeString(g.PolicyIDs, id)
		m.iamGroups[gid] = g
	}
	for uid, u := range m.iamUsers {
		u.PolicyIDs = removeString(u.PolicyIDs, id)
		m.iamUsers[uid] = u
	}
	return nil
}
func (m *MemoryStore) SaveIAMAccessKey(v IAMAccessKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.iamUsers[v.UserID]; !ok {
		return ErrNotFound
	}
	m.iamKeys[v.AccessKeyID] = v
	return nil
}
func (m *MemoryStore) GetIAMAccessKey(id string) (IAMAccessKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.iamKeys[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (m *MemoryStore) ListIAMAccessKeys(userID string) ([]IAMAccessKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []IAMAccessKey{}
	for _, v := range m.iamKeys {
		if v.UserID == userID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (m *MemoryStore) DeleteIAMAccessKey(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.iamKeys[id]; !ok {
		return ErrNotFound
	}
	delete(m.iamKeys, id)
	return nil
}

func (m *MemoryStore) CreateToken(hash string, expires time.Time, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[hash] = &tokenRec{expires: expires}
	return nil
}

func (m *MemoryStore) ConsumeToken(hash string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok || t.used || now.After(t.expires) {
		return ErrTokenInvalid
	}
	t.used = true
	return nil
}

func (m *MemoryStore) UpsertWorker(w Worker) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers[w.ID] = w
	return nil
}

func (m *MemoryStore) GetWorker(id string) (Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return w, ErrNotFound
	}
	return w, nil
}

func (m *MemoryStore) ListWorkers() ([]Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Worker
	for _, w := range m.workers {
		out = append(out, w)
	}
	return out, nil
}

func (m *MemoryStore) SetWorkerStatus(id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return ErrNotFound
	}
	w.Status = status
	m.workers[id] = w
	return nil
}

func (m *MemoryStore) SaveInstance(i Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[i.ID] = i
	return nil
}

func (m *MemoryStore) GetInstance(id string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.instances[id]
	if !ok {
		return i, ErrNotFound
	}
	return i, nil
}

func (m *MemoryStore) ListInstances() ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, i := range m.instances {
		out = append(out, i)
	}
	return out, nil
}

func (m *MemoryStore) DeleteInstance(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.instances, id)
	return nil
}

func (m *MemoryStore) SaveVolume(v Volume) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volumes[v.ID] = v
	return nil
}
func (m *MemoryStore) GetVolume(id string) (Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.volumes[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (m *MemoryStore) ListVolumes() ([]Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Volume, 0, len(m.volumes))
	for _, v := range m.volumes {
		out = append(out, v)
	}
	return out, nil
}
func (m *MemoryStore) DeleteVolume(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.volumes, id)
	return nil
}

func (m *MemoryStore) QueueOp(w string, op proto.Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops[w] = append(m.ops[w], op)
	return nil
}

func (m *MemoryStore) TakeOps(w string) ([]proto.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.ops[w]
	delete(m.ops, w)
	return o, nil
}

func (m *MemoryStore) Audit(actor, action, target, result string) error {
	return m.AuditScoped("", "", actor, action, target, result, "")
}

func (m *MemoryStore) AuditScoped(accountID, actorID, actor, action, target, result, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, AuditRec{time.Now(), accountID, actorID, actor, action, target, result, detail})
	return nil
}

func auditLimit(limit int) int {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func auditOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func auditQueryMatch(rec AuditRec, q AuditQuery, allow map[string]bool, filterAccounts bool, accountID, action, actionPrefix, actor, text string) bool {
	if filterAccounts && !allow[rec.AccountID] {
		return false
	}
	if accountID != "" && rec.AccountID != accountID {
		return false
	}
	if !q.Since.IsZero() && rec.TS.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && rec.TS.After(q.Until) {
		return false
	}
	if action != "" && rec.Action != action {
		return false
	}
	if actionPrefix != "" && !strings.HasPrefix(rec.Action, actionPrefix) {
		return false
	}
	if actor != "" {
		hay := strings.ToLower(rec.Actor + " " + rec.ActorID)
		if !strings.Contains(hay, actor) {
			return false
		}
	}
	if text != "" {
		hay := strings.ToLower(strings.Join([]string{rec.Actor, rec.Action, rec.Target, rec.Result, rec.AccountID}, " "))
		if !strings.Contains(hay, text) {
			return false
		}
	}
	return true
}

func parseAuditQueryFilters(q AuditQuery) (allow map[string]bool, filterAccounts bool, accountID, action, actionPrefix, actor, text string) {
	allow = map[string]bool{}
	filterAccounts = len(q.AccountIDs) > 0
	for _, id := range q.AccountIDs {
		if id != "" {
			allow[id] = true
		}
	}
	accountID = strings.TrimSpace(q.AccountID)
	action = strings.TrimSpace(q.Action)
	actor = strings.ToLower(strings.TrimSpace(q.Actor))
	text = strings.ToLower(strings.TrimSpace(q.Q))
	if strings.HasSuffix(action, "*") || strings.HasSuffix(action, "%") {
		actionPrefix = strings.TrimRight(action, "*%")
		action = ""
	}
	return
}

func (m *MemoryStore) ListAudit(limit int) ([]AuditEntry, error) {
	return m.ListAuditQuery(AuditQuery{Limit: limit})
}

func (m *MemoryStore) ListAuditScoped(accountIDs []string, limit int) ([]AuditEntry, error) {
	return m.ListAuditQuery(AuditQuery{AccountIDs: accountIDs, Limit: limit})
}

// AuditScopedAt inserts an audit row at a fixed timestamp (tests / retention).
func (m *MemoryStore) AuditScopedAt(ts time.Time, accountID, actorID, actor, action, target, result, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts.IsZero() {
		ts = time.Now()
	}
	m.audit = append(m.audit, AuditRec{ts, accountID, actorID, actor, action, target, result, detail})
	return nil
}

func (m *MemoryStore) ListAuditQuery(q AuditQuery) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := auditLimit(q.Limit)
	offset := auditOffset(q.Offset)
	allow, filterAccounts, accountID, action, actionPrefix, actor, text := parseAuditQueryFilters(q)
	n := len(m.audit)
	if n == 0 {
		return []AuditEntry{}, nil
	}
	matched := 0
	out := make([]AuditEntry, 0, limit)
	for i := n - 1; i >= 0; i-- {
		rec := m.audit[i]
		if !auditQueryMatch(rec, q, allow, filterAccounts, accountID, action, actionPrefix, actor, text) {
			continue
		}
		if matched < offset {
			matched++
			continue
		}
		if len(out) >= limit {
			break
		}
		matched++
		out = append(out, AuditEntry{
			ID:        int64(i + 1),
			At:        rec.TS,
			AccountID: rec.AccountID,
			ActorID:   rec.ActorID,
			Actor:     rec.Actor,
			Action:    rec.Action,
			Target:    rec.Target,
			Result:    rec.Result,
			Detail:    rec.Detail,
		})
	}
	return out, nil
}

func (m *MemoryStore) CountAuditQuery(q AuditQuery) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	allow, filterAccounts, accountID, action, actionPrefix, actor, text := parseAuditQueryFilters(q)
	total := 0
	for i := len(m.audit) - 1; i >= 0; i-- {
		if auditQueryMatch(m.audit[i], q, allow, filterAccounts, accountID, action, actionPrefix, actor, text) {
			total++
		}
	}
	return total, nil
}

func (m *MemoryStore) PurgeAuditBefore(before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if before.IsZero() || len(m.audit) == 0 {
		return 0, nil
	}
	kept := m.audit[:0]
	var removed int64
	for _, rec := range m.audit {
		if rec.TS.Before(before) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	m.audit = kept
	return removed, nil
}

func (m *MemoryStore) GetAudit(id int64) (AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.audit) {
		return AuditEntry{}, ErrNotFound
	}
	rec := m.audit[int(id)-1]
	return AuditEntry{
		ID:        id,
		At:        rec.TS,
		AccountID: rec.AccountID,
		ActorID:   rec.ActorID,
		Actor:     rec.Actor,
		Action:    rec.Action,
		Target:    rec.Target,
		Result:    rec.Result,
		Detail:    rec.Detail,
	}, nil
}
