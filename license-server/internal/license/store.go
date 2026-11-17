package license

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminAccount is a CloFix company operator who manages admin-server licenses.
type AdminAccount struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// Customer is a customer organization that receives admin-server licenses.
type Customer struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Contact string    `json:"contact,omitempty"`
	Notes   string    `json:"notes,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Store persists plans, licenses, customers, and company admin accounts.
type Store interface {
	Load() (plans map[string]Plan, licenses map[string]License, customers map[string]Customer, admins map[string]AdminAccount, legacyRaw []byte, err error)
	Save(plans map[string]Plan, licenses map[string]License, customers map[string]Customer, admins map[string]AdminAccount) error
	Close() error
}

type fileStore struct{ path string }

func NewFileStore(dir string) Store {
	return &fileStore{path: filepath.Join(dir, "licenses.json")}
}

func (f *fileStore) Load() (map[string]Plan, map[string]License, map[string]Customer, map[string]AdminAccount, []byte, error) {
	raw, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return map[string]Plan{}, map[string]License{}, map[string]Customer{}, map[string]AdminAccount{}, nil, nil
	}
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	var payload struct {
		Plans     map[string]Plan         `json:"plans"`
		Licenses  map[string]License      `json:"licenses"`
		Customers map[string]Customer     `json:"customers"`
		Admins    map[string]AdminAccount `json:"admins"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("invalid license database: %w", err)
	}
	if payload.Plans == nil {
		payload.Plans = map[string]Plan{}
	}
	if payload.Licenses == nil {
		payload.Licenses = map[string]License{}
	}
	if payload.Customers == nil {
		payload.Customers = map[string]Customer{}
	}
	if payload.Admins == nil {
		payload.Admins = map[string]AdminAccount{}
	}
	return payload.Plans, payload.Licenses, payload.Customers, payload.Admins, raw, nil
}

func (f *fileStore) Save(plans map[string]Plan, licenses map[string]License, customers map[string]Customer, admins map[string]AdminAccount) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Plans     map[string]Plan         `json:"plans"`
		Licenses  map[string]License      `json:"licenses"`
		Customers map[string]Customer     `json:"customers"`
		Admins    map[string]AdminAccount `json:"admins"`
	}{Plans: plans, Licenses: licenses, Customers: customers, Admins: admins})
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *fileStore) Close() error { return nil }

type postgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(databaseURL string) (Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("license database connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("license database ping: %w", err)
	}
	_, err = pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS license_state (
  id smallint PRIMARY KEY CHECK (id = 1),
  plans jsonb NOT NULL DEFAULT '{}'::jsonb,
  licenses jsonb NOT NULL DEFAULT '{}'::jsonb,
  customers jsonb NOT NULL DEFAULT '{}'::jsonb,
  admins jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS admins jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS customers jsonb NOT NULL DEFAULT '{}'::jsonb;
INSERT INTO license_state (id, plans, licenses, customers, admins)
VALUES (1, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)
ON CONFLICT (id) DO NOTHING;
`)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("license database migrate: %w", err)
	}
	return &postgresStore{pool: pool}, nil
}

func (p *postgresStore) Load() (map[string]Plan, map[string]License, map[string]Customer, map[string]AdminAccount, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var plansRaw, licensesRaw, customersRaw, adminsRaw []byte
	err := p.pool.QueryRow(ctx, `SELECT plans, licenses, COALESCE(customers, '{}'::jsonb), COALESCE(admins, '{}'::jsonb) FROM license_state WHERE id = 1`).Scan(&plansRaw, &licensesRaw, &customersRaw, &adminsRaw)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	plans := map[string]Plan{}
	licenses := map[string]License{}
	customers := map[string]Customer{}
	admins := map[string]AdminAccount{}
	if err := json.Unmarshal(plansRaw, &plans); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("decode plans: %w", err)
	}
	if err := json.Unmarshal(licensesRaw, &licenses); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("decode licenses: %w", err)
	}
	if err := json.Unmarshal(customersRaw, &customers); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("decode customers: %w", err)
	}
	if err := json.Unmarshal(adminsRaw, &admins); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("decode admins: %w", err)
	}
	if plans == nil {
		plans = map[string]Plan{}
	}
	if licenses == nil {
		licenses = map[string]License{}
	}
	if customers == nil {
		customers = map[string]Customer{}
	}
	if admins == nil {
		admins = map[string]AdminAccount{}
	}
	return plans, licenses, customers, admins, nil, nil
}

func (p *postgresStore) Save(plans map[string]Plan, licenses map[string]License, customers map[string]Customer, admins map[string]AdminAccount) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	plansRaw, err := json.Marshal(plans)
	if err != nil {
		return err
	}
	licensesRaw, err := json.Marshal(licenses)
	if err != nil {
		return err
	}
	customersRaw, err := json.Marshal(customers)
	if err != nil {
		return err
	}
	adminsRaw, err := json.Marshal(admins)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
UPDATE license_state
SET plans = $1::jsonb, licenses = $2::jsonb, customers = $3::jsonb, admins = $4::jsonb, updated_at = now()
WHERE id = 1`, plansRaw, licensesRaw, customersRaw, adminsRaw)
	return err
}

func (p *postgresStore) Close() error {
	p.pool.Close()
	return nil
}

func (p *postgresStore) empty() (bool, error) {
	plans, licenses, customers, admins, _, err := p.Load()
	if err != nil {
		return false, err
	}
	return len(plans) == 0 && len(licenses) == 0 && len(customers) == 0 && len(admins) == 0, nil
}
