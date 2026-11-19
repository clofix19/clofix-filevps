package database

import "testing"

func TestValidateImportAndRowMaps(t *testing.T) {
	if err := ValidateImportSQL("INSERT INTO t(id) VALUES (1);"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImportSQL("DROP DATABASE x;"); err == nil {
		t.Fatal("expected blocked import")
	}
	value := "hello"
	if err := ValidateRowMaps(map[string]*string{"name": &value, "note": nil}, map[string]string{"id": "1"}); err != nil {
		t.Fatal(err)
	}
	if SQLValue(nil) != "NULL" || SQLValue(&value) != "'hello'" {
		t.Fatal("SQLValue mismatch")
	}
}
