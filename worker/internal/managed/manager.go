//go:build linux

// Package managed runs persistent native CloFix application sandboxes.
package managed

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/internal/engine/cgroups"
	"github.com/clofix/filevps/worker/internal/engine/network"
	"github.com/clofix/filevps/worker/pkg/proto"
)

type record struct {
	backupActive bool
	logReader    *os.File
	LastStart    time.Time           `json:"last_start"`
	Spec         proto.ManagedSpec   `json:"spec"`
	Status       proto.ManagedStatus `json:"status"`
	PID          int                 `json:"pid"`
}
type Manager struct {
	deleted map[string]proto.ManagedStatus
	sync.Mutex
	Root      string
	Templates string
	CG        *cgroups.Manager
	Network   *network.Manager
	apps      map[string]*record
	gateways  map[string]*http.Server
}

func New(state string) *Manager {
	return &Manager{deleted: map[string]proto.ManagedStatus{}, Root: filepath.Join(state, "managed-apps"), Templates: filepath.Join(state, "managed-templates"), CG: cgroups.New(), Network: network.New(), apps: map[string]*record{}, gateways: map[string]*http.Server{}}
}
func (m *Manager) path(id string) string { return filepath.Join(m.Root, id) }
func (m *Manager) save(a *record) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	path := filepath.Join(m.path(a.Spec.ID), "metadata.json")
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (m *Manager) Load() error {
	if err := os.MkdirAll(m.Root, 0700); err != nil {
		return err
	}
	dirs, err := os.ReadDir(m.Root)
	if err != nil {
		return err
	}
	m.Lock()
	defer m.Unlock()
	if b, err := os.ReadFile(filepath.Join(m.Root, "deleted.json")); err == nil {
		_ = json.Unmarshal(b, &m.deleted)
	}
	if m.deleted == nil {
		m.deleted = map[string]proto.ManagedStatus{}
	}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(m.Root, d.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		var a record
		if json.Unmarshal(b, &a) == nil && proto.ValidateManaged(a.Spec) == nil {
			m.apps[a.Spec.ID] = &a
		}
	}
	return nil
}
func (m *Manager) ReadyTemplates() []string {
	out := []string{}
	for _, t := range proto.ManagedTemplates {
		if f, err := os.Stat(filepath.Join(m.Templates, t.ID, "app", "start")); err == nil && f.Mode().IsRegular() && f.Mode()&0111 != 0 {
			out = append(out, t.ID)
		}
	}
	return out
}
func (m *Manager) Reserved() (uint64, float64, uint64) {
	m.Lock()
	defer m.Unlock()
	var ram, disk uint64
	var cpu float64
	for _, a := range m.apps {
		if a.Status.Desired == "stopped" {
			_ = m.gateway(a)
		}
		if a.Status.Desired == "running" {
			ram += uint64(a.Spec.RAMMB) << 20
			cpu += a.Spec.CPU
		}
		disk += ManagedDiskBytes
	}
	return ram, cpu, disk
}
func (m *Manager) Apply(op proto.Operation) (resultErr error) {
	if op.Managed == nil {
		return fmt.Errorf("managed spec is required")
	}
	spec := *op.Managed
	if err := proto.ValidateManaged(spec); err != nil {
		return err
	}
	m.Lock()
	defer m.Unlock()
	defer func() {
		if a := m.apps[spec.ID]; a != nil {
			a.Status.OperationID = op.ID
			if resultErr != nil {
				a.Status.Error = resultErr.Error()
			}
			_ = m.save(a)
		}
	}()
	a := m.apps[spec.ID]
	if op.ServiceAction == "deploy" {
		if a == nil {
			if err := os.MkdirAll(m.path(spec.ID), 0700); err != nil {
				return err
			}
			a = &record{Spec: spec, Status: proto.ManagedStatus{ID: spec.ID, State: "STOPPED", Desired: "stopped", Created: time.Now().UTC()}}
			m.apps[spec.ID] = a
			if err := m.save(a); err != nil {
				return err
			}
		}
		return m.start(a)
	}
	if a == nil {
		if op.ServiceAction == "delete" {
			if _, err := os.Stat(m.path(spec.ID)); os.IsNotExist(err) {
				m.deleted[spec.ID] = proto.ManagedStatus{ID: spec.ID, State: "DELETED", Desired: "deleted", OperationID: op.ID, Created: time.Now().UTC()}
				return m.saveDeleted()
			}
		}
		return fmt.Errorf("managed app not found on worker")
	}
	if a.backupActive {
		return fmt.Errorf("wait for the data backup to finish")
	}
	switch op.ServiceAction {
	case "start":
		return m.start(a)
	case "stop":
		return m.stop(a)
	case "restart":
		if err := m.stop(a); err != nil {
			return err
		}
		return m.start(a)
	case "delete":
		if err := m.stop(a); err != nil {
			return err
		}
		if server := m.gateways[spec.ID]; server != nil {
			_ = server.Close()
			delete(m.gateways, spec.ID)
		}
		if mountedData(filepath.Join(m.path(spec.ID), "data")) {
			if err := syscall.Unmount(filepath.Join(m.path(spec.ID), "data"), 0); err != nil {
				return fmt.Errorf("data volume is still mounted: %w", err)
			}
		}
		if a.logReader != nil {
			_ = a.logReader.Close()
			a.logReader = nil
		}
		if err := os.RemoveAll(m.path(spec.ID)); err != nil {
			return err
		}
		delete(m.apps, spec.ID)
		m.deleted[spec.ID] = proto.ManagedStatus{ID: spec.ID, State: "DELETED", Desired: "deleted", OperationID: op.ID, Created: time.Now().UTC()}
		return m.saveDeleted()
	}
	return fmt.Errorf("unknown managed action")
}
func (m *Manager) start(a *record) error {
	id := a.Spec.ID
	a.Status.Desired = "running"
	if m.CG.Alive(id) {
		if err := m.ensureLog(a); err != nil {
			return err
		}
		a.Status.State = "RUNNING"
		if err := m.gateway(a); err != nil {
			return err
		}
		return m.save(a)
	}
	a.LastStart = time.Now().UTC()
	a.Status.State = "STARTING"
	a.Status.Error = ""
	if err := m.save(a); err != nil {
		return err
	}
	fail := func(err error) error {
		m.CG.Kill(id)
		_ = m.CG.Remove(id)
		m.Network.CleanupManaged(id)
		_ = syscall.Unmount(filepath.Join(m.path(id), "root"), syscall.MNT_DETACH)
		a.Status.State = "FAILED"
		a.Status.Error = err.Error()
		_ = m.save(a)
		return err
	}
	lower := filepath.Join(m.Templates, a.Spec.Template)
	if _, err := os.Stat(filepath.Join(lower, "app", "start")); err != nil {
		return fail(fmt.Errorf("managed template %s must be provisioned on worker", a.Spec.Template))
	}
	for _, d := range []string{"upper", "work", "root", "data"} {
		if err := os.MkdirAll(filepath.Join(m.path(id), d), 0755); err != nil {
			return fail(err)
		}
	}
	data := filepath.Join(m.path(id), "data")
	if err := m.ensureData(id); err != nil {
		return fail(err)
	}
	root := filepath.Join(m.path(id), "root")
	_ = syscall.Unmount(root, syscall.MNT_DETACH)
	opts := "lowerdir=" + lower + ",upperdir=" + filepath.Join(m.path(id), "upper") + ",workdir=" + filepath.Join(m.path(id), "work")
	if err := syscall.Mount("overlay", root, "overlay", syscall.MS_NODEV|syscall.MS_NOSUID, opts); err != nil {
		return fail(err)
	}
	dnsPath := filepath.Join(root, "etc", "resolv.conf")
	_ = os.Remove(dnsPath)
	if err := os.WriteFile(dnsPath, []byte("nameserver 1.1.1.1\nnameserver 8.8.8.8\n"), 0644); err != nil {
		return fail(err)
	}
	env := []byte(`{"HOME":"/data","DATA_DIR":"/data","UPTIME_KUMA_PORT":"8080","UPTIME_KUMA_HOST":"0.0.0.0","UPTIME_KUMA_DATA_DIR":"/data","NODE_ENV":"production"}`)
	if err := os.WriteFile(filepath.Join(root, "app", ".clofix-env.json"), env, 0600); err != nil {
		return fail(err)
	}
	if err := m.CG.Create(id, cgroups.Limits{MemMax: uint64(a.Spec.RAMMB) << 20, CPUQuota: a.Spec.CPU, Pids: 256}); err != nil {
		return fail(err)
	}
	fd, err := m.CG.Open(id)
	if err != nil {
		return fail(err)
	}
	defer fd.Close()
	if err := m.ensureLog(a); err != nil {
		return fail(err)
	}
	logWriter, err := os.OpenFile(filepath.Join(m.path(id), "stdout.pipe"), os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	defer logWriter.Close()
	cmd := exec.Command("/proc/self/exe", "fx-init", root, id, "--protection-mode", "--unprivileged", "--bind-mount", data, "/data", "--", "/app/start")
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET, UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 65536}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 65536}}, GidMappingsEnableSetgroups: true, UseCgroupFD: true, CgroupFD: int(fd.Fd()), Setsid: true}
	if err = cmd.Start(); err != nil {
		return fail(err)
	}
	a.PID = cmd.Process.Pid
	_ = logWriter.Close()
	go func() {
		err := cmd.Wait()
		m.Lock()
		defer m.Unlock()
		if m.apps[id] != a || a.PID != cmd.Process.Pid {
			return
		}
		if a.Status.Desired == "running" {
			a.Status.State = "FAILED"
			a.Status.Ready = false
			a.Status.Error = "application exited"
			if err != nil {
				a.Status.Error = err.Error()
			}
			_ = m.save(a)
		}
	}()
	if err = m.Network.SetupManaged(id, a.PID, a.Spec.Address); err != nil {
		return fail(err)
	}
	if err = m.gateway(a); err != nil {
		return fail(err)
	}
	a.Status.State = "RUNNING"
	return m.save(a)
}
func (m *Manager) stop(a *record) error {
	a.Status.Desired = "stopped"
	a.Status.Ready = false
	a.Status.State = "STOPPED"
	a.Status.Error = ""
	if err := m.save(a); err != nil {
		return err
	}
	m.CG.Kill(a.Spec.ID)
	for i := 0; i < 100 && m.CG.Alive(a.Spec.ID); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if m.CG.Alive(a.Spec.ID) {
		return fmt.Errorf("sandbox processes have not stopped")
	}
	_ = m.CG.Remove(a.Spec.ID)
	m.Network.CleanupManaged(a.Spec.ID)
	_ = syscall.Unmount(filepath.Join(m.path(a.Spec.ID), "root"), syscall.MNT_DETACH)
	return m.save(a)
}
func (m *Manager) gateway(a *record) error {
	if m.gateways[a.Spec.ID] != nil {
		return nil
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", a.Spec.Port))
	if err != nil {
		return err
	}
	target, _ := url.Parse("http://" + net.JoinHostPort(a.Spec.Address, "8080"))
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(r *http.Request) {
		publicHost := r.Host
		original(r)
		r.Host = publicHost
		r.Header.Del("Authorization")
		r.Header.Del("Proxy-Authorization")
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-Host")
		r.Header.Del("X-Forwarded-Proto")
		r.Header.Del("X-Forwarded-For")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Application is starting or unavailable", http.StatusServiceUnavailable)
	}
	expected := a.Spec.GatewayHash
	realm := a.Spec.ID
	slots := make(chan struct{}, 64)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "Application connection limit reached", 503)
			return
		}
		user, password, ok := r.BasicAuth()
		sum := sha256.Sum256([]byte(password))
		hash := hex.EncodeToString(sum[:])
		if !ok || user != "client" || subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
			http.Error(w, "Authentication required", 401)
			return
		}
		if r.URL.Path == "/__clofix/backup" {
			m.downloadBackup(w, r, realm)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		proxy.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}
	m.gateways[a.Spec.ID] = server
	go server.Serve(listener)
	return nil
}
func (m *Manager) Reconcile() {
	m.Lock()
	defer m.Unlock()
	for _, a := range m.apps {
		if a.Status.Desired == "stopped" {
			_ = m.gateway(a)
		}
		if a.Status.Desired == "running" {
			if err := m.ensureLog(a); err != nil {
				a.Status.Error = err.Error()
				continue
			}
			if !m.CG.Alive(a.Spec.ID) && time.Since(a.LastStart) > 30*time.Second {
				_ = m.start(a)
			} else {
				_ = m.gateway(a)
			}
		}
	}
}
func (m *Manager) Statuses() []proto.ManagedStatus {
	m.Lock()
	defer m.Unlock()
	out := []proto.ManagedStatus{}
	for id, status := range m.deleted {
		if time.Since(status.Created) > 24*time.Hour {
			delete(m.deleted, id)
			continue
		}
		out = append(out, status)
	}
	for _, a := range m.apps {
		s := a.Status
		s.Ready = false
		if m.CG.Alive(a.Spec.ID) {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(a.Spec.Address, "8080"), 50*time.Millisecond)
			if err == nil {
				s.Ready = true
				conn.Close()
			}
		}
		b, _ := os.ReadFile(filepath.Join(m.path(a.Spec.ID), "app.log"))
		if len(b) > 64<<10 {
			b = b[len(b)-(64<<10):]
		}
		s.Logs = string(b)
		out = append(out, s)
	}
	return out
}

type boundedLog struct {
	sync.Mutex
	Path string
}

func (l *boundedLog) Write(p []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	n := len(p)
	if len(p) > 1<<20 {
		p = p[len(p)-(1<<20):]
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if f, err := os.Stat(l.Path); err == nil && f.Size()+int64(len(p)) > 1<<20 {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(l.Path, flag, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	_, err = file.Write(p)
	return n, err
}

func (m *Manager) NeedsReservation(id string) bool {
	m.Lock()
	defer m.Unlock()
	a := m.apps[id]
	return a == nil || a.Status.Desired != "running"
}

func (m *Manager) saveDeleted() error {
	b, err := json.Marshal(m.deleted)
	if err != nil {
		return err
	}
	path := filepath.Join(m.Root, "deleted.json")
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// A persistent FIFO allows a restarted worker to reattach its bounded log reader.
// The app can reopen its own stdout/stderr without gaining access to a host file.
func (m *Manager) ensureLog(a *record) error {
	if a.logReader != nil {
		return nil
	}
	path := filepath.Join(m.path(a.Spec.ID), "stdout.pipe")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = syscall.Mkfifo(path, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if info.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("managed stdout must be a FIFO")
	}
	if err = os.Chown(path, 65534, 65534); err != nil {
		return err
	}
	reader, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	a.logReader = reader
	go func() { _, _ = io.Copy(&boundedLog{Path: filepath.Join(m.path(a.Spec.ID), "app.log")}, reader) }()
	return nil
}
