//go:build linux

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func TestMissingCredentialKeyFailsClosed(t *testing.T) {
	root := t.TempDir()
	m, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.save(); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(m.Root, "credentials.key")); e != nil {
		t.Fatal(e)
	}
	if _, e = New(root); e == nil {
		t.Fatal("missing credential key was silently replaced")
	}
}
func TestJournalInterruptedOnRestart(t *testing.T) {
	root := t.TempDir()
	m, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	m.results["dbj-"+strings.Repeat("a", 32)] = db.Result{State: "RUNNING"}
	if e = m.journal(); e != nil {
		t.Fatal(e)
	}
	restarted, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	if restarted.Results()[0].State != "INTERRUPTED" {
		t.Fatal("interrupted work was not fenced")
	}
}
func TestPostgresSQLRejectsMetaCommands(t *testing.T) {
	for _, value := range []string{"\\! id", "SELECT 1;\\gexec", "\\copy foo FROM PROGRAM 'id'", "\\i /etc/passwd", "SELECT '\\x';"} {
		if e := filterPostgresSQL(io.Discard, strings.NewReader(value), 1024); e == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if e := filterPostgresSQL(io.Discard, strings.NewReader("\\restrict token\nSELECT 1;\n\\unrestrict token\n"), 1024); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresArchiveRejectsExecutableObjects(t *testing.T) {
	allowed := "; Archive created at 2026\n1; 0 0 TABLE public items postgres\n2; 0 0 TABLE DATA public items postgres\n3; 0 0 INDEX public items_pkey postgres\n"
	if err := validatePostgresTOC(allowed); err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"FUNCTION public dangerous() postgres", "PROCEDURE public dangerous postgres", "TRIGGER public run_shell postgres", "EXTENSION - plpython3u postgres", "ACL public items postgres"} {
		if err := validatePostgresTOC("1; 0 0 " + object + "\n"); err == nil {
			t.Fatalf("accepted %s", object)
		}
	}
}

// Run only in the dedicated Docker-in-Docker environment documented in docs/managed-databases.md.
func TestRealDatabaseEngines(t *testing.T) {
	if os.Getenv("CLOFIX_DATABASE_INTEGRATION") != "1" {
		t.Skip("requires isolated database integration daemon")
	}
	for engineIndex, engine := range []struct{ name, image, version string }{{"mysql", "mysql:8.4", "8.4"}, {"postgresql", "postgres:17-bookworm", "17"}, {"mongodb", "mongo:8.0", "8.0"}} {
		t.Run(engine.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			root := t.TempDir()
			m, e := New(root)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.docker(ctx, nil, "pull", engine.image); e != nil {
				t.Fatal(e)
			}
			raw, e := m.docker(ctx, nil, "image", "inspect", "--format", "{{index .RepoDigests 0}}", engine.image)
			if e != nil {
				t.Fatal(e)
			}
			m.images[engine.name] = Image{Ref: strings.TrimSpace(string(raw)), Version: engine.version}
			project := db.Project{ID: fmt.Sprintf("dbp-%032x", engineIndex+1), Engine: engine.name, Version: engine.version, RAMMB: 1024, CPU: 1, DiskMB: 2048, Port: 26000 + engineIndex, Host: "127.0.0.1"}
			sequence := 0
			run := func(op db.Operation) db.Result {
				sequence++
				op.JobID = fmt.Sprintf("dbj-%032x", sequence)
				op.Project = project
				result := m.Run(ctx, op)
				if result.State != "SUCCEEDED" {
					t.Fatalf("%s failed: %s", op.Action, result.Error)
				}
				return result
			}
			defer func() {
				m.Lock()
				r, ok := m.records[project.ID]
				m.Unlock()
				if ok {
					clean, done := context.WithTimeout(context.Background(), 30*time.Second)
					defer done()
					if e := m.remove(clean, r); e != nil {
						t.Error(e)
					}
				}
			}()
			provision := run(db.Operation{Action: "provision"})
			if !bytes.Contains(provision.Data, []byte("BEGIN CERTIFICATE")) {
				t.Fatal("missing TLS CA")
			}
			for _, name := range []string{"firstdb", "seconddb"} {
				run(db.Operation{Action: "create-database", Database: name})
			}
			result := run(db.Operation{Action: "list-databases"})
			for _, name := range []string{"firstdb", "seconddb"} {
				if !bytes.Contains(result.Data, []byte(name)) {
					t.Fatalf("missing %s: %s", name, result.Data)
				}
			}
			run(db.Operation{Action: "create-user", Username: "testuser", Password: strings.Repeat("7", 32), Grants: map[string]string{"firstdb": "write", "seconddb": "read"}})
			run(db.Operation{Action: "create-table", Database: "firstdb", Table: "items", Columns: []db.Column{{Name: "id", Type: "INTEGER", Primary: true}}})
			m.Lock()
			r := m.records[project.ID]
			m.Unlock()
			status := db.Status{}
			if e = m.sample(ctx, r, &status); e != nil || status.RAMBytes == 0 || status.DiskBytes == 0 || status.Connections < 1 {
				t.Fatalf("native resource sample failed: %+v %v", status, e)
			}
			if engine.name == "mongodb" {
				_, e = m.mongo(ctx, r, `db.getSiblingDB('firstdb').items.insertOne({id:42})`)
			} else {
				_, e = m.sql(ctx, r, "firstdb", "INSERT INTO items(id) VALUES(42);")
			}
			if e != nil {
				t.Fatal(e)
			}
			assertPermissions := func(expectFirst bool) {
				t.Helper()
				private := filepath.Join("/dev/shm/clofix-database", project.ID, "probe")
				var probe func(string, bool) error
				switch engine.name {
				case "mysql":
					value := "[client]\nuser=testuser\npassword=" + strings.Repeat("7", 32) + "\nhost=127.0.0.1\nssl-mode=VERIFY_CA\nssl-ca=/run/clofix/ca.pem\n"
					if e = os.WriteFile(private, []byte(value), 0600); e != nil {
						t.Fatal(e)
					}
					probe = func(database string, write bool) error {
						statement := "SHOW TABLES;"
						if write {
							statement = "CREATE TABLE forbidden(id INTEGER);"
						}
						_, err := m.docker(ctx, strings.NewReader(statement), "exec", "-i", "--user", "65533:65533", container(project.ID), "mysql", "--defaults-extra-file=/run/clofix/probe", "--database="+database, "--batch")
						return err
					}
				case "postgresql":
					if e = os.WriteFile(private, []byte("127.0.0.1:5432:*:testuser:"+strings.Repeat("7", 32)+"\n"), 0600); e != nil {
						t.Fatal(e)
					}
					probe = func(database string, write bool) error {
						statement := "SELECT 1;"
						if write {
							statement = "CREATE TABLE forbidden(id INTEGER);"
						}
						_, err := m.docker(ctx, strings.NewReader(statement), "exec", "-i", "--user", "65533:65533", "-e", "PGPASSFILE=/run/clofix/probe", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(project.ID), "psql", "-X", "--no-password", "--set=ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", "testuser", "-d", database)
						return err
					}
				default:
					probe = func(database string, write bool) error {
						statement := "db.getSiblingDB(" + db.MongoJSON(database) + ").getCollectionInfos()"
						if write {
							statement = "db.getSiblingDB(" + db.MongoJSON(database) + ").forbidden.insertOne({id:1})"
						}
						_, err := m.docker(ctx, strings.NewReader("db.getSiblingDB('admin').auth('testuser',"+db.MongoJSON(strings.Repeat("7", 32))+");"+statement), "exec", "-i", "--user", "65533:65533", "-e", "HOME=/tmp/clofix-client", container(project.ID), "mongosh", "--quiet", "--norc", "--file", "/dev/stdin", "--host", "127.0.0.1", "--tls", "--tlsCAFile", "/run/clofix/ca.pem")
						return err
					}
				}
				if engine.name != "mongodb" {
					if e = os.Chown(private, 65533, 65533); e != nil {
						t.Fatal(e)
					}
					defer os.Remove(private)
				}
				if e = probe("firstdb", false); (e == nil) != expectFirst {
					t.Fatalf("first database permission mismatch: %v", e)
				}
				if e = probe("seconddb", false); e != nil {
					t.Fatal("read permission denied", e)
				}
				if e = probe("seconddb", true); e == nil {
					t.Fatal("read-only user gained schema write access")
				}
			}
			assertPermissions(true)
			run(db.Operation{Action: "create-index", Database: "firstdb", Table: "items", Index: &db.Index{Name: "items_id", Columns: []string{"id"}}})
			key := bytes.Repeat([]byte{11}, 32)
			backupID := "dbb-" + strings.Repeat("b", 32)
			result = run(db.Operation{Action: "backup", Database: "firstdb", Backup: &db.BackupSpec{ID: backupID, Key: key, LimitBytes: 64 << 20}})
			var meta struct {
				SHA256 string `json:"sha256"`
				Format string `json:"format"`
			}
			if e = json.Unmarshal(result.Data, &meta); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(m.path(project.ID), "backups", backupID+".enc")
			encrypted, e := os.Open(file)
			if e != nil {
				t.Fatal(e)
			}
			var plain bytes.Buffer
			_, e = db.Decrypt(&plain, encrypted, key, backupID, 64<<20)
			encrypted.Close()
			if e != nil || plain.Len() == 0 {
				t.Fatal("backup integrity", e)
			}
			m.Transfer = func(_ context.Context, method, path string, _ io.Reader) (io.ReadCloser, error) {
				if method != "GET" || path != "/test-backup" {
					return nil, fmt.Errorf("unexpected transfer")
				}
				return os.Open(file)
			}
			run(db.Operation{Action: "restore", Database: "restoreddb", Restore: &db.RestoreSpec{BackupID: backupID, Key: key, SHA256: meta.SHA256, Format: meta.Format, SourceDatabase: "firstdb", EngineVersion: engine.version, SourceURL: "/test-backup", LimitBytes: 64 << 20, ConfirmOverwrite: true}})
			result = run(db.Operation{Action: "list-tables", Database: "restoreddb"})
			if !bytes.Contains(result.Data, []byte("items")) {
				t.Fatal("restored table is missing")
			}
			sequence++
			rejected := m.Run(ctx, db.Operation{JobID: fmt.Sprintf("dbj-%032x", sequence), Project: project, Action: "restore", Database: "firstdb", Restore: &db.RestoreSpec{BackupID: backupID, Key: key, SHA256: meta.SHA256, Format: meta.Format, SourceDatabase: "firstdb", EngineVersion: engine.version, SourceURL: "/test-backup", LimitBytes: 64 << 20, ConfirmOverwrite: true}})
			if rejected.State != "FAILED" || !strings.Contains(rejected.Error, "safety backup") {
				t.Fatal("overwrite without destination safety backup was accepted", rejected.State, rejected.Error)
			}
			run(db.Operation{Action: "update-user", Username: "testuser", Grants: map[string]string{"seconddb": "read"}})
			assertPermissions(false)
			run(db.Operation{Action: "delete-user", Username: "testuser"})
			run(db.Operation{Action: "delete-database", Database: "restoreddb"})
		})
	}
}

func TestSystemUsersCannotBeModified(t *testing.T) {
	m, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"root", "ClofixAdmin", "postgres", "pg_read_server_files", "pg_monitor"} {
		if _, e = m.userOperation(context.Background(), record{}, db.Operation{Action: "update-user", Username: name}); e == nil {
			t.Fatalf("system user %q accepted", name)
		}
	}
}

func TestInterruptedProjectRejectsFurtherMutations(t *testing.T) {
	root := t.TempDir()
	m, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	spec := db.Project{ID: "dbp-" + strings.Repeat("c", 32), Engine: "mysql", Version: "8.4", RAMMB: 512, CPU: 1, DiskMB: 1024, Port: 26000}
	credential, e := m.seal(spec.ID, secrets{Password: "encrypted-administrator-secret"})
	if e != nil {
		t.Fatal(e)
	}
	m.records[spec.ID] = record{Spec: spec, State: "READY", Credential: credential}
	id := "dbj-" + strings.Repeat("d", 32)
	m.results[id] = db.Result{JobID: id, ProjectID: spec.ID, State: "RUNNING"}
	if e = m.save(); e != nil {
		t.Fatal(e)
	}
	if e = m.journal(); e != nil {
		t.Fatal(e)
	}
	restarted, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	if restarted.records[spec.ID].State != "NEEDS_INSPECTION" {
		t.Fatal("interrupted project was not fenced")
	}
	if _, e = restarted.apply(context.Background(), db.Operation{Project: spec, Action: "create-database", Database: "newdb"}); e == nil || !strings.Contains(e.Error(), "inspection") {
		t.Fatal("mutation was allowed on interrupted project", e)
	}
}
