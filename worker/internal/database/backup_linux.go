//go:build linux

package database

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
)

func (m *Manager) backupOperation(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	if op.Action == "restore" {
		return m.restore(ctx, r, op)
	}
	b := op.Backup
	if b == nil || !db.ID.MatchString(b.ID) || !strings.HasPrefix(b.ID, "dbb-") || b.LimitBytes < 1 || len(b.Key) != 32 {
		return nil, fmt.Errorf("invalid backup request")
	}
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	for _, name := range b.Tables {
		if e := db.Name(name); e != nil {
			return nil, e
		}
	}
	directory := filepath.Join(m.path(r.Spec.ID), "backups")
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, e
	}
	file := filepath.Join(directory, b.ID+".enc")
	if _, e := os.Stat(file); e == nil {
		return nil, fmt.Errorf("backup already exists; verify its recorded result before retrying")
	}
	output, e := os.OpenFile(file+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	committed := false
	defer func() {
		output.Close()
		if !committed {
			os.Remove(file + ".tmp")
		}
	}()
	args := []string{"exec", "-i", "--user", "0:0"}
	format := "sql.gz"
	switch r.Spec.Engine {
	case "mysql":
		unsupported, e := m.sql(ctx, r, op.Database, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema="+db.Literal(op.Database)+" AND table_type='BASE TABLE' AND engine <> 'InnoDB';")
		if e != nil {
			return nil, e
		}
		if unsupported != "0" {
			return nil, fmt.Errorf("consistent MySQL backups require InnoDB tables; convert other storage engines first")
		}
		args = append(args, container(r.Spec.ID), "mysqldump", "--defaults-extra-file=/run/clofix/mysql.cnf", "--single-transaction", "--quick", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-triggers", "--skip-add-locks", op.Database)
		args = append(args, b.Tables...)
	case "postgresql":
		format = "dump"
		args = append(args, "-e", "PGPASSFILE=/run/clofix/pgpass", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(r.Spec.ID), "pg_dump", "-h", "127.0.0.1", "-U", "postgres", "--format=custom", "--no-owner", "--no-acl", "--dbname="+op.Database)
		for _, name := range b.Tables {
			args = append(args, "--table=public."+name)
		}
	case "mongodb":
		format = "archive.gz"
		secret, e := m.secret(r)
		if e != nil {
			return nil, e
		}
		config := "password: " + db.MongoJSON(secret.Password) + "\n"
		cfg := filepath.Join("/dev/shm/clofix-database", r.Spec.ID, "dump.yml")
		if e = os.WriteFile(cfg, []byte(config), 0600); e != nil {
			return nil, e
		}
		defer os.Remove(cfg)
		if _, e = m.mongo(ctx, r, `if(db.adminCommand({fsync:1,lock:true}).ok!==1)throw Error('could not pause writes')`); e != nil {
			return nil, e
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m.mongo(cleanup, r, `db.adminCommand({fsyncUnlock:1});`)
		}()
		args = append(args, container(r.Spec.ID), "mongodump", "--config=/run/clofix/dump.yml", "--host=127.0.0.1", "--username=clofixadmin", "--authenticationDatabase=admin", "--ssl", "--sslCAFile=/run/clofix/ca.pem", "--archive", "--gzip", "--db="+op.Database)
		if len(b.Tables) > 0 {
			raw, e := m.mongo(ctx, r, "print(JSON.stringify(db.getSiblingDB("+db.MongoJSON(op.Database)+").getCollectionNames()))")
			if e != nil {
				return nil, e
			}
			var names []string
			if json.Unmarshal([]byte(raw), &names) != nil {
				return nil, fmt.Errorf("collection inventory failed")
			}
			selected := map[string]bool{}
			for _, name := range b.Tables {
				selected[name] = true
			}
			for _, name := range names {
				if !selected[name] {
					args = append(args, "--excludeCollection="+name)
				}
			}
		}
	}
	pipeReader, pipeWriter := io.Pipe()
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stderr = io.Discard
	done := make(chan error, 1)
	go func() {
		var writer io.WriteCloser = pipeWriter
		if r.Spec.Engine == "mysql" {
			writer = gzip.NewWriter(pipeWriter)
		}
		command.Stdout = writer
		e := command.Run()
		closeErr := writer.Close()
		if e == nil {
			e = closeErr
		}
		pipeWriter.CloseWithError(e)
		done <- e
	}()
	h := sha256.New()
	written, e := db.Encrypt(io.MultiWriter(output, h), pipeReader, b.Key, b.ID, b.LimitBytes)
	pipeReader.CloseWithError(e)
	commandErr := <-done
	if e != nil || commandErr != nil {
		return nil, fmt.Errorf("native backup failed or exceeded its reserved capacity")
	}
	if e = output.Sync(); e != nil {
		return nil, e
	}
	output.Close()
	if e = os.Rename(file+".tmp", file); e != nil {
		return nil, e
	}
	committed = true
	info, e := os.Stat(file)
	if e != nil {
		return nil, e
	}
	sum := hex.EncodeToString(h.Sum(nil))
	metadata, _ := json.Marshal(backupReceipt{Database: op.Database, ProjectID: r.Spec.ID, SHA256: sum, Created: time.Now().UTC(), Bytes: info.Size(), Complete: true})
	if e = os.WriteFile(file+".json.tmp", metadata, 0600); e != nil {
		return nil, e
	}
	if e = os.Rename(file+".json.tmp", file+".json"); e != nil {
		return nil, e
	}
	if b.CentralURL != "" {
		if m.Transfer == nil {
			return nil, fmt.Errorf("secure controller transfer unavailable")
		}
		source, e := os.Open(file)
		if e != nil {
			return nil, e
		}
		response, e := m.Transfer(ctx, "PUT", b.CentralURL, source)
		source.Close()
		if e != nil {
			return nil, e
		}
		response.Close()
	}
	raw, _ := json.Marshal(map[string]any{"backup_id": b.ID, "size_bytes": info.Size(), "plain_bytes": written, "sha256": sum, "format": format, "engine_version": r.Image.Version})
	return raw, nil
}
func (m *Manager) restore(ctx context.Context, r record, op db.Operation) (json.RawMessage, error) {
	restore := op.Restore
	if restore == nil || !restore.ConfirmOverwrite || !db.ID.MatchString(restore.BackupID) || len(restore.Key) != 32 || restore.LimitBytes < 1 {
		return nil, fmt.Errorf("confirmed restore and valid backup reference required")
	}
	if e := db.Name(op.Database); e != nil {
		return nil, e
	}
	if e := db.Name(restore.SourceDatabase); e != nil {
		return nil, e
	}
	if restore.EngineVersion != r.Image.Version {
		return nil, fmt.Errorf("engine version mismatch; logical upgrade requires a separately tested migration")
	}
	for _, name := range restore.Tables {
		if e := db.Name(name); e != nil {
			return nil, e
		}
	}
	names, e := m.listDatabases(ctx, r)
	if e != nil {
		return nil, e
	}
	for _, name := range names {
		if name == op.Database {
			if e = m.verifySafetyBackup(r, op.Database, restore.SafetyBackupID); e != nil {
				return nil, e
			}
		}
	}
	directory := filepath.Join(m.path(r.Spec.ID), "restore")
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, e
	}
	file := filepath.Join(directory, op.JobID+".enc")
	defer os.Remove(file)
	if m.Transfer == nil {
		return nil, fmt.Errorf("secure controller transfer unavailable")
	}
	response, e := m.Transfer(ctx, "GET", restore.SourceURL, nil)
	if e != nil {
		return nil, e
	}
	output, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		response.Close()
		return nil, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(output, h), io.LimitReader(response, restore.LimitBytes+(restore.LimitBytes/db.ChunkBytes+2)*32+17))
	response.Close()
	output.Close()
	if e != nil || n <= 0 || hex.EncodeToString(h.Sum(nil)) != restore.SHA256 {
		return nil, fmt.Errorf("backup checksum verification failed")
	}
	// Authenticate the entire file before any database changes.
	source, e := os.Open(file)
	if e != nil {
		return nil, e
	}
	_, e = db.Decrypt(io.Discard, source, restore.Key, restore.BackupID, restore.LimitBytes)
	source.Close()
	if e != nil {
		return nil, e
	}
	// Validate decompression and format before allocating or mutating any staging database.
	source, e = os.Open(file)
	if e != nil {
		return nil, e
	}
	validatedReader, validatedWriter := io.Pipe()
	validationDone := make(chan error, 1)
	go func() {
		_, err := db.Decrypt(validatedWriter, source, restore.Key, restore.BackupID, restore.LimitBytes)
		validatedWriter.CloseWithError(err)
		validationDone <- err
	}()
	e = db.ValidateContent(r.Spec.Engine, restore.Format, validatedReader, r.Spec.DiskMB<<20)
	validatedReader.CloseWithError(e)
	decryptErr := <-validationDone
	source.Close()
	if e != nil {
		return nil, e
	}
	if decryptErr != nil {
		return nil, decryptErr
	}
	if r.Spec.Engine == "postgresql" && restore.Format == "dump" {
		if e = m.validatePostgresArchive(ctx, r, file, restore); e != nil {
			return nil, e
		}
	}
	stage := "stage_" + strings.TrimPrefix(op.JobID, "dbj-")[:20]
	for _, name := range names {
		if name == stage {
			return nil, fmt.Errorf("restore staging name already exists; inspect interrupted job before retrying")
		}
	}
	if _, e = m.engineOperation(ctx, r, db.Operation{Action: "create-database", Database: stage}); e != nil {
		return nil, e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		m.engineOperation(cleanup, r, db.Operation{Action: "delete-database", Database: stage})
	}()
	username := "restore_" + strings.TrimPrefix(op.JobID, "dbj-")[:16]
	password := m.password()
	if _, e = m.userOperation(ctx, r, db.Operation{Action: "create-user", Username: username, Password: password, Grants: map[string]string{stage: "write"}}); e != nil {
		return nil, e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		m.userOperation(cleanup, r, db.Operation{Action: "delete-user", Username: username})
	}()
	private := filepath.Join("/dev/shm/clofix-database", r.Spec.ID)
	credential := filepath.Join(private, "restore-credential")
	var args []string
	format := restore.Format
	switch r.Spec.Engine {
	case "postgresql":
		if format != "dump" && format != "sql" && format != "sql.gz" {
			return nil, fmt.Errorf("incompatible PostgreSQL backup format")
		}
		value := "127.0.0.1:5432:*:" + username + ":" + password + "\n"
		if e = os.WriteFile(credential, []byte(value), 0600); e != nil {
			return nil, e
		}
		args = []string{"exec", "-i", "--user", "65533:65533", "-e", "PGPASSFILE=/run/clofix/restore-credential", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(r.Spec.ID)}
		if format == "dump" {
			args = append(args, "pg_restore", "--no-owner", "--no-acl", "--exit-on-error", "-h", "127.0.0.1", "-U", username, "-d", stage)
			for _, table := range restore.Tables {
				args = append(args, "--table="+table)
			}
		} else {
			if len(restore.Tables) > 0 {
				return nil, fmt.Errorf("selected PostgreSQL tables require custom-format dumps")
			}
			args = append(args, "psql", "-X", "--set=ON_ERROR_STOP=1", "--no-password", "-h", "127.0.0.1", "-U", username, "-d", stage)
		}
		// Restore account may own only the temporary database, never server-wide rights.
		q, _ := db.Quote("postgresql", stage)
		u, _ := db.Quote("postgresql", username)
		if _, e = m.sql(ctx, r, "postgres", "ALTER DATABASE "+q+" OWNER TO "+u+";"); e != nil {
			return nil, e
		}
	case "mysql":
		if format != "sql" && format != "sql.gz" {
			return nil, fmt.Errorf("incompatible MySQL backup format")
		}
		if len(restore.Tables) > 0 {
			return nil, fmt.Errorf("selected MySQL restore requires a backup containing only those tables")
		}
		value := "[client]\nuser=" + username + "\npassword=" + password + "\nhost=127.0.0.1\nssl-mode=VERIFY_CA\nssl-ca=/run/clofix/ca.pem\n"
		if e = os.WriteFile(credential, []byte(value), 0600); e != nil {
			return nil, e
		}
		args = []string{"exec", "-i", "--user", "65533:65533", container(r.Spec.ID), "mysql", "--defaults-extra-file=/run/clofix/restore-credential", "--binary-mode=1", "--local-infile=0", "--database=" + stage}
	case "mongodb":
		if format != "archive" && format != "archive.gz" {
			return nil, fmt.Errorf("incompatible MongoDB backup format")
		}
		if e = os.WriteFile(credential, []byte("password: "+db.MongoJSON(password)+"\n"), 0600); e != nil {
			return nil, e
		}
		args = []string{"exec", "-i", "--user", "65533:65533", container(r.Spec.ID), "mongorestore", "--config=/run/clofix/restore-credential", "--host=127.0.0.1", "--username=" + username, "--authenticationDatabase=admin", "--ssl", "--sslCAFile=/run/clofix/ca.pem", "--archive", "--nsFrom=" + restore.SourceDatabase + ".*", "--nsTo=" + stage + ".*", "--stopOnError"}
		if format == "archive.gz" {
			args = append(args, "--gzip")
		}
		if len(restore.Tables) == 0 {
			args = append(args, "--nsInclude="+restore.SourceDatabase+".*")
		} else {
			for _, table := range restore.Tables {
				args = append(args, "--nsInclude="+restore.SourceDatabase+"."+table)
			}
		}
	}
	if e = os.Chown(credential, 65533, 65533); e != nil {
		return nil, e
	}
	defer os.Remove(credential)
	source, e = os.Open(file)
	if e != nil {
		return nil, e
	}
	defer source.Close()
	plainReader, plainWriter := io.Pipe()
	decryptDone := make(chan error, 1)
	go func() {
		_, e := db.Decrypt(plainWriter, source, restore.Key, restore.BackupID, restore.LimitBytes)
		plainWriter.CloseWithError(e)
		decryptDone <- e
	}()
	var input io.Reader = plainReader
	var compressed *gzip.Reader
	if strings.HasSuffix(format, ".gz") && r.Spec.Engine != "mongodb" {
		compressed, e = gzip.NewReader(plainReader)
		if e != nil {
			plainReader.CloseWithError(e)
			return nil, e
		}
		defer compressed.Close()
		input = compressed
	}
	if r.Spec.Engine == "postgresql" && format != "dump" {
		filteredReader, filteredWriter := io.Pipe()
		go func() {
			e := filterPostgresSQL(filteredWriter, input, r.Spec.DiskMB<<20)
			filteredWriter.CloseWithError(e)
		}()
		input = filteredReader
		defer filteredReader.Close()
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = io.LimitReader(input, (r.Spec.DiskMB<<20)+1)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	commandErr := cmd.Run()
	plainReader.Close()
	decryptionErr := <-decryptDone
	if commandErr != nil || decryptionErr != nil {
		return nil, fmt.Errorf("native restore into staging failed; destination database was not replaced")
	}
	m.pauseConnections(r.Spec.ID, true)
	safeToResume := true
	defer func() {
		if safeToResume {
			m.pauseConnections(r.Spec.ID, false)
		}
	}()
	// Verify staging before controlled cutover. A safety backup is required by the controller.
	if _, e = m.engineOperation(ctx, r, db.Operation{Action: "list-tables", Database: stage}); e != nil {
		return nil, fmt.Errorf("restored staging verification failed")
	}
	safeToResume = false
	if r.Spec.Engine == "postgresql" && len(restore.Tables) > 0 {
		if e = m.selectedPostgres(ctx, r, op, file, stage, username); e != nil {
			return nil, fmt.Errorf("%w: %v", errNeedsInspection, e)
		}
	} else if e = m.cutover(ctx, r, stage, op.Database, restore.Tables, op.JobID); e != nil {
		return nil, fmt.Errorf("%w: %v", errNeedsInspection, e)
	}
	for user, grants := range restore.UserGrants {
		if _, e = m.userOperation(ctx, r, db.Operation{Action: "update-user", Username: user, Grants: grants}); e != nil {
			return nil, fmt.Errorf("%w: user grants need repair; safety backup retained", errNeedsInspection)
		}
	}
	if _, e = m.engineOperation(ctx, r, db.Operation{Action: "list-tables", Database: op.Database}); e != nil {
		return nil, fmt.Errorf("%w: post-cutover verification failed; safety backup retained", errNeedsInspection)
	}
	if r.Spec.Engine == "postgresql" && len(restore.Tables) == 0 {
		old := "safety_" + strings.TrimPrefix(op.JobID, "dbj-")[:20]
		m.engineOperation(ctx, r, db.Operation{Action: "delete-database", Database: old})
	}
	safeToResume = true
	raw, _ := json.Marshal(map[string]any{"database": op.Database, "staging_verified": true})
	return raw, nil
}

func (m *Manager) validatePostgresArchive(ctx context.Context, r record, file string, restore *db.RestoreSpec) error {
	source, err := os.Open(file)
	if err != nil {
		return err
	}
	defer source.Close()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, e := db.Decrypt(writer, source, restore.Key, restore.BackupID, restore.LimitBytes)
		writer.CloseWithError(e)
		done <- e
	}()
	command := exec.CommandContext(ctx, "docker", "exec", "-i", "--user", "65533:65533", container(r.Spec.ID), "pg_restore", "--list")
	command.Stdin = reader
	var toc boundedBuffer
	command.Stdout = &toc
	command.Stderr = io.Discard
	commandErr := command.Run()
	reader.Close()
	decryptErr := <-done
	if commandErr != nil || decryptErr != nil {
		return fmt.Errorf("PostgreSQL archive inventory validation failed")
	}
	return validatePostgresTOC(toc.String())
}

func validatePostgresTOC(value string) error {
	allowed := []string{" ENCODING ", " STDSTRINGS ", " SEARCHPATH ", " SCHEMA ", " COMMENT ", " TABLE ", " TABLE DATA ", " SEQUENCE ", " SEQUENCE OWNED BY ", " SEQUENCE SET ", " DEFAULT ", " CONSTRAINT ", " INDEX ", " FK CONSTRAINT "}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		matched := false
		for _, marker := range allowed {
			if strings.Contains(" "+line+" ", marker) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("PostgreSQL backup contains unsupported executable or server-level objects")
		}
	}
	return nil
}
func filterPostgresSQL(dst io.Writer, src io.Reader, limit int64) error {
	scanner := bufio.NewScanner(io.LimitReader(src, limit+1))
	scanner.Buffer(make([]byte, 65536), 8<<20)
	var size int64
	for scanner.Scan() {
		line := scanner.Text()
		size += int64(len(line) + 1)
		if size > limit {
			return fmt.Errorf("expanded SQL exceeds target storage limit")
		}
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, `\restrict `) || strings.HasPrefix(trim, `\unrestrict `) {
			continue
		}
		for i := 0; i < len(line); i++ {
			if line[i] == '\\' {
				if i+1 == len(line) {
					return fmt.Errorf("unsupported SQL escape")
				}
				next := line[i+1]
				if next != '\\' && next != '.' && next != 'N' {
					return fmt.Errorf("psql meta-commands and unsafe escapes are not permitted in uploaded SQL")
				}
				i++
			}
		}
		if _, e := io.WriteString(dst, line+"\n"); e != nil {
			return e
		}
	}
	return scanner.Err()
}
func (m *Manager) cutover(ctx context.Context, r record, stage, target string, tables []string, job string) error {
	names, e := m.listDatabases(ctx, r)
	if e != nil {
		return e
	}
	exists := false
	for _, name := range names {
		if name == target {
			exists = true
		}
	}
	stageQ, _ := db.Quote(r.Spec.Engine, stage)
	targetQ, _ := db.Quote(r.Spec.Engine, target)
	if r.Spec.Engine == "postgresql" && len(tables) == 0 {
		old := "safety_" + strings.TrimPrefix(job, "dbj-")[:20]
		oldQ, _ := db.Quote("postgresql", old)
		if exists {
			if _, e = m.sql(ctx, r, "postgres", "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname="+db.Literal(target)+" AND pid<>pg_backend_pid(); ALTER DATABASE "+targetQ+" RENAME TO "+oldQ+";"); e != nil {
				return e
			}
		}
		if _, e = m.sql(ctx, r, "postgres", "ALTER DATABASE "+stageQ+" RENAME TO "+targetQ+"; ALTER DATABASE "+targetQ+" OWNER TO postgres;"); e != nil {
			return fmt.Errorf("cutover interrupted; safety database may require administrator recovery")
		}
		return nil
	}
	if exists && len(tables) == 0 && r.Spec.Engine != "postgresql" {
		if _, e = m.engineOperation(ctx, r, db.Operation{Action: "delete-database", Database: target}); e != nil {
			return e
		}
		exists = false
	}
	if !exists {
		if _, e = m.engineOperation(ctx, r, db.Operation{Action: "create-database", Database: target}); e != nil {
			return e
		}
	}
	raw, e := m.engineOperation(ctx, r, db.Operation{Action: "list-tables", Database: stage})
	if e != nil {
		return e
	}
	var data struct {
		Tables json.RawMessage `json:"tables"`
	}
	json.Unmarshal(raw, &data)
	selected := []string{}
	if r.Spec.Engine == "mongodb" {
		var collections []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data.Tables, &collections) != nil {
			return fmt.Errorf("invalid staging inventory")
		}
		for _, c := range collections {
			if c.Name != "clofix_metadata" {
				selected = append(selected, c.Name)
			}
		}
		for _, name := range selected {
			if e := db.Name(name); e != nil {
				return e
			}
			if _, e = m.mongo(ctx, r, "const result=db.getSiblingDB('admin').runCommand({renameCollection:"+db.MongoJSON(stage+"."+name)+",to:"+db.MongoJSON(target+"."+name)+",dropTarget:true});if(result.ok!==1)throw Error('collection cutover failed');"); e != nil {
				return fmt.Errorf("collection cutover interrupted; safety backup is retained")
			}
		}
		return nil
	}
	if json.Unmarshal(data.Tables, &selected) != nil {
		return fmt.Errorf("invalid staging inventory")
	}
	for _, name := range selected {
		table, e := db.Quote(r.Spec.Engine, name)
		if e != nil {
			return e
		}
		if r.Spec.Engine == "mysql" {
			if _, e = m.sql(ctx, r, target, "DROP TABLE IF EXISTS "+table+"; RENAME TABLE "+stageQ+"."+table+" TO "+targetQ+"."+table+";"); e != nil {
				return fmt.Errorf("table cutover interrupted; restore the retained safety backup if needed")
			}
		} else {
			return fmt.Errorf("invalid selected cutover path")
		}
	}
	return nil
}

func (m *Manager) selectedPostgres(ctx context.Context, r record, op db.Operation, file, stage, user string) error {
	if _, e := m.engineOperation(ctx, r, db.Operation{Action: "create-database", Database: op.Database}); e != nil {
		return e
	}
	if _, e := m.userOperation(ctx, r, db.Operation{Action: "update-user", Username: user, Grants: map[string]string{stage: "write", op.Database: "write"}}); e != nil {
		return e
	}
	quoted := []string{}
	for _, table := range op.Restore.Tables {
		q, e := db.Quote("postgresql", table)
		if e != nil {
			return e
		}
		quoted = append(quoted, q)
	}
	if _, e := m.sql(ctx, r, op.Database, "DROP TABLE IF EXISTS "+strings.Join(quoted, ",")+";"); e != nil {
		return fmt.Errorf("selected tables have dependencies outside selection; cutover rejected")
	}
	args := []string{"exec", "-i", "--user", "65533:65533", "-e", "PGPASSFILE=/run/clofix/restore-credential", "-e", "PGSSLMODE=verify-full", "-e", "PGSSLROOTCERT=/run/clofix/ca.pem", container(r.Spec.ID), "pg_restore", "--no-owner", "--no-acl", "--exit-on-error", "-h", "127.0.0.1", "-U", user, "-d", op.Database}
	for _, table := range op.Restore.Tables {
		args = append(args, "--table="+table)
	}
	source, e := os.Open(file)
	if e != nil {
		return e
	}
	defer source.Close()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, e := db.Decrypt(writer, source, op.Restore.Key, op.Restore.BackupID, op.Restore.LimitBytes)
		writer.CloseWithError(e)
		done <- e
	}()
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = reader
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	e = command.Run()
	reader.Close()
	decryptErr := <-done
	if e != nil || decryptErr != nil {
		return fmt.Errorf("selected restore interrupted; restore retained safety backup to recover")
	}
	return nil
}

type backupReceipt struct {
	ProjectID string    `json:"project_id"`
	Database  string    `json:"database"`
	SHA256    string    `json:"sha256"`
	Bytes     int64     `json:"bytes"`
	Created   time.Time `json:"created"`
	Complete  bool      `json:"complete"`
}

// Worker-side enforcement complements the controller's explicit confirmation and
// dependency chain; an overwrite never relies solely on a client assertion.
func (m *Manager) verifySafetyBackup(r record, database, id string) error {
	if !db.ID.MatchString(id) || !strings.HasPrefix(id, "dbb-") {
		return fmt.Errorf("a fresh completed destination safety backup is required before overwrite")
	}
	file := filepath.Join(m.path(r.Spec.ID), "backups", id+".enc")
	raw, e := os.ReadFile(file + ".json")
	if e != nil {
		return fmt.Errorf("destination safety backup receipt is missing")
	}
	var receipt backupReceipt
	if json.Unmarshal(raw, &receipt) != nil || !receipt.Complete || receipt.ProjectID != r.Spec.ID || receipt.Database != database || time.Since(receipt.Created) > 10*time.Minute || receipt.Created.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("destination safety backup is invalid or older than ten minutes")
	}
	source, e := os.Open(file)
	if e != nil {
		return fmt.Errorf("destination safety backup is missing")
	}
	defer source.Close()
	digest := sha256.New()
	n, e := io.Copy(digest, source)
	if e != nil || n != receipt.Bytes || hex.EncodeToString(digest.Sum(nil)) != receipt.SHA256 {
		return fmt.Errorf("destination safety backup checksum verification failed")
	}
	return nil
}
