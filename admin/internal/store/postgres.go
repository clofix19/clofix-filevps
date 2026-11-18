//go:build postgres

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

// Postgres implements Store on database/sql. The driver ("pgx") is registered by the main package.
type Postgres struct{ db *sql.DB }

func NewPostgres(db *sql.DB) *Postgres { return &Postgres{db} }

func (p *Postgres) EnsureSchema() error {
	_, err := p.db.Exec(`CREATE TABLE IF NOT EXISTS cf_volumes (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, worker_id TEXT NOT NULL, state TEXT NOT NULL,
		instance_id TEXT, guest_path TEXT, usage_bytes BIGINT NOT NULL DEFAULT 0, limit_mb BIGINT NOT NULL DEFAULT 0, error TEXT,
		created TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		return err
	}
	if _, err = p.db.Exec(`ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS limit_mb BIGINT NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS cf_managed_database_state(singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton), state JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS cf_kubernetes_state(singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton), state JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS cf_iam_users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, role TEXT NOT NULL, status TEXT NOT NULL, group_ids JSONB NOT NULL DEFAULT '[]', policy_ids JSONB NOT NULL DEFAULT '[]', created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS cf_iam_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', policy_ids JSONB NOT NULL DEFAULT '[]', created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS cf_iam_policies (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', permissions JSONB NOT NULL DEFAULT '[]', created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS cf_iam_access_keys (access_key_id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES cf_iam_users(id) ON DELETE CASCADE, secret_hash TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT true, created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS cf_iam_keys_user ON cf_iam_access_keys(user_id, created)`,
		`CREATE TABLE IF NOT EXISTS cf_accounts (id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, parent_id TEXT, created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`ALTER TABLE cf_accounts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`,
		`ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS email TEXT`,
		`ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'iam'`,
		`ALTER TABLE cf_iam_users ADD COLUMN IF NOT EXISTS account_id TEXT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS cf_iam_users_email ON cf_iam_users (lower(email)) WHERE email IS NOT NULL AND email <> ''`,
		`ALTER TABLE cf_iam_groups ADD COLUMN IF NOT EXISTS account_id TEXT`,
		`ALTER TABLE cf_iam_policies ADD COLUMN IF NOT EXISTS account_id TEXT`,
		`CREATE TABLE IF NOT EXISTS cf_sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES cf_iam_users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, expires TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS cf_sessions_user ON cf_sessions(user_id, expires)`,
		`ALTER TABLE cf_instances ADD COLUMN IF NOT EXISTS account_id TEXT`,
		`ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS account_id TEXT`,
		`ALTER TABLE cf_volumes ADD COLUMN IF NOT EXISTS delete_protection BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE cf_workers ADD COLUMN IF NOT EXISTS installation_id TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS cf_metrics_samples (
			id BIGSERIAL PRIMARY KEY,
			series TEXT NOT NULL,
			at TIMESTAMPTZ NOT NULL,
			sample JSONB NOT NULL,
			created TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS cf_metrics_samples_series_at ON cf_metrics_samples(series, at DESC)`,
		`CREATE INDEX IF NOT EXISTS cf_metrics_samples_at ON cf_metrics_samples(at)`,
		`CREATE TABLE IF NOT EXISTS cf_audit (
			id BIGSERIAL PRIMARY KEY, ts TIMESTAMPTZ NOT NULL DEFAULT now(),
			actor TEXT, action TEXT NOT NULL, target TEXT, result TEXT)`,
		`ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS account_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cf_audit ADD COLUMN IF NOT EXISTS detail JSONB`,
		`CREATE INDEX IF NOT EXISTS cf_audit_account_id_id ON cf_audit (account_id, id DESC)`,
		`CREATE INDEX IF NOT EXISTS cf_audit_actor_id ON cf_audit (actor_id) WHERE actor_id <> ''`,
		`CREATE INDEX IF NOT EXISTS cf_audit_ts ON cf_audit (ts DESC)`,
	} {
		if _, err := p.db.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) CreateToken(hash string, expires time.Time, by string) error {
	_, err := p.db.Exec(`INSERT INTO cf_tokens(hash, expires_at, created_by) VALUES($1,$2,$3)`, hash, expires, by)
	return err
}

// ConsumeToken is atomic: a single UPDATE succeeds for exactly one caller.
func (p *Postgres) ConsumeToken(hash string, now time.Time) error {
	res, err := p.db.Exec(`UPDATE cf_tokens SET used_at=$2 WHERE hash=$1 AND used_at IS NULL AND expires_at>$2`, hash, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrTokenInvalid
	}
	return nil
}

func (p *Postgres) UpsertWorker(w Worker) error {
	c, _ := json.Marshal(w.Capacity)
	_, err := p.db.Exec(`INSERT INTO cf_workers(id,hostname,status,version,capacity,last_heartbeat,installation_id) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET hostname=$2,status=$3,version=$4,capacity=$5,last_heartbeat=$6,installation_id=$7`,
		w.ID, w.Hostname, w.Status, w.Version, c, w.LastHeartbeat, w.InstallationID)
	return err
}

type scanner interface{ Scan(...any) error }

func scanWorker(s scanner) (Worker, error) {
	var w Worker
	var c []byte
	var hb sql.NullTime
	var host, ver, install sql.NullString
	if err := s.Scan(&w.ID, &host, &w.Status, &ver, &c, &hb, &install); err != nil {
		return w, err
	}
	w.Hostname, w.Version, w.LastHeartbeat, w.InstallationID = host.String, ver.String, hb.Time, install.String
	json.Unmarshal(c, &w.Capacity)
	return w, nil
}

const workerCols = `id,hostname,status,version,capacity,last_heartbeat,COALESCE(installation_id,'')`

func (p *Postgres) GetWorker(id string) (Worker, error) {
	w, err := scanWorker(p.db.QueryRow(`SELECT `+workerCols+` FROM cf_workers WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

func (p *Postgres) ListWorkers() ([]Worker, error) {
	rows, err := p.db.Query(`SELECT ` + workerCols + ` FROM cf_workers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (p *Postgres) SetWorkerStatus(id, status string) error {
	res, err := p.db.Exec(`UPDATE cf_workers SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SaveInstance(i Instance) error {
	spec, _ := json.Marshal(i.Spec)
	_, err := p.db.Exec(`INSERT INTO cf_instances(id,worker_id,state,error,placement_method,spec,created,account_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET worker_id=$2,state=$3,error=$4,placement_method=$5,spec=$6,account_id=$8`,
		i.ID, i.WorkerID, i.State, i.Error, i.PlacementMethod, spec, i.Created, nullString(i.AccountID))
	return err
}

func scanInstance(s scanner) (Instance, error) {
	var i Instance
	var spec []byte
	var e, account sql.NullString
	if err := s.Scan(&i.ID, &i.WorkerID, &i.State, &e, &i.PlacementMethod, &spec, &i.Created, &account); err != nil {
		return i, err
	}
	i.Error, i.AccountID = e.String, account.String
	json.Unmarshal(spec, &i.Spec)
	return i, nil
}

const instCols = `id,worker_id,state,error,placement_method,spec,created,account_id`

func (p *Postgres) GetInstance(id string) (Instance, error) {
	i, err := scanInstance(p.db.QueryRow(`SELECT `+instCols+` FROM cf_instances WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

func (p *Postgres) ListInstances() ([]Instance, error) {
	rows, err := p.db.Query(`SELECT ` + instCols + ` FROM cf_instances ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteInstance(id string) error {
	_, err := p.db.Exec(`DELETE FROM cf_instances WHERE id=$1`, id)
	return err
}

func (p *Postgres) SaveVolume(v Volume) error {
	_, err := p.db.Exec(`INSERT INTO cf_volumes(id,name,worker_id,state,instance_id,guest_path,usage_bytes,limit_mb,error,created,account_id,delete_protection) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(id) DO UPDATE SET name=$2,worker_id=$3,state=$4,instance_id=$5,guest_path=$6,usage_bytes=$7,limit_mb=$8,error=$9,account_id=$11,delete_protection=$12`, v.ID, v.Name, v.WorkerID, v.State, nullString(v.InstanceID), nullString(v.GuestPath), v.UsageBytes, v.LimitMB, nullString(v.Error), v.Created, nullString(v.AccountID), v.DeleteProtection)
	return err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func scanVolume(s scanner) (Volume, error) {
	var v Volume
	var inst, path, errText, account sql.NullString
	if err := s.Scan(&v.ID, &v.Name, &v.WorkerID, &v.State, &inst, &path, &v.UsageBytes, &v.LimitMB, &errText, &v.Created, &account, &v.DeleteProtection); err != nil {
		return v, err
	}
	v.InstanceID, v.GuestPath, v.Error, v.AccountID = inst.String, path.String, errText.String, account.String
	return v, nil
}

const volumeCols = `id,name,worker_id,state,instance_id,guest_path,usage_bytes,limit_mb,error,created,account_id,delete_protection`

func (p *Postgres) GetVolume(id string) (Volume, error) {
	v, err := scanVolume(p.db.QueryRow(`SELECT `+volumeCols+` FROM cf_volumes WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) ListVolumes() ([]Volume, error) {
	rows, err := p.db.Query(`SELECT ` + volumeCols + ` FROM cf_volumes ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Volume{}
	for rows.Next() {
		v, e := scanVolume(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) DeleteVolume(id string) error {
	_, err := p.db.Exec(`DELETE FROM cf_volumes WHERE id=$1`, id)
	return err
}

func (p *Postgres) QueueOp(workerID string, op proto.Operation) error {
	b, _ := json.Marshal(op)
	_, err := p.db.Exec(`INSERT INTO cf_ops(worker_id, op) VALUES($1,$2)`, workerID, b)
	return err
}

// TakeOps atomically removes and returns the worker's queued operations in order.
func (p *Postgres) TakeOps(workerID string) ([]proto.Operation, error) {
	rows, err := p.db.Query(`WITH d AS (DELETE FROM cf_ops WHERE worker_id=$1 RETURNING seq, op) SELECT op FROM d ORDER BY seq`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proto.Operation
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var op proto.Operation
		if json.Unmarshal(b, &op) == nil {
			out = append(out, op)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) Audit(actor, action, target, result string) error {
	return p.AuditScoped("", "", actor, action, target, result, "")
}

func (p *Postgres) AuditScoped(accountID, actorID, actor, action, target, result, detail string) error {
	var detailArg any
	if strings.TrimSpace(detail) != "" {
		detailArg = detail
	}
	_, err := p.db.Exec(`INSERT INTO cf_audit(account_id,actor_id,actor,action,target,result,detail) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		accountID, actorID, actor, action, target, result, detailArg)
	return err
}

func scanAudit(s scanner) (AuditEntry, error) {
	var e AuditEntry
	var detail []byte
	err := s.Scan(&e.ID, &e.At, &e.AccountID, &e.ActorID, &e.Actor, &e.Action, &e.Target, &e.Result, &detail)
	if err != nil {
		return e, err
	}
	if len(detail) > 0 {
		e.Detail = string(detail)
	}
	return e, nil
}

func (p *Postgres) ListAudit(limit int) ([]AuditEntry, error) {
	return p.ListAuditQuery(AuditQuery{Limit: limit})
}

func (p *Postgres) ListAuditScoped(accountIDs []string, limit int) ([]AuditEntry, error) {
	return p.ListAuditQuery(AuditQuery{AccountIDs: accountIDs, Limit: limit})
}

func auditSQLWhere(q AuditQuery) (whereSQL string, args []any) {
	where := []string{}
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, strings.Replace(clause, "?", fmt.Sprintf("$%d", len(args)), 1))
	}
	if len(q.AccountIDs) > 0 {
		add("account_id = ANY(?)", q.AccountIDs)
	}
	if id := strings.TrimSpace(q.AccountID); id != "" {
		add("account_id = ?", id)
	}
	if !q.Since.IsZero() {
		add("ts >= ?", q.Since.UTC())
	}
	if !q.Until.IsZero() {
		add("ts <= ?", q.Until.UTC())
	}
	if action := strings.TrimSpace(q.Action); action != "" {
		if strings.HasSuffix(action, "*") || strings.HasSuffix(action, "%") {
			add("action ILIKE ?", strings.TrimRight(action, "*%")+"%")
		} else {
			add("action = ?", action)
		}
	}
	if actor := strings.TrimSpace(q.Actor); actor != "" {
		args = append(args, "%"+actor+"%", "%"+actor+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(actor ILIKE $%d OR actor_id ILIKE $%d)", n-1, n))
	}
	if text := strings.TrimSpace(q.Q); text != "" {
		pat := "%" + text + "%"
		args = append(args, pat, pat, pat, pat, pat)
		n := len(args)
		where = append(where, fmt.Sprintf("(actor ILIKE $%d OR action ILIKE $%d OR COALESCE(target,'') ILIKE $%d OR COALESCE(result,'') ILIKE $%d OR COALESCE(account_id,'') ILIKE $%d)", n-4, n-3, n-2, n-1, n))
	}
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	return whereSQL, args
}

func (p *Postgres) ListAuditQuery(q AuditQuery) ([]AuditEntry, error) {
	limit := auditLimit(q.Limit)
	offset := auditOffset(q.Offset)
	whereSQL, args := auditSQLWhere(q)
	query := `SELECT id, ts, COALESCE(account_id,''), COALESCE(actor_id,''), COALESCE(actor,''), action, COALESCE(target,''), COALESCE(result,''), detail FROM cf_audit` + whereSQL
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) CountAuditQuery(q AuditQuery) (int, error) {
	whereSQL, args := auditSQLWhere(q)
	var total int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM cf_audit`+whereSQL, args...).Scan(&total)
	return total, err
}

func (p *Postgres) PurgeAuditBefore(before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	res, err := p.db.Exec(`DELETE FROM cf_audit WHERE ts < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (p *Postgres) GetAudit(id int64) (AuditEntry, error) {
	row := p.db.QueryRow(`SELECT id, ts, COALESCE(account_id,''), COALESCE(actor_id,''), COALESCE(actor,''), action, COALESCE(target,''), COALESCE(result,''), detail FROM cf_audit WHERE id=$1`, id)
	e, err := scanAudit(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return AuditEntry{}, ErrNotFound
		}
		return AuditEntry{}, err
	}
	return e, nil
}

func scanIAMUser(s scanner) (IAMUser, error) {
	var u IAMUser
	var groups, policies, allowIPs, denyIPs []byte
	var email, account, display sql.NullString
	err := s.Scan(&u.ID, &u.Username, &u.Role, &u.Status, &groups, &policies, &u.Created, &email, &u.PasswordHash, &u.Kind, &account, &display, &u.MFAEnabled, &u.MFASecret, &allowIPs, &denyIPs)
	if err == nil {
		_ = json.Unmarshal(groups, &u.GroupIDs)
		_ = json.Unmarshal(policies, &u.PolicyIDs)
		_ = json.Unmarshal(allowIPs, &u.AllowIPs)
		_ = json.Unmarshal(denyIPs, &u.DenyIPs)
		u.Email, u.AccountID, u.DisplayName = email.String, account.String, display.String
	}
	return u, err
}

const iamUserCols = `id,username,role,status,group_ids,policy_ids,created,email,password_hash,kind,account_id,display_name,mfa_enabled,mfa_secret,allow_ips,deny_ips`

func (p *Postgres) ListIAMUsers() ([]IAMUser, error) {
	rows, err := p.db.Query(`SELECT ` + iamUserCols + ` FROM cf_iam_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMUser{}
	for rows.Next() {
		v, e := scanIAMUser(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) GetIAMUser(id string) (IAMUser, error) {
	v, err := scanIAMUser(p.db.QueryRow(`SELECT `+iamUserCols+` FROM cf_iam_users WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) GetIAMUserByEmail(email string) (IAMUser, error) {
	v, err := scanIAMUser(p.db.QueryRow(`SELECT `+iamUserCols+` FROM cf_iam_users WHERE lower(email)=lower($1)`, strings.TrimSpace(email)))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) SaveIAMUser(v IAMUser) error {
	g, _ := json.Marshal(v.GroupIDs)
	ps, _ := json.Marshal(v.PolicyIDs)
	allow, _ := json.Marshal(v.AllowIPs)
	deny, _ := json.Marshal(v.DenyIPs)
	_, err := p.db.Exec(`INSERT INTO cf_iam_users(id,username,role,status,group_ids,policy_ids,created,email,password_hash,kind,account_id,display_name,mfa_enabled,mfa_secret,allow_ips,deny_ips) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(id) DO UPDATE SET username=$2,role=$3,status=$4,group_ids=$5,policy_ids=$6,email=$8,password_hash=$9,kind=$10,account_id=$11,display_name=$12,mfa_enabled=$13,mfa_secret=$14,allow_ips=$15,deny_ips=$16`, v.ID, v.Username, v.Role, v.Status, g, ps, v.Created, nullString(v.Email), v.PasswordHash, v.Kind, nullString(v.AccountID), v.DisplayName, v.MFAEnabled, v.MFASecret, allow, deny)
	return err
}
func (p *Postgres) DeleteIAMUser(id string) error {
	res, err := p.db.Exec(`DELETE FROM cf_iam_users WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanIAMGroup(s scanner) (IAMGroup, error) {
	var g IAMGroup
	var policies []byte
	var account sql.NullString
	err := s.Scan(&g.ID, &g.Name, &g.Description, &policies, &g.Created, &account)
	if err == nil {
		_ = json.Unmarshal(policies, &g.PolicyIDs)
		g.AccountID = account.String
	}
	return g, err
}

const iamGroupCols = `id,name,description,policy_ids,created,account_id`

func (p *Postgres) ListIAMGroups() ([]IAMGroup, error) {
	rows, err := p.db.Query(`SELECT ` + iamGroupCols + ` FROM cf_iam_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMGroup{}
	for rows.Next() {
		v, e := scanIAMGroup(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) GetIAMGroup(id string) (IAMGroup, error) {
	v, err := scanIAMGroup(p.db.QueryRow(`SELECT `+iamGroupCols+` FROM cf_iam_groups WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) SaveIAMGroup(v IAMGroup) error {
	ps, _ := json.Marshal(v.PolicyIDs)
	_, err := p.db.Exec(`INSERT INTO cf_iam_groups(id,name,description,policy_ids,created,account_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=$2,description=$3,policy_ids=$4,account_id=$6`, v.ID, v.Name, v.Description, ps, v.Created, nullString(v.AccountID))
	return err
}
func (p *Postgres) DeleteIAMGroup(id string) error {
	res, err := p.db.Exec(`DELETE FROM cf_iam_groups WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanIAMPolicy(s scanner) (IAMPolicy, error) {
	var v IAMPolicy
	var permissions []byte
	var account sql.NullString
	err := s.Scan(&v.ID, &v.Name, &v.Description, &permissions, &v.Created, &account)
	if err == nil {
		_ = json.Unmarshal(permissions, &v.Permissions)
		v.AccountID = account.String
	}
	return v, err
}

const iamPolicyCols = `id,name,description,permissions,created,account_id`

func (p *Postgres) ListIAMPolicies() ([]IAMPolicy, error) {
	rows, err := p.db.Query(`SELECT ` + iamPolicyCols + ` FROM cf_iam_policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMPolicy{}
	for rows.Next() {
		v, e := scanIAMPolicy(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) GetIAMPolicy(id string) (IAMPolicy, error) {
	v, err := scanIAMPolicy(p.db.QueryRow(`SELECT `+iamPolicyCols+` FROM cf_iam_policies WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) SaveIAMPolicy(v IAMPolicy) error {
	ps, _ := json.Marshal(v.Permissions)
	_, err := p.db.Exec(`INSERT INTO cf_iam_policies(id,name,description,permissions,created,account_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=$2,description=$3,permissions=$4,account_id=$6`, v.ID, v.Name, v.Description, ps, v.Created, nullString(v.AccountID))
	return err
}
func (p *Postgres) DeleteIAMPolicy(id string) error {
	res, err := p.db.Exec(`DELETE FROM cf_iam_policies WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SaveIAMAccessKey(v IAMAccessKey) error {
	_, err := p.db.Exec(`INSERT INTO cf_iam_access_keys(access_key_id,user_id,secret_hash,active,created) VALUES($1,$2,$3,$4,$5)`, v.AccessKeyID, v.UserID, v.SecretHash, v.Active, v.Created)
	return err
}
func scanIAMAccessKey(s scanner) (IAMAccessKey, error) {
	var v IAMAccessKey
	err := s.Scan(&v.AccessKeyID, &v.UserID, &v.SecretHash, &v.Active, &v.Created)
	return v, err
}

const iamAccessKeyCols = `access_key_id,user_id,secret_hash,active,created`

func (p *Postgres) GetIAMAccessKey(id string) (IAMAccessKey, error) {
	v, err := scanIAMAccessKey(p.db.QueryRow(`SELECT `+iamAccessKeyCols+` FROM cf_iam_access_keys WHERE access_key_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) ListIAMAccessKeys(userID string) ([]IAMAccessKey, error) {
	rows, err := p.db.Query(`SELECT `+iamAccessKeyCols+` FROM cf_iam_access_keys WHERE user_id=$1 ORDER BY created`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMAccessKey{}
	for rows.Next() {
		v, e := scanIAMAccessKey(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) DeleteIAMAccessKey(id string) error {
	res, err := p.db.Exec(`DELETE FROM cf_iam_access_keys WHERE access_key_id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListAccounts() ([]Account, error) {
	rows, err := p.db.Query(`SELECT id,name,kind,COALESCE(NULLIF(status,''),'active'),parent_id,created FROM cf_accounts ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var v Account
		var parent sql.NullString
		if e := rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Status, &parent, &v.Created); e != nil {
			return nil, e
		}
		v.ParentID = parent.String
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) GetAccount(id string) (Account, error) {
	var v Account
	var parent sql.NullString
	err := p.db.QueryRow(`SELECT id,name,kind,COALESCE(NULLIF(status,''),'active'),parent_id,created FROM cf_accounts WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.Kind, &v.Status, &parent, &v.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	v.ParentID = parent.String
	return v, err
}
func (p *Postgres) SaveAccount(v Account) error {
	if v.Status == "" {
		v.Status = "active"
	}
	_, err := p.db.Exec(`INSERT INTO cf_accounts(id,name,kind,status,parent_id,created) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=$2,kind=$3,status=$4,parent_id=$5`, v.ID, v.Name, v.Kind, v.Status, nullString(v.ParentID), v.Created)
	return err
}
func (p *Postgres) DeleteAccount(id string) error {
	res, err := p.db.Exec(`DELETE FROM cf_accounts WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (p *Postgres) SaveSession(v Session) error {
	_, err := p.db.Exec(`INSERT INTO cf_sessions(id,user_id,token_hash,expires) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET token_hash=$3,expires=$4`, v.ID, v.UserID, v.TokenHash, v.Expires)
	return err
}
func (p *Postgres) GetSessionByHash(hash string) (Session, error) {
	var v Session
	err := p.db.QueryRow(`SELECT id,user_id,token_hash,expires FROM cf_sessions WHERE token_hash=$1 AND expires>$2`, hash, time.Now()).Scan(&v.ID, &v.UserID, &v.TokenHash, &v.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (p *Postgres) DeleteSessionsForUser(userID string) error {
	_, err := p.db.Exec(`DELETE FROM cf_sessions WHERE user_id=$1`, userID)
	return err
}
func (p *Postgres) DeleteSession(id string) error {
	_, err := p.db.Exec(`DELETE FROM cf_sessions WHERE id=$1`, id)
	return err
}

var _ = scheduler.Capacity{}
