//go:build linux

// Package database provisions isolated, resource-bounded worker database instances.
package database

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	db "github.com/clofix/filevps/worker/pkg/database"
)

var errNeedsInspection = errors.New("restore cutover requires inspection")

type Image struct {
	Ref     string `json:"ref"`
	Version string `json:"version"`
}
type record struct {
	Spec        db.Project `json:"spec"`
	Image       Image      `json:"image"`
	Credential  []byte     `json:"credential"`
	CA          string     `json:"ca"`
	Certificate string     `json:"certificate"`
	State       string     `json:"state"`
}
type secrets struct {
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
}
type Manager struct {
	Transfer func(context.Context, string, string, io.Reader) (io.ReadCloser, error)
	sync.Mutex
	Root        string
	images      map[string]Image
	records     map[string]record
	key         cipher.AEAD
	listeners   map[string]net.Listener
	cancels     map[string]context.CancelFunc
	results     map[string]db.Result
	running     int
	connections map[string]map[net.Conn]struct{}
	paused      map[string]bool
}

func New(root string) (*Manager, error) {
	m := &Manager{connections: map[string]map[net.Conn]struct{}{}, paused: map[string]bool{}, Root: filepath.Join(root, "databases"), images: map[string]Image{}, records: map[string]record{}, listeners: map[string]net.Listener{}, cancels: map[string]context.CancelFunc{}, results: map[string]db.Result{}}
	if e := os.MkdirAll(m.Root, 0700); e != nil {
		return nil, e
	}
	keyfile := filepath.Join(m.Root, "credentials.key")
	key, e := os.ReadFile(keyfile)
	if os.IsNotExist(e) {
		if _, metadataErr := os.Stat(filepath.Join(m.Root, "projects.json")); !os.IsNotExist(metadataErr) {
			return nil, fmt.Errorf("database credential key is missing; restore it from the worker backup")
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e == nil {
			var f *os.File
			f, e = os.OpenFile(keyfile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e == nil {
				_, e = f.Write(key)
				if e == nil {
					e = f.Sync()
				}
				closeErr := f.Close()
				if e == nil {
					e = closeErr
				}
			}
		}
	}
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	m.key, e = cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(filepath.Join(m.Root, "projects.json"))
	if e == nil {
		if json.Unmarshal(raw, &m.records) != nil || m.records == nil {
			return nil, fmt.Errorf("invalid database project metadata")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	for id, r := range m.records {
		if id != r.Spec.ID || r.Spec.Validate() != nil {
			return nil, fmt.Errorf("invalid persisted database project")
		}
		if _, err := m.secret(r); err != nil {
			return nil, fmt.Errorf("database credentials cannot be decrypted; restore the original worker key")
		}
	}
	// A manifest is operator-managed; never accept image names from customer input.
	if raw, e = os.ReadFile(filepath.Join(root, "database-images.json")); e == nil {
		if json.Unmarshal(raw, &m.images) != nil {
			return nil, fmt.Errorf("invalid trusted database image manifest")
		}
	}
	raw, e = os.ReadFile(filepath.Join(m.Root, "jobs.json"))
	if e == nil {
		if json.Unmarshal(raw, &m.results) != nil || m.results == nil {
			return nil, fmt.Errorf("invalid database job journal")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	for id, result := range m.results {
		if result.State == "RUNNING" {
			result.State = "INTERRUPTED"
			result.Error = "worker restarted during operation; verify database before retrying"
			m.results[id] = result
			if project, ok := m.records[result.ProjectID]; ok {
				project.State = "NEEDS_INSPECTION"
				m.records[result.ProjectID] = project
			}
		}
	}
	if len(m.records) > 0 {
		if e = m.save(); e != nil {
			return nil, e
		}
	}
	if e = m.journal(); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) save() error {
	raw, e := json.Marshal(m.records)
	if e != nil {
		return e
	}
	file := filepath.Join(m.Root, "projects.json")
	if e = os.WriteFile(file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(file+".tmp", file)
}
func (m *Manager) journal() error {
	raw, e := json.Marshal(m.results)
	if e != nil {
		return e
	}
	file := filepath.Join(m.Root, "jobs.json")
	if e = os.WriteFile(file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(file+".tmp", file)
}
func (m *Manager) password() string {
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		panic(e)
	}
	return hex.EncodeToString(raw)
}
func (m *Manager) seal(id string, value any) ([]byte, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, m.key.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return m.key.Seal(nonce, nonce, raw, []byte(id)), nil
}
func (m *Manager) secret(r record) (secrets, error) {
	var s secrets
	n := m.key.NonceSize()
	if len(r.Credential) < n {
		return s, fmt.Errorf("missing database credentials")
	}
	raw, e := m.key.Open(nil, r.Credential[:n], r.Credential[n:], []byte(r.Spec.ID))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(raw, &s)
	return s, e
}
func (m *Manager) path(id string) string { return filepath.Join(m.Root, id) }
func container(id string) string         { return "clofix-" + id }
func (m *Manager) docker(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = input
	var out boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if detail := strings.TrimSpace(out.String()); detail != "" {
			return nil, fmt.Errorf("database runtime command failed: %s", detail)
		}
		return nil, fmt.Errorf("database runtime command failed: %s", e)
	}
	return out.Bytes(), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("engine output limit")
	}
	return b.Buffer.Write(p)
}
func (m *Manager) Ready() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, e := m.docker(ctx, nil, "info", "--format", "{{.Name}}")
	host, _ := os.Hostname()
	if e != nil || strings.TrimSpace(string(raw)) != host {
		return nil
	}
	ready := []string{}
	for engine, image := range m.images {
		if !db.Engine(engine) || !strings.Contains(image.Ref, "@sha256:") || image.Version == "" {
			continue
		}
		if _, e = m.docker(ctx, nil, "image", "inspect", image.Ref); e == nil {
			ready = append(ready, engine)
		}
	}
	return ready
}
func (m *Manager) Reserved() (uint64, float64, uint64) {
	m.Lock()
	defer m.Unlock()
	var ram, disk uint64
	var cpu float64
	for _, r := range m.records {
		ram += uint64(r.Spec.RAMMB) << 20
		cpu += r.Spec.CPU
		disk += uint64(r.Spec.DiskMB) << 20
	}
	return ram, cpu, disk
}
func (m *Manager) Results() []db.Result {
	m.Lock()
	defer m.Unlock()
	out := []db.Result{}
	for _, r := range m.results {
		out = append(out, r)
	}
	return out
}
func (m *Manager) Cancel(id string) {
	m.Lock()
	defer m.Unlock()
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
	}
}
func (m *Manager) Run(ctx context.Context, op db.Operation) db.Result {
	result := db.Result{JobID: op.JobID, ProjectID: op.Project.ID, State: "FAILED", At: time.Now().UTC()}
	if !db.ID.MatchString(op.JobID) || !strings.HasPrefix(op.JobID, "dbj-") {
		result.Error = "invalid job ID"
		return result
	}
	if e := op.Project.Validate(); e != nil {
		result.Error = e.Error()
		return result
	}
	m.Lock()
	if previous, ok := m.results[op.JobID]; ok {
		m.Unlock()
		return previous
	}
	if m.running >= 2 {
		m.Unlock()
		result.Error = "database job concurrency limit"
		return result
	}
	for id, r := range m.results {
		if r.State == "RUNNING" && r.ProjectID == op.Project.ID {
			m.Unlock()
			result.Error = "another operation is active for this database project: " + id
			return result
		}
	}
	jobCtx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	m.cancels[op.JobID] = cancel
	m.running++
	result.State = "RUNNING"
	m.results[op.JobID] = result
	if e := m.journal(); e != nil {
		delete(m.cancels, op.JobID)
		m.running--
		delete(m.results, op.JobID)
		m.Unlock()
		result.State = "FAILED"
		result.Error = "could not persist worker job"
		return result
	}
	m.Unlock()
	data, e := m.apply(jobCtx, op)
	m.Lock()
	defer m.Unlock()
	delete(m.cancels, op.JobID)
	m.running--
	result.At = time.Now().UTC()
	if e != nil {
		result.State = "FAILED"
		result.Error = e.Error()
		if errors.Is(e, errNeedsInspection) {
			if project, ok := m.records[op.Project.ID]; ok {
				project.State = "NEEDS_INSPECTION"
				m.records[op.Project.ID] = project
				m.paused[op.Project.ID] = true
				for conn := range m.connections[op.Project.ID] {
					conn.Close()
				}
				if m.save() != nil {
					result.Error = "restore failure could not persist the project fence; worker requires inspection"
				}
			}
		}
		if jobCtx.Err() != nil {
			result.State = "CANCELLED"
			result.Error = "operation cancelled or timed out; database is fenced pending inspection"
			if project, ok := m.records[op.Project.ID]; ok {
				project.State = "NEEDS_INSPECTION"
				m.records[op.Project.ID] = project
				if m.save() != nil {
					result.Error = "cancelled operation could not persist project fence; worker requires inspection"
				}
			}
		}
	} else {
		result.State = "SUCCEEDED"
		result.Progress = 100
		result.Data = data
	}
	m.results[op.JobID] = result
	if m.journal() != nil {
		result.Error = "job completed but worker journal persistence failed"
		result.State = "INTERRUPTED"
		m.results[op.JobID] = result
	}
	return result
}
func (m *Manager) apply(ctx context.Context, op db.Operation) (json.RawMessage, error) {
	if op.Action == "provision" {
		return m.provision(ctx, op.Project)
	}
	m.Lock()
	r, ok := m.records[op.Project.ID]
	m.Unlock()
	if !ok {
		if op.Action == "delete-backup" || op.Action == "delete-project" {
			return json.RawMessage(`{"deleted":true}`), nil
		}
		return nil, fmt.Errorf("database project not found on worker")
	}
	if r.Spec.Engine != op.Project.Engine {
		return nil, fmt.Errorf("engine mismatch")
	}
	if op.Action == "delete-backup" {
		return m.removeBackup(r, op)
	}
	if op.Action == "delete-project" {
		return nil, m.remove(ctx, r)
	}
	if r.State == "NEEDS_INSPECTION" && op.Action != "ping" && !strings.HasPrefix(op.Action, "list-") {
		return nil, fmt.Errorf("database project requires inspection after interrupted work")
	}
	return m.engineOperation(ctx, r, op)
}
func makeCertificate(host string) (string, string, string, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", "", "", e
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "CloFix isolated database CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		return "", "", "", e
	}
	serverKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", "", "", e
	}
	serial, _ = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	server := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	if ip := net.ParseIP(host); ip != nil {
		server.IPAddresses = append(server.IPAddresses, ip)
	} else if host != "" {
		server.DNSNames = append(server.DNSNames, host)
	}
	cert, e := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, key)
	if e != nil {
		return "", "", "", e
	}
	priv, e := x509.MarshalPKCS8PrivateKey(serverKey)
	if e != nil {
		return "", "", "", e
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})), nil
}
func (m *Manager) prepare(r record) (string, string, error) {
	root := m.path(r.Spec.ID)
	data := filepath.Join(root, "data")
	if e := os.MkdirAll(data, 0700); e != nil {
		return "", "", e
	}
	if os.Getenv("CLOFIX_DATABASE_DIRECTORY_STORAGE") != "true" {
		image := filepath.Join(root, "data.img")
		if _, e := os.Stat(image); os.IsNotExist(e) {
			f, e := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return "", "", e
			}
			// Reserve physical blocks before formatting, so accepted capacity is real.
			e = syscall.Fallocate(int(f.Fd()), 0, 0, r.Spec.DiskMB<<20)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e == nil {
				e = closeErr
			}
			if e != nil {
				os.Remove(image)
				return "", "", fmt.Errorf("cannot reserve database disk: %w", e)
			}
			if e = exec.Command("mkfs.ext4", "-q", "-F", image).Run(); e != nil {
				os.Remove(image)
				return "", "", e
			}
		} else if e != nil {
			return "", "", e
		}
		mounted, e := isMounted(data)
		if e != nil {
			return "", "", e
		}
		if !mounted {
			if e := exec.Command("mount", "-t", "ext4", "-o", "loop,nosuid,nodev", image, data).Run(); e != nil {
				return "", "", fmt.Errorf("database disk mount failed")
			}
		}
	}
	uid := 999
	// Never expose the ext4 filesystem root to an engine. A freshly formatted
	// filesystem contains lost+found, while MySQL requires an empty data
	// directory during initialization.
	engineData := filepath.Join(data, "engine")
	if e := os.MkdirAll(engineData, 0700); e != nil {
		return "", "", e
	}
	if e := os.Chown(engineData, uid, uid); e != nil {
		return "", "", e
	}
	secret, e := m.secret(r)
	if e != nil {
		return "", "", e
	}
	private := filepath.Join("/dev/shm/clofix-database", r.Spec.ID)
	if e = os.MkdirAll(private, 0755); e != nil {
		return "", "", e
	}
	files := map[string]struct {
		value string
		mode  os.FileMode
		uid   int
	}{"pg_hba.conf": {"local all postgres scram-sha-256\nlocal all all reject\nhostssl all all 0.0.0.0/0 scram-sha-256\nhostssl all all ::/0 scram-sha-256\nhostnossl all all 0.0.0.0/0 reject\nhostnossl all all ::/0 reject\n", 0644, 0}, "ca.pem": {r.CA, 0644, 0}, "server.pem": {r.Certificate, 0644, 0}, "server.key": {secret.PrivateKey, 0400, uid}, "server-combined.pem": {r.Certificate + secret.PrivateKey, 0400, uid}, "admin-password": {secret.Password, 0440, uid}, "mongo-admin-password": {secret.Password, 0600, 0}, "mysql.cnf": {"[client]\nuser=root\npassword=" + secret.Password + "\nhost=127.0.0.1\nssl-mode=VERIFY_CA\nssl-ca=/run/clofix/ca.pem\n", 0600, 0}, "pgpass": {"127.0.0.1:5432:*:postgres:" + secret.Password + "\n", 0600, 0}}
	for name, file := range files {
		path := filepath.Join(private, name)
		if e = os.WriteFile(path, []byte(file.value), file.mode); e != nil {
			return "", "", e
		}
		if e = os.Chown(path, file.uid, file.uid); e != nil {
			return "", "", e
		}
		if e = os.Chmod(path, file.mode); e != nil {
			return "", "", e
		}
	}
	return engineData, private, nil
}
func (m *Manager) provision(ctx context.Context, spec db.Project) (json.RawMessage, error) {
	m.Lock()
	r, exists := m.records[spec.ID]
	if !exists {
		image, ok := m.images[spec.Engine]
		if !ok || !strings.Contains(image.Ref, "@sha256:") || image.Version != spec.Version {
			m.Unlock()
			return nil, fmt.Errorf("trusted compatible engine image is unavailable")
		}
		ca, cert, private, e := makeCertificate(spec.Host)
		if e != nil {
			m.Unlock()
			return nil, e
		}
		credential, e := m.seal(spec.ID, secrets{Password: m.password(), PrivateKey: private})
		if e != nil {
			m.Unlock()
			return nil, e
		}
		r = record{Spec: spec, Image: image, CA: ca, Certificate: cert, Credential: credential, State: "PROVISIONING"}
		m.records[spec.ID] = r
		if e = m.save(); e != nil {
			delete(m.records, spec.ID)
			m.Unlock()
			return nil, e
		}
	}
	m.Unlock()
	if r.State == "NEEDS_INSPECTION" {
		return nil, fmt.Errorf("database project requires inspection after interrupted work")
	}
	if r.Spec != spec {
		return nil, fmt.Errorf("existing database specification does not match request")
	}
	ready := false
	for _, engine := range m.Ready() {
		if engine == spec.Engine {
			ready = true
		}
	}
	if !ready {
		return nil, fmt.Errorf("local database runtime is not ready")
	}
	data, private, e := m.prepare(r)
	if e != nil {
		return nil, e
	}
	name := container(spec.ID)
	network := "net-" + name
	if _, e = m.docker(ctx, nil, "network", "inspect", network); e != nil {
		if _, e = m.docker(ctx, nil, "network", "create", "--internal", "--label", "clofix.database.project="+spec.ID, network); e != nil {
			return nil, e
		}
	}
	if _, e = m.docker(ctx, nil, "inspect", name); e != nil {
		args := []string{"run", "-d", "--name", name, "--label", "clofix.database.project=" + spec.ID, "--network", network, "--memory", strconv.FormatInt(spec.RAMMB<<20, 10), "--memory-swap", strconv.FormatInt(spec.RAMMB<<20, 10), "--cpus", strconv.FormatFloat(spec.CPU, 'f', 2, 64), "--pids-limit", "256", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--user", "999:999", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m", "--tmpfs", "/run/mysqld:rw,noexec,nosuid,size=16m,uid=999,gid=999", "--tmpfs", "/var/run/postgresql:rw,noexec,nosuid,size=16m,uid=999,gid=999", "--mount", "type=bind,src=" + private + ",dst=/run/clofix,readonly"}
		switch spec.Engine {
		case "mysql":
			args = append(args, "--mount", "type=bind,src="+data+",dst=/var/lib/mysql", "-e", "MYSQL_ROOT_PASSWORD_FILE=/run/clofix/admin-password", r.Image.Ref, "--require-secure-transport=ON", "--ssl-ca=/run/clofix/ca.pem", "--ssl-cert=/run/clofix/server.pem", "--ssl-key=/run/clofix/server.key", "--local-infile=OFF")
		case "postgresql":
			args = append(args, "--mount", "type=bind,src="+data+",dst=/var/lib/postgresql/data", "-e", "PGDATA=/var/lib/postgresql/data/pgdata", "-e", "POSTGRES_PASSWORD_FILE=/run/clofix/admin-password", "-e", "POSTGRES_INITDB_ARGS=--auth-local=scram-sha-256 --auth-host=scram-sha-256", r.Image.Ref, "postgres", "-c", "hba_file=/run/clofix/pg_hba.conf", "-c", "ssl=on", "-c", "ssl_ca_file=/run/clofix/ca.pem", "-c", "ssl_cert_file=/run/clofix/server.pem", "-c", "ssl_key_file=/run/clofix/server.key")
		case "mongodb":
			args = append(args, "--tmpfs", "/data/configdb:rw,noexec,nosuid,size=16m,uid=999,gid=999", "--mount", "type=bind,src="+data+",dst=/data/db", "-e", "MONGO_INITDB_ROOT_USERNAME=clofixadmin", "-e", "MONGO_INITDB_ROOT_PASSWORD_FILE=/run/clofix/admin-password", r.Image.Ref, "mongod", "--auth", "--bind_ip_all", "--noscripting", "--tlsMode", "requireTLS", "--tlsCertificateKeyFile", "/run/clofix/server-combined.pem", "--tlsCAFile", "/run/clofix/ca.pem", "--tlsAllowConnectionsWithoutCertificates")
		}
		if _, e = m.docker(ctx, nil, args...); e != nil {
			return nil, e
		}
	} else {
		if _, e = m.docker(ctx, nil, "start", name); e != nil {
			return nil, e
		}
	}
	ready = false
	readinessCtx, stopReadiness := context.WithTimeout(ctx, 90*time.Second)
	defer stopReadiness()
	for i := 0; i < 90; i++ {
		probeCtx, stopProbe := context.WithTimeout(readinessCtx, 3*time.Second)
		_, e = m.engineOperation(probeCtx, r, db.Operation{Action: "ping"})
		stopProbe()
		if e == nil {
			ready = true
			break
		}
		select {
		case <-readinessCtx.Done():
			return nil, fmt.Errorf("database engine readiness timed out")
		case <-time.After(time.Second):
		}
	}
	if !ready {
		return nil, fmt.Errorf("database engine failed readiness checks; inspect private worker logs")
	}
	actualVersion, e := m.engineVersion(ctx, r)
	if e != nil {
		return nil, e
	}
	if actualVersion != spec.Version && !strings.HasPrefix(actualVersion, spec.Version+".") && !strings.HasPrefix(actualVersion, spec.Version+" ") {
		return nil, fmt.Errorf("native database version %q does not match trusted version %q", actualVersion, spec.Version)
	}
	if e = m.listen(r); e != nil {
		return nil, e
	}
	m.Lock()
	r.State = "READY"
	m.records[spec.ID] = r
	e = m.save()
	m.Unlock()
	if e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(map[string]string{"ca": r.CA, "engine_version": r.Image.Version})
	return raw, nil
}
func (m *Manager) listen(r record) error {
	m.Lock()
	defer m.Unlock()
	if m.listeners[r.Spec.ID] != nil {
		return nil
	}
	listener, e := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(r.Spec.Port)))
	if e != nil {
		return e
	}
	m.listeners[r.Spec.ID] = listener
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			m.Lock()
			if m.connections[r.Spec.ID] == nil {
				m.connections[r.Spec.ID] = map[net.Conn]struct{}{}
			}
			if m.paused[r.Spec.ID] || len(m.connections[r.Spec.ID]) >= 128 {
				m.Unlock()
				conn.Close()
				continue
			}
			m.connections[r.Spec.ID][conn] = struct{}{}
			m.Unlock()
			go func() {
				defer func() { conn.Close(); m.Lock(); delete(m.connections[r.Spec.ID], conn); m.Unlock() }()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				raw, e := m.docker(ctx, nil, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", container(r.Spec.ID))
				cancel()
				if e != nil {
					return
				}
				port := 3306
				if r.Spec.Engine == "postgresql" {
					port = 5432
				} else if r.Spec.Engine == "mongodb" {
					port = 27017
				}
				upstream, e := net.DialTimeout("tcp", net.JoinHostPort(strings.TrimSpace(string(raw)), strconv.Itoa(port)), 3*time.Second)
				if e != nil {
					return
				}
				defer upstream.Close()
				// Bound stalled streams and ensure both copy goroutines leave when one side exits.
				deadline := time.Now().Add(24 * time.Hour)
				conn.SetDeadline(deadline)
				upstream.SetDeadline(deadline)
				done := make(chan struct{})
				go func() {
					io.Copy(upstream, conn)
					if c, ok := upstream.(*net.TCPConn); ok {
						c.CloseWrite()
					}
					close(done)
				}()
				io.Copy(conn, upstream)
				conn.Close()
				upstream.Close()
				if c, ok := conn.(*net.TCPConn); ok {
					c.CloseWrite()
				}
				<-done
			}()
		}
	}()
	return nil
}
func isMounted(path string) (bool, error) {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && unescape.Replace(fields[4]) == path {
			return true, nil
		}
	}
	return false, nil
}
func (m *Manager) remove(ctx context.Context, r record) error {
	m.pauseConnections(r.Spec.ID, true)
	defer m.pauseConnections(r.Spec.ID, false)
	name := container(r.Spec.ID)
	raw, e := m.docker(ctx, nil, "container", "ls", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.Names}}")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(raw)) != "" {
		raw, e = m.docker(ctx, nil, "inspect", "--format", `{{index .Config.Labels "clofix.database.project"}}`, name)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(raw)) != r.Spec.ID {
			return fmt.Errorf("database container ownership mismatch")
		}
		if _, e = m.docker(ctx, nil, "rm", "-f", name); e != nil {
			return e
		}
	}
	m.Lock()
	if l := m.listeners[r.Spec.ID]; l != nil {
		l.Close()
		delete(m.listeners, r.Spec.ID)
	}
	m.Unlock()
	data := filepath.Join(m.path(r.Spec.ID), "data")
	mounted, e := isMounted(data)
	if e != nil {
		return e
	}
	if mounted {
		if e = exec.CommandContext(ctx, "umount", data).Run(); e != nil {
			return fmt.Errorf("database disk is still mounted; data cleanup stopped")
		}
	}
	if e = os.RemoveAll(m.path(r.Spec.ID)); e != nil {
		return e
	}
	if e = os.RemoveAll(filepath.Join("/dev/shm/clofix-database", r.Spec.ID)); e != nil {
		return e
	}
	m.docker(ctx, nil, "network", "rm", "net-"+name)
	m.Lock()
	defer m.Unlock()
	delete(m.records, r.Spec.ID)
	if e = m.save(); e != nil {
		m.records[r.Spec.ID] = r
		return e
	}
	return nil
}

// pauseConnections prevents new application traffic and closes existing gateway
// connections during a restore. Native administration uses the private network.
func (m *Manager) pauseConnections(id string, paused bool) {
	m.Lock()
	defer m.Unlock()
	m.paused[id] = paused
	if paused {
		for conn := range m.connections[id] {
			conn.Close()
		}
	}
}
