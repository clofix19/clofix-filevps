//go:build postgres

package store

import "database/sql"

func (p *Postgres) LoadDatabaseState() ([]byte, error) {
	var raw []byte
	e := p.db.QueryRow(`SELECT state FROM cf_managed_database_state WHERE singleton=TRUE`).Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	return raw, e
}
func (p *Postgres) SaveDatabaseState(raw []byte) error {
	_, e := p.db.Exec(`INSERT INTO cf_managed_database_state(singleton,state) VALUES(TRUE,$1::jsonb) ON CONFLICT(singleton) DO UPDATE SET state=EXCLUDED.state,updated_at=now()`, string(raw))
	return e
}
