package database

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var dataType = regexp.MustCompile(`^(SMALLINT|INTEGER|BIGINT|BOOLEAN|TEXT|DATE|TIMESTAMP|JSON|JSONB|DOUBLE PRECISION|REAL|UUID|BYTEA|BLOB|VARCHAR\([1-9][0-9]{0,3}\)|DECIMAL\([1-9][0-9]?,[0-9]{1,2}\))$`)

func Quote(engine, name string) (string, error) {
	if e := Name(name); e != nil {
		return "", e
	}
	if engine == "mysql" {
		return "`" + name + "`", nil
	}
	return `"` + name + `"`, nil
}
func Literal(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func TableDDL(engine, table string, columns []Column) (string, error) {
	if engine != "mysql" && engine != "postgresql" {
		return "", fmt.Errorf("MongoDB uses collections")
	}
	q, e := Quote(engine, table)
	if e != nil {
		return "", e
	}
	if len(columns) < 1 || len(columns) > 128 {
		return "", fmt.Errorf("provide 1–128 columns")
	}
	seen := map[string]bool{}
	definitions := []string{}
	primary := []string{}
	for _, c := range columns {
		name, e := Quote(engine, c.Name)
		if e != nil {
			return "", e
		}
		if seen[strings.ToLower(c.Name)] {
			return "", fmt.Errorf("duplicate column")
		}
		seen[strings.ToLower(c.Name)] = true
		t := strings.ToUpper(strings.TrimSpace(c.Type))
		if !dataType.MatchString(t) || engine == "mysql" && (t == "JSONB" || t == "UUID" || t == "BYTEA") || engine == "postgresql" && t == "BLOB" {
			return "", fmt.Errorf("unsupported column type")
		}
		if strings.HasPrefix(t, "DECIMAL(") {
			parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(t, "DECIMAL("), ")"), ",")
			precision, _ := strconv.Atoi(parts[0])
			scale, _ := strconv.Atoi(parts[1])
			if scale > precision || precision > 65 || engine == "mysql" && scale > 30 {
				return "", fmt.Errorf("invalid decimal precision or scale")
			}
		}
		definition := name + " " + t
		if !c.Nullable || c.Primary {
			definition += " NOT NULL"
		}
		if c.Unique {
			definition += " UNIQUE"
		}
		if c.Default != nil {
			if len(*c.Default) > 1024 || strings.ContainsRune(*c.Default, 0) {
				return "", fmt.Errorf("invalid default")
			}
			definition += " DEFAULT " + Literal(*c.Default)
		}
		definitions = append(definitions, definition)
		if c.Primary {
			primary = append(primary, name)
		}
	}
	if len(primary) > 0 {
		definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ",")+")")
	}
	return "CREATE TABLE " + q + " (" + strings.Join(definitions, ",") + ")", nil
}
func IndexDDL(engine, table string, index Index) (string, error) {
	if engine != "mysql" && engine != "postgresql" {
		return "", fmt.Errorf("unsupported SQL engine")
	}
	t, e := Quote(engine, table)
	if e != nil {
		return "", e
	}
	name, e := Quote(engine, index.Name)
	if e != nil {
		return "", e
	}
	if len(index.Columns) < 1 || len(index.Columns) > 16 {
		return "", fmt.Errorf("provide 1–16 indexed columns")
	}
	cols := []string{}
	for _, col := range index.Columns {
		q, e := Quote(engine, col)
		if e != nil {
			return "", e
		}
		cols = append(cols, q)
	}
	unique := ""
	if index.Unique {
		unique = "UNIQUE "
	}
	return "CREATE " + unique + "INDEX " + name + " ON " + t + " (" + strings.Join(cols, ",") + ")", nil
}
func MongoJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
