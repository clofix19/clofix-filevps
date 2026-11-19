package database

import "testing"

func TestClassifySQL(t *testing.T) {
	kind, err := ClassifySQL("SELECT id FROM orders LIMIT 10")
	if err != nil || kind != "read" {
		t.Fatalf("select: %s %v", kind, err)
	}
	kind, err = ClassifySQL("INSERT INTO orders(id) VALUES (1)")
	if err != nil || kind != "write" {
		t.Fatalf("insert: %s %v", kind, err)
	}
	if _, err = ClassifySQL("SELECT 1; DROP TABLE orders"); err == nil {
		t.Fatal("multi-statement accepted")
	}
	if _, err = ClassifySQL("DROP DATABASE production"); err == nil {
		t.Fatal("drop database accepted")
	}
	if _, err = ClassifySQL("SELECT load_file('/etc/passwd')"); err == nil {
		t.Fatal("load_file accepted")
	}
}
