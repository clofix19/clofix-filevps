// Package lifecycle is the clofix-fx engine core: create/start/stop/delete of FileVPS instances using
// OverlayFS, namespaces and cgroups v2 directly. It does not use Docker, containerd, runc or LXC.
package lifecycle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/engine/cgroups"
	"github.com/clofix/filevps/worker/internal/engine/image"
	"github.com/clofix/filevps/worker/internal/engine/network"
	"github.com/clofix/filevps/worker/internal/netstats"
	"github.com/clofix/filevps/worker/pkg/proto"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var wafTagByCategory = map[string]string{
	"protocol": "attack-protocol", "lfi": "attack-lfi", "rfi": "attack-rfi",
	"rce": "attack-rce", "php": "attack-php", "generic": "attack-generic",
	"xss": "attack-xss", "sqli": "attack-sqli", "fixation": "attack-fixation",
	"method": "id:911000-911999", "iprep": "id:910000-910999",
	"dos": "id:912000-912999", "scanner": "id:913000-913999",
	"protocol_enforcement": "id:920000-920999", "multipart": "id:922000-922999",
	"nodejs": "id:934000-934999", "java": "id:944000-944999",
	"shell": "language-shell", "powershell": "language-powershell",
	"javascript": "language-javascript", "aspnet": "language-aspnet",
	"disclosure": "attack-disclosure",
	"leakage_sql": "id:951000-951999", "leakage_java": "id:952000-952999",
	"leakage_php": "id:953000-953999", "leakage_iis": "id:954000-954999",
}

type Meta struct {
	ID              string             `json:"id"`
	Spec            proto.InstanceSpec `json:"spec"`
	State           string             `json:"state"`
	Desired         string             `json:"desired"` // running|stopped
	Pid             int                `json:"pid"`
	Error           string             `json:"error,omitempty"`
	S3MountPath     string             `json:"s3_mount_path,omitempty"`
	ServiceInstalls map[string]string  `json:"service_installs,omitempty"`
	Created         time.Time          `json:"created"`
	HostUID         uint32             `json:"host_uid,omitempty"`
}

type Engine struct {
	StateDir string
	LogDir   string
	Cfg      config.Config
	Images   *image.Manager
	CG       *cgroups.Manager
	Network  *network.Manager

	mu                sync.Mutex
	meta              map[string]*Meta
	cpuSamples        map[string]cpuSample
	netSamples        map[string]netSample
	diskMu            sync.Mutex
	volumeMu          sync.Mutex
	diskSamples       map[string]diskSample
	serviceUsageMu    sync.Mutex
	serviceCPUSamples map[string]serviceCPUSample
	s3Mu              sync.Mutex
	s3Mounts          map[string]*s3MountProcess
	efsMu             sync.Mutex
	efsMounts         map[string]*s3MountProcess
	dnsMu             sync.Mutex
	dnsHosts          []proto.DNSHost
	fwMu              sync.Mutex
	hostFW            proto.FirewallSpec
	vpsFW             map[string]proto.FirewallSpec
}

func (e *Engine) SetDNSHosts(hosts []proto.DNSHost) {
	e.dnsMu.Lock()
	e.dnsHosts = append([]proto.DNSHost(nil), hosts...)
	e.dnsMu.Unlock()
	e.mu.Lock()
	ids := make([]string, 0, len(e.meta))
	for id := range e.meta {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		if m, err := e.get(id); err == nil && isMounted(e.merged(id)) {
			_ = e.configureHostname(m)
		}
	}
}

type cpuSample struct {
	usage uint64
	at    time.Time
}

type netSample struct {
	rx uint64
	tx uint64
	at time.Time
}

type diskSample struct {
	bytes uint64
	at    time.Time
}

type serviceCPUSample struct {
	ticks uint64
	at    time.Time
}

func New(cfg config.Config) *Engine {
	st := config.StateDir()
	shift := uint32(0)
	if cfg.UserNS {
		shift = cfg.UIDBase
	}
	return &Engine{
		StateDir: st, LogDir: envOr("CLOFIX_LOG_DIR", "/var/log/clofix-filevps"), Cfg: cfg,
		Images: image.New(filepath.Join(st, "images", "cache"), shift),
		CG:     cgroups.New(), Network: network.New(), meta: map[string]*Meta{}, cpuSamples: map[string]cpuSample{}, netSamples: map[string]netSample{}, diskSamples: map[string]diskSample{}, serviceCPUSamples: map[string]serviceCPUSample{}, s3Mounts: map[string]*s3MountProcess{}, efsMounts: map[string]*s3MountProcess{},
	}
}

func protectionEnabled(spec proto.InstanceSpec) bool {
	return spec.ProtectionMode == nil || *spec.ProtectionMode
}

func sshLoginBlocked(spec proto.InstanceSpec) bool {
	return spec.SSHLoginBlocked != nil && *spec.SSHLoginBlocked
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

const legacyDefaultGuestInit = "mkdir -p /run/sshd && ssh-keygen -A && exec /usr/sbin/sshd -D -e"

func defaultGuestInit() []string {
	boot := "mkdir -p /run/sshd /run/nginx /run/modsecurity /var/log/nginx /var/cache/nginx; chown www-data:www-data /run/modsecurity 2>/dev/null || true; " +
		"ssh-keygen -A; " +
		"for svc in nginx apache2 mysql mariadb postgresql redis-server haproxy memcached supervisor tomcat10 wildfly; do if [ -x /etc/init.d/$svc ]; then service $svc start || true; fi; done; " +
		"for f in /etc/init.d/php*-fpm; do [ -x $f ] && service ${f##*/} start || true; done; " +
		"exec /usr/sbin/sshd -D -e"
	return []string{"/bin/sh", "-c", boot}
}

func (e *Engine) idir(id string) string { return filepath.Join(e.StateDir, "instances", id) }
func (e *Engine) merged(id string) string {
	return filepath.Join(e.idir(id), "rootfs-mount")
}

func (e *Engine) save(m *Meta) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(e.idir(m.ID), "metadata.json"), b, 0o640)
}

// Load reads persisted instance metadata (local desired-state cache; survives controller outages).
func (e *Engine) Load() error {
	if err := os.MkdirAll(filepath.Join(e.StateDir, "instances"), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(e.LogDir, "instances"), 0o750); err != nil {
		return err
	}
	ents, _ := os.ReadDir(filepath.Join(e.StateDir, "instances"))
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, d := range ents {
		b, err := os.ReadFile(filepath.Join(e.StateDir, "instances", d.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil && validID.MatchString(m.ID) {
			if m.Spec.SwapMB == nil {
				swapMB := proto.DefaultSwapMB
				m.Spec.SwapMB = &swapMB
				_ = e.save(&m)
			}
			e.meta[m.ID] = &m
		}
	}
	return e.CG.Init()
}

// Reconcile aligns reality with persisted desired state after a worker or host restart.
func (e *Engine) Reconcile() {
	e.mu.Lock()
	var ids []string
	for id := range e.meta {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.mu.Lock()
		m := e.meta[id]
		alive := e.CG.Alive(id)
		want := m.Desired
		e.mu.Unlock()
		switch {
		case want == "running" && !alive:
			log.Printf("reconcile: starting %s", id)
			if err := e.Start(id); err != nil {
				log.Printf("reconcile: start %s: %v", id, err)
			}
		case want != "running" && alive:
			e.Stop(id)
		case want != "running" && !alive:
			e.stopS3Mount(id)
			e.cleanupMounts(id)
		}
	}
}

func (e *Engine) get(id string) (*Meta, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.meta[id]
	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}
	return m, nil
}

func (e *Engine) setState(m *Meta, state, errMsg string) {
	e.mu.Lock()
	m.State, m.Error = state, errMsg
	e.save(m)
	e.mu.Unlock()
}

func (e *Engine) configureSSHFiles(id string, spec proto.InstanceSpec, owner int) error {
	if spec.SSHAuthorizedKey != "" {
		if len(spec.SSHAuthorizedKey) > 8192 || strings.ContainsAny(spec.SSHAuthorizedKey, "\r\n") {
			return errors.New("invalid SSH authorized key")
		}
	}
	upper := e.upperDir(id)
	sshDir := filepath.Join(upper, "root", ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(upper, "root"), sshDir} {
		if err := os.Lchown(p, owner, owner); err != nil {
			return err
		}
	}
	if spec.SSHAuthorizedKey != "" {
		keyPath := filepath.Join(sshDir, "authorized_keys")
		if err := os.WriteFile(keyPath, []byte(spec.SSHAuthorizedKey+"\n"), 0o600); err != nil {
			return err
		}
		if err := os.Lchown(keyPath, owner, owner); err != nil {
			return err
		}
	}
	sshdConfigDir := filepath.Join(upper, "etc", "ssh", "sshd_config.d")
	if err := os.MkdirAll(sshdConfigDir, 0o755); err != nil {
		return err
	}
	sshdConfig := filepath.Join(sshdConfigDir, "00-clofix.conf")
	configBody := sshdAuthConfig(spec)
	if sshLoginBlocked(spec) {
		configBody += "DenyUsers *\n"
	}
	if err := os.WriteFile(sshdConfig, []byte(configBody), 0o644); err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(upper, "etc"), filepath.Join(upper, "etc", "ssh"), sshdConfigDir, sshdConfig} {
		if err := os.Lchown(p, owner, owner); err != nil {
			return err
		}
	}
	return nil
}

func sshdAuthConfig(spec proto.InstanceSpec) string {
	brand := "Banner /etc/issue.net\nPrintMotd yes\n"
	if protectionEnabled(spec) {
		brand += "ForceCommand /etc/clofix-protected-shell\n"
	}
	if spec.PasswordAuth {
		body := "UsePAM yes\nPermitRootLogin yes\nPasswordAuthentication yes\nKbdInteractiveAuthentication yes\n"
		if spec.SSHAuthorizedKey != "" {
			body += "PubkeyAuthentication yes\n"
		} else {
			body += "PubkeyAuthentication no\n"
		}
		return body + brand
	}
	return "UsePAM no\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nAuthenticationMethods publickey\n" + brand
}

func (e *Engine) updatePasswordAuth(id string, enabled bool) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	m.Spec.PasswordAuth = enabled
	if err := e.configureSSHFiles(id, m.Spec, e.guestOwner(m)); err != nil {
		return err
	}
	return e.save(m)
}

func (e *Engine) setRootPassword(id, password string) error {
	if len(password) < 8 || len(password) > 128 || strings.ContainsAny(password, "\r\n:") {
		return errors.New("invalid root password")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	hash, err := cryptSHA512(password)
	if err != nil {
		return err
	}
	if err := e.writeRootShadow(id, m, hash); err != nil {
		return err
	}
	if e.CG.Alive(id) && m.Pid > 0 {
		script := "usermod -p '" + strings.ReplaceAll(hash, "'", "") + "' root; kill -HUP 1 2>/dev/null || true"
		_ = e.guestExec(id, m.Pid, "/bin/sh", []string{"-c", script})
	}
	return nil
}

func cryptSHA512(password string) (string, error) {
	cmd := exec.Command("openssl", "passwd", "-6", "-stdin")
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	hash := strings.TrimSpace(string(out))
	if !strings.HasPrefix(hash, "$6$") {
		return "", errors.New("openssl did not return a SHA-512 crypt hash")
	}
	return hash, nil
}

func (e *Engine) writeRootShadow(id string, m *Meta, hash string) error {
	owner := e.guestOwner(m)
	upperEtc := filepath.Join(e.upperDir(id), "etc")
	if err := os.MkdirAll(upperEtc, 0o755); err != nil {
		return err
	}
	src := filepath.Join(e.merged(id), "etc", "shadow")
	if _, err := os.Stat(src); err != nil {
		if root, imgErr := e.Images.Ensure(m.Spec.Image); imgErr == nil {
			src = filepath.Join(root, "etc", "shadow")
		}
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		raw = []byte("root:*:19600:0:99999:7:::\n")
	}
	var lines []string
	replaced := false
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "root:") {
			fields := strings.Split(line, ":")
			if len(fields) < 9 {
				fields = []string{"root", hash, "19600", "0", "99999", "7", "", "", ""}
			} else {
				fields[1] = hash
			}
			line = strings.Join(fields, ":")
			replaced = true
		}
		lines = append(lines, line)
	}
	if !replaced {
		lines = append([]string{"root:" + hash + ":19600:0:99999:7:::"}, lines...)
	}
	path := filepath.Join(upperEtc, "shadow")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		return err
	}
	return os.Lchown(path, owner, owner)
}

// Create prepares metadata and writable-layer directories. Idempotent.
func (e *Engine) Create(id string, spec proto.InstanceSpec) error {
	if !validID.MatchString(id) {
		return errors.New("invalid instance id")
	}
	e.mu.Lock()
	existing, hadExisting := e.meta[id]
	if hadExisting && e.CG.Alive(id) {
		e.mu.Unlock()
		return nil
	}
	hostUID := uint32(0)
	if hadExisting {
		hostUID = existing.HostUID
	}
	if e.Cfg.UserNS && hostUID == 0 {
		uid, err := e.allocateHostUIDLocked()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		hostUID = uid
		if hadExisting {
			existing.HostUID = hostUID
		} else {
			e.meta[id] = &Meta{ID: id, HostUID: hostUID, State: proto.StateStopped}
		}
	}
	e.mu.Unlock()
	if spec.SwapMB == nil {
		swapMB := proto.DefaultSwapMB
		spec.SwapMB = &swapMB
	}
	if spec.BackupID != "" {
		var manifest backupManifest
		b, err := os.ReadFile(filepath.Join(e.backupDir(spec.BackupID), "backup.json"))
		if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Info.State != "READY" {
			return errors.New("backup is missing or not ready on this worker")
		}
		spec.Image = manifest.Image
	}
	if spec.RAMMB <= 0 || spec.CPU <= 0 || *spec.SwapMB < 0 {
		return errors.New("spec requires positive ram_mb and cpu and non-negative swap_mb")
	}
	if spec.SSHAuthorizedKey == "" && !spec.PasswordAuth {
		return errors.New("SSH public key or PAM password auth is required")
	}
	if _, err := e.Images.Ensure(spec.Image); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	if len(spec.Init) == 0 {
		spec.Init = defaultGuestInit()
	}
	if spec.ProtectionMode == nil {
		enabled := true
		spec.ProtectionMode = &enabled
	}
	if err := os.MkdirAll(e.merged(id), 0o750); err != nil {
		return err
	}
	if err := e.ensureDiskQuota(&Meta{ID: id, Spec: spec, HostUID: hostUID}); err != nil {
		return err
	}
	for _, d := range []string{e.upperDir(id), e.workDir(id)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	// Keep the upper layer owned by the guest root according to the selected UID map.
	owner := 0
	if e.Cfg.UserNS {
		owner = int(hostUID)
	}
	upper := e.upperDir(id)
	if err := os.Chown(upper, owner, owner); err != nil {
		return fmt.Errorf("set upper layer ownership: %w", err)
	}
	if err := os.Chmod(upper, 0o755); err != nil {
		return fmt.Errorf("set upper layer permissions: %w", err)
	}
	if spec.BackupID != "" {
		backupID := spec.BackupID
		if err := e.restoreBackup(id, backupID); err != nil {
			return fmt.Errorf("restore backup files: %w", err)
		}
		spec.BackupID = ""
		// Backups contain only the writable root layer; host mounts and port assignments
		// are deliberately not copied into the new guest.
		spec.StorageMounts, spec.S3MountPath, spec.S3ConnectionID, spec.EFSMounts = nil, "", "", nil
	}
	if err := e.configureSSHFiles(id, spec, owner); err != nil {
		return err
	}
	created := time.Now()
	if hadExisting {
		created = existing.Created
	}
	m := &Meta{ID: id, Spec: spec, State: proto.StateStopped, Desired: "stopped", Created: created, HostUID: hostUID}
	e.mu.Lock()
	e.meta[id] = m
	err := e.save(m)
	e.mu.Unlock()
	return err
}

func isMounted(path string) bool {
	b, _ := os.ReadFile("/proc/self/mountinfo")
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 4 && f[4] == path {
			return true
		}
	}
	return false
}

func (e *Engine) mountRoot(m *Meta) error {
	merged := e.merged(m.ID)
	if isMounted(merged) {
		return nil
	}
	lower, err := e.Images.Ensure(m.Spec.Image)
	if err != nil {
		return err
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, e.upperDir(m.ID), e.workDir(m.ID))
	if err := syscall.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return fmt.Errorf("overlay mount: %w", err)
	}
	return nil
}

// prepareRootLayer makes the overlay root traversable by guest services running as
// unprivileged users. Existing layers may have been created while UID shifting was
// enabled and retained when local Docker Desktop switched to no-userns mode.
func (e *Engine) prepareRootLayer(id string) error {
	m, _ := e.get(id)
	owner := e.guestOwner(m)
	for _, path := range []string{e.upperDir(id), e.merged(id)} {
		if err := os.Chown(path, owner, owner); err != nil {
			return fmt.Errorf("set root layer ownership on %s: %w", filepath.Base(path), err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			return fmt.Errorf("set root layer permissions on %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func (e *Engine) cleanupMounts(id string) {
	merged := e.merged(id)
	if isMounted(merged) {
		if err := syscall.Unmount(merged, 0); err != nil {
			syscall.Unmount(merged, syscall.MNT_DETACH)
		}
	}
	e.unmountLayer(id)
}

func guestHostname(m *Meta) string {
	if m.Spec.Hostname != "" && !strings.ContainsAny(m.Spec.Hostname, "\r\n\x00 /") {
		return m.Spec.Hostname
	}
	return m.ID
}

func (e *Engine) configureHostname(m *Meta) error {
	host := guestHostname(m)
	root := e.merged(m.ID)
	owner := e.guestOwner(m)
	hostnamePath := filepath.Join(root, "etc", "hostname")
	if err := os.MkdirAll(filepath.Dir(hostnamePath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(hostnamePath, []byte(host+"\n"), 0o644); err != nil {
		return fmt.Errorf("write /etc/hostname: %w", err)
	}
	hosts := "127.0.0.1\tlocalhost\n127.0.1.1\t" + host + "\n::1\tlocalhost ip6-localhost ip6-loopback\n"
	e.dnsMu.Lock()
	extra := append([]proto.DNSHost(nil), e.dnsHosts...)
	e.dnsMu.Unlock()
	for _, rec := range extra {
		if rec.Name == "" || rec.IP == "" {
			continue
		}
		hosts += rec.IP + "\t" + rec.Name + "\n"
	}
	hostsPath := filepath.Join(root, "etc", "hosts")
	if err := os.WriteFile(hostsPath, []byte(hosts), 0o644); err != nil {
		return fmt.Errorf("write /etc/hosts: %w", err)
	}
	for _, p := range []string{hostnamePath, hostsPath} {
		if err := os.Lchown(p, owner, owner); err != nil {
			return fmt.Errorf("set hostname file ownership: %w", err)
		}
	}
	return nil
}

func (e *Engine) configureGuestBrand(m *Meta) error {
	host := guestHostname(m)
	root := e.merged(m.ID)
	etc := filepath.Join(root, "etc")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		return err
	}
	owner := e.guestOwner(m)
	motd := "CloFix FileVPS\nHostname: " + host + "\nInstance: " + m.ID + "\n"
	issue := "CloFix FileVPS · " + host + "\n"
	if protectionEnabled(m.Spec) {
		motd = "CloFix Security\nProtection Mode is enabled.\nGuest root is read-only. Commands are not available over SSH.\nDisable Protection Mode in CloFix Admin to open a shell.\n"
		issue = "CloFix Security · Protection Mode\n"
	}
	release := "NAME=CloFix FileVPS\nVERSION=0.1.0\n"
	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"motd", motd, 0o644},
		{"issue.net", issue, 0o644},
		{"clofix-release", release, 0o644},
		{"clofix-protected-shell", "#!/bin/sh\ncat /etc/motd\nexit 0\n", 0o755},
	}
	for _, f := range files {
		path := filepath.Join(etc, f.name)
		if err := os.WriteFile(path, []byte(f.body), f.mode); err != nil {
			return fmt.Errorf("write /etc/%s: %w", f.name, err)
		}
		if err := os.Lchown(path, owner, owner); err != nil {
			return fmt.Errorf("set /etc/%s ownership: %w", f.name, err)
		}
	}
	return nil
}

func (e *Engine) configureDNS(m *Meta) error {
	if m.Spec.IPAddress == "" {
		return nil
	}
	p := filepath.Join(e.merged(m.ID), "etc", "resolv.conf")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(p, []byte("nameserver 1.1.1.1\nnameserver 8.8.8.8\n"), 0o644)
}

// ensureSSHHostKeys creates persistent per-guest SSH host keys before the guest
// root is remounted read-only. ssh-keygen -A leaves existing keys unchanged.
func (e *Engine) ensureSSHHostKeys(m *Meta) error {
	root := e.merged(m.ID)
	if _, err := os.Stat(filepath.Join(root, "usr/sbin/sshd")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// The bundled rootfs intentionally omits device nodes. ssh-keygen expects
	// /dev/null even when it runs inside the guest chroot, so temporarily bind
	// the harmless host device until key generation is complete. fx-init will
	// later create the guest's isolated /dev tmpfs.
	devDir := filepath.Join(root, "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		return fmt.Errorf("prepare guest /dev: %w", err)
	}
	nullPath := filepath.Join(devDir, "null")
	nullFile, err := os.OpenFile(nullPath, os.O_CREATE|os.O_RDONLY, 0o666)
	if err != nil {
		return fmt.Errorf("prepare guest /dev/null: %w", err)
	}
	if err := nullFile.Close(); err != nil {
		return err
	}
	if err := syscall.Mount("/dev/null", nullPath, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind /dev/null for SSH key generation: %w", err)
	}
	defer syscall.Unmount(nullPath, 0)
	cmd := exec.Command("chroot", root, "/usr/bin/ssh-keygen", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare guest SSH host keys: %s: %w", strings.TrimSpace(string(out)), err)
	}
	owner := e.guestOwner(m)
	keys, err := filepath.Glob(filepath.Join(root, "etc/ssh/ssh_host_*_key*"))
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := os.Chown(key, owner, owner); err != nil {
			return fmt.Errorf("set SSH host key ownership: %w", err)
		}
	}
	// Unique uid ranges leave the shared OverlayFS lower as overflowuid for
	// later guests. sshd+PAM then denies root before pubkey auth. Pubkey-only
	// without PAM still reads /root/.ssh/authorized_keys from the upper layer.
	dropInDir := filepath.Join(root, "etc/ssh/sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		return fmt.Errorf("prepare sshd drop-in: %w", err)
	}
	dropIn := filepath.Join(dropInDir, "99-clofix.conf")
	if err := os.WriteFile(dropIn, []byte(sshdAuthConfig(m.Spec)), 0o644); err != nil {
		return fmt.Errorf("write sshd drop-in: %w", err)
	}
	if err := os.Chown(dropIn, owner, owner); err != nil {
		return fmt.Errorf("set sshd drop-in ownership: %w", err)
	}
	return nil
}

// Start composes the rootfs, creates the cgroup and launches the instance init in new namespaces.
func (e *Engine) Start(id string) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if len(m.Spec.Init) == 3 && m.Spec.Init[0] == "/bin/sh" && m.Spec.Init[1] == "-c" && m.Spec.Init[2] == legacyDefaultGuestInit {
		m.Spec.Init = defaultGuestInit()
		if err := e.save(m); err != nil {
			return fmt.Errorf("upgrade guest boot command: %w", err)
		}
	}
	if e.CG.Alive(id) {
		return nil
	}
	e.setState(m, proto.StateStarting, "")
	fail := func(err error) error {
		e.setState(m, proto.StateFailed, err.Error())
		e.CG.Kill(id)
		e.CG.Remove(id)
		e.stopS3Mount(id)
		e.Network.Cleanup(id, m.Spec.SSHPort)
		e.cleanupMounts(id)
		return err
	}

	if err := e.ensureHostUID(m); err != nil {
		return fail(err)
	}
	if err := e.ensureDiskQuota(m); err != nil {
		return fail(err)
	}
	if err := e.prepareRootLayer(id); err != nil {
		return fail(err)
	}
	if err := e.mountRoot(m); err != nil {
		return fail(err)
	}
	if err := image.EnsureStickyTmp(e.merged(id)); err != nil {
		return fail(err)
	}
	if err := e.configureSSHFiles(id, m.Spec, e.guestOwner(m)); err != nil {
		return fail(fmt.Errorf("configure SSH: %w", err))
	}
	if err := e.configureHostname(m); err != nil {
		return fail(fmt.Errorf("configure hostname: %w", err))
	}
	if err := e.configureGuestBrand(m); err != nil {
		return fail(fmt.Errorf("configure guest brand: %w", err))
	}
	if err := e.configureDNS(m); err != nil {
		return fail(fmt.Errorf("configure DNS: %w", err))
	}
	if err := e.ensureSSHHostKeys(m); err != nil {
		return fail(err)
	}
	mem := uint64(m.Spec.RAMMB) << 20
	if err := e.CG.Create(id, cgroups.Limits{MemMax: mem, MemHigh: mem / 10 * 9, SwapMax: uint64(proto.SwapLimitMB(m.Spec)) << 20, CPUQuota: m.Spec.CPU, Pids: orDefault(m.Spec.Pids, 512)}); err != nil {
		return fail(err)
	}
	cgf, err := e.CG.Open(id)
	if err != nil {
		return fail(err)
	}
	defer cgf.Close()
	logf, err := os.OpenFile(filepath.Join(e.LogDir, "instances", id+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fail(err)
	}
	defer logf.Close()

	host := guestHostname(m)
	args := []string{"fx-init", e.merged(id), host}
	if protectionEnabled(m.Spec) {
		args = append(args, "--protection-mode")
	}
	if err := e.writeGuestMeminfo(m); err != nil {
		return fail(err)
	}
	args = append(args, "--meminfo-file", e.meminfoPath(id))
	afterMount := userNSAfterMount()
	if e.Cfg.UserNS && afterMount {
		args = append(args, "--user-ns-host-uid", fmt.Sprint(e.guestOwner(m)))
	}
	var s3HostPath string
	if m.S3MountPath != "" {
		s3HostPath, err = e.startS3Mount(id, int(cgf.Fd()), logf)
		if err != nil {
			return fail(err)
		}
		args = append(args, "--bind-mount", s3HostPath, m.S3MountPath)
	}
	for _, mount := range m.Spec.EFSMounts {
		if mount.GuestPath == "" {
			continue
		}
		if err := e.ensureEFSHost(mount.ID); err != nil {
			return fail(err)
		}
		args = append(args, "--bind-mount", efsHostPath(e.StateDir, mount.ID), mount.GuestPath)
	}
	for _, mount := range m.Spec.StorageMounts {
		if mount.GuestPath == "" {
			continue
		}
		volumePath := filepath.Join(e.StateDir, "volumes", mount.ID)
		if _, err := e.volumePath(mount.ID); err != nil {
			return fail(err)
		}
		if err := e.ensureVolumeMounted(mount.ID); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(volumePath); err != nil {
			return fail(fmt.Errorf("storage volume %s is missing on this worker", mount.ID))
		}
		args = append(args, "--bind-mount", volumePath, mount.GuestPath)
	}
	args = append(args, "--")
	args = append(args, m.Spec.Init...)
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	// Cgroup is created and CgroupFD is set first so the child is placed in
	// /clofix-filevps/<id> before CLONE_NEWCGROUP virtualizes the view to 0::/.
	flags := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET | cloneNewCgroup)
	sp := &syscall.SysProcAttr{CgroupFD: int(cgf.Fd()), UseCgroupFD: true, Setsid: true}
	if e.Cfg.UserNS && !afterMount {
		flags |= syscall.CLONE_NEWUSER
		hostUID := int(e.guestOwner(m))
		sp.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: uidRangeSize}}
		sp.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: uidRangeSize}}
		sp.GidMappingsEnableSetgroups = true
		// After uid_map is written, the child is still host uid 0 (unmapped →
		// overflowuid, no caps). setuid(0) here runs in the new ns so the
		// process becomes mapped root (kuid=hostUID) with CAP_SYS_ADMIN.
		sp.Credential = &syscall.Credential{Uid: 0, Gid: 0}
	} else if !e.Cfg.UserNS {
		log.Printf("WARNING: instance %s starts WITHOUT a user namespace (container root == host root)", id)
	}
	sp.Cloneflags = flags
	cmd.SysProcAttr = sp
	if err := cmd.Start(); err != nil {
		e.CG.Remove(id)
		return fail(fmt.Errorf("launch: %w", err))
	}
	e.mu.Lock()
	m.Pid, m.Desired = cmd.Process.Pid, "running"
	e.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if e.Cfg.UserNS && afterMount {
		if err := applyUserNSMapping(cmd.Process.Pid, int(e.guestOwner(m))); err != nil {
			e.CG.Kill(id)
			<-done
			e.CG.Remove(id)
			return fail(fmt.Errorf("user namespace mapping: %w", err))
		}
	}
	if m.Spec.IPAddress != "" {
		// Let fx-init finish namespace/root setup before moving the veth into its netns.
		// If it exited early (for example, mount setup was denied), report that cause
		// instead of a misleading `ip link ... netns <pid>: No such process` error.
		select {
		case werr := <-done:
			e.CG.Remove(id)
			return fail(fmt.Errorf("init exited before network setup (%v); see %s", werr, filepath.Join(e.LogDir, "instances", id+".log")))
		case <-time.After(100 * time.Millisecond):
		}
		if err := e.Network.Setup(id, cmd.Process.Pid, m.Spec); err != nil {
			e.CG.Kill(id)
			<-done
			e.CG.Remove(id)
			return fail(fmt.Errorf("network setup: %w", err))
		}
		e.applyRememberedVPSFirewall(id, m.Spec.IPAddress)
	}
	select {
	case werr := <-done: // exited immediately: setup failed
		return fail(fmt.Errorf("init exited during startup (%v); see %s", werr, filepath.Join(e.LogDir, "instances", id+".log")))
	case <-time.After(1500 * time.Millisecond):
	}
	e.setState(m, proto.StateRunning, "")
	go func() { // reap and record exit
		<-done
		e.mu.Lock()
		stillWanted := m.Desired == "running" && m.State == proto.StateRunning
		e.mu.Unlock()
		if stillWanted {
			_ = e.Stop(id)
			e.setState(m, proto.StateStopped, "instance init exited")
		}
	}()
	return nil
}

func orDefault(v, d int64) int64 {
	if v > 0 {
		return v
	}
	return d
}

// Stop sends SIGTERM to the instance, waits, then kills the whole cgroup and unmounts the rootfs.
func (e *Engine) Stop(id string) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	m.Desired = "stopped"
	e.mu.Unlock()
	e.setState(m, proto.StateStopping, "")
	e.CG.Signal(id, syscall.SIGTERM)
	for i := 0; i < 100 && e.CG.Alive(id); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if e.CG.Alive(id) {
		e.CG.Kill(id)
		for i := 0; i < 50 && e.CG.Alive(id); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	e.stopS3Mount(id)
	e.CG.Remove(id)
	e.Network.Cleanup(id, m.Spec.SSHPort)
	e.cleanupMounts(id)
	e.mu.Lock()
	m.Pid = 0
	e.mu.Unlock()
	e.setState(m, proto.StateStopped, "")
	return nil
}

// Delete stops the instance and removes its writable layer. Only paths under this instance's directory are touched.
func (e *Engine) Delete(id string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid instance id")
	}
	m, err := e.get(id)
	if err != nil {
		return nil // already gone: idempotent
	}
	if m.Spec.DeleteProtection {
		return errors.New("VPS Delete Protection is enabled; disable it before deleting")
	}
	efsIDs := make([]string, 0, len(m.Spec.EFSMounts))
	for _, mount := range m.Spec.EFSMounts {
		if mount.ID != "" {
			efsIDs = append(efsIDs, mount.ID)
		}
	}
	if err := e.Stop(id); err != nil {
		return err
	}
	if isMounted(e.merged(id)) {
		return errors.New("rootfs still mounted; refusing to delete data")
	}
	if err := os.RemoveAll(e.idir(id)); err != nil {
		return err
	}
	os.Remove(filepath.Join(e.LogDir, "instances", id+".log"))
	e.mu.Lock()
	delete(e.meta, id)
	e.mu.Unlock()
	for _, eid := range efsIDs {
		if !e.localEFSUsers(eid) {
			e.stopEFSHost(eid)
		}
	}
	return nil
}

// Apply runs a controller operation idempotently.
func (e *Engine) Apply(op proto.Operation) error {
	switch op.Action {
	case proto.ActionCreate:
		if op.Spec == nil {
			return errors.New("create requires spec")
		}
		if err := e.Create(op.InstanceID, *op.Spec); err != nil {
			return err
		}
		if op.RootPassword != "" {
			if err := e.setRootPassword(op.InstanceID, op.RootPassword); err != nil {
				return err
			}
		}
		return e.Start(op.InstanceID)
	case proto.ActionSetPassword:
		if op.RootPassword == "" {
			return errors.New("password update requires root_password")
		}
		if op.Spec != nil {
			if err := e.updatePasswordAuth(op.InstanceID, op.Spec.PasswordAuth); err != nil {
				return err
			}
		}
		return e.setRootPassword(op.InstanceID, op.RootPassword)
	case proto.ActionUpdatePasswordAuth:
		if op.Spec == nil {
			return errors.New("password auth update requires instance spec")
		}
		return e.updatePasswordAuth(op.InstanceID, op.Spec.PasswordAuth)
	case proto.ActionStart:
		return e.Start(op.InstanceID)
	case proto.ActionStop:
		return e.Stop(op.InstanceID)
	case proto.ActionRestart:
		return e.Restart(op.InstanceID)
	case proto.ActionServiceControl:
		return e.ControlService(op.InstanceID, op.ServiceName, op.ServiceAction)
	case proto.ActionInstallService:
		return e.InstallService(op.InstanceID, op.ServiceID)
	case proto.ActionUninstallService:
		return e.UninstallService(op.InstanceID, op.ServiceID)
	case proto.ActionDelete:
		return e.Delete(op.InstanceID)
	case proto.ActionUpdatePorts:
		if op.Spec == nil {
			return errors.New("port update requires instance spec")
		}
		return e.UpdatePorts(op.InstanceID, *op.Spec)
	case proto.ActionUpdateFirewall:
		if op.Firewall == nil {
			return errors.New("firewall update requires spec")
		}
		return e.UpdateFirewall(*op.Firewall)
	case proto.ActionUpdateEFS:
		if op.EFS == nil {
			return errors.New("EFS update requires share")
		}
		return e.UpdateEFS(op.InstanceID, *op.EFS)
	case proto.ActionUpdateResources:
		if op.Spec == nil {
			return errors.New("resource update requires instance spec")
		}
		return e.UpdateResources(op.InstanceID, *op.Spec)
	case proto.ActionUpdateStorageMounts:
		if op.Spec == nil {
			return errors.New("storage mount update requires instance spec")
		}
		return e.UpdateStorageMounts(op.InstanceID, op.Spec.StorageMounts)
	case proto.ActionCreateVolume:
		return e.CreateVolume(op.VolumeID, op.VolumeLimitMB)
	case proto.ActionDeleteVolume:
		return e.DeleteVolume(op.VolumeID)
	case proto.ActionCreateBackup:
		return e.CreateBackup(op.InstanceID, op.BackupID, op.WorkerID)
	case proto.ActionDeleteBackup:
		return e.DeleteBackup(op.BackupID)
	case proto.ActionUpdateProtection:
		if op.Spec == nil || op.Spec.ProtectionMode == nil {
			return errors.New("protection update requires protection_mode")
		}
		return e.UpdateProtection(op.InstanceID, *op.Spec.ProtectionMode)
	case proto.ActionUpdateSSHLogin:
		if op.Spec == nil || op.Spec.SSHLoginBlocked == nil {
			return errors.New("SSH login update requires ssh_login_blocked")
		}
		return e.UpdateSSHLogin(op.InstanceID, *op.Spec.SSHLoginBlocked)
	case proto.ActionUpdateDeleteProtection:
		if op.Spec == nil {
			return errors.New("delete protection update requires instance spec")
		}
		return e.UpdateDeleteProtection(op.InstanceID, op.Spec.DeleteProtection)
	case proto.ActionUpdateWAF:
		return e.UpdateWAFCategory(op.InstanceID, op.WAFCategory, op.WAFEnabled)
	case proto.ActionUpdateWAFErrorPages:
		if op.Spec == nil {
			return errors.New("WAF error page update requires instance spec")
		}
		return e.UpdateWAFErrorPages(op.InstanceID, op.Spec.WAFErrorPages)
	case proto.ActionUpdateS3Mount:
		if op.UnmountS3 {
			return e.UpdateS3Mount(op.InstanceID, nil)
		}
		if op.S3Mount == nil {
			return errors.New("S3 mount update requires S3 mount settings")
		}
		return e.UpdateS3Mount(op.InstanceID, op.S3Mount)
	}
	return fmt.Errorf("unknown action %q", op.Action)
}

// UpdateResources stores the requested limits and recreates the guest cgroup.
// DiskMB sizes the guest overlay backing image. Changing the size after the
// image exists is not supported; the running guest still restarts for RAM/CPU.
func (e *Engine) UpdateResources(id string, requested proto.InstanceSpec) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if requested.RAMMB < 128 || requested.RAMMB > 1<<20 || requested.CPU < .1 || requested.CPU > 1024 || requested.DiskMB < 128 || requested.DiskMB > 1<<30 || proto.SwapLimitMB(requested) < 0 || proto.SwapLimitMB(requested) > 1<<20 {
		return errors.New("resource values are outside supported limits")
	}
	previous := m.Spec
	m.Spec.RAMMB, m.Spec.CPU, m.Spec.DiskMB = requested.RAMMB, requested.CPU, requested.DiskMB
	swap := proto.SwapLimitMB(requested)
	m.Spec.SwapMB = &swap
	if err := e.save(m); err != nil {
		m.Spec = previous
		return fmt.Errorf("save VPS resource settings: %w", err)
	}
	if e.CG.Alive(id) {
		return e.Restart(id)
	}
	return nil
}

func (e *Engine) UpdateStorageMounts(id string, mounts []proto.StorageMount) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
	for _, mount := range mounts {
		if !validID.MatchString(mount.ID) || (mount.GuestPath != "" && !validStorageGuestPath(mount.GuestPath)) || seenIDs[mount.ID] || (mount.GuestPath != "" && seenPaths[mount.GuestPath]) || (mount.GuestPath != "" && pathsOverlap(mount.GuestPath, m.Spec.S3MountPath)) {
			return errors.New("invalid or duplicate storage mount")
		}
		for _, efs := range m.Spec.EFSMounts {
			if mount.GuestPath != "" && pathsOverlap(mount.GuestPath, efs.GuestPath) {
				return errors.New("storage mount paths cannot overlap EFS")
			}
		}
		seenIDs[mount.ID] = true
		if mount.GuestPath != "" {
			for existing := range seenPaths {
				if pathsOverlap(existing, mount.GuestPath) {
					return errors.New("storage mount paths cannot overlap")
				}
			}
			seenPaths[mount.GuestPath] = true
		}
	}
	previous := append([]proto.StorageMount(nil), m.Spec.StorageMounts...)
	m.Spec.StorageMounts = append([]proto.StorageMount(nil), mounts...)
	if err := e.save(m); err != nil {
		m.Spec.StorageMounts = previous
		return fmt.Errorf("save VPS storage mounts: %w", err)
	}
	if e.CG.Alive(id) {
		return e.Restart(id)
	}
	return nil
}

func (e *Engine) volumePath(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", errors.New("invalid volume id")
	}
	return filepath.Join(e.StateDir, "volumes", id), nil
}

func (e *Engine) volumeImagePath(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", errors.New("invalid volume id")
	}
	return filepath.Join(e.StateDir, "volume-images", id+".img"), nil
}

// CreateVolume creates an ordinary directory when unlimited, or an ext4
// loopback filesystem when a hard size limit is requested.
func (e *Engine) CreateVolume(id string, limitMB int64) error {
	p, err := e.volumePath(id)
	if err != nil {
		return err
	}
	if limitMB < 0 || limitMB > 1_048_576 || (limitMB > 0 && limitMB < 64) {
		return errors.New("volume limit must be 0 (unlimited) or 64–1,048,576 MB")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	if limitMB == 0 {
		if err := os.MkdirAll(p, 0o750); err != nil {
			return fmt.Errorf("create persistent volume: %w", err)
		}
		return e.setVolumeOwner(p)
	}
	image, err := e.volumeImagePath(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(image); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(image), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("create capped volume image: %w", err)
		}
		if err := f.Truncate(limitMB << 20); err != nil {
			f.Close()
			_ = os.Remove(image)
			return fmt.Errorf("size capped volume image: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(image)
			return err
		}
		out, err := exec.Command("mkfs.ext4", "-F", "-m", "0", image).CombinedOutput()
		if err != nil {
			_ = os.Remove(image)
			return fmt.Errorf("format capped volume filesystem: %s: %w", strings.TrimSpace(string(out)), err)
		}
	} else if err != nil {
		return err
	}
	if err := e.ensureVolumeMounted(id); err != nil {
		return err
	}
	return e.setVolumeOwner(p)
}

func (e *Engine) setVolumeOwner(p string) error {
	// Volumes can be attached to more than one guest over time, so they stay
	// on the shared image uid_base rather than a per-instance HostUID.
	owner := uint32(0)
	if e.Cfg.UserNS {
		owner = e.Cfg.UIDBase
	}
	if err := os.Chown(p, int(owner), int(owner)); err != nil {
		return fmt.Errorf("set volume ownership: %w", err)
	}
	// The volume root is commonly mounted as a website document root. Keep it
	// traversable by unprivileged guest services such as nginx (www-data).
	if err := os.Chmod(p, 0o755); err != nil {
		return fmt.Errorf("set volume permissions: %w", err)
	}
	return nil
}

func (e *Engine) ensureVolumeMounted(id string) error {
	e.volumeMu.Lock()
	defer e.volumeMu.Unlock()
	p, err := e.volumePath(id)
	if err != nil {
		return err
	}
	image, err := e.volumeImagePath(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(image); os.IsNotExist(err) {
		return e.setVolumeOwner(p)
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(p, 0o750); err != nil {
		return err
	}
	if isMounted(p) {
		return e.setVolumeOwner(p)
	}
	if err := mountLoopExt4(image, p, "nosuid,nodev"); err != nil {
		return fmt.Errorf("mount capped volume %s (loop/ext4 support required): %w", id, err)
	}
	return e.setVolumeOwner(p)
}

func (e *Engine) DeleteVolume(id string) error {
	p, err := e.volumePath(id)
	if err != nil {
		return err
	}
	e.volumeMu.Lock()
	defer e.volumeMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.meta {
		for _, mount := range m.Spec.StorageMounts {
			if mount.ID == id && mount.GuestPath != "" {
				return errors.New("volume is still attached to a VPS")
			}
		}
	}
	if isMounted(p) {
		if err := syscall.Unmount(p, 0); err != nil {
			return fmt.Errorf("unmount capped volume before deleting: %w", err)
		}
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("delete persistent volume data: %w", err)
	}
	image, err := e.volumeImagePath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(image); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete capped volume image: %w", err)
	}
	return nil
}

func (e *Engine) VolumeStatuses() []proto.WorkerVolumeStatus {
	root := filepath.Join(e.StateDir, "volumes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return []proto.WorkerVolumeStatus{}
	}
	out := make([]proto.WorkerVolumeStatus, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && validID.MatchString(entry.Name()) {
			_ = e.ensureVolumeMounted(entry.Name())
			out = append(out, proto.WorkerVolumeStatus{ID: entry.Name(), UsageBytes: e.cachedDirectoryBytes("volume/"+entry.Name(), filepath.Join(root, entry.Name()))})
		}
	}
	return out
}

func pathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func validStorageGuestPath(p string) bool {
	if !filepath.IsAbs(p) || p == "/" || filepath.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") || len(p) > 256 {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, root := range []string{"/proc", "/sys", "/dev", "/run", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/root", "/var/lib", "/var/log", "/var/cache", "/var/run"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return false
		}
	}
	return true
}

// Restart stops the existing guest and starts it again, preserving its disk and settings.
func (e *Engine) Restart(id string) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	e.setState(m, proto.StateRestarting, "")
	if e.CG.Alive(id) {
		if err := e.Stop(id); err != nil {
			return fmt.Errorf("stop guest for restart: %w", err)
		}
	}
	return e.Start(id)
}

// UpdateProtection applies a protection change by restarting a running guest,
// because the root mount's read-only state is fixed when fx-init starts.
func (e *Engine) UpdateProtection(id string, enabled bool) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if protectionEnabled(m.Spec) == enabled {
		return nil
	}
	running := e.CG.Alive(id)
	if running {
		if err := e.Stop(id); err != nil {
			return fmt.Errorf("stop guest to update protection mode: %w", err)
		}
	}
	previous := m.Spec.ProtectionMode
	m.Spec.ProtectionMode = &enabled
	if err := e.save(m); err != nil {
		m.Spec.ProtectionMode = previous
		if running {
			if restartErr := e.Start(id); restartErr != nil {
				return fmt.Errorf("save protection mode: %v; restore guest startup: %w", err, restartErr)
			}
		}
		return fmt.Errorf("save protection mode: %w", err)
	}
	if running {
		if err := e.Start(id); err != nil {
			return fmt.Errorf("restart guest with updated protection mode: %w", err)
		}
	}
	return nil
}

// UpdateSSHLogin applies the SSH login policy. sshd reads the generated config
// at guest startup, so a running guest is restarted after the config is saved.
func (e *Engine) UpdateSSHLogin(id string, blocked bool) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if sshLoginBlocked(m.Spec) == blocked {
		return nil
	}
	running := e.CG.Alive(id)
	previous := m.Spec.SSHLoginBlocked
	updated := m.Spec
	updated.SSHLoginBlocked = &blocked
	if err := e.configureSSHFiles(id, updated, e.guestOwner(m)); err != nil {
		return fmt.Errorf("configure SSH login policy: %w", err)
	}
	m.Spec.SSHLoginBlocked = &blocked
	if err := e.save(m); err != nil {
		m.Spec.SSHLoginBlocked = previous
		_ = e.configureSSHFiles(id, m.Spec, e.guestOwner(m))
		return fmt.Errorf("save SSH login policy: %w", err)
	}
	if running {
		if err := e.Stop(id); err != nil {
			return fmt.Errorf("stop guest to apply SSH login policy: %w", err)
		}
		if err := e.Start(id); err != nil {
			return fmt.Errorf("restart guest with updated SSH login policy: %w", err)
		}
	}
	return nil
}

// UpdateDeleteProtection persists the worker-side deletion guard without
// restarting the guest. The controller also enforces this setting at its API.
func (e *Engine) UpdateDeleteProtection(id string, enabled bool) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if m.Spec.DeleteProtection == enabled {
		return nil
	}
	previous := m.Spec.DeleteProtection
	m.Spec.DeleteProtection = enabled
	if err := e.save(m); err != nil {
		m.Spec.DeleteProtection = previous
		return fmt.Errorf("save VPS delete protection: %w", err)
	}
	return nil
}

// UsedNetwork returns IPs and SSH host ports already assigned to local instances.
func (e *Engine) UsedNetwork() (map[string]bool, map[int]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ips, ports := map[string]bool{}, map[int]bool{}
	for _, m := range e.meta {
		if m.Spec.IPAddress != "" {
			ips[m.Spec.IPAddress] = true
		}
		if m.Spec.SSHPort > 0 {
			ports[m.Spec.SSHPort] = true
		}
	}
	return ips, ports
}

func (e *Engine) Statuses() []proto.InstanceStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []proto.InstanceStatus
	now := time.Now()
	for _, m := range e.meta {
		st := m.State
		if st == proto.StateRunning && !e.CG.Alive(m.ID) {
			st = proto.StateStopped
		}
		var cpuPercent float64
		memoryCurrent := e.CG.MemoryCurrent(m.ID)
		if st == proto.StateRunning {
			usage := e.CG.CPUUsageUsec(m.ID)
			if previous, ok := e.cpuSamples[m.ID]; ok && usage >= previous.usage && m.Spec.CPU > 0 {
				elapsed := now.Sub(previous.at).Microseconds()
				if elapsed > 0 {
					cpuPercent = float64(usage-previous.usage) / float64(elapsed) / m.Spec.CPU * 100
					if cpuPercent > 100 {
						cpuPercent = 100
					}
				}
			}
			e.cpuSamples[m.ID] = cpuSample{usage: usage, at: now}
		} else {
			delete(e.cpuSamples, m.ID)
			delete(e.netSamples, m.ID)
		}
		out = append(out, proto.InstanceStatus{
			ID: m.ID, State: st, Error: m.Error, Pid: m.Pid,
			Metrics:  e.instanceMetrics(m, cpuPercent, memoryCurrent, st == proto.StateRunning, now),
			Services: e.applicationServices(m.ID, m.Pid, st == proto.StateRunning), ServiceInstalls: cloneStringMap(m.ServiceInstalls),
		})
	}
	return out
}

func (e *Engine) instanceMetrics(m *Meta, cpuPercent float64, memoryCurrent uint64, running bool, now time.Time) proto.InstanceMetrics {
	metrics := proto.InstanceMetrics{CPUPercent: cpuPercent, MemoryCurrentBytes: memoryCurrent, MemoryLimitBytes: uint64(m.Spec.RAMMB) << 20, SwapCurrentBytes: e.CG.MemorySwapCurrent(m.ID), SwapLimitBytes: uint64(proto.SwapLimitMB(m.Spec)) << 20, DiskUsageBytes: e.cachedDirectoryBytes("upper/"+m.ID, e.upperDir(m.ID))}
	if running {
		if counters, ok := netstats.Guest(m.Pid, network.HostInterface(m.ID)); ok {
			metrics.NetRxBytes, metrics.NetTxBytes = counters.RxBytes, counters.TxBytes
			if previous, have := e.netSamples[m.ID]; have {
				metrics.NetRxBps, metrics.NetTxBps = netstats.Rate(netstats.Counters{RxBytes: previous.rx, TxBytes: previous.tx}, counters, now.Sub(previous.at).Seconds())
			}
			e.netSamples[m.ID] = netSample{rx: counters.RxBytes, tx: counters.TxBytes, at: now}
		}
	}
	for _, mount := range m.Spec.StorageMounts {
		bytes := e.cachedDirectoryBytes("volume/"+mount.ID, filepath.Join(e.StateDir, "volumes", mount.ID))
		metrics.DiskUsageBytes += bytes
		metrics.StorageVolumes = append(metrics.StorageVolumes, proto.StorageVolumeMetric{ID: mount.ID, UsageBytes: bytes})
	}
	return metrics
}

func (e *Engine) cachedDirectoryBytes(key, path string) uint64 {
	e.diskMu.Lock()
	defer e.diskMu.Unlock()
	now := time.Now()
	if sample, ok := e.diskSamples[key]; ok && now.Sub(sample.at) < 30*time.Second {
		return sample.bytes
	}
	bytes := directoryBytes(path)
	e.diskSamples[key] = diskSample{bytes: bytes, at: now}
	return bytes
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func directoryBytes(root string) uint64 {
	var total uint64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, e := d.Info(); e == nil && info.Size() > 0 {
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return total
}

// InstallService accepts catalog IDs only; package names and shell commands are never supplied by the API.
func (e *Engine) InstallService(id, serviceID string) error {
	packages, ok := map[string][]string{
		"tomcat":      {"tomcat10", "default-jre-headless"},
		"wildfly":     {"openjdk-21-jre-headless", "curl", "ca-certificates", "tar"},
		"haproxy":     {"haproxy"},
		"memcached":   {"memcached"},
		"supervisor":  {"supervisor"},
		"git":         {"git"},
		"composer":    {"composer"},
		"golang":      {"golang-go"},
		"java":        {"default-jdk"},
		"ruby":        {"ruby-full"},
		"build-tools": {"build-essential", "pkg-config"},
		"sqlite":      {"sqlite3"},
		"certbot":     {"certbot"},
		"rsync":       {"rsync"},

		"nginx": {"nginx"}, "apache": {"apache2"}, "mysql": {"mysql-server"},
		"mariadb": {"mariadb-server"}, "postgresql": {"postgresql"}, "redis": {"redis-server"},
		"php":    {"php-fpm", "php-cli", "php-mysql", "php-xml", "php-mbstring", "php-curl", "php-zip", "php-gd"},
		"nodejs": {"nodejs", "npm"}, "python": {"python3", "python3-venv", "python3-pip"},
		"wordpress": {"wordpress", "apache2", "libapache2-mod-php", "php-mysql", "php-gd", "php-curl", "php-xml", "php-mbstring", "php-zip", "mariadb-server"},
		"joomla":    {"apache2", "libapache2-mod-php", "php-mysql", "php-gd", "php-curl", "php-xml", "php-mbstring", "php-zip", "php-intl", "mariadb-server", "curl", "tar"},
		"owasp-waf": {"nginx", "libnginx-mod-http-modsecurity", "modsecurity-crs"},
	}[serviceID]
	if !ok {
		return errors.New("unknown service catalog item")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if protectionEnabled(m.Spec) {
		return errors.New("disable Protection Mode before installing packages")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to install a service")
	}
	e.mu.Lock()
	if m.ServiceInstalls == nil {
		m.ServiceInstalls = map[string]string{}
	}
	m.ServiceInstalls[serviceID] = "INSTALLING"
	_ = e.save(m)
	e.mu.Unlock()
	defer func() { e.mu.Lock(); _ = e.save(m); e.mu.Unlock() }()
	if err = e.guestPackageCommand(id, m.Pid, []string{"update"}); err != nil {
		e.mu.Lock()
		setInstallState(m, serviceID, "FAILED")
		e.mu.Unlock()
		return fmt.Errorf("apt update failed: %w", err)
	}
	args := append([]string{"install", "--no-install-recommends"}, packages...)
	if err = e.guestPackageCommand(id, m.Pid, args); err != nil {
		e.mu.Lock()
		setInstallState(m, serviceID, "FAILED")
		e.mu.Unlock()
		return fmt.Errorf("package installation failed: %w", err)
	}
	if setup := javaServiceSetup(serviceID); setup != "" {
		if err = e.guestShell(id, m.Pid, setup); err != nil {
			e.mu.Lock()
			setInstallState(m, serviceID, "FAILED")
			e.mu.Unlock()
			return fmt.Errorf("%s setup failed: %w", serviceID, err)
		}
	}
	if serviceID == "joomla" {
		// Fixed upstream release and official checksum; no request data is interpolated into the guest shell.
		if err = e.guestShell(id, m.Pid, "set -eu; cd /tmp; curl -fL --retry 3 -o clofix-joomla.tar.gz 'https://downloads.joomla.org/cms/joomla6/6-2-0/Joomla_6-2-0-Stable-Full_Package.tar.gz?format=gz'; echo '078706721f94dd814a1175207145aeb4380c00ff  clofix-joomla.tar.gz' | sha1sum -c -; mkdir -p /var/www/html/joomla; tar -xzf clofix-joomla.tar.gz -C /var/www/html/joomla; chown -R www-data:www-data /var/www/html/joomla; rm -f clofix-joomla.tar.gz"); err != nil {
			e.mu.Lock()
			setInstallState(m, serviceID, "FAILED")
			e.mu.Unlock()
			return fmt.Errorf("Joomla download or setup failed: %w", err)
		}
	}
	if serviceID == "owasp-waf" {
		if err = e.configureNginxWAFWithPages(id, m.Pid, m.Spec.OWASPProtection, m.Spec.WAFErrorPages); err != nil {
			e.mu.Lock()
			setInstallState(m, serviceID, "FAILED")
			e.mu.Unlock()
			return fmt.Errorf("OWASP CRS WAF configuration failed: %w", err)
		}
	}
	e.mu.Lock()
	setInstallState(m, serviceID, "INSTALLED")
	e.mu.Unlock()
	return nil
}

// UninstallService removes packages installed through the fixed service catalog.
// Package names and paths are selected here, never supplied by the API caller.
func (e *Engine) UninstallService(id, serviceID string) error {
	if serviceID == "owasp-waf" {
		return e.UninstallWAF(id, serviceID)
	}
	packages, ok := map[string][]string{
		"tomcat": {"tomcat10"}, "wildfly": {},
		"haproxy":     {"haproxy"},
		"memcached":   {"memcached"},
		"supervisor":  {"supervisor"},
		"git":         {"git"},
		"composer":    {"composer"},
		"golang":      {"golang-go"},
		"java":        {"default-jdk"},
		"ruby":        {"ruby-full"},
		"build-tools": {"build-essential", "pkg-config"},
		"sqlite":      {"sqlite3"},
		"certbot":     {"certbot"},
		"rsync":       {"rsync"},

		"nginx": {"nginx"}, "apache": {"apache2"}, "mysql": {"mysql-server"},
		"mariadb": {"mariadb-server"}, "postgresql": {"postgresql"}, "redis": {"redis-server"},
		"php":    {"php-fpm", "php-cli", "php-mysql", "php-xml", "php-mbstring", "php-curl", "php-zip", "php-gd"},
		"nodejs": {"nodejs", "npm"}, "wordpress": {"wordpress"},
	}[serviceID]
	if !ok && serviceID != "joomla" {
		return errors.New("service does not support uninstall from CloFix")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if protectionEnabled(m.Spec) {
		return errors.New("disable Protection Mode before uninstalling packages")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to uninstall a service")
	}
	e.mu.Lock()
	if m.ServiceInstalls == nil {
		m.ServiceInstalls = map[string]string{}
	}
	state := m.ServiceInstalls[serviceID]
	if state == "INSTALLING" || state == "UNINSTALLING" {
		e.mu.Unlock()
		return errors.New("wait for the current service change to finish before uninstalling")
	}
	if state != "INSTALLED" && state != "FAILED" && state != "UNINSTALL_FAILED" {
		e.mu.Unlock()
		return errors.New("service is not installed or there is no failed install to clean up")
	}
	if serviceID == "nginx" && m.ServiceInstalls["owasp-waf"] != "UNINSTALLED" && m.ServiceInstalls["owasp-waf"] != "" {
		e.mu.Unlock()
		return errors.New("uninstall the OWASP CRS WAF before removing Nginx")
	}
	for _, dependent := range []string{"wordpress", "joomla"} {
		if (serviceID == "apache" || serviceID == "php" || serviceID == "mariadb") && m.ServiceInstalls[dependent] != "" && m.ServiceInstalls[dependent] != "UNINSTALLED" {
			e.mu.Unlock()
			return fmt.Errorf("uninstall %s before removing its shared %s dependency", dependent, serviceID)
		}
	}
	if serviceID == "java" {
		for _, dependent := range []string{"tomcat", "wildfly"} {
			if m.ServiceInstalls[dependent] != "" && m.ServiceInstalls[dependent] != "UNINSTALLED" {
				e.mu.Unlock()
				return fmt.Errorf("uninstall %s before removing Java", dependent)
			}
		}
	}
	setInstallState(m, serviceID, "UNINSTALLING")
	_ = e.save(m)
	e.mu.Unlock()
	defer func() { e.mu.Lock(); _ = e.save(m); e.mu.Unlock() }()
	if serviceID == "joomla" {
		if err := e.guestShell(id, m.Pid, "set -eu; rm -rf -- /var/www/html/joomla"); err != nil {
			e.mu.Lock()
			setInstallState(m, serviceID, "UNINSTALL_FAILED")
			e.mu.Unlock()
			return fmt.Errorf("Joomla uninstall failed: %w", err)
		}
		e.mu.Lock()
		setInstallState(m, serviceID, "UNINSTALLED")
		e.mu.Unlock()
		return nil
	}
	if serviceID == "wildfly" || serviceID == "tomcat" {
		cleanup := "set -eu; if [ -x /etc/init.d/tomcat10 ]; then service tomcat10 stop; fi; update-rc.d -f tomcat10 remove; rm -f /etc/init.d/tomcat10"
		if serviceID == "wildfly" {
			cleanup = "set -eu; if [ -x /etc/init.d/wildfly ]; then service wildfly stop; fi; update-rc.d -f wildfly remove; rm -f /etc/init.d/wildfly /opt/wildfly; rm -rf -- /opt/clofix-wildfly-42.0.0.Final"
		}
		if err := e.guestShell(id, m.Pid, cleanup); err != nil {
			e.mu.Lock()
			setInstallState(m, serviceID, "UNINSTALL_FAILED")
			e.mu.Unlock()
			return err
		}
		if serviceID == "wildfly" {
			e.mu.Lock()
			setInstallState(m, serviceID, "UNINSTALLED")
			e.mu.Unlock()
			return nil
		}
	}
	if err := e.guestPackageCommand(id, m.Pid, append([]string{"remove", "--no-install-recommends"}, packages...)); err != nil {
		e.mu.Lock()
		setInstallState(m, serviceID, "UNINSTALL_FAILED")
		e.mu.Unlock()
		return fmt.Errorf("%s uninstall failed: %w", serviceID, err)
	}
	e.mu.Lock()
	setInstallState(m, serviceID, "UNINSTALLED")
	e.mu.Unlock()
	return nil
}

// UninstallWAF removes the WAF integration and its packages without removing Nginx.
func (e *Engine) UninstallWAF(id, serviceID string) error {
	if serviceID != "owasp-waf" {
		return errors.New("unknown WAF service")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if protectionEnabled(m.Spec) {
		return errors.New("disable Protection Mode before uninstalling packages")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to uninstall the WAF")
	}
	e.mu.Lock()
	if m.ServiceInstalls == nil {
		m.ServiceInstalls = map[string]string{}
	}
	state := m.ServiceInstalls[serviceID]
	if state == "INSTALLING" || state == "UNINSTALLING" {
		e.mu.Unlock()
		return errors.New("wait for the current WAF change to finish before uninstalling")
	}
	if state != "INSTALLED" && state != "FAILED" && state != "UNINSTALL_FAILED" {
		e.mu.Unlock()
		return errors.New("OWASP CRS WAF is not installed")
	}
	setInstallState(m, serviceID, "UNINSTALLING")
	_ = e.save(m)
	e.mu.Unlock()
	defer func() { e.mu.Lock(); _ = e.save(m); e.mu.Unlock() }()
	script := "set -eu; " +
		"rm -f /etc/nginx/conf.d/clofix-modsecurity.conf /etc/nginx/modsecurity_includes.conf /etc/nginx/clofix-modsecurity-overrides.conf /etc/nginx/conf.d/clofix-waf-error-pages.conf /etc/nginx/snippets/clofix-waf-error-pages.conf; " +
		"rm -f /var/www/html/clofix-waf-*.html; " +
		"for f in /etc/nginx/sites-enabled/*; do [ -f \"$f\" ] || continue; sed -i '/snippets\\/clofix-waf-error-pages.conf/d' \"$f\"; done; " +
		"apt-get -y purge modsecurity-crs libnginx-mod-http-modsecurity; " +
		"if [ -f /etc/modsecurity/modsecurity.conf ]; then " +
		"sed -i 's/^[[:space:]]*SecRuleEngine.*/SecRuleEngine DetectionOnly/' /etc/modsecurity/modsecurity.conf; " +
		"sed -i 's/^[[:space:]]*SecResponseBodyAccess.*/SecResponseBodyAccess Off/' /etc/modsecurity/modsecurity.conf; fi; " +
		"nginx -t; if service nginx status >/dev/null 2>&1; then service nginx reload; fi"
	if err := e.guestShell(id, m.Pid, script); err != nil {
		e.mu.Lock()
		setInstallState(m, serviceID, "UNINSTALL_FAILED")
		e.mu.Unlock()
		return fmt.Errorf("OWASP CRS WAF uninstall failed: %w", err)
	}
	e.mu.Lock()
	setInstallState(m, serviceID, "UNINSTALLED")
	e.mu.Unlock()
	return nil
}

func wafOverrides(policy map[string]bool) string {
	var out strings.Builder
	keys := make([]string, 0, len(policy))
	for key := range policy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if control, ok := wafTagByCategory[key]; ok && !policy[key] {
			if strings.HasPrefix(control, "id:") {
				fmt.Fprintf(&out, "SecRuleRemoveById \"%s\"\n", strings.TrimPrefix(control, "id:"))
			} else {
				fmt.Fprintf(&out, "SecRuleRemoveByTag \"%s\"\n", control)
			}
		}
	}
	return out.String()
}

func guestSingleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

var wafErrorPageStatuses = []string{"403", "404", "406", "429", "500"}

func defaultWAFErrorPage(status string) string {
	titles := map[string]string{
		"403": "Request blocked",
		"404": "Page not found",
		"406": "Not acceptable",
		"429": "Too many requests",
		"500": "Server error",
	}
	messages := map[string]string{
		"403": "This request was blocked by the web application firewall protecting this site.",
		"404": "The page you requested could not be found.",
		"406": "The server cannot produce a response matching the request.",
		"429": "Too many requests were sent in a short time. Please try again later.",
		"500": "The server encountered an error while handling this request.",
	}
	title := titles[status]
	if title == "" {
		title = "Error " + status
	}
	msg := messages[status]
	if msg == "" {
		msg = "An error occurred while processing this request."
	}
	return "<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">" +
		"<title>" + status + " · " + title + "</title><style>" +
		":root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;" +
		"font-family:ui-sans-serif,system-ui,-apple-system,sans-serif;background:#0b1218;color:#e8eef4}" +
		"main{max-width:34rem;padding:2rem 1.5rem;text-align:center}code{font-size:.85em;color:#87dcd1}" +
		"h1{margin:.35rem 0 .65rem;font-size:1.45rem;letter-spacing:-.02em}p{margin:0;color:#9aabbd;line-height:1.55}" +
		"</style></head><body><main><p><code>" + status + "</code></p><h1>" + title + "</h1><p>" + msg + "</p></main></body></html>\n"
}

func resolveWAFErrorPages(pages map[string]string) map[string]string {
	out := make(map[string]string, len(wafErrorPageStatuses))
	for _, status := range wafErrorPageStatuses {
		html := strings.TrimSpace(pages[status])
		if html == "" {
			html = defaultWAFErrorPage(status)
		}
		out[status] = html
	}
	return out
}

func wafErrorPagesNginxConf(pages map[string]string) string {
	_ = resolveWAFErrorPages(pages)
	var out strings.Builder
	out.WriteString("# Managed by CloFix · custom WAF / Nginx error pages (server context)\n")
	for _, status := range wafErrorPageStatuses {
		fmt.Fprintf(&out, "error_page %s /clofix-waf-%s.html;\n", status, status)
	}
	for _, status := range wafErrorPageStatuses {
		fmt.Fprintf(&out, "location = /clofix-waf-%s.html {\n  root /var/www/html;\n  internal;\n  default_type text/html;\n  charset utf-8;\n  access_log off;\n}\n", status)
	}
	return out.String()
}

func wafErrorPagesInstallScript(pages map[string]string) string {
	resolved := resolveWAFErrorPages(pages)
	var b strings.Builder
	// conf.d is included in the http{} context on Debian/Ubuntu Nginx, where
	// location is invalid. Keep the snippet under /etc/nginx/snippets and
	// include it from each sites-enabled server block.
	b.WriteString("mkdir -p /var/www/html /etc/nginx/snippets; ")
	b.WriteString("rm -f /etc/nginx/conf.d/clofix-waf-error-pages.conf; ")
	for _, status := range wafErrorPageStatuses {
		encoded := base64.StdEncoding.EncodeToString([]byte(resolved[status]))
		fmt.Fprintf(&b, "printf %%s %s | base64 -d > /var/www/html/clofix-waf-%s.html; ", guestSingleQuote(encoded), status)
	}
	fmt.Fprintf(&b, "printf %%s %s > /etc/nginx/snippets/clofix-waf-error-pages.conf; ", guestSingleQuote(wafErrorPagesNginxConf(pages)))
	b.WriteString(`for f in /etc/nginx/sites-enabled/*; do ` +
		`[ -f "$f" ] || continue; ` +
		`if ! grep -q 'snippets/clofix-waf-error-pages.conf' "$f"; then ` +
		`sed -i '0,/server[[:space:]]*{/s||&\n\tinclude /etc/nginx/snippets/clofix-waf-error-pages.conf;|' "$f"; ` +
		`fi; ` +
		`done; `)
	return b.String()
}

func (e *Engine) configureNginxWAF(id string, pid int, policy map[string]bool) error {
	return e.configureNginxWAFWithPages(id, pid, policy, nil)
}

func (e *Engine) configureNginxWAFWithPages(id string, pid int, policy map[string]bool, pages map[string]string) error {
	includes := "include modsecurity.conf\n" +
		"include /etc/modsecurity/crs/crs-setup.conf\n" +
		"include /etc/modsecurity/crs/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf\n" +
		"include /usr/share/modsecurity-crs/rules/*.conf\n" +
		"include /etc/modsecurity/crs/RESPONSE-999-EXCLUSION-RULES-AFTER-CRS.conf\n" +
		"include /etc/nginx/clofix-modsecurity-overrides.conf\n"
	script := "set -eu; " +
		"if grep -q '^[[:space:]]*SecRuleEngine' /etc/nginx/modsecurity.conf; then sed -i 's/^[[:space:]]*SecRuleEngine.*/SecRuleEngine On/' /etc/nginx/modsecurity.conf; else printf '%s\\n' 'SecRuleEngine On' >> /etc/nginx/modsecurity.conf; fi; " +
		"if grep -q '^[[:space:]]*SecRequestBodyAccess' /etc/nginx/modsecurity.conf; then sed -i 's/^[[:space:]]*SecRequestBodyAccess.*/SecRequestBodyAccess On/' /etc/nginx/modsecurity.conf; else printf '%s\\n' 'SecRequestBodyAccess On' >> /etc/nginx/modsecurity.conf; fi; " +
		"if grep -q '^[[:space:]]*SecResponseBodyAccess' /etc/nginx/modsecurity.conf; then sed -i 's/^[[:space:]]*SecResponseBodyAccess.*/SecResponseBodyAccess On/' /etc/nginx/modsecurity.conf; else printf '%s\\n' 'SecResponseBodyAccess On' >> /etc/nginx/modsecurity.conf; fi; " +
		// Use a non-slash delimiter because MIME values contain application/json.
		"if grep -q '^[[:space:]]*SecResponseBodyMimeType' /etc/nginx/modsecurity.conf; then sed -i 's|^[[:space:]]*SecResponseBodyMimeType.*|SecResponseBodyMimeType text/plain text/html text/xml application/json|' /etc/nginx/modsecurity.conf; else printf '%s\\n' 'SecResponseBodyMimeType text/plain text/html text/xml application/json' >> /etc/nginx/modsecurity.conf; fi; " +
		"if grep -q '^[[:space:]]*SecDataDir' /etc/nginx/modsecurity.conf; then sed -i 's|^[[:space:]]*SecDataDir.*|SecDataDir /run/modsecurity|' /etc/nginx/modsecurity.conf; else printf '%s\\n' 'SecDataDir /run/modsecurity' >> /etc/nginx/modsecurity.conf; fi; " +
		"mkdir -p /run/modsecurity; chown www-data:www-data /run/modsecurity; " +
		"printf %s " + guestSingleQuote(includes) + " > /etc/nginx/modsecurity_includes.conf; " +
		"printf %s " + guestSingleQuote(wafOverrides(policy)) + " > /etc/nginx/clofix-modsecurity-overrides.conf; " +
		"printf %s " + guestSingleQuote("modsecurity on;\nmodsecurity_rules_file /etc/nginx/modsecurity_includes.conf;\n") + " > /etc/nginx/conf.d/clofix-modsecurity.conf; " +
		wafErrorPagesInstallScript(pages) +
		"nginx -t && (service nginx reload || service nginx restart)"
	return e.guestShell(id, pid, script)
}

func (e *Engine) UpdateWAFCategory(id, category string, enabled bool) error {
	tag, ok := wafTagByCategory[category]
	if !ok || tag == "" {
		return errors.New("unsupported OWASP CRS category")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if m.ServiceInstalls["owasp-waf"] != "INSTALLED" {
		return errors.New("install OWASP CRS WAF before changing its rules")
	}
	if protectionEnabled(m.Spec) {
		return errors.New("disable Protection Mode before changing WAF rules")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to update WAF rules")
	}
	previous := make(map[string]bool, len(m.Spec.OWASPProtection))
	for key, value := range m.Spec.OWASPProtection {
		previous[key] = value
	}
	policy := make(map[string]bool, len(previous)+1)
	for key, value := range previous {
		policy[key] = value
	}
	policy[category] = enabled
	if err := e.configureNginxWAFWithPages(id, m.Pid, policy, m.Spec.WAFErrorPages); err != nil {
		_ = e.configureNginxWAFWithPages(id, m.Pid, previous, m.Spec.WAFErrorPages)
		return err
	}
	m.Spec.OWASPProtection = policy
	if err := e.save(m); err != nil {
		m.Spec.OWASPProtection = previous
		_ = e.configureNginxWAFWithPages(id, m.Pid, previous, m.Spec.WAFErrorPages)
		return fmt.Errorf("save WAF category policy: %w", err)
	}
	return nil
}

func (e *Engine) UpdateWAFErrorPages(id string, pages map[string]string) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if m.ServiceInstalls["owasp-waf"] != "INSTALLED" {
		return errors.New("install OWASP CRS WAF before setting custom error pages")
	}
	if protectionEnabled(m.Spec) {
		return errors.New("disable Protection Mode before updating WAF error pages")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to update WAF error pages")
	}
	previous := make(map[string]string, len(m.Spec.WAFErrorPages))
	for key, value := range m.Spec.WAFErrorPages {
		previous[key] = value
	}
	cleaned := make(map[string]string, len(wafErrorPageStatuses))
	for _, status := range wafErrorPageStatuses {
		if html := strings.TrimSpace(pages[status]); html != "" {
			cleaned[status] = html
		}
	}
	script := "set -eu; " + wafErrorPagesInstallScript(cleaned) + "nginx -t && (service nginx reload || service nginx restart)"
	if err := e.guestShell(id, m.Pid, script); err != nil {
		_ = e.guestShell(id, m.Pid, "set -eu; "+wafErrorPagesInstallScript(previous)+"nginx -t && (service nginx reload || service nginx restart)")
		return err
	}
	m.Spec.WAFErrorPages = cleaned
	if err := e.save(m); err != nil {
		m.Spec.WAFErrorPages = previous
		_ = e.guestShell(id, m.Pid, "set -eu; "+wafErrorPagesInstallScript(previous)+"nginx -t && (service nginx reload || service nginx restart)")
		return fmt.Errorf("save WAF error pages: %w", err)
	}
	return nil
}

func setInstallState(m *Meta, id, state string) { m.ServiceInstalls[id] = state }

func (e *Engine) guestPackageCommand(id string, pid int, aptArgs []string) error {
	return e.guestExec(id, pid, "apt-get", append([]string{"-y"}, aptArgs...))
}

func (e *Engine) guestShell(id string, pid int, script string) error {
	return e.guestExec(id, pid, "/bin/sh", []string{"-c", script})
}

func (e *Engine) guestExec(id string, pid int, program string, commandArgs []string) error {
	var output cappedOutput
	if err := e.execInGuest(id, pid, program, commandArgs, &output, &output); err != nil {
		if output.b.Len() > 0 {
			return errors.New(strings.TrimSpace(output.b.String()))
		}
		return err
	}
	return nil
}

// Exec runs a command inside a running guest and writes its output to the given writers.
// It enters the guest namespaces and cgroup; it does not run the command on the host.
func (e *Engine) Exec(id string, argv []string, stdout, stderr io.Writer) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("command required")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("instance is not running")
	}
	return e.execInGuest(id, m.Pid, argv[0], argv[1:], stdout, stderr)
}

func (e *Engine) execInGuest(id string, pid int, program string, commandArgs []string, stdout, stderr io.Writer) error {
	cgf, err := e.CG.Open(id)
	if err != nil {
		return err
	}
	defer cgf.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	self, err := os.Open("/proc/self/exe")
	if err != nil {
		return err
	}
	defer self.Close()
	args := []string{"--target", strconv.Itoa(pid)}
	if e.Cfg.UserNS {
		// Host root is not in the guest uid_map (0→hostUID). After setns,
		// --setuid 0 becomes mapped guest root instead of overflowuid.
		args = append(args, "--user", "--setuid", "0", "--setgid", "0")
	}
	// nsenter is not a child of guest PID 1, so it does not inherit that
	// process's seccomp. After setns, exec the worker via an inherited fd
	// (the guest has no worker binary) and install the filter before the
	// command. fd 3 is ExtraFiles[0].
	args = append(args, "--mount", "--uts", "--ipc", "--net", "--pid", "--cgroup", "--", "/proc/self/fd/3", "fx-confine", "--chroot", "/proc/1/root", "--", "/usr/bin/env", "DEBIAN_FRONTEND=noninteractive", program)
	args = append(args, commandArgs...)
	cmd := exec.CommandContext(ctx, "nsenter", args...)
	cmd.ExtraFiles = []*os.File{self}
	cmd.SysProcAttr = &syscall.SysProcAttr{CgroupFD: int(cgf.Fd()), UseCgroupFD: true, Setsid: true}
	// Do not inherit worker env (CLOFIX_*, secrets) into the guest command.
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"LANG=C.UTF-8",
		"DEBIAN_FRONTEND=noninteractive",
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("guest command timed out after 15 minutes")
		}
		return err
	}
	return nil
}

var excludedSystemServices = map[string]bool{
	"apparmor": true, "apt-daily": true, "apt-daily-upgrade": true, "cloud-config": true,
	"cloud-final": true, "cloud-init": true, "console-getty": true, "cron": true,
	"dbus": true, "getty": true, "networking": true, "polkit": true, "rc-local": true,
	"e2scrub_reap": true, "e2scrub_all": true, "fstrim": true, "logrotate": true, "man-db": true,
	"motd-news": true, "rescue": true, "serial-getty": true, "ssh": true, "sshd": true, "systemd-ask-password-console": true,
	"systemd-fsck": true, "systemd-logind": true, "systemd-networkd": true,
	"systemd-resolved": true, "systemd-timesyncd": true, "systemd-udevd": true,
	"unattended-upgrades": true,
}

// applicationServices reports enabled application units (not base OS daemons).
// Directory components are checked with Lstat so a guest symlink cannot redirect
// host-side service discovery outside its mounted root filesystem.
func (e *Engine) applicationServices(id string, pid int, running bool) []proto.ServiceStatus {
	if !running {
		return nil
	}
	root := e.merged(id)
	enabled := map[string]bool{}
	for _, rel := range []string{
		"etc/systemd/system/multi-user.target.wants",
		"etc/systemd/system/graphical.target.wants",
		"etc/rc2.d", "etc/rc3.d", "etc/rc5.d",
	} {
		dir := root
		safe := true
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			dir = filepath.Join(dir, part)
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				safe = false
				break
			}
		}
		if !safe {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(rel, "etc/rc") {
				if len(name) < 4 || name[0] != 'S' || name[1] < '0' || name[1] > '9' || name[2] < '0' || name[2] > '9' {
					continue
				}
				name = name[3:]
			} else {
				if !strings.HasSuffix(name, ".service") {
					continue
				}
				name = strings.TrimSuffix(name, ".service")
			}
			if validServiceName(name) && !isSystemService(name) {
				enabled[name] = true
			}
		}
	}
	if len(enabled) == 0 {
		return []proto.ServiceStatus{}
	}
	type serviceUsage struct {
		proc                []int
		ticks, memory, swap uint64
	}
	usage := map[string]*serviceUsage{}
	for name := range enabled {
		usage[name] = &serviceUsage{}
	}
	for _, processPID := range e.CG.Pids(id) {
		comm, ticks, memory, swap, ok := processResources(processPID)
		if !ok {
			continue
		}
		for name, stats := range usage {
			for _, candidate := range serviceProcessNames(name) {
				if comm == candidate {
					if name == "wildfly" || name == "tomcat10" {
						cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", processPID))
						marker := "org.apache.catalina.startup.Bootstrap"
						if name == "wildfly" {
							marker = "jboss-modules.jar"
						}
						if !strings.Contains(string(cmdline), marker) {
							continue
						}
					}
					stats.proc = append(stats.proc, processPID)
					stats.ticks += ticks
					stats.memory += memory
					stats.swap += swap
					break
				}
			}
		}
	}
	var out []proto.ServiceStatus
	for name := range enabled {
		state := "ENABLED"
		stats := usage[name]
		if len(stats.proc) > 0 {
			state = "RUNNING"
		}
		out = append(out, proto.ServiceStatus{Name: name, State: state, CPUPercent: e.sampleServiceCPU(id, name, stats.ticks), MemoryBytes: stats.memory, SwapBytes: stats.swap, ListenPorts: serviceListenPorts(stats.proc)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	logBudget := 16 << 10
	logOrder := make([]int, 0, len(out))
	for i := range out {
		if out[i].Name == "nginx" {
			logOrder = append(logOrder, i)
		}
	}
	for i := range out {
		if out[i].Name != "nginx" {
			logOrder = append(logOrder, i)
		}
	}
	for _, i := range logOrder {
		if logBudget <= 0 {
			continue
		}
		limit := 4 << 10
		if out[i].Name == "nginx" {
			limit = 8 << 10
		}
		if limit > logBudget {
			limit = logBudget
		}
		out[i].Logs = e.serviceLogs(pid, out[i].Name, limit)
		logBudget -= len(out[i].Logs)
	}
	return out
}

// processResources reads procfs counters for a host PID belonging to this VPS cgroup.
// PSS apportions shared pages across processes; VmRSS is retained as a fallback for
// kernels without smaps_rollup. VmSwap is process-level; ZRAM cannot be assigned per process.
func processResources(pid int) (comm string, cpuTicks, memoryBytes, swapBytes uint64, ok bool) {
	proc := filepath.Join("/proc", strconv.Itoa(pid))
	b, err := os.ReadFile(filepath.Join(proc, "comm"))
	if err != nil {
		return "", 0, 0, 0, false
	}
	comm = strings.ToLower(strings.TrimSpace(string(b)))
	stat, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err == nil {
		if end := strings.LastIndex(string(stat), ")"); end >= 0 {
			fields := strings.Fields(string(stat)[end+1:])
			if len(fields) > 12 {
				utime, e1 := strconv.ParseUint(fields[11], 10, 64)
				stime, e2 := strconv.ParseUint(fields[12], 10, 64)
				if e1 == nil && e2 == nil {
					cpuTicks = utime + stime
				}
			}
		}
	}
	status, err := os.ReadFile(filepath.Join(proc, "status"))
	if err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			var value uint64
			value, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case "VmRSS:":
				memoryBytes = value * 1024
			case "VmSwap:":
				swapBytes = value * 1024
			}
		}
	}
	if rollup, err := os.ReadFile(filepath.Join(proc, "smaps_rollup")); err == nil {
		for _, line := range strings.Split(string(rollup), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "Pss:" {
				if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					memoryBytes = value * 1024
				}
				break
			}
		}
	}
	return comm, cpuTicks, memoryBytes, swapBytes, true
}

func (e *Engine) sampleServiceCPU(instanceID, name string, ticks uint64) float64 {
	now := time.Now()
	key := instanceID + "/" + name
	e.serviceUsageMu.Lock()
	defer e.serviceUsageMu.Unlock()
	previous, exists := e.serviceCPUSamples[key]
	e.serviceCPUSamples[key] = serviceCPUSample{ticks: ticks, at: now}
	if !exists || ticks < previous.ticks {
		return 0
	}
	seconds := now.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return 0
	}
	// Linux procfs CPU counters use USER_HZ, conventionally 100 ticks per second.
	return float64(ticks-previous.ticks) / seconds
}

func validServiceName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 || strings.HasPrefix(name, "systemd-") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.@+-", r)) {
			return false
		}
	}
	return true
}

func serviceLogCandidates(root, service string) [][2]string {
	logDir := filepath.Join(root, "var", "log")
	pair := func(path, label string) [2]string { return [2]string{path, label} }
	switch {
	case service == "nginx":
		return [][2]string{
			pair(filepath.Join(logDir, "nginx", "error.log"), "error.log"),
			pair(filepath.Join(logDir, "nginx", "modsec_audit.log"), "modsec_audit.log"),
			pair(filepath.Join(logDir, "modsec_audit.log"), "modsec_audit.log"),
		}
	case service == "apache2" || service == "apache":
		return [][2]string{pair(filepath.Join(logDir, "apache2", "error.log"), "error.log")}
	case strings.Contains(service, "php") && strings.Contains(service, "fpm"):
		return [][2]string{
			pair(filepath.Join(logDir, service+".log"), "error.log"),
			pair(filepath.Join(logDir, "php-fpm.log"), "error.log"),
			pair(filepath.Join(logDir, service, "error.log"), "error.log"),
		}
	case service == "mysql" || service == "mariadb" || service == "mariadbd":
		return [][2]string{pair(filepath.Join(logDir, "mysql", "error.log"), "error.log")}
	case service == "postgresql":
		return [][2]string{
			pair(filepath.Join(logDir, "postgresql", "postgresql-16-main.log"), "error.log"),
			pair(filepath.Join(logDir, "postgresql", "postgresql-15-main.log"), "error.log"),
			pair(filepath.Join(logDir, service, "error.log"), "error.log"),
		}
	case service == "redis" || service == "redis-server":
		return [][2]string{
			pair(filepath.Join(logDir, "redis", "redis-server.log"), "error.log"),
			pair(filepath.Join(logDir, service, "error.log"), "error.log"),
		}
	case service == "tomcat10":
		return [][2]string{
			pair(filepath.Join(logDir, "tomcat10", "clofix-console.log"), "error.log"),
			pair(filepath.Join(logDir, "tomcat10", "error.log"), "error.log"),
		}
	case service == "wildfly":
		return [][2]string{
			pair(filepath.Join(root, "lib", "wildfly", "standalone", "log", "server.log"), "error.log"),
			pair(filepath.Join(root, "lib", "wildfly", "standalone", "log", "clofix-console.log"), "error.log"),
		}
	default:
		return [][2]string{
			pair(filepath.Join(logDir, service, "error.log"), "error.log"),
			pair(filepath.Join(logDir, service+".log"), "error.log"),
			pair(filepath.Join(logDir, service, "application.log"), "error.log"),
		}
	}
}

func tailRegularFile(path string, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info.Size() > int64(limit) {
		_, _ = f.Seek(-int64(limit), io.SeekEnd)
	}
	b, _ := io.ReadAll(io.LimitReader(f, int64(limit)))
	return b
}

func (e *Engine) serviceLogs(pid int, service string, maxBytes int) string {
	if pid <= 0 || maxBytes <= 0 || !validServiceName(service) {
		return ""
	}
	root := filepath.Join("/proc", strconv.Itoa(pid), "root")
	var out strings.Builder
	seen := map[string]bool{}
	wroteError := false
	for _, item := range serviceLogCandidates(root, service) {
		path, label := item[0], item[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		data := tailRegularFile(path, maxBytes-out.Len())
		if len(data) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString("# ")
		out.WriteString(service)
		out.WriteByte('/')
		out.WriteString(label)
		out.WriteByte('\n')
		out.Write(data)
		if data[len(data)-1] != '\n' && out.Len() < maxBytes {
			out.WriteByte('\n')
		}
		if label == "error.log" {
			wroteError = true
		}
		if out.Len() >= maxBytes {
			break
		}
	}
	if !wroteError && out.Len() < maxBytes {
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString("# ")
		out.WriteString(service)
		out.WriteString("/error.log\n(empty)\n")
	}
	if out.Len() > maxBytes {
		return out.String()[out.Len()-maxBytes:]
	}
	return out.String()
}

type listenSock struct {
	Port  int
	Inode uint64
}

func parseProcNetListenSocks(s string) []listenSock {
	seen := map[string]bool{}
	var socks []listenSock
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[0] == "sl" || fields[3] != "0A" {
			continue
		}
		_, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		key := strconv.FormatUint(inode, 10) + ":" + strconv.FormatUint(port, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		socks = append(socks, listenSock{Port: int(port), Inode: inode})
	}
	return socks
}

func parseProcNetListen(s string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, sock := range parseProcNetListenSocks(s) {
		if seen[sock.Port] {
			continue
		}
		seen[sock.Port] = true
		ports = append(ports, sock.Port)
	}
	sort.Ints(ports)
	return ports
}

func processSocketInodes(pid int) map[uint64]bool {
	owned := map[uint64]bool{}
	if pid <= 0 {
		return owned
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return owned
	}
	for _, entry := range entries {
		target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, entry.Name()))
		if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		owned[inode] = true
	}
	return owned
}

func serviceListenPorts(pids []int) []int {
	owned := map[uint64]bool{}
	seen := map[int]bool{}
	var ports []int
	for _, pid := range pids {
		for inode := range processSocketInodes(pid) {
			owned[inode] = true
		}
	}
	if len(owned) == 0 {
		return nil
	}
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, sock := range parseProcNetListenSocks(string(b)) {
			if !owned[sock.Inode] || seen[sock.Port] {
				continue
			}
			seen[sock.Port] = true
			ports = append(ports, sock.Port)
		}
	}
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		add(fmt.Sprintf("/proc/%d/net/tcp", pid))
		add(fmt.Sprintf("/proc/%d/net/tcp6", pid))
	}
	sort.Ints(ports)
	return ports
}

type cappedOutput struct{ b strings.Builder }

func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (8 << 10) - w.b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.b.Write(p)
	}
	return n, nil
}

// ControlService runs a validated SysV service command inside the guest's
// namespaces and cgroup, keeping spawned daemons within the VPS boundaries.
func (e *Engine) ControlService(id, name, action string) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if !validServiceName(name) || (action != "start" && action != "stop" && action != "restart") {
		return errors.New("invalid service control request")
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS is not running")
	}
	known := false
	for _, svc := range e.applicationServices(id, m.Pid, true) {
		if svc.Name == name {
			known = true
			break
		}
	}
	if !known {
		return errors.New("service is not an enabled application service")
	}
	cgf, err := e.CG.Open(id)
	if err != nil {
		return err
	}
	defer cgf.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--target", strconv.Itoa(m.Pid)}
	if e.Cfg.UserNS {
		args = append(args, "--user", "--setuid", "0", "--setgid", "0")
	}
	args = append(args, "--mount", "--uts", "--ipc", "--net", "--pid", "--cgroup", "--", "chroot", "/proc/1/root", "/usr/sbin/service", name, action)
	cmd := exec.CommandContext(ctx, "nsenter", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CgroupFD: int(cgf.Fd()), UseCgroupFD: true, Setsid: true}
	var output cappedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("service %s %s timed out", action, name)
		}
		if output.b.Len() > 0 {
			return fmt.Errorf("service %s %s failed: %s", action, name, strings.TrimSpace(output.b.String()))
		}
		return fmt.Errorf("service %s %s failed: %w", action, name, err)
	}
	return nil
}

func isSystemService(name string) bool {
	name = strings.TrimSuffix(name, "@")
	return excludedSystemServices[name] || strings.HasPrefix(name, "systemd-") || strings.HasPrefix(name, "getty@") || strings.HasPrefix(name, "apt-daily")
}

func serviceProcessNames(service string) []string {
	aliases := map[string][]string{
		"apache2": {"apache2"}, "mariadb": {"mariadbd", "mysqld"}, "mysql": {"mysqld"},
		"postgresql": {"postgres"}, "redis": {"redis-server"}, "redis-server": {"redis-server"},
		"wildfly": {"java"}, "tomcat10": {"java"}, "supervisor": {"supervisord"}, "docker": {"dockerd"}, "php-fpm": {"php-fpm", "php-fpm8.3", "php-fpm8.4"},
	}
	if values, ok := aliases[service]; ok {
		return values
	}
	if strings.Contains(service, "php") && strings.Contains(service, "fpm") {
		return []string{service, "php-fpm", "php-fpm8.3", "php-fpm8.4"}
	}
	return []string{strings.ToLower(service)}
}

func (e *Engine) rememberFirewall(spec proto.FirewallSpec) {
	e.fwMu.Lock()
	defer e.fwMu.Unlock()
	if spec.Scope == "host" {
		e.hostFW = spec
		return
	}
	if e.vpsFW == nil {
		e.vpsFW = map[string]proto.FirewallSpec{}
	}
	e.vpsFW[spec.InstanceID] = spec
}

func (e *Engine) applyRememberedVPSFirewall(id, guestIP string) {
	e.fwMu.Lock()
	spec, ok := e.vpsFW[id]
	e.fwMu.Unlock()
	if !ok {
		return
	}
	spec.GuestIP = guestIP
	if err := e.UpdateFirewall(spec); err != nil {
		log.Printf("firewall start %s: %v", id, err)
	}
}

func (e *Engine) ApplyFirewalls(specs []proto.FirewallSpec) {
	for _, spec := range specs {
		if err := e.UpdateFirewall(spec); err != nil {
			log.Printf("firewall apply: %v", err)
		}
	}
}

func (e *Engine) UpdateFirewall(spec proto.FirewallSpec) error {
	host := spec.Scope == "host" || spec.InstanceID == ""
	if err := proto.NormalizeFirewallSpec(&spec, host); err != nil {
		return err
	}
	if !host && spec.InstanceID == "" {
		return errors.New("instance id required")
	}
	if host {
		e.rememberFirewall(spec)
		return e.Network.SyncHostFirewall(spec.Restrict, spec.Rules)
	}
	e.mu.Lock()
	m, ok := e.meta[spec.InstanceID]
	guestIP := spec.GuestIP
	running := false
	if ok && m.Spec.IPAddress != "" {
		guestIP = m.Spec.IPAddress
		running = m.State == proto.StateRunning
	}
	e.mu.Unlock()
	if guestIP == "" {
		e.rememberFirewall(spec)
		return nil
	}
	spec.GuestIP = guestIP
	e.rememberFirewall(spec)
	if !ok || !running {
		return nil
	}
	return e.Network.SyncVPSFirewall(spec.InstanceID, guestIP, spec.Restrict, spec.Rules)
}

func (e *Engine) UpdatePorts(id string, spec proto.InstanceSpec) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.meta[id]
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	previous := m.Spec
	m.Spec.PortMappings = append([]proto.PortMapping(nil), spec.PortMappings...)
	m.Spec.PublishSSH = spec.PublishSSH
	m.Spec.PublishHTTP = spec.PublishHTTP
	m.Spec.PublishHTTPS = spec.PublishHTTPS
	if m.State == proto.StateRunning && m.Spec.IPAddress != "" {
		if err := e.Network.SyncPorts(id, m.Spec.IPAddress, m.Spec.SSHPort, m.Spec); err != nil {
			m.Spec = previous
			return err
		}
	}
	if err := e.save(m); err != nil {
		m.Spec = previous
		if m.State == proto.StateRunning && m.Spec.IPAddress != "" {
			_ = e.Network.SyncPorts(id, m.Spec.IPAddress, m.Spec.SSHPort, previous)
		}
		return err
	}
	return nil
}

// Committed returns the sum of instance limits for the capacity report.
func (e *Engine) Committed() (ramBytes, swapBytes uint64, cpu float64, diskBytes uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.meta {
		ramBytes += uint64(m.Spec.RAMMB) << 20
		swapBytes += uint64(proto.SwapLimitMB(m.Spec)) << 20
		cpu += m.Spec.CPU
		diskBytes += uint64(m.Spec.DiskMB) << 20
	}
	return
}
