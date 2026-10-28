//go:build postgres

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

// Postgres implements Store on database/sql. The driver ("pgx") is registered by the main package.
type Postgres struct{ db *sql.DB }

func NewPostgres(db *sql.DB) *Postgres { return &Postgres{db} }

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
	_, err := p.db.Exec(`INSERT INTO cf_workers(id,hostname,status,version,capacity,last_heartbeat) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET hostname=$2,status=$3,version=$4,capacity=$5,last_heartbeat=$6`,
		w.ID, w.Hostname, w.Status, w.Version, c, w.LastHeartbeat)
	return err
}

type scanner interface{ Scan(...any) error }

func scanWorker(s scanner) (Worker, error) {
	var w Worker
	var c []byte
	var hb sql.NullTime
	var host, ver sql.NullString
	if err := s.Scan(&w.ID, &host, &w.Status, &ver, &c, &hb); err != nil {
		return w, err
	}
	w.Hostname, w.Version, w.LastHeartbeat = host.String, ver.String, hb.Time
	json.Unmarshal(c, &w.Capacity)
	return w, nil
}

const workerCols = `id,hostname,status,version,capacity,last_heartbeat`

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
	_, err := p.db.Exec(`INSERT INTO cf_instances(id,worker_id,state,error,placement_method,spec,created) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET worker_id=$2,state=$3,error=$4,placement_method=$5,spec=$6`,
		i.ID, i.WorkerID, i.State, i.Error, i.PlacementMethod, spec, i.Created)
	return err
}

func scanInstance(s scanner) (Instance, error) {
	var i Instance
	var spec []byte
	var e sql.NullString
	if err := s.Scan(&i.ID, &i.WorkerID, &i.State, &e, &i.PlacementMethod, &spec, &i.Created); err != nil {
		return i, err
	}
	i.Error = e.String
	json.Unmarshal(spec, &i.Spec)
	return i, nil
}

const instCols = `id,worker_id,state,error,placement_method,spec,created`

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
	_, err := p.db.Exec(`INSERT INTO cf_audit(actor,action,target,result) VALUES($1,$2,$3,$4)`, actor, action, target, result)
	return err
}

var _ = scheduler.Capacity{}
