//go:build linux

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func (m *Manager) mutateRow(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	if e := db.Name(op.Table); e != nil {
		return nil, e
	}
	if e := db.ValidateRowMaps(op.Values, op.Keys); e != nil {
		return nil, e
	}
	engine := r.Spec.Engine
	switch engine {
	case "mysql", "postgresql":
		statement, e := buildRowSQL(engine, op)
		if e != nil {
			return nil, e
		}
		if _, e = m.sql(ctx, r, op.Database, statement); e != nil {
			return nil, e
		}
		return json.Marshal(map[string]any{"ok": true, "database": op.Database, "table": op.Table, "action": op.Action})
	case "mongodb":
		return m.mutateMongoRow(ctx, r, op)
	default:
		return nil, fmt.Errorf("%s is not supported for row edits", engine)
	}
}

func buildRowSQL(engine string, op db.Operation) (string, error) {
	table, e := db.Quote(engine, op.Table)
	if e != nil {
		return "", e
	}
	switch op.Action {
	case "insert-row":
		if len(op.Values) == 0 {
			return "", fmt.Errorf("provide at least one column value")
		}
		cols, vals := []string{}, []string{}
		for name, value := range op.Values {
			q, e := db.Quote(engine, name)
			if e != nil {
				return "", e
			}
			cols = append(cols, q)
			vals = append(vals, db.SQLValue(value))
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", table, strings.Join(cols, ","), strings.Join(vals, ",")), nil
	case "update-row":
		if len(op.Values) == 0 {
			return "", fmt.Errorf("provide values to update")
		}
		if len(op.Keys) == 0 {
			return "", fmt.Errorf("primary key values are required to update a row")
		}
		sets, where := []string{}, []string{}
		for name, value := range op.Values {
			q, e := db.Quote(engine, name)
			if e != nil {
				return "", e
			}
			sets = append(sets, q+"="+db.SQLValue(value))
		}
		for name, value := range op.Keys {
			q, e := db.Quote(engine, name)
			if e != nil {
				return "", e
			}
			where = append(where, q+"="+db.Literal(value))
		}
		return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", table, strings.Join(sets, ","), strings.Join(where, " AND ")), nil
	case "delete-row":
		if len(op.Keys) == 0 {
			return "", fmt.Errorf("primary key values are required to delete a row")
		}
		where := []string{}
		for name, value := range op.Keys {
			q, e := db.Quote(engine, name)
			if e != nil {
				return "", e
			}
			where = append(where, q+"="+db.Literal(value))
		}
		return fmt.Sprintf("DELETE FROM %s WHERE %s;", table, strings.Join(where, " AND ")), nil
	default:
		return "", fmt.Errorf("unsupported row action")
	}
}

func (m *Manager) mutateMongoRow(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	dbName := db.MongoJSON(op.Database)
	coll := db.MongoJSON(op.Table)
	switch op.Action {
	case "insert-row":
		doc := strings.TrimSpace(op.Query)
		if doc == "" && op.Values != nil {
			if raw, ok := op.Values["document"]; ok && raw != nil {
				doc = strings.TrimSpace(*raw)
			}
		}
		if doc == "" || len(doc) > db.MaxRowValueBytes {
			return nil, fmt.Errorf("MongoDB document JSON required")
		}
		if !json.Valid([]byte(doc)) {
			return nil, fmt.Errorf("document must be valid JSON")
		}
		_, e := m.mongo(ctx, r, "const res=db.getSiblingDB("+dbName+").getCollection("+coll+").insertOne("+doc+"); print(JSON.stringify({ok:1,insertedId:res.insertedId}));")
		if e != nil {
			return nil, e
		}
	case "update-row":
		id := strings.TrimSpace(op.Keys["_id"])
		doc := strings.TrimSpace(op.Query)
		if doc == "" && op.Values != nil {
			if raw, ok := op.Values["document"]; ok && raw != nil {
				doc = strings.TrimSpace(*raw)
			}
		}
		if id == "" || doc == "" || !json.Valid([]byte(doc)) {
			return nil, fmt.Errorf("MongoDB update requires _id and JSON document")
		}
		_, e := m.mongo(ctx, r, "const res=db.getSiblingDB("+dbName+").getCollection("+coll+").updateOne({_id:ObjectId("+db.MongoJSON(id)+")},{$set:"+doc+"}); if(!res.matchedCount){const res2=db.getSiblingDB("+dbName+").getCollection("+coll+").updateOne({_id:"+db.MongoJSON(id)+"},{$set:"+doc+"}); print(JSON.stringify(res2));} else {print(JSON.stringify(res));}")
		if e != nil {
			return nil, e
		}
	case "delete-row":
		id := strings.TrimSpace(op.Keys["_id"])
		if id == "" {
			return nil, fmt.Errorf("MongoDB delete requires _id")
		}
		_, e := m.mongo(ctx, r, "let res=db.getSiblingDB("+dbName+").getCollection("+coll+").deleteOne({_id:ObjectId("+db.MongoJSON(id)+")}); if(!res.deletedCount){res=db.getSiblingDB("+dbName+").getCollection("+coll+").deleteOne({_id:"+db.MongoJSON(id)+"});} print(JSON.stringify(res));")
		if e != nil {
			return nil, e
		}
	default:
		return nil, fmt.Errorf("unsupported MongoDB row action")
	}
	return json.Marshal(map[string]any{"ok": true, "database": op.Database, "table": op.Table, "action": op.Action})
}

func (m *Manager) importSQL(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	engine := r.Spec.Engine
	if engine != "mysql" && engine != "postgresql" {
		return nil, fmt.Errorf("SQL import is only supported for MySQL and PostgreSQL")
	}
	if e := db.ValidateImportSQL(op.Query); e != nil {
		return nil, e
	}
	script := strings.TrimSpace(op.Query)
	if !strings.HasSuffix(strings.TrimRight(script, " \t\r\n"), ";") {
		script += ";"
	}
	if _, e := m.sql(ctx, r, op.Database, script); e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"ok": true, "database": op.Database, "bytes": len(op.Query)})
}
