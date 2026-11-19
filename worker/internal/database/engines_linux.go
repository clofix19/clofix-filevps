//go:build linux

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func (m *Manager) sql(ctx context.Context, r record, database, statement string) (string, error) {
	args := []string{"exec", "-i", "--user", "0:0"}
	if r.Spec.Engine == "postgresql" {
		args = append(args, "-e", "PGPASSFILE=/run/clofix/pgpass", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(r.Spec.ID), "psql", "-X", "--no-password", "--set=ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", "postgres", "-d", database, "-qAt")
		statement = "SET standard_conforming_strings=on;\n" + statement
	} else {
		args = append(args, container(r.Spec.ID), "mysql", "--defaults-extra-file=/run/clofix/mysql.cnf", "--batch", "--skip-column-names", "--binary-mode=1", "--local-infile=0")
		if database != "" {
			args = append(args, "--database="+database)
		}
		statement = "SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';\n" + statement
	}
	raw, e := m.docker(ctx, strings.NewReader(statement), args...)
	return strings.TrimSpace(string(raw)), e
}
func (m *Manager) mongo(ctx context.Context, r record, script string) (string, error) {
	prefix := `const secret=require('fs').readFileSync('/run/clofix/mongo-admin-password','utf8').trim(); if(!db.getSiblingDB('admin').auth('clofixadmin',secret)){throw Error('authentication failed')};` + "\n"
	raw, e := m.docker(ctx, strings.NewReader(prefix+script), "exec", "-i", "--user", "0:0", "-e", "HOME=/tmp/clofix-admin", container(r.Spec.ID), "mongosh", "--quiet", "--norc", "--file", "/dev/stdin", "--host", "127.0.0.1", "--tls", "--tlsCAFile", "/run/clofix/ca.pem")
	return strings.TrimSpace(string(raw)), e
}
func (m *Manager) redis(ctx context.Context, r record, args ...string) (string, error) {
	// Engine containers drop all capabilities, so root cannot read the 0440
	// admin-password file owned by uid 999. Probe as that user instead.
	cmd := []string{"exec", "-i", "--user", "999:999", container(r.Spec.ID), "sh", "-c", `REDISCLI_AUTH=$(cat /run/clofix/admin-password) exec redis-cli -h 127.0.0.1 -p 6379 --raw "$@"`, "redis-cli"}
	cmd = append(cmd, args...)
	raw, e := m.docker(ctx, nil, cmd...)
	return strings.TrimSpace(string(raw)), e
}
func (m *Manager) listDatabases(ctx context.Context, r record) ([]string, error) {
	var raw string
	var e error
	switch r.Spec.Engine {
	case "postgresql":
		raw, e = m.sql(ctx, r, "postgres", "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY datname;")
	case "mysql":
		raw, e = m.sql(ctx, r, "", "SHOW DATABASES;")
	case "mongodb":
		raw, e = m.mongo(ctx, r, `print(JSON.stringify(db.adminCommand({listDatabases:1,nameOnly:true}).databases.map(x=>x.name)))`)
		if e != nil {
			return nil, e
		}
		var names []string
		if e = json.Unmarshal([]byte(raw), &names); e != nil {
			return nil, fmt.Errorf("could not parse engine database list")
		}
		out := []string{}
		for _, name := range names {
			if db.Name(name) == nil {
				out = append(out, name)
			}
		}
		return out, nil
	case "redis":
		if _, e = m.redis(ctx, r, "PING"); e != nil {
			return nil, e
		}
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, name := range strings.Split(raw, "\n") {
		if db.Name(name) == nil {
			out = append(out, name)
		}
	}
	return out, nil
}
func (m *Manager) engineOperation(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if op.Action == "backup" || op.Action == "restore" {
		return m.backupOperation(ctx, r, op)
	}
	result := map[string]any{}
	engine := r.Spec.Engine
	admin := ""
	if engine == "postgresql" {
		admin = "postgres"
	}
	switch op.Action {
	case "ping":
		if engine == "mongodb" {
			_, e := m.mongo(ctx, r, `if(db.adminCommand({ping:1}).ok!==1)throw Error('not ready');`)
			return nil, e
		}
		if engine == "redis" {
			reply, e := m.redis(ctx, r, "PING")
			if e != nil {
				return nil, e
			}
			if !strings.EqualFold(reply, "PONG") {
				return nil, fmt.Errorf("redis ping failed")
			}
			return nil, nil
		}
		_, e := m.sql(ctx, r, admin, "SELECT 1;")
		return nil, e
	case "list-databases":
		names, e := m.listDatabases(ctx, r)
		if e != nil {
			return nil, e
		}
		result["databases"] = names
	case "create-database":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		q, _ := db.Quote(engine, op.Database)
		names, e := m.listDatabases(ctx, r)
		if e != nil {
			return nil, e
		}
		exists := false
		for _, name := range names {
			if name == op.Database {
				exists = true
			}
		}
		if !exists {
			switch engine {
			case "postgresql":
				_, e = m.sql(ctx, r, admin, "CREATE DATABASE "+q+"; REVOKE ALL ON DATABASE "+q+" FROM PUBLIC;")
			case "mysql":
				_, e = m.sql(ctx, r, "", "CREATE DATABASE "+q+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;")
			case "mongodb":
				_, e = m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").createCollection('clofix_metadata');")
			case "redis":
				_, e = m.redis(ctx, r, "PING")
			}
		}
		if e != nil {
			return nil, e
		}
		result["database"] = op.Database
	case "delete-database":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		q, _ := db.Quote(engine, op.Database)
		var e error
		switch engine {
		case "postgresql":
			_, e = m.sql(ctx, r, admin, "DROP DATABASE "+q+" WITH (FORCE);")
		case "mysql":
			_, e = m.sql(ctx, r, "", "DROP DATABASE "+q+";")
		case "mongodb":
			_, e = m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").dropDatabase();")
		case "redis":
			_, e = m.redis(ctx, r, "PING")
		}
		if e != nil {
			return nil, e
		}
	case "rename-database":
		if engine != "postgresql" {
			return nil, fmt.Errorf("database rename is supported only by PostgreSQL")
		}
		a, e := db.Quote(engine, op.Database)
		if e != nil {
			return nil, e
		}
		b, e := db.Quote(engine, op.NewName)
		if e != nil {
			return nil, e
		}
		if _, e = m.sql(ctx, r, admin, "ALTER DATABASE "+a+" RENAME TO "+b+";"); e != nil {
			return nil, e
		}
	case "list-tables", "inspect-schema":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		if engine == "redis" {
			return nil, fmt.Errorf("Redis uses a shared keyspace; SQL schema operations are not supported")
		}
		if engine == "mongodb" {
			raw, e := m.mongo(ctx, r, "print(JSON.stringify(db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollectionInfos().map(x=>({name:x.name,type:x.type}))))")
			if e != nil {
				return nil, e
			}
			var value any
			if json.Unmarshal([]byte(raw), &value) != nil {
				return nil, fmt.Errorf("invalid collection inventory")
			}
			result["tables"] = value
		} else {
			query := "SELECT table_name FROM information_schema.tables WHERE table_schema=" + db.Literal(op.Database) + " ORDER BY table_name;"
			if engine == "postgresql" {
				query = "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename;"
			}
			raw, e := m.sql(ctx, r, op.Database, query)
			if e != nil {
				return nil, e
			}
			tables := []string{}
			for _, name := range strings.Split(raw, "\n") {
				if name != "" {
					tables = append(tables, name)
				}
			}
			result["tables"] = tables
		}
		if op.Action == "inspect-schema" {
			tables := []string{}
			switch value := result["tables"].(type) {
			case []string:
				tables = value
			case []any:
				for _, item := range value {
					switch row := item.(type) {
					case string:
						if row != "" {
							tables = append(tables, row)
						}
					case map[string]any:
						if name, ok := row["name"].(string); ok && name != "" {
							tables = append(tables, name)
						}
					}
				}
			}
			target := strings.TrimSpace(op.Table)
			if target == "" && len(tables) > 0 {
				target = tables[0]
			}
			if target != "" {
				described, e := m.describeTable(ctx, r, db.Operation{Database: op.Database, Table: target})
				if e != nil {
					return nil, e
				}
				var detail map[string]any
				if json.Unmarshal(described, &detail) != nil {
					return nil, fmt.Errorf("invalid table description")
				}
				for key, value := range detail {
					result[key] = value
				}
				result["table"] = target
			}
		}
	case "create-table":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		if engine == "redis" {
			return nil, fmt.Errorf("Redis uses a shared keyspace; SQL schema operations are not supported")
		}
		if engine == "mongodb" {
			if e := db.Name(op.Table); e != nil {
				return nil, e
			}
			if _, e := m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").createCollection("+db.MongoJSON(op.Table)+");"); e != nil {
				return nil, e
			}
		} else {
			ddl, e := db.TableDDL(engine, op.Table, op.Columns)
			if e != nil {
				return nil, e
			}
			if _, e = m.sql(ctx, r, op.Database, ddl+";"); e != nil {
				return nil, e
			}
		}
	case "delete-table":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		if e := db.Name(op.Table); e != nil {
			return nil, e
		}
		if engine == "redis" {
			return nil, fmt.Errorf("Redis uses a shared keyspace; SQL schema operations are not supported")
		}
		if engine == "mongodb" {
			if _, e := m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").drop();"); e != nil {
				return nil, e
			}
		} else {
			q, _ := db.Quote(engine, op.Table)
			if _, e := m.sql(ctx, r, op.Database, "DROP TABLE "+q+";"); e != nil {
				return nil, e
			}
		}
	case "create-index":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		if op.Index == nil {
			return nil, fmt.Errorf("index definition required")
		}
		if engine == "redis" {
			return nil, fmt.Errorf("Redis uses a shared keyspace; SQL schema operations are not supported")
		}
		if engine == "mongodb" {
			if e := db.Name(op.Table); e != nil {
				return nil, e
			}
			if e := db.Name(op.Index.Name); e != nil {
				return nil, e
			}
			keys := []string{}
			seen := map[string]bool{}
			if len(op.Index.Columns) < 1 || len(op.Index.Columns) > 16 {
				return nil, fmt.Errorf("invalid index columns")
			}
			for _, col := range op.Index.Columns {
				if e := db.Name(col); e != nil {
					return nil, e
				}
				if seen[col] {
					return nil, fmt.Errorf("duplicate index column")
				}
				seen[col] = true
				keys = append(keys, db.MongoJSON(col)+":1")
			}
			if _, e := m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").createIndex("+"{"+strings.Join(keys, ",")+"}"+","+db.MongoJSON(map[string]any{"name": op.Index.Name, "unique": op.Index.Unique})+");"); e != nil {
				return nil, e
			}
		} else {
			ddl, e := db.IndexDDL(engine, op.Table, *op.Index)
			if e != nil {
				return nil, e
			}
			if _, e = m.sql(ctx, r, op.Database, ddl+";"); e != nil {
				return nil, e
			}
		}
	case "delete-index":
		if op.Index == nil {
			return nil, fmt.Errorf("index required")
		}
		if engine == "redis" {
			return nil, fmt.Errorf("Redis uses a shared keyspace; SQL schema operations are not supported")
		}
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		name, e := db.Quote(engine, op.Index.Name)
		if e != nil {
			return nil, e
		}
		if engine == "mongodb" {
			if e := db.Name(op.Table); e != nil {
				return nil, e
			}
			if _, e = m.mongo(ctx, r, "db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").dropIndex("+db.MongoJSON(op.Index.Name)+");"); e != nil {
				return nil, e
			}
		} else {
			ddl := "DROP INDEX " + name
			if engine == "mysql" {
				table, e := db.Quote(engine, op.Table)
				if e != nil {
					return nil, e
				}
				ddl += " ON " + table
			}
			if _, e = m.sql(ctx, r, op.Database, ddl+";"); e != nil {
				return nil, e
			}
		}
	case "add-column", "update-column", "delete-column":
		if e := db.Name(op.Database); e != nil {
			return nil, e
		}
		if e := db.Name(op.Table); e != nil {
			return nil, e
		}
		if engine == "redis" || engine == "mongodb" {
			return nil, fmt.Errorf("column structure changes require MySQL or PostgreSQL")
		}
		if len(op.Columns) != 1 {
			return nil, fmt.Errorf("provide exactly one column")
		}
		col := op.Columns[0]
		var ddl string
		var e error
		switch op.Action {
		case "add-column":
			ddl, e = db.AddColumnDDL(engine, op.Table, col)
		case "update-column":
			ddl, e = db.AlterColumnDDL(engine, op.Table, col, op.NewName)
		default:
			ddl, e = db.DropColumnDDL(engine, op.Table, col.Name)
		}
		if e != nil {
			return nil, e
		}
		if _, e = m.sql(ctx, r, op.Database, ddl+";"); e != nil {
			return nil, e
		}
	case "describe-table":
		return m.describeTable(ctx, r, op)
	case "select-rows":
		return m.selectRows(ctx, r, op)
	case "execute-sql":
		return m.executeSQL(ctx, r, op)
	case "insert-row", "update-row", "delete-row":
		return m.mutateRow(ctx, r, op)
	case "import-sql":
		return m.importSQL(ctx, r, op)
	case "create-user", "update-user", "delete-user":
		return m.userOperation(ctx, r, op)
	default:
		return nil, fmt.Errorf("unsupported database operation")
	}
	raw, e := json.Marshal(result)
	return raw, e
}

func (m *Manager) describeTable(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	if e := db.Name(op.Table); e != nil {
		return nil, e
	}
	engine := r.Spec.Engine
	out := map[string]any{"database": op.Database, "table": op.Table}
	switch engine {
	case "mysql":
		colsRaw, e := m.sql(ctx, r, op.Database, "SELECT column_name, column_type, is_nullable, column_key, column_default, extra FROM information_schema.columns WHERE table_schema="+db.Literal(op.Database)+" AND table_name="+db.Literal(op.Table)+" ORDER BY ordinal_position;")
		if e != nil {
			return nil, e
		}
		indexesRaw, e := m.sql(ctx, r, op.Database, "SELECT index_name, non_unique, seq_in_index, column_name FROM information_schema.statistics WHERE table_schema="+db.Literal(op.Database)+" AND table_name="+db.Literal(op.Table)+" ORDER BY index_name, seq_in_index;")
		if e != nil {
			return nil, e
		}
		out["columns"] = parseTSVObjects(colsRaw, []string{"name", "type", "nullable", "key", "default", "extra"})
		out["indexes"] = parseTSVObjects(indexesRaw, []string{"name", "non_unique", "seq", "column"})
	case "postgresql":
		colsRaw, e := m.sql(ctx, r, op.Database, `SELECT c.column_name, c.data_type, c.is_nullable,
CASE WHEN pk.attname IS NOT NULL THEN 'PRI' ELSE '' END AS column_key,
c.column_default, '' AS extra
FROM information_schema.columns c
LEFT JOIN (
  SELECT a.attname
  FROM pg_index i
  JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=ANY(i.indkey)
  JOIN pg_class cl ON cl.oid=i.indrelid
  JOIN pg_namespace n ON n.oid=cl.relnamespace
  WHERE i.indisprimary AND n.nspname='public' AND cl.relname=`+db.Literal(op.Table)+`
) pk ON pk.attname=c.column_name
WHERE c.table_schema='public' AND c.table_name=`+db.Literal(op.Table)+` ORDER BY c.ordinal_position;`)
		if e != nil {
			return nil, e
		}
		indexesRaw, e := m.sql(ctx, r, op.Database, "SELECT indexname, indexdef FROM pg_indexes WHERE schemaname='public' AND tablename="+db.Literal(op.Table)+" ORDER BY indexname;")
		if e != nil {
			return nil, e
		}
		out["columns"] = parseTSVObjects(colsRaw, []string{"name", "type", "nullable", "key", "default", "extra"})
		out["indexes"] = parseTSVObjects(indexesRaw, []string{"name", "definition"})
	case "mongodb":
		raw, e := m.mongo(ctx, r, "print(JSON.stringify({indexes:db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").getIndexes(),stats:db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").stats()}))")
		if e != nil {
			return nil, e
		}
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			return nil, fmt.Errorf("invalid collection description")
		}
		out["mongodb"] = value
	default:
		return nil, fmt.Errorf("describe-table is not supported for %s", engine)
	}
	return json.Marshal(out)
}

func (m *Manager) selectRows(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	if e := db.Name(op.Table); e != nil {
		return nil, e
	}
	limit := db.ClampExplorerLimit(op.Limit)
	offset := db.ClampExplorerOffset(op.Offset)
	engine := r.Spec.Engine
	out := map[string]any{"database": op.Database, "table": op.Table, "limit": limit, "offset": offset}
	switch engine {
	case "mysql", "postgresql":
		table, e := db.Quote(engine, op.Table)
		if e != nil {
			return nil, e
		}
		query := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d;", table, limit, offset)
		columns, rows, e := m.sqlTable(ctx, r, op.Database, query)
		if e != nil {
			return nil, e
		}
		out["columns"] = columns
		out["rows"] = rows
	case "mongodb":
		raw, e := m.mongo(ctx, r, "print(JSON.stringify(db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollection("+db.MongoJSON(op.Table)+").find().skip("+fmt.Sprintf("%d", offset)+").limit("+fmt.Sprintf("%d", limit)+").toArray()))")
		if e != nil {
			return nil, e
		}
		var docs any
		if json.Unmarshal([]byte(raw), &docs) != nil {
			return nil, fmt.Errorf("invalid collection rows")
		}
		out["documents"] = docs
	default:
		return nil, fmt.Errorf("select-rows is not supported for %s", engine)
	}
	return json.Marshal(out)
}

func (m *Manager) executeSQL(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	engine := r.Spec.Engine
	if engine == "mongodb" {
		script := strings.TrimSpace(op.Query)
		if script == "" || len(script) > db.MaxExplorerQueryBytes || strings.ContainsAny(script, "\x00") {
			return nil, fmt.Errorf("MongoDB script required")
		}
		blocked := []string{"dropDatabase", "shutdownServer", "fsyncLock", "addUser", "createUser", "dropUser", "grantRoles", "updateUser"}
		lower := strings.ToLower(script)
		for _, item := range blocked {
			if strings.Contains(lower, strings.ToLower(item)) {
				return nil, fmt.Errorf("MongoDB script contains a blocked operation")
			}
		}
		raw, e := m.mongo(ctx, r, "const __db=db.getSiblingDB("+db.MongoJSON(op.Database)+"); const db=__db; const result=(function(){"+script+"\n})(); print(JSON.stringify(result===undefined?{ok:true}:result));")
		if e != nil {
			return nil, e
		}
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			value = map[string]any{"output": raw}
		}
		return json.Marshal(map[string]any{"database": op.Database, "engine": engine, "result": value})
	}
	if engine != "mysql" && engine != "postgresql" {
		return nil, fmt.Errorf("execute-sql is not supported for %s", engine)
	}
	kind, e := db.ClassifySQL(op.Query)
	if e != nil {
		return nil, e
	}
	statement := strings.TrimSpace(op.Query)
	if !strings.HasSuffix(strings.TrimRightFunc(statement, unicode.IsSpace), ";") {
		statement += ";"
	}
	upper := strings.ToUpper(strings.TrimSpace(statement))
	isQuery := kind == "read" && (strings.HasPrefix(firstWord(upper), "SELECT") || strings.HasPrefix(firstWord(upper), "WITH") || strings.HasPrefix(firstWord(upper), "SHOW") || strings.HasPrefix(firstWord(upper), "DESCRIBE") || strings.HasPrefix(firstWord(upper), "DESC") || strings.HasPrefix(firstWord(upper), "EXPLAIN") || strings.HasPrefix(firstWord(upper), "TABLE") || strings.HasPrefix(firstWord(upper), "VALUES"))
	if isQuery {
		columns, rows, e := m.sqlTable(ctx, r, op.Database, statement)
		if e != nil {
			return nil, e
		}
		return json.Marshal(map[string]any{"database": op.Database, "engine": engine, "kind": kind, "columns": columns, "rows": rows})
	}
	if _, e = m.sql(ctx, r, op.Database, statement); e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"database": op.Database, "engine": engine, "kind": kind, "ok": true})
}

func firstWord(upper string) string {
	fields := strings.Fields(upper)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func parseTSVObjects(raw string, keys []string) []map[string]string {
	out := []map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		row := map[string]string{}
		for i, key := range keys {
			if i < len(parts) {
				row[key] = parts[i]
			} else {
				row[key] = ""
			}
		}
		out = append(out, row)
	}
	return out
}

func (m *Manager) sqlTable(ctx context.Context, r record, database, statement string) ([]string, [][]string, error) {
	args := []string{"exec", "-i", "--user", "0:0"}
	if r.Spec.Engine == "postgresql" {
		args = append(args, "-e", "PGPASSFILE=/run/clofix/pgpass", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(r.Spec.ID), "psql", "-X", "--no-password", "--set=ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", "postgres", "-d", database, "-A", "-F", "\t", "--pset=footer=off")
		statement = "SET standard_conforming_strings=on;\n" + statement
	} else {
		args = append(args, container(r.Spec.ID), "mysql", "--defaults-extra-file=/run/clofix/mysql.cnf", "--batch", "--binary-mode=1", "--local-infile=0")
		if database != "" {
			args = append(args, "--database="+database)
		}
		statement = "SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';\n" + statement
	}
	rawBytes, e := m.docker(ctx, strings.NewReader(statement), args...)
	if e != nil {
		return nil, nil, e
	}
	raw := strings.TrimRight(string(rawBytes), "\n")
	if raw == "" {
		return []string{}, [][]string{}, nil
	}
	lines := strings.Split(raw, "\n")
	columns := strings.Split(lines[0], "\t")
	rows := [][]string{}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return columns, rows, nil
}
func (m *Manager) userOperation(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Username); e != nil {
		return nil, e
	}
	if strings.EqualFold(op.Username, "root") || strings.EqualFold(op.Username, "clofixadmin") || strings.EqualFold(op.Username, "default") || strings.HasPrefix(strings.ToLower(op.Username), "pg_") {
		return nil, fmt.Errorf("reserved administrative username")
	}
	if op.Action != "delete-user" && (len(op.Password) > 0 && (len(op.Password) < 16 || len(op.Password) > 128 || strings.ContainsAny(op.Password, "\x00\r\n"))) {
		return nil, fmt.Errorf("password must have 16–128 characters without line breaks")
	}
	if op.Action == "create-user" && op.Password == "" {
		return nil, fmt.Errorf("password required")
	}
	names, e := m.listDatabases(ctx, r)
	if e != nil {
		return nil, e
	}
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	for name, role := range op.Grants {
		if !known[name] || role != "read" && role != "write" {
			return nil, fmt.Errorf("grants must reference an existing project database and read/write permission")
		}
	}
	if r.Spec.Engine == "redis" {
		if op.Action == "delete-user" {
			if _, e = m.redis(ctx, r, "ACL", "DELUSER", op.Username); e != nil {
				return nil, e
			}
			return json.RawMessage(`{"updated":true}`), nil
		}
		args := []string{"ACL", "SETUSER", op.Username}
		if op.Action == "create-user" {
			args = append(args, "on", ">"+op.Password)
		} else {
			args = append(args, "resetkeys", "resetchannels", "nocommands", "on")
			if op.Password != "" {
				args = append(args, ">"+op.Password)
			}
		}
		write := false
		for name, role := range op.Grants {
			args = append(args, "~"+name+":*")
			if role == "write" {
				write = true
			}
		}
		args = append(args, "&*", "-@all", "+@read", "+ping", "+info", "+select", "+echo")
		if write {
			args = append(args, "+@write")
		}
		if _, e = m.redis(ctx, r, args...); e != nil {
			return nil, e
		}
		return json.RawMessage(`{"updated":true}`), nil
	}
	if r.Spec.Engine == "mongodb" {
		roles := []map[string]string{}
		for name, role := range op.Grants {
			mongoRole := "read"
			if role == "write" {
				mongoRole = "readWrite"
			}
			roles = append(roles, map[string]string{"db": name, "role": mongoRole})
		}
		command := ""
		switch op.Action {
		case "create-user":
			command = "db.getSiblingDB('admin').createUser(" + db.MongoJSON(map[string]any{"user": op.Username, "pwd": op.Password, "roles": roles}) + ");"
		case "update-user":
			value := map[string]any{"roles": roles}
			if op.Password != "" {
				value["pwd"] = op.Password
			}
			command = "db.getSiblingDB('admin').updateUser(" + db.MongoJSON(op.Username) + "," + db.MongoJSON(value) + ");"
		case "delete-user":
			command = "db.getSiblingDB('admin').dropUser(" + db.MongoJSON(op.Username) + ");"
		}
		if _, e = m.mongo(ctx, r, command); e != nil {
			return nil, e
		}
		return json.RawMessage(`{"updated":true}`), nil
	}
	user, _ := db.Quote(r.Spec.Engine, op.Username)
	if r.Spec.Engine == "mysql" {
		account := db.Literal(op.Username) + "@'%'"
		statement := ""
		switch op.Action {
		case "create-user":
			statement = "CREATE USER " + account + " IDENTIFIED BY " + db.Literal(op.Password) + " REQUIRE SSL;"
		case "update-user":
			if op.Password != "" {
				statement = "ALTER USER " + account + " IDENTIFIED BY " + db.Literal(op.Password) + " REQUIRE SSL;"
			}
		case "delete-user":
			_, e = m.sql(ctx, r, "", "DROP USER "+account+";")
			return nil, e
		}
		if op.Action == "update-user" {
			statement += "REVOKE ALL PRIVILEGES, GRANT OPTION FROM " + account + ";"
		}
		for name, role := range op.Grants {
			q, _ := db.Quote("mysql", name)
			privileges := "SELECT,SHOW VIEW"
			if role == "write" {
				privileges = "SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,REFERENCES,CREATE VIEW,SHOW VIEW,TRIGGER,LOCK TABLES"
			}
			statement += "GRANT " + privileges + " ON " + q + ".* TO " + account + ";"
		}
		_, e = m.sql(ctx, r, "", statement)
		return nil, e
	}
	// PostgreSQL roles never receive server administration privileges.
	if op.Action == "create-user" {
		if _, e = m.sql(ctx, r, "postgres", "CREATE ROLE "+user+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD "+db.Literal(op.Password)+";"); e != nil {
			return nil, e
		}
	} else if op.Password != "" {
		if _, e = m.sql(ctx, r, "postgres", "ALTER ROLE "+user+" PASSWORD "+db.Literal(op.Password)+";"); e != nil {
			return nil, e
		}
	}
	for _, name := range names {
		q, _ := db.Quote("postgresql", name)
		if _, e = m.sql(ctx, r, "postgres", "REVOKE ALL ON DATABASE "+q+" FROM "+user+";"); e != nil {
			return nil, e
		}
		if _, e = m.sql(ctx, r, name, "REASSIGN OWNED BY "+user+" TO postgres; REVOKE ALL ON ALL TABLES IN SCHEMA public FROM "+user+"; REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM "+user+"; REVOKE ALL ON SCHEMA public FROM "+user+"; ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM "+user+"; ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM "+user+";"); e != nil {
			return nil, e
		}
		if op.Action == "delete-user" {
			if _, e = m.sql(ctx, r, name, "REASSIGN OWNED BY "+user+" TO postgres;"); e != nil {
				return nil, e
			}
			continue
		}
		role, ok := op.Grants[name]
		if !ok {
			continue
		}
		if _, e = m.sql(ctx, r, "postgres", "GRANT CONNECT ON DATABASE "+q+" TO "+user+";"); e != nil {
			return nil, e
		}
		statement := "GRANT USAGE ON SCHEMA public TO " + user + "; GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + user + "; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO " + user + ";"
		if role == "write" {
			statement = "GRANT USAGE,CREATE ON SCHEMA public TO " + user + "; GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON ALL TABLES IN SCHEMA public TO " + user + "; GRANT USAGE,SELECT,UPDATE ON ALL SEQUENCES IN SCHEMA public TO " + user + "; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON TABLES TO " + user + "; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE,SELECT,UPDATE ON SEQUENCES TO " + user + ";"
		}
		if _, e = m.sql(ctx, r, name, statement); e != nil {
			return nil, e
		}
	}
	if op.Action == "delete-user" {
		_, e = m.sql(ctx, r, "postgres", "DROP ROLE "+user+";")
	}
	return nil, e
}
