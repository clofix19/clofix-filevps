package database

import (
	"bytes"
	"compress/gzip"
	"math"
	"strings"
	"testing"
)

func TestUploadFormatAndExpansionLimits(t *testing.T) {
	for _, name := range []string{"../dump.sql", "C:\\dump.sql", "backup.sql\x00", "backup.exe"} {
		if _, e := BackupFormat("mysql", name); e == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, e := BackupFormat("mysql", "dump.archive"); e == nil {
		t.Fatal("cross-engine format accepted")
	}
	if e := ValidateContent("postgresql", "dump", strings.NewReader("not a dump"), 1024); e == nil {
		t.Fatal("fake native dump accepted")
	}
	if e := ValidateContent("mysql", "sql", bytes.NewReader([]byte{0xff, 0}), 1024); e == nil {
		t.Fatal("binary SQL accepted")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Write(bytes.Repeat([]byte{'a'}, 4096))
	writer.Close()
	if e := ValidateContent("mysql", "sql.gz", bytes.NewReader(compressed.Bytes()), 1024); e == nil {
		t.Fatal("gzip expansion limit bypass")
	}
	if e := ValidateContent("mysql", "sql.gz", bytes.NewReader(compressed.Bytes()[:compressed.Len()-1]), 8192); e == nil {
		t.Fatal("gzip truncation accepted")
	}
	if e := ValidateContent("mysql", "sql.gz", bytes.NewReader(compressed.Bytes()), 8192); e != nil {
		t.Fatal(e)
	}
}
func TestProjectRejectsInvalidResourceNumbers(t *testing.T) {
	p := Project{ID: "dbp-" + strings.Repeat("a", 32), Engine: "mysql", RAMMB: 512, CPU: 1, DiskMB: 1024, Port: 26000}
	for _, cpu := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1, 129} {
		p.CPU = cpu
		if p.Validate() == nil {
			t.Fatalf("accepted CPU %v", cpu)
		}
	}
}
