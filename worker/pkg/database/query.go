package database

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	MaxExplorerQueryBytes = 48 << 10
	MaxImportSQLBytes     = 2 << 20
	MaxRowValueBytes      = 64 << 10
	MaxRowColumns         = 64
	MaxExplorerLimit      = 200
	DefaultExplorerLimit  = 50
)

// ClassifySQL returns "read", "write", or an error for a single SQL statement.
func ClassifySQL(statement string) (string, error) {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" {
		return "", fmt.Errorf("SQL statement required")
	}
	if len(trimmed) > MaxExplorerQueryBytes {
		return "", fmt.Errorf("SQL statement exceeds %d bytes", MaxExplorerQueryBytes)
	}
	if strings.ContainsAny(trimmed, "\x00") {
		return "", fmt.Errorf("SQL statement contains NUL bytes")
	}
	body := strings.TrimRightFunc(trimmed, func(r rune) bool { return r == ';' || unicode.IsSpace(r) })
	if strings.Contains(body, ";") {
		return "", fmt.Errorf("only a single SQL statement is allowed")
	}
	upper := strings.ToUpper(body)
	blocked := []string{
		"INTO OUTFILE", "INTO DUMPFILE", "LOAD_FILE", "LOAD DATA", "LOAD XML",
		"INSTALL PLUGIN", "UNINSTALL PLUGIN", "GRANT ", "REVOKE ",
		"CREATE USER", "ALTER USER", "DROP USER", "SET PASSWORD",
		"CREATE ROLE", "DROP ROLE", "ALTER SYSTEM", "COPY ", "\\COPY",
		"PG_READ_FILE", "PG_WRITE_FILE", "LO_IMPORT", "LO_EXPORT",
		"DROP DATABASE", "CREATE DATABASE", "ALTER DATABASE",
	}
	for _, item := range blocked {
		if strings.Contains(upper, item) {
			return "", fmt.Errorf("statement contains a blocked operation")
		}
	}
	first := firstSQLKeyword(upper)
	switch first {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "TABLE", "VALUES":
		return "read", nil
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "CALL", "DO", "ANALYZE", "OPTIMIZE", "REINDEX", "VACUUM", "COMMENT":
		return "write", nil
	default:
		return "", fmt.Errorf("unsupported SQL statement type")
	}
}

func firstSQLKeyword(upper string) string {
	start := 0
	for start < len(upper) {
		for start < len(upper) && unicode.IsSpace(rune(upper[start])) {
			start++
		}
		if strings.HasPrefix(upper[start:], "--") {
			if nl := strings.IndexByte(upper[start:], '\n'); nl >= 0 {
				start += nl + 1
				continue
			}
			return ""
		}
		if strings.HasPrefix(upper[start:], "/*") {
			if end := strings.Index(upper[start:], "*/"); end >= 0 {
				start += end + 2
				continue
			}
			return ""
		}
		break
	}
	end := start
	for end < len(upper) && (unicode.IsLetter(rune(upper[end])) || upper[end] == '_') {
		end++
	}
	return upper[start:end]
}

func ClampExplorerLimit(limit int) int {
	if limit <= 0 {
		return DefaultExplorerLimit
	}
	if limit > MaxExplorerLimit {
		return MaxExplorerLimit
	}
	return limit
}

func ClampExplorerOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > 1_000_000 {
		return 1_000_000
	}
	return offset
}

// ValidateImportSQL checks a multi-statement SQL script before worker import.
func ValidateImportSQL(script string) error {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return fmt.Errorf("SQL script required")
	}
	if len(trimmed) > MaxImportSQLBytes {
		return fmt.Errorf("SQL script exceeds %d bytes", MaxImportSQLBytes)
	}
	if strings.ContainsAny(trimmed, "\x00") {
		return fmt.Errorf("SQL script contains NUL bytes")
	}
	upper := strings.ToUpper(trimmed)
	blocked := []string{
		"INTO OUTFILE", "INTO DUMPFILE", "LOAD_FILE", "LOAD DATA", "LOAD XML",
		"INSTALL PLUGIN", "UNINSTALL PLUGIN", "GRANT ", "REVOKE ",
		"CREATE USER", "ALTER USER", "DROP USER", "SET PASSWORD",
		"CREATE ROLE", "DROP ROLE", "ALTER SYSTEM", "COPY ", "\\COPY",
		"PG_READ_FILE", "PG_WRITE_FILE", "LO_IMPORT", "LO_EXPORT",
		"DROP DATABASE", "CREATE DATABASE", "ALTER DATABASE",
		"DROP SCHEMA", "CREATE SCHEMA",
	}
	for _, item := range blocked {
		if strings.Contains(upper, item) {
			return fmt.Errorf("SQL script contains a blocked operation")
		}
	}
	return nil
}

// SQLValue renders a row form value for INSERT/UPDATE (nil pointer => NULL).
func SQLValue(raw *string) string {
	if raw == nil {
		return "NULL"
	}
	return Literal(*raw)
}

func ValidateRowMaps(values map[string]*string, keys map[string]string) error {
	if len(values) > MaxRowColumns || len(keys) > MaxRowColumns {
		return fmt.Errorf("too many columns (max %d)", MaxRowColumns)
	}
	for name, value := range values {
		if e := Name(name); e != nil {
			return fmt.Errorf("invalid column %q", name)
		}
		if value != nil && (len(*value) > MaxRowValueBytes || strings.ContainsRune(*value, 0)) {
			return fmt.Errorf("value for %s is too large or contains NUL", name)
		}
	}
	for name, value := range keys {
		if e := Name(name); e != nil {
			return fmt.Errorf("invalid key column %q", name)
		}
		if value == "" || len(value) > MaxRowValueBytes || strings.ContainsRune(value, 0) {
			return fmt.Errorf("key value for %s is required", name)
		}
	}
	return nil
}
