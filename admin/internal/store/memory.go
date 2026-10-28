package store

import (
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
	mu        sync.Mutex
	tokens    map[string]*tokenRec
	workers   map[string]Worker
	instances map[string]Instance
	ops       map[string][]proto.Operation
	audit     []AuditRec
}

func NewMemory() *MemoryStore {
	return &MemoryStore{tokens: map[string]*tokenRec{}, workers: map[string]Worker{}, instances: map[string]Instance{}, ops: map[string][]proto.Operation{}}
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
