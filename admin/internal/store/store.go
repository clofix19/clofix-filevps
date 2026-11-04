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
	AccountID       string             `json:"account_id,omitempty"`
	WorkerID        string             `json:"worker_id"`
	State           string             `json:"state"`
	Error           string             `json:"error,omitempty"`
	PlacementMethod string             `json:"placement_method"` // automatic|manual
	Spec            proto.InstanceSpec `json:"spec"`
	Created         time.Time          `json:"created"`
}

type Volume struct {
	ID         string    `json:"id"`
	AccountID  string    `json:"account_id,omitempty"`
	Name       string    `json:"name"`
	WorkerID   string    `json:"worker_id"`
	State      string    `json:"state"` // CREATING|READY|ATTACHING|ATTACHED|DETACHING|DELETING|FAILED
	InstanceID string    `json:"instance_id,omitempty"`
	GuestPath  string    `json:"guest_path,omitempty"`
	UsageBytes uint64    `json:"usage_bytes"`
	LimitMB          int64     `json:"limit_mb"`
	DeleteProtection bool      `json:"delete_protection,omitempty"`
	Error            string    `json:"error,omitempty"`
	Created          time.Time `json:"created"`
}

type Account struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"` // platform|partner|client
	ParentID string    `json:"parent_id,omitempty"`
	Created  time.Time `json:"created"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TokenHash string    `json:"-"`
	Expires   time.Time `json:"expires"`
}

type IAMUser struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id,omitempty"`
	Kind         string    `json:"kind,omitempty"` // admin|partner|root|iam
	Email        string    `json:"email,omitempty"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name,omitempty"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`   // IAM User|Read Only|Full Administrator
	Status       string    `json:"status"` // active|inactive
	GroupIDs     []string  `json:"group_ids"`
	PolicyIDs    []string  `json:"policy_ids"`
	MFAEnabled   bool      `json:"mfa_enabled,omitempty"`
	MFASecret    string    `json:"-"` // Google Authenticator TOTP secret (base32)
	AllowIPs     []string  `json:"allow_ips,omitempty"`
	DenyIPs      []string  `json:"deny_ips,omitempty"`
	Created      time.Time `json:"created"`
}

type IAMGroup struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PolicyIDs   []string  `json:"policy_ids"`
	Created     time.Time `json:"created"`
}

type IAMPolicy struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id,omitempty"`
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

	ListAccounts() ([]Account, error)
	GetAccount(id string) (Account, error)
	SaveAccount(account Account) error
	DeleteAccount(id string) error
	ListIAMUsers() ([]IAMUser, error)
	GetIAMUser(id string) (IAMUser, error)
	GetIAMUserByEmail(email string) (IAMUser, error)
	SaveIAMUser(user IAMUser) error
	DeleteIAMUser(id string) error
	SaveSession(session Session) error
	GetSessionByHash(hash string) (Session, error)
	DeleteSession(id string) error
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
	ListAudit(limit int) ([]AuditEntry, error)
}

// AuditEntry is a cf_audit row returned newest-first by ListAudit.
type AuditEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Result string    `json:"result,omitempty"`
}
