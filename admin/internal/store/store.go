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
	ErrConflict     = errors.New("resource already exists")
	ErrTokenInvalid = errors.New("enrollment token invalid, expired or already used")
)

func removeString(list []string, target string) []string {
	out := list[:0]
	for _, v := range list {
		if v != target {
			out = append(out, v)
		}
	}
	return out
}

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

type Volume struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	WorkerID   string    `json:"worker_id"`
	State      string    `json:"state"` // CREATING|READY|ATTACHING|ATTACHED|DETACHING|DELETING|FAILED
	InstanceID string    `json:"instance_id,omitempty"`
	GuestPath  string    `json:"guest_path,omitempty"`
	UsageBytes uint64    `json:"usage_bytes"`
	LimitMB    int64     `json:"limit_mb"`
	Error      string    `json:"error,omitempty"`
	Created    time.Time `json:"created"`
}

type IAMUser struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`   // IAM User|Read Only|Full Administrator
	Status    string    `json:"status"` // active|inactive
	GroupIDs  []string  `json:"group_ids"`
	PolicyIDs []string  `json:"policy_ids"`
	Created   time.Time `json:"created"`
}

type IAMGroup struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PolicyIDs   []string  `json:"policy_ids"`
	Created     time.Time `json:"created"`
}

type IAMPolicy struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	Created     time.Time `json:"created"`
}

type IAMAccessKey struct {
	AccessKeyID string    `json:"access_key_id"`
	UserID      string    `json:"user_id"`
	SecretHash  string    `json:"-"`
	Active      bool      `json:"active"`
	Created     time.Time `json:"created"`
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
	SaveVolume(v Volume) error
	GetVolume(id string) (Volume, error)
	ListVolumes() ([]Volume, error)
	DeleteVolume(id string) error

	ListIAMUsers() ([]IAMUser, error)
	GetIAMUser(id string) (IAMUser, error)
	SaveIAMUser(user IAMUser) error
	DeleteIAMUser(id string) error
	ListIAMGroups() ([]IAMGroup, error)
	GetIAMGroup(id string) (IAMGroup, error)
	SaveIAMGroup(group IAMGroup) error
	DeleteIAMGroup(id string) error
	ListIAMPolicies() ([]IAMPolicy, error)
	GetIAMPolicy(id string) (IAMPolicy, error)
	SaveIAMPolicy(policy IAMPolicy) error
	DeleteIAMPolicy(id string) error
	SaveIAMAccessKey(key IAMAccessKey) error
	GetIAMAccessKey(id string) (IAMAccessKey, error)
	ListIAMAccessKeys(userID string) ([]IAMAccessKey, error)
	DeleteIAMAccessKey(id string) error

	// Operations queued per worker; drained by heartbeat.
	QueueOp(workerID string, op proto.Operation) error
	TakeOps(workerID string) ([]proto.Operation, error)

	Audit(actor, action, target, result string) error
}
