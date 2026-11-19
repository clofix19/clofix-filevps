package database

import "testing"

func TestDefaultAndNormalizeProperties(t *testing.T) {
	mysql := DefaultProperties("mysql", 1024)
	if mysql.MaxConnections < 50 || mysql.BufferMB < 128 || mysql.WorkMemMB != 0 {
		t.Fatalf("unexpected mysql defaults: %+v", mysql)
	}
	if mysql.BufferMB > 819 { // ~80% of 1024
		t.Fatalf("mysql buffer too large: %+v", mysql)
	}
	pg := DefaultProperties("postgresql", 2048)
	if pg.WorkMemMB < 4 || pg.BufferMB > 1024 {
		t.Fatalf("unexpected postgres defaults: %+v", pg)
	}
	normalized := NormalizeProperties("postgresql", 2048, Properties{MaxConnections: 120})
	if normalized.MaxConnections != 120 || normalized.BufferMB == 0 || normalized.WorkMemMB == 0 {
		t.Fatalf("normalize should fill missing fields: %+v", normalized)
	}
	if e := (Properties{MaxConnections: 5, BufferMB: 128}).Validate("mysql", 1024); e == nil {
		t.Fatal("expected max_connections validation error")
	}
	a := Project{ID: "dbp-" + "0123456789abcdef0123456789abcdef", Engine: "mysql", Version: "8.4", RAMMB: 1024, CPU: 1, DiskMB: 2048, Port: hostDatabaseBase, Properties: Properties{MaxConnections: 100}}
	b := a
	b.Properties.MaxConnections = 200
	if !a.CoreEqual(b) {
		t.Fatal("CoreEqual should ignore properties")
	}
	b.RAMMB = 2048
	if a.CoreEqual(b) {
		t.Fatal("CoreEqual should detect resource changes")
	}
}
