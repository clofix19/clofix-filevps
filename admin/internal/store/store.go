// Package store defines controller persistence. MemoryStore is for dev/tests;
// PostgresStore (build tag "postgres") uses the schema in admin/db/migrations.
package store

import (
	"errors"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrTokenInvalid = errors.New("enrollment token invalid, expired or already used")
)

type Worker struct {
	ID            string             `json:"id"`
	Hostname      string             `json:"hostname"`
	Status        string             `json:"status"` // pending|active|draining|revoked
	Version       string             `json:"version"`
	Capacity      scheduler.Capacity `json:"capacity"`
	LastHeartbeat time.Time          `json:"last_heartbeat"`
}

type Instance struct {
	ID              string             `json:"id"`
	WorkerID        string             `json:"worker_id"`
	State           string             `json:"state"`
	Error           string             `json:"error,omitempty"`
	PlacementMethod string             `json:"placement_method"` // automatic|manual
	Spec            proto.InstanceSpec `json:"spec"`
	Created         time.Time          `json:"created"`
}

type Store interface {
	// Enrollment tokens: only the SHA-256 hash is stored; consume is atomic.
	CreateToken(hash string, expires time.Time, createdBy string) error
	ConsumeToken(hash string, now time.Time) error

	UpsertWorker(w Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
	SetWorkerStatus(id, status string) error

	SaveInstance(i Instance) error
	GetInstance(id string) (Instance, error)
	ListInstances() ([]Instance, error)
	DeleteInstance(id string) error

	// Operations queued per worker; drained by heartbeat.
	QueueOp(workerID string, op proto.Operation) error
	TakeOps(workerID string) ([]proto.Operation, error)

	Audit(actor, action, target, result string) error
}
