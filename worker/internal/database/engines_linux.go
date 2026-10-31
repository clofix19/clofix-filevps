//go:build linux

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	case "list-tables":
		if e := db.Name(op.Database); e != nil {
			return nil, e
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
	case "create-table":
		if e := db.Name(op.Database); e != nil {
			return nil, e
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
	case "create-user", "update-user", "delete-user":
		return m.userOperation(ctx, r, op)
	default:
		return nil, fmt.Errorf("unsupported database operation")
	}
	raw, e := json.Marshal(result)
	return raw, e
}
func (m *Manager) userOperation(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Username); e != nil {
		return nil, e
	}
	if strings.EqualFold(op.Username, "root") || strings.EqualFold(op.Username, "clofixadmin") || strings.HasPrefix(strings.ToLower(op.Username), "pg_") {
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
