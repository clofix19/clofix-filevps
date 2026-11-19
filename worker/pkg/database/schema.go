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

func normalizeColumnType(engine, raw string) (string, error) {
	t := strings.ToUpper(strings.TrimSpace(raw))
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
	return t, nil
}

func columnAttributes(engine string, c Column, allowUnique bool) (string, error) {
	t, e := normalizeColumnType(engine, c.Type)
	if e != nil {
		return "", e
	}
	definition := t
	if !c.Nullable || c.Primary {
		definition += " NOT NULL"
	}
	if allowUnique && c.Unique {
		definition += " UNIQUE"
	}
	if c.Default != nil {
		if len(*c.Default) > 1024 || strings.ContainsRune(*c.Default, 0) {
			return "", fmt.Errorf("invalid default")
		}
		definition += " DEFAULT " + Literal(*c.Default)
	}
	return definition, nil
}

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
		attrs, e := columnAttributes(engine, c, true)
		if e != nil {
			return "", e
		}
		definitions = append(definitions, name+" "+attrs)
		if c.Primary {
			primary = append(primary, name)
		}
	}
	if len(primary) > 0 {
		definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ",")+")")
	}
	return "CREATE TABLE " + q + " (" + strings.Join(definitions, ",") + ")", nil
}

// AddColumnDDL builds ALTER TABLE … ADD COLUMN for MySQL/PostgreSQL.
func AddColumnDDL(engine, table string, column Column) (string, error) {
	if engine != "mysql" && engine != "postgresql" {
		return "", fmt.Errorf("column changes require MySQL or PostgreSQL")
	}
	if column.Primary {
		return "", fmt.Errorf("cannot add a primary-key column this way; recreate the table or use SQL")
	}
	t, e := Quote(engine, table)
	if e != nil {
		return "", e
	}
	name, e := Quote(engine, column.Name)
	if e != nil {
		return "", e
	}
	attrs, e := columnAttributes(engine, column, true)
	if e != nil {
		return "", e
	}
	return "ALTER TABLE " + t + " ADD COLUMN " + name + " " + attrs, nil
}

// AlterColumnDDL builds ALTER/MODIFY/CHANGE statements. newName may rename the column.
func AlterColumnDDL(engine, table string, column Column, newName string) (string, error) {
	if engine != "mysql" && engine != "postgresql" {
		return "", fmt.Errorf("column changes require MySQL or PostgreSQL")
	}
	if column.Primary {
		return "", fmt.Errorf("cannot change primary-key membership this way")
	}
	t, e := Quote(engine, table)
	if e != nil {
		return "", e
	}
	oldName, e := Quote(engine, column.Name)
	if e != nil {
		return "", e
	}
	target := column.Name
	if newName != "" {
		if e := Name(newName); e != nil {
			return "", e
		}
		target = newName
	}
	newQuoted, e := Quote(engine, target)
	if e != nil {
		return "", e
	}
	typ, e := normalizeColumnType(engine, column.Type)
	if e != nil {
		return "", e
	}
	if column.Default != nil {
		if len(*column.Default) > 1024 || strings.ContainsRune(*column.Default, 0) {
			return "", fmt.Errorf("invalid default")
		}
	}
	if engine == "mysql" {
		verb := "MODIFY COLUMN " + oldName
		if target != column.Name {
			verb = "CHANGE COLUMN " + oldName + " " + newQuoted
		}
		definition := typ
		if !column.Nullable {
			definition += " NOT NULL"
		} else {
			definition += " NULL"
		}
		if column.Default != nil {
			definition += " DEFAULT " + Literal(*column.Default)
		}
		return "ALTER TABLE " + t + " " + verb + " " + definition, nil
	}
	stmts := []string{}
	if target != column.Name {
		stmts = append(stmts, "ALTER TABLE "+t+" RENAME COLUMN "+oldName+" TO "+newQuoted)
	}
	stmts = append(stmts, "ALTER TABLE "+t+" ALTER COLUMN "+newQuoted+" TYPE "+typ+" USING "+newQuoted+"::"+typ)
	if !column.Nullable {
		stmts = append(stmts, "ALTER TABLE "+t+" ALTER COLUMN "+newQuoted+" SET NOT NULL")
	} else {
		stmts = append(stmts, "ALTER TABLE "+t+" ALTER COLUMN "+newQuoted+" DROP NOT NULL")
	}
	if column.Default != nil {
		stmts = append(stmts, "ALTER TABLE "+t+" ALTER COLUMN "+newQuoted+" SET DEFAULT "+Literal(*column.Default))
	} else {
		stmts = append(stmts, "ALTER TABLE "+t+" ALTER COLUMN "+newQuoted+" DROP DEFAULT")
	}
	return strings.Join(stmts, ";\n"), nil
}

// DropColumnDDL builds ALTER TABLE … DROP COLUMN.
func DropColumnDDL(engine, table, column string) (string, error) {
	if engine != "mysql" && engine != "postgresql" {
		return "", fmt.Errorf("column changes require MySQL or PostgreSQL")
	}
	t, e := Quote(engine, table)
	if e != nil {
		return "", e
	}
	c, e := Quote(engine, column)
	if e != nil {
		return "", e
	}
	return "ALTER TABLE " + t + " DROP COLUMN " + c, nil
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
