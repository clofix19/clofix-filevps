package server

import "testing"

func TestExtractTaggedLog(t *testing.T) {
	logs := "# nginx/access.log\nignore\n# nginx/error.log\nModSecurity: Access denied\n# nginx/modsec_audit.log\naudit\n"
	got, ok := extractTaggedLog(logs, "nginx", "error.log")
	if !ok || got != "ModSecurity: Access denied" {
		t.Fatalf("error.log: ok=%v got=%q", ok, got)
	}
	got, ok = extractTaggedLog(logs, "nginx", "modsec_audit.log")
	if !ok || got != "audit" {
		t.Fatalf("audit: ok=%v got=%q", ok, got)
	}
	if _, ok = extractTaggedLog("", "nginx", "error.log"); ok {
		t.Fatal("empty logs should miss")
	}
	legacy, ok := extractTaggedLog("# log/php8.3-fpm.log\nNOTICE: fpm is running\n", "php8.3-fpm", "error.log")
	if !ok || legacy != "NOTICE: fpm is running" {
		t.Fatalf("legacy marker: ok=%v got=%q", ok, legacy)
	}
}
