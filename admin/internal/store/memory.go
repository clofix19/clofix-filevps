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
	TS                            time.Time
	Actor, Action, Target, Result string
}

type MemoryStore struct {
	mu          sync.Mutex
	tokens      map[string]*tokenRec
	workers     map[string]Worker
	instances   map[string]Instance
	volumes     map[string]Volume
	iamUsers    map[string]IAMUser
	iamGroups   map[string]IAMGroup
	iamPolicies map[string]IAMPolicy
	iamKeys     map[string]IAMAccessKey
	ops         map[string][]proto.Operation
	audit       []AuditRec
}

func NewMemory() *MemoryStore {
	return &MemoryStore{tokens: map[string]*tokenRec{}, workers: map[string]Worker{}, instances: map[string]Instance{}, volumes: map[string]Volume{}, iamUsers: map[string]IAMUser{}, iamGroups: map[string]IAMGroup{}, iamPolicies: map[string]IAMPolicy{}, iamKeys: map[string]IAMAccessKey{}, ops: map[string][]proto.Operation{}}
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
func (m *MemoryStore) SaveIAMUser(v IAMUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, old := range m.iamUsers {
		if id != v.ID && strings.EqualFold(old.Username, v.Username) {
			return ErrConflict
		}
	}
	m.iamUsers[v.ID] = copyIAMUser(v)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, AuditRec{time.Now(), actor, action, target, result})
	return nil
}
