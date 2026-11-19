package database

import (
	"strings"
	"testing"
)

func TestAddAlterDropColumnDDL(t *testing.T) {
	add, err := AddColumnDDL("mysql", "all_types_demo", Column{Name: "notes", Type: "TEXT", Nullable: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(add, "ALTER TABLE `all_types_demo` ADD COLUMN `notes` TEXT") {
		t.Fatalf("mysql add: %s", add)
	}
	def := "x"
	alter, err := AlterColumnDDL("mysql", "all_types_demo", Column{Name: "notes", Type: "VARCHAR(255)", Nullable: false, Default: &def}, "note_text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alter, "CHANGE COLUMN `notes` `note_text` VARCHAR(255) NOT NULL DEFAULT 'x'") {
		t.Fatalf("mysql alter: %s", alter)
	}
	drop, err := DropColumnDDL("postgresql", "all_types_demo", "note_text")
	if err != nil {
		t.Fatal(err)
	}
	if drop != `ALTER TABLE "all_types_demo" DROP COLUMN "note_text"` {
		t.Fatalf("pg drop: %s", drop)
	}
	pg, err := AlterColumnDDL("postgresql", "all_types_demo", Column{Name: "notes", Type: "TEXT", Nullable: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pg, `ALTER COLUMN "notes" DROP NOT NULL`) || !strings.Contains(pg, `DROP DEFAULT`) {
		t.Fatalf("pg alter: %s", pg)
	}
	if _, err := AddColumnDDL("mysql", "all_types_demo", Column{Name: "id", Type: "INTEGER", Primary: true}); err == nil {
		t.Fatal("primary add should fail")
	}
}
