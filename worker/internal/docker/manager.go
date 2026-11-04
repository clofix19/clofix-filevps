// Package docker implements worker-side Docker Engine product operations.
// Uses the host Docker daemon (CLOFIX_DOCKER_PRODUCT_HOST), never the database DinD socket.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dock "github.com/clofix/filevps/worker/pkg/docker"
)

type Manager struct {
	mu       sync.Mutex
	host     string
	stateDir string
	results  map[string]dock.Result
	cancel   map[string]context.CancelFunc
	statuses dock.Status
	wafReady bool
	wafVer   string
	wafErr   string
}

func New(stateDir string) *Manager {
	host := strings.TrimSpace(os.Getenv("CLOFIX_DOCKER_PRODUCT_HOST"))
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	return &Manager{
		host:     host,
		stateDir: filepath.Join(stateDir, "docker-product"),
		results:  map[string]dock.Result{},
		cancel:   map[string]context.CancelFunc{},
	}
}

func (m *Manager) dockerEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "DOCKER_HOST="+m.host)
}

func (m *Manager) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, "docker", args...)
	c.Env = m.dockerEnv()
	return c
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	c := m.cmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func (m *Manager) EngineInfo(ctx context.Context) dock.EngineInfo {
	info := dock.EngineInfo{Host: m.host}
	out, err := m.run(ctx, "version", "--format", "{{json .}}")
	if err != nil {
		info.Error = err.Error()
		return info
	}
	var raw struct {
		Server struct {
			Version    string `json:"Version"`
			APIVersion string `json:"ApiVersion"`
			Os         string `json:"Os"`
			Arch       string `json:"Arch"`
		} `json:"Server"`
	}
	_ = json.Unmarshal([]byte(out), &raw)
	info.Version = raw.Server.Version
	info.APIVersion = raw.Server.APIVersion
	info.OS = raw.Server.Os
	info.Arch = raw.Server.Arch
	if info.Version == "" {
		info.Error = "docker server not available"
		return info
	}
	sys, err := m.run(ctx, "system", "df", "--format", "{{json .}}")
	if err == nil {
		// docker system df --format may emit multiple JSON objects; parse Images line loosely
		for _, line := range strings.Split(sys, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			if t, _ := row["Type"].(string); t == "Images" || t == "Local Volumes" || t == "Build Cache" || t == "Containers" {
				if sz, ok := row["Size"].(string); ok {
					info.RootDir = sz
				}
			}
		}
	}
	du, _ := m.run(ctx, "info", "--format", "{{.DockerRootDir}}|{{.Driver}}")
	if parts := strings.Split(strings.TrimSpace(du), "|"); len(parts) > 0 {
		info.RootDir = parts[0]
	}
	info.Ready = true
	return info
}

func (m *Manager) Ready() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.EngineInfo(ctx).Ready
}

func (m *Manager) Inventory(ctx context.Context) dock.Status {
	st := dock.Status{Engine: m.EngineInfo(ctx)}
	if !st.Engine.Ready {
		m.mu.Lock()
		m.statuses = st
		m.mu.Unlock()
		return st
	}
	st.Containers = m.listContainers(ctx)
	st.Images = m.listImages(ctx)
	st.Networks = m.listNetworks(ctx)
	st.Volumes = m.listVolumes(ctx)
	m.mu.Lock()
	m.statuses = st
	m.mu.Unlock()
	return st
}

func (m *Manager) Statuses() dock.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statuses
}

func (m *Manager) Results() []dock.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]dock.Result, 0, len(m.results))
	for _, r := range m.results {
		out = append(out, r)
	}
	return out
}

func (m *Manager) Ack(ids []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.results, id)
	}
}

func (m *Manager) Cancel(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.cancel[jobID]; ok {
		c()
		delete(m.cancel, jobID)
	}
	m.results[jobID] = dock.Result{JobID: jobID, State: "CANCELLED", At: time.Now().UTC()}
}

func (m *Manager) Reject(op dock.Operation, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[op.JobID] = dock.Result{JobID: op.JobID, State: "FAILED", Error: reason, At: time.Now().UTC()}
}

func (m *Manager) setResult(r dock.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[r.JobID] = r
}

func (m *Manager) Run(ctx context.Context, op dock.Operation) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel[op.JobID] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.cancel, op.JobID)
		m.mu.Unlock()
		cancel()
	}()
	m.setResult(dock.Result{JobID: op.JobID, State: "RUNNING", Progress: 5, At: time.Now().UTC()})
	var err error
	var data any
	var logs string
	var container *dock.ContainerInfo
	switch op.Action {
	case "engine-status":
		data = m.EngineInfo(ctx)
	case "engine-install", "engine-start", "engine-stop", "engine-restart", "engine-upgrade":
		logs, err = m.engineService(ctx, strings.TrimPrefix(op.Action, "engine-"))
		data = m.EngineInfo(ctx)
	case "image-pull":
		ref := op.Image
		if ref == "" && op.Container != nil {
			ref = op.Container.ImageRef()
		}
		logs, err = m.pullImage(ctx, ref, op.Registry)
		data = m.listImages(ctx)
	case "image-list":
		data = m.listImages(ctx)
	case "image-remove":
		_, err = m.run(ctx, "rmi", "-f", op.Image)
		data = m.listImages(ctx)
	case "image-prune":
		logs, err = m.run(ctx, "image", "prune", "-af")
		data = m.listImages(ctx)
	case "container-create", "container-recreate":
		if op.Container == nil {
			err = fmt.Errorf("container spec required")
			break
		}
		if err = op.Container.Validate(); err != nil {
			break
		}
		if op.Action == "container-recreate" {
			_, _ = m.run(ctx, "rm", "-f", labelName(op.Container.ID))
		}
		container, err = m.createContainer(ctx, *op.Container)
	case "container-start":
		_, err = m.run(ctx, "start", labelName(idOr(op)))
		container = m.inspectContainer(ctx, labelName(idOr(op)))
	case "container-stop":
		_, err = m.run(ctx, "stop", labelName(idOr(op)))
		container = m.inspectContainer(ctx, labelName(idOr(op)))
	case "container-restart":
		_, err = m.run(ctx, "restart", labelName(idOr(op)))
		container = m.inspectContainer(ctx, labelName(idOr(op)))
	case "container-pause":
		_, err = m.run(ctx, "pause", labelName(idOr(op)))
		container = m.inspectContainer(ctx, labelName(idOr(op)))
	case "container-unpause":
		_, err = m.run(ctx, "unpause", labelName(idOr(op)))
		container = m.inspectContainer(ctx, labelName(idOr(op)))
	case "container-remove":
		_, err = m.run(ctx, "rm", "-f", labelName(idOr(op)))
	case "container-inspect":
		container = m.inspectContainer(ctx, labelName(idOr(op)))
		if container == nil {
			err = fmt.Errorf("container not found")
		}
	case "container-logs":
		tail := op.LogTail
		if tail <= 0 {
			tail = 200
		}
		logs, err = m.run(ctx, "logs", "--tail", strconv.Itoa(tail), labelName(idOr(op)))
	case "container-exec":
		if len(op.ExecCommand) == 0 {
			err = fmt.Errorf("exec command required")
			break
		}
		if err = validateExec(op.ExecCommand); err != nil {
			break
		}
		args := append([]string{"exec", labelName(idOr(op))}, op.ExecCommand...)
		logs, err = m.run(ctx, args...)
	case "container-stats":
		container = m.statsContainer(ctx, labelName(idOr(op)))
	case "network-create":
		if op.Network == nil {
			err = fmt.Errorf("network required")
			break
		}
		args := []string{"network", "create", "--label", "clofix.docker=1", "--label", "clofix.id=" + op.Network.ID}
		if op.Network.Internal {
			args = append(args, "--internal")
		}
		driver := op.Network.Driver
		if driver == "" {
			driver = "bridge"
		}
		args = append(args, "--driver", driver, netName(op.Network.ID))
		_, err = m.run(ctx, args...)
	case "network-remove":
		_, err = m.run(ctx, "network", "rm", netName(idOrNet(op)))
	case "volume-create":
		if op.Volume == nil {
			err = fmt.Errorf("volume required")
			break
		}
		_, err = m.run(ctx, "volume", "create", "--label", "clofix.docker=1", "--label", "clofix.id="+op.Volume.ID, volName(op.Volume.ID))
	case "volume-remove":
		_, err = m.run(ctx, "volume", "rm", volName(idOrVol(op)))
	case "compose-deploy":
		logs, err = m.compose(ctx, op, "up", "-d", "--remove-orphans")
	case "compose-stop":
		logs, err = m.compose(ctx, op, "stop")
	case "compose-start":
		logs, err = m.compose(ctx, op, "start")
	case "compose-restart":
		logs, err = m.compose(ctx, op, "restart")
	case "compose-remove":
		logs, err = m.compose(ctx, op, "down", "--remove-orphans")
	case "compose-logs":
		logs, err = m.compose(ctx, op, "logs", "--no-color", "--tail", "200")
	case "compose-scale":
		if op.ServiceName == "" || op.Scale < 0 {
			err = fmt.Errorf("service and scale required")
			break
		}
		logs, err = m.compose(ctx, op, "up", "-d", "--scale", fmt.Sprintf("%s=%d", op.ServiceName, op.Scale), "--no-recreate")
	case "git-build":
		logs, data, err = m.gitBuild(ctx, op)
	case "volume-backup":
		logs, err = m.volumeBackup(ctx, op)
	case "volume-restore":
		logs, err = m.volumeRestore(ctx, op)
	case "volume-backup-delete":
		logs, err = m.volumeBackupDelete(ctx, op)
	case "security-scan":
		logs, data, err = m.securityScan(ctx, op.Image)
	case "waf-enable":
		if op.WAFMode == "vps" {
			logs, err = m.wafEnableVPS(ctx, op)
		} else {
			logs, err = m.wafEnable(ctx, op)
		}
	case "waf-disable":
		if op.WAFMode == "vps" {
			logs, err = m.wafDisableVPS(ctx, op)
		} else {
			logs, err = m.wafDisable(ctx, op)
		}
	case "waf-update":
		logs, err = m.wafUpdate(ctx, op)
	case "waf-stats":
		logs, data, err = m.wafStats(ctx, op)
	default:
		err = fmt.Errorf("unsupported docker action %q", op.Action)
	}
	res := dock.Result{JobID: op.JobID, Progress: 100, Logs: truncate(logs, 256<<10), Container: container, At: time.Now().UTC()}
	if err != nil {
		res.State, res.Error = "FAILED", err.Error()
	} else {
		res.State = "SUCCEEDED"
		if data != nil {
			raw, _ := json.Marshal(data)
			res.Data = raw
		}
	}
	m.setResult(res)
	_ = m.Inventory(ctx)
}

func idOr(op dock.Operation) string {
	if op.Container != nil && op.Container.ID != "" {
		return op.Container.ID
	}
	return op.Image // misuse guard
}

func idOrNet(op dock.Operation) string {
	if op.Network != nil {
		return op.Network.ID
	}
	return ""
}
func idOrVol(op dock.Operation) string {
	if op.Volume != nil {
		return op.Volume.ID
	}
	return ""
}

func labelName(id string) string { return "clofix-" + id }
func netName(id string) string   { return "clofix-net-" + id }
func volName(id string) string   { return "clofix-vol-" + id }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…truncated"
}

func validateExec(cmd []string) error {
	if len(cmd) > 32 {
		return fmt.Errorf("exec command too long")
	}
	blocked := map[string]bool{"docker": true, "sudo": true, "su": true, "mount": true, "nsenter": true}
	if blocked[filepath.Base(cmd[0])] {
		return fmt.Errorf("exec binary not allowed")
	}
	for _, a := range cmd {
		if strings.ContainsAny(a, "\x00\n\r") {
			return fmt.Errorf("invalid exec argument")
		}
	}
	return nil
}

func (m *Manager) pullImage(ctx context.Context, ref string, reg *dock.RegistryAuth) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("image required")
	}
	if reg != nil && reg.Username != "" {
		server := reg.Server
		if server == "" {
			server = "https://index.docker.io/v1/"
		}
		login := m.cmd(ctx, "login", "--username", reg.Username, "--password-stdin", server)
		login.Stdin = strings.NewReader(reg.Password)
		var stderr bytes.Buffer
		login.Stderr = &stderr
		if err := login.Run(); err != nil {
			return "", fmt.Errorf("registry login failed")
		}
	}
	return m.run(ctx, "pull", ref)
}

func (m *Manager) createContainer(ctx context.Context, spec dock.ContainerSpec) (*dock.ContainerInfo, error) {
	if _, err := m.pullImage(ctx, spec.ImageRef(), nil); err != nil {
		// continue if already present
		if !strings.Contains(err.Error(), "not found") {
			// still try create — local image may exist
		}
	}
	args := []string{"create",
		"--name", labelName(spec.ID),
		"--label", "clofix.docker=1",
		"--label", "clofix.id=" + spec.ID,
		"--label", "clofix.owner=" + spec.Owner,
		"--memory", fmt.Sprintf("%dm", spec.RAMMB),
		"--cpus", fmt.Sprintf("%.3f", spec.CPU),
		"--security-opt", "no-new-privileges:true",
		"--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "DAC_OVERRIDE", "--cap-add", "NET_BIND_SERVICE",
	}
	rp := spec.RestartPolicy
	if rp == "" {
		rp = "unless-stopped"
	}
	args = append(args, "--restart", rp)
	if spec.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m")
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.WorkDir != "" {
		args = append(args, "-w", spec.WorkDir)
	}
	for k, v := range spec.Env {
		if reservedEnv(k) {
			continue
		}
		args = append(args, "-e", k+"="+v)
	}
	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}
	for _, p := range spec.Ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		if p.HostPort > 0 {
			args = append(args, "-p", fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, proto))
		} else {
			args = append(args, "-p", fmt.Sprintf("%d/%s", p.ContainerPort, proto))
		}
	}
	for _, v := range spec.Volumes {
		src := v.Source
		if v.VolumeID != "" {
			src = volName(v.VolumeID)
		} else if v.Name != "" && src == "" {
			src = v.Name
		}
		if src == "" || v.Target == "" || strings.Contains(src, "..") || strings.HasPrefix(v.Target, "/var/run") {
			return nil, fmt.Errorf("invalid volume mount")
		}
		mode := "rw"
		if v.ReadOnly {
			mode = "ro"
		}
		args = append(args, "-v", src+":"+v.Target+":"+mode)
	}
	for _, n := range spec.Networks {
		args = append(args, "--network", n)
	}
	if spec.Health != nil && len(spec.Health.Test) > 0 {
		args = append(args, "--health-cmd", strings.Join(spec.Health.Test, " "))
		if spec.Health.IntervalSec > 0 {
			args = append(args, "--health-interval", fmt.Sprintf("%ds", spec.Health.IntervalSec))
		}
		if spec.Health.TimeoutSec > 0 {
			args = append(args, "--health-timeout", fmt.Sprintf("%ds", spec.Health.TimeoutSec))
		}
		if spec.Health.Retries > 0 {
			args = append(args, "--health-retries", strconv.Itoa(spec.Health.Retries))
		}
	}
	if len(spec.Entrypoint) > 0 {
		args = append(args, "--entrypoint", spec.Entrypoint[0])
	}
	args = append(args, spec.ImageRef())
	if len(spec.Command) > 0 {
		args = append(args, spec.Command...)
	} else if len(spec.Entrypoint) > 1 {
		args = append(args, spec.Entrypoint[1:]...)
	}
	if _, err := m.run(ctx, args...); err != nil {
		return nil, err
	}
	if _, err := m.run(ctx, "start", labelName(spec.ID)); err != nil {
		return nil, err
	}
	return m.inspectContainer(ctx, labelName(spec.ID)), nil
}

func reservedEnv(k string) bool {
	u := strings.ToUpper(k)
	return u == "PATH" || strings.HasPrefix(u, "LD_") || strings.HasPrefix(u, "DOCKER_") || strings.HasPrefix(u, "CLOFIX_")
}

func parseDockerBytes(s string) uint64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" || s == "--" || strings.EqualFold(s, "N/A") {
		return 0
	}
	// "10.5MiB / 256MiB" or "1.05MB (virtual 187MB)" → first token
	if i := strings.IndexAny(s, "/("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	mult := 1.0
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "KIB"):
		mult, s = 1024, s[:len(s)-3]
	case strings.HasSuffix(upper, "MIB"):
		mult, s = 1024 * 1024, s[:len(s)-3]
	case strings.HasSuffix(upper, "GIB"):
		mult, s = 1024 * 1024 * 1024, s[:len(s)-3]
	case strings.HasSuffix(upper, "TIB"):
		mult, s = 1024 * 1024 * 1024 * 1024, s[:len(s)-3]
	case strings.HasSuffix(upper, "KB"):
		mult, s = 1000, s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		mult, s = 1000 * 1000, s[:len(s)-2]
	case strings.HasSuffix(upper, "GB"):
		mult, s = 1000 * 1000 * 1000, s[:len(s)-2]
	case strings.HasSuffix(upper, "TB"):
		mult, s = 1000 * 1000 * 1000 * 1000, s[:len(s)-2]
	case strings.HasSuffix(upper, "B"):
		mult, s = 1, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0
	}
	return uint64(v * mult)
}

func (m *Manager) attachContainerStats(ctx context.Context, items []dock.ContainerInfo) {
	if len(items) == 0 {
		return
	}
	out, err := m.run(ctx, "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.NetIO}}")
	if err != nil {
		return
	}
	type sample struct{ cpu, mem, net string }
	byName := map[string]sample{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(parts[0]), "/")
		s := sample{cpu: parts[1], mem: parts[2]}
		if len(parts) >= 4 {
			s.net = parts[3]
		}
		byName[name] = s
	}
	for i := range items {
		s, ok := byName[items[i].Name]
		if !ok {
			for name, v := range byName {
				if name == items[i].Name || strings.HasPrefix(name, items[i].Name) || strings.Contains(items[i].Name, name) {
					s, ok = v, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		items[i].CPUPct, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s.cpu), "%"), 64)
		memParts := strings.Split(s.mem, "/")
		items[i].MemBytes = parseDockerBytes(memParts[0])
		if len(memParts) > 1 {
			items[i].MemLimit = parseDockerBytes(memParts[1])
		}
		if s.net != "" {
			netParts := strings.Split(s.net, "/")
			items[i].NetRx = parseDockerBytes(netParts[0])
			if len(netParts) > 1 {
				items[i].NetTx = parseDockerBytes(netParts[1])
			}
		}
	}
}

func (m *Manager) listContainers(ctx context.Context) []dock.ContainerInfo {
	out, err := m.run(ctx, "ps", "-a", "--size", "--filter", "label=clofix.docker=1", "--format", "{{json .}}")
	if err != nil {
		return nil
	}
	var items []dock.ContainerInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			ID         string `json:"ID"`
			Names      string `json:"Names"`
			Image      string `json:"Image"`
			Status     string `json:"Status"`
			State      string `json:"State"`
			Ports      string `json:"Ports"`
			Labels     string `json:"Labels"`
			RunningFor string `json:"RunningFor"`
			Size       string `json:"Size"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		ci := dock.ContainerInfo{
			ID: row.ID, Name: strings.TrimPrefix(row.Names, "/"), Image: row.Image,
			State: mapDockerState(row.State, row.Status), Status: row.Status,
			DiskBytes: parseDockerBytes(row.Size),
		}
		if row.Ports != "" {
			ci.Ports = strings.Split(row.Ports, ", ")
		}
		items = append(items, ci)
	}
	m.attachContainerStats(ctx, items)
	return items
}

func mapDockerState(state, status string) string {
	s := strings.ToLower(state)
	switch {
	case strings.Contains(strings.ToLower(status), "unhealthy"):
		return "Unhealthy"
	case s == "running":
		return "Running"
	case s == "paused":
		return "Paused"
	case s == "restarting":
		return "Restarting"
	case s == "created":
		return "Creating"
	case s == "exited", s == "dead":
		return "Stopped"
	case s == "removing":
		return "Removing"
	default:
		if state == "" {
			return "Failed"
		}
		if len(s) == 0 {
			return "Failed"
		}
		return strings.ToUpper(s[:1]) + s[1:]
	}
}

func (m *Manager) inspectContainer(ctx context.Context, name string) *dock.ContainerInfo {
	out, err := m.run(ctx, "inspect", name)
	if err != nil {
		return nil
	}
	var arr []map[string]any
	if json.Unmarshal([]byte(out), &arr) != nil || len(arr) == 0 {
		return nil
	}
	raw, _ := json.Marshal(arr[0])
	state, _ := arr[0]["State"].(map[string]any)
	cfg, _ := arr[0]["Config"].(map[string]any)
	nameStr, _ := arr[0]["Name"].(string)
	st, _ := state["Status"].(string)
	health := ""
	if h, ok := state["Health"].(map[string]any); ok {
		health, _ = h["Status"].(string)
	}
	img, _ := cfg["Image"].(string)
	restarts := 0
	if n, ok := state["RestartCount"].(float64); ok {
		restarts = int(n)
	}
	return &dock.ContainerInfo{
		ID: name, Name: strings.TrimPrefix(nameStr, "/"), Image: img,
		State: mapDockerState(st, st), Status: st, Health: health, Restarts: restarts, Inspect: raw,
	}
}

func (m *Manager) statsContainer(ctx context.Context, name string) *dock.ContainerInfo {
	ci := m.inspectContainer(ctx, name)
	if ci == nil {
		return nil
	}
	out, err := m.run(ctx, "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}|{{.NetIO}}", name)
	if err != nil {
		return ci
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) >= 1 {
		ci.CPUPct, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[0]), "%"), 64)
	}
	if len(parts) >= 2 {
		memParts := strings.Split(parts[1], "/")
		ci.MemBytes = parseDockerBytes(memParts[0])
		if len(memParts) > 1 {
			ci.MemLimit = parseDockerBytes(memParts[1])
		}
	}
	if len(parts) >= 3 {
		netParts := strings.Split(parts[2], "/")
		ci.NetRx = parseDockerBytes(netParts[0])
		if len(netParts) > 1 {
			ci.NetTx = parseDockerBytes(netParts[1])
		}
	}
	sizeOut, err := m.run(ctx, "ps", "-a", "--size", "--filter", "name=^/"+strings.TrimPrefix(name, "/")+"$", "--format", "{{.Size}}")
	if err == nil {
		ci.DiskBytes = parseDockerBytes(strings.TrimSpace(sizeOut))
	}
	return ci
}

func (m *Manager) listImages(ctx context.Context) []dock.ImageInfo {
	out, err := m.run(ctx, "images", "--format", "{{json .}}")
	if err != nil {
		return nil
	}
	var items []dock.ImageInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			ID         string `json:"ID"`
			Repository string `json:"Repository"`
			Tag        string `json:"Tag"`
			Digest     string `json:"Digest"`
			Size       string `json:"Size"`
			CreatedAt  string `json:"CreatedAt"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		items = append(items, dock.ImageInfo{
			ID: row.ID, Repository: row.Repository, Tag: row.Tag, Digest: row.Digest, Created: row.CreatedAt,
		})
	}
	return items
}

func (m *Manager) listNetworks(ctx context.Context) []dock.NetworkSpec {
	out, err := m.run(ctx, "network", "ls", "--filter", "label=clofix.docker=1", "--format", "{{.ID}}|{{.Name}}|{{.Driver}}")
	if err != nil {
		return nil
	}
	var items []dock.NetworkSpec
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) < 3 {
			continue
		}
		items = append(items, dock.NetworkSpec{ID: parts[0], Name: parts[1], Driver: parts[2]})
	}
	return items
}

func (m *Manager) listVolumes(ctx context.Context) []dock.VolumeSpec {
	out, err := m.run(ctx, "volume", "ls", "--filter", "label=clofix.docker=1", "--format", "{{.Name}}")
	if err != nil {
		return nil
	}
	var items []dock.VolumeSpec
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		id := strings.TrimPrefix(name, "clofix-vol-")
		items = append(items, dock.VolumeSpec{ID: id, Name: name})
	}
	return items
}

func (m *Manager) engineService(ctx context.Context, action string) (string, error) {
	// Admin-authorized host service control via systemctl when available.
	bin, err := exec.LookPath("systemctl")
	if err != nil {
		return "", fmt.Errorf("systemctl not available on this worker")
	}
	var args []string
	switch action {
	case "install":
		return m.installEngine(ctx)
	case "start":
		args = []string{"start", "docker"}
	case "stop":
		args = []string{"stop", "docker"}
	case "restart":
		args = []string{"restart", "docker"}
	case "upgrade":
		return m.installEngine(ctx)
	default:
		return "", fmt.Errorf("unknown engine action")
	}
	c := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (m *Manager) installEngine(ctx context.Context) (string, error) {
	// Distribution-appropriate install for Ubuntu/Debian only; never silent.
	if _, err := exec.LookPath("apt-get"); err != nil {
		return "", fmt.Errorf("automatic Docker install supports apt-based hosts only; install Docker Engine manually")
	}
	script := `set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl
install -m 0755 -d /etc/apt/keyrings
if [ ! -f /etc/apt/keyrings/docker.asc ]; then
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
fi
. /etc/os-release
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker
`
	c := exec.CommandContext(ctx, "bash", "-c", script)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return stdout.String() + stderr.String(), fmt.Errorf("docker install failed: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (m *Manager) compose(ctx context.Context, op dock.Operation, args ...string) (string, error) {
	if op.Compose == nil || strings.TrimSpace(op.Compose.YAML) == "" {
		return "", fmt.Errorf("compose yaml required")
	}
	if err := validateComposeYAML(op.Compose.YAML); err != nil {
		return "", err
	}
	dir := filepath.Join(m.stateDir, "compose", op.Compose.ID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(op.Compose.YAML), 0600); err != nil {
		return "", err
	}
	project := op.Compose.Project
	if project == "" {
		project = "clofix-" + op.Compose.ID
	}
	cmdArgs := append([]string{"compose", "-f", path, "-p", project}, args...)
	return m.run(ctx, cmdArgs...)
}

func validateComposeYAML(yaml string) error {
	lower := strings.ToLower(yaml)
	for _, bad := range []string{"privileged: true", "privileged:true", "network_mode: host", "network_mode:host", "/var/run/docker.sock", "pid: host", "pid:host"} {
		if strings.Contains(lower, bad) {
			return fmt.Errorf("compose security policy rejected: %s", bad)
		}
	}
	if len(yaml) > 1<<20 {
		return fmt.Errorf("compose yaml too large")
	}
	return nil
}

func (m *Manager) gitBuild(ctx context.Context, op dock.Operation) (string, any, error) {
	if op.GitURL == "" {
		return "", nil, fmt.Errorf("git url required")
	}
	dir := filepath.Join(m.stateDir, "builds", op.JobID)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", nil, err
	}
	url := op.GitURL
	if op.GitToken != "" {
		url = injectGitToken(op.GitURL, op.GitToken)
	}
	branch := op.GitBranch
	if branch == "" {
		branch = "main"
	}
	clone := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", branch, url, dir)
	var bout bytes.Buffer
	clone.Stdout, clone.Stderr = &bout, &bout
	if err := clone.Run(); err != nil {
		return bout.String(), nil, fmt.Errorf("git clone failed")
	}
	df := op.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	context := op.BuildContext
	if context == "" {
		context = "."
	}
	tag := op.Image
	if tag == "" {
		tag = "clofix-build/" + op.JobID + ":latest"
	}
	args := []string{"build", "-f", filepath.Join(dir, df), "-t", tag, filepath.Join(dir, context)}
	logs, err := m.run(ctx, args...)
	return bout.String() + "\n" + logs, map[string]string{"image": tag}, err
}

func injectGitToken(rawURL, token string) string {
	if strings.HasPrefix(rawURL, "https://") {
		return "https://oauth2:" + token + "@" + strings.TrimPrefix(rawURL, "https://")
	}
	return rawURL
}

func (m *Manager) volumeBackup(ctx context.Context, op dock.Operation) (string, error) {
	if op.Volume == nil {
		return "", fmt.Errorf("volume required")
	}
	dest := op.BackupDest
	if dest == "" {
		dest = filepath.Join(m.stateDir, "backups")
	}
	if err := os.MkdirAll(dest, 0750); err != nil {
		return "", err
	}
	out := filepath.Join(dest, op.BackupID+".tar.gz")
	// Busybox alpine helper to archive named volume
	args := []string{"run", "--rm", "-v", volName(op.Volume.ID) + ":/data:ro", "-v", dest + ":/backup", "alpine:3.20", "tar", "czf", "/backup/" + op.BackupID + ".tar.gz", "-C", "/data", "."}
	logs, err := m.run(ctx, args...)
	if err != nil {
		return logs, err
	}
	return logs + "\n" + out, nil
}

func (m *Manager) volumeRestore(ctx context.Context, op dock.Operation) (string, error) {
	if op.Volume == nil || op.BackupID == "" {
		return "", fmt.Errorf("volume and backup_id required")
	}
	dest := op.BackupDest
	if dest == "" {
		dest = filepath.Join(m.stateDir, "backups")
	}
	args := []string{"run", "--rm", "-v", volName(op.Volume.ID) + ":/data", "-v", dest + ":/backup:ro", "alpine:3.20", "sh", "-c", "tar xzf /backup/" + op.BackupID + ".tar.gz -C /data"}
	return m.run(ctx, args...)
}

func (m *Manager) volumeBackupDelete(ctx context.Context, op dock.Operation) (string, error) {
	if op.BackupID == "" {
		return "", fmt.Errorf("backup_id required")
	}
	dest := op.BackupDest
	if dest == "" {
		dest = filepath.Join(m.stateDir, "backups")
	}
	path := filepath.Join(dest, op.BackupID+".tar.gz")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "removed " + path + "\n", nil
}

func (m *Manager) securityScan(ctx context.Context, image string) (string, any, error) {
	if image == "" {
		return "", nil, fmt.Errorf("image required")
	}
	if _, err := exec.LookPath("trivy"); err != nil {
		return "", map[string]any{"status": "unavailable", "reason": "trivy not installed on worker"}, nil
	}
	// Prefer local trivy binary (no docker.sock mount for the scanner itself).
	c := exec.CommandContext(ctx, "trivy", "image", "--quiet", "--format", "json", image)
	c.Env = m.dockerEnv()
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return stderr.String(), nil, fmt.Errorf("scan failed")
	}
	var data any
	_ = json.Unmarshal(stdout.Bytes(), &data)
	return "", data, nil
}

const dockerWAFImage = "owasp/modsecurity-crs:nginx"

func wafProxyName(targetID string) string { return "clofix-waf-" + targetID }

func dockerWAFOverrides(policy map[string]bool) string {
	// Mirror FileVPS CRS category → SecRuleRemoveByTag / SecRuleRemoveById mapping.
	tagBy := map[string]string{
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
	var b strings.Builder
	b.WriteString("# CloFix Docker WAF overrides\n")
	keys := make([]string, 0, len(policy))
	for k := range policy {
		keys = append(keys, k)
	}
	// stable order without importing sort if already have sort - check imports
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, key := range keys {
		if policy[key] {
			continue
		}
		control, ok := tagBy[key]
		if !ok {
			continue
		}
		if strings.HasPrefix(control, "id:") {
			fmt.Fprintf(&b, "SecRuleRemoveById \"%s\"\n", strings.TrimPrefix(control, "id:"))
		} else {
			fmt.Fprintf(&b, "SecRuleRemoveByTag \"%s\"\n", control)
		}
	}
	return b.String()
}

func (m *Manager) wafDir(targetID string) string {
	return filepath.Join(m.stateDir, "waf", targetID)
}

var edgeWAFErrorStatuses = []string{"403", "404", "406", "429", "500"}

func defaultEdgeWAFErrorHTML(status string) string {
	titles := map[string]string{
		"403": "Request blocked", "404": "Page not found", "406": "Not acceptable",
		"429": "Too many requests", "500": "Server error",
	}
	messages := map[string]string{
		"403": "This request was blocked by the web application firewall protecting this site.",
		"404": "The page you requested could not be found.",
		"406": "The server cannot produce a response matching the request.",
		"429": "Too many requests were sent in a short time. Please try again later.",
		"500": "The server encountered an error while handling this request.",
	}
	title, msg := titles[status], messages[status]
	if title == "" {
		title = "Error " + status
	}
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

func (m *Manager) writeWAFFiles(targetID string, policy map[string]bool, pages map[string]string) (string, error) {
	dir := m.wafDir(targetID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	overrides := filepath.Join(dir, "REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf")
	if err := os.WriteFile(overrides, []byte(dockerWAFOverrides(policy)), 0o640); err != nil {
		return "", err
	}
	var conf strings.Builder
	conf.WriteString("# CloFix edge WAF custom error pages (server context)\n")
	for _, status := range edgeWAFErrorStatuses {
		html := strings.TrimSpace(pages[status])
		if html == "" {
			html = defaultEdgeWAFErrorHTML(status)
		}
		if err := os.WriteFile(filepath.Join(dir, "clofix-waf-"+status+".html"), []byte(html), 0o640); err != nil {
			return "", err
		}
		fmt.Fprintf(&conf, "error_page %s /clofix-waf-%s.html;\n", status, status)
		fmt.Fprintf(&conf, "location = /clofix-waf-%s.html {\n  root /var/www/clofix-html;\n  internal;\n  default_type text/html;\n  charset utf-8;\n  access_log off;\n}\n", status)
	}
	if err := os.WriteFile(filepath.Join(dir, "clofix-error-pages.conf"), []byte(conf.String()), 0o640); err != nil {
		return "", err
	}
	return overrides, nil
}

// dockerWriteFile writes content into a container path via stdin (works when host bind-mounts cannot see worker FS).
// CRS image runs as USER=nginx; writes use -u 0.
func (m *Manager) dockerWriteFile(ctx context.Context, dockerHost, container, dest string, content []byte) error {
	c := exec.CommandContext(ctx, "docker", "exec", "-u", "0", "-i", container, "sh", "-c", "mkdir -p \"$(dirname \"$1\")\" && cat > \"$1\"", "sh", dest)
	env := os.Environ()
	outEnv := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			continue
		}
		outEnv = append(outEnv, e)
	}
	c.Env = append(outEnv, "DOCKER_HOST="+dockerHost)
	c.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("write %s: %s", dest, msg)
	}
	return nil
}

// applyEdgeWAFErrorPages copies CloFix HTML error pages into the CRS proxy and reloads nginx.
func (m *Manager) applyEdgeWAFErrorPages(ctx context.Context, dockerHost, proxy, targetID string) error {
	dir := m.wafDir(targetID)
	for _, status := range edgeWAFErrorStatuses {
		raw, err := os.ReadFile(filepath.Join(dir, "clofix-waf-"+status+".html"))
		if err != nil {
			raw = []byte(defaultEdgeWAFErrorHTML(status))
		}
		// Prefer /var/www/clofix-html (writable); html root path used in snippet.
		if err := m.dockerWriteFile(ctx, dockerHost, proxy, "/var/www/clofix-html/clofix-waf-"+status+".html", raw); err != nil {
			return err
		}
	}
	conf, err := os.ReadFile(filepath.Join(dir, "clofix-error-pages.conf"))
	if err != nil {
		return err
	}
	if _, err := m.runOn(ctx, dockerHost, "exec", "-u", "0", proxy, "mkdir", "-p", "/etc/nginx/snippets", "/var/www/clofix-html"); err != nil {
		return err
	}
	if err := m.dockerWriteFile(ctx, dockerHost, proxy, "/etc/nginx/snippets/clofix-error-pages.conf", conf); err != nil {
		return err
	}
	inject := `set -e
f=/etc/nginx/conf.d/default.conf
[ -f "$f" ] || exit 0
if ! grep -q 'snippets/clofix-error-pages.conf' "$f"; then
  sed -i 's/server {/server {\n    include \/etc\/nginx\/snippets\/clofix-error-pages.conf;/' "$f"
fi
nginx -t
nginx -s reload
`
	if _, err := m.runOn(ctx, dockerHost, "exec", "-u", "0", proxy, "sh", "-c", inject); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ensureWAFNetwork(ctx context.Context) error {
	name := "clofix-waf"
	if _, err := m.run(ctx, "network", "inspect", name); err == nil {
		return nil
	}
	_, err := m.run(ctx, "network", "create", "--label", "clofix.docker=1", "--label", "clofix.role=waf", name)
	return err
}

// WAFEngineStatus returns cached readiness of the shared OWASP CRS edge engine.
func (m *Manager) WAFEngineStatus() (ready bool, version, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wafReady, m.wafVer, m.wafErr
}

// PrepareWAFEngine pulls the CRS image and ensures the clofix-waf network exists.
// Called once on worker join (and retried) so bindings can activate without reinstalling the engine.
func (m *Manager) PrepareWAFEngine(ctx context.Context) {
	eng := m.EngineInfo(ctx)
	if !eng.Ready {
		m.mu.Lock()
		m.wafReady, m.wafVer, m.wafErr = false, "", "Docker engine not ready"
		if eng.Error != "" {
			m.wafErr = "Docker engine not ready: " + eng.Error
		}
		m.mu.Unlock()
		return
	}
	if err := m.ensureWAFNetwork(ctx); err != nil {
		m.mu.Lock()
		m.wafReady, m.wafVer, m.wafErr = false, "", "WAF network: "+err.Error()
		m.mu.Unlock()
		return
	}
	if _, err := m.run(ctx, "image", "inspect", dockerWAFImage); err != nil {
		m.mu.Lock()
		m.wafReady, m.wafVer, m.wafErr = false, "", "pulling "+dockerWAFImage+"…"
		m.mu.Unlock()
		pullCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		_, err = m.run(pullCtx, "pull", dockerWAFImage)
		cancel()
		if err != nil {
			m.mu.Lock()
			m.wafReady, m.wafVer, m.wafErr = false, "", "pull "+dockerWAFImage+": "+err.Error()
			m.mu.Unlock()
			return
		}
	}
	m.mu.Lock()
	m.wafReady, m.wafVer, m.wafErr = true, dockerWAFImage, ""
	m.mu.Unlock()
	m.healVPSWAFGateways(ctx)
}

// PrepareWAFEngineLoop keeps the shared WAF engine ready while the worker runs.
func (m *Manager) PrepareWAFEngineLoop(ctx context.Context) {
	m.PrepareWAFEngine(ctx)
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	heal := time.NewTicker(30 * time.Second)
	defer heal.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heal.C:
			m.healVPSWAFGateways(ctx)
		case <-t.C:
			m.mu.Lock()
			ready := m.wafReady
			m.mu.Unlock()
			if !ready {
				m.PrepareWAFEngine(ctx)
			} else {
				m.healVPSWAFGateways(ctx)
			}
		}
	}
}

// healVPSWAFGateways recreates FileVPS edge proxies whose --network container:<worker>
// points at a stale worker container (common after docker compose recreate).
func (m *Manager) healVPSWAFGateways(ctx context.Context) {
	want, err := m.wafVPSNetworkMode()
	if err != nil || want == "" {
		return
	}
	host := m.host
	list, err := m.runOn(ctx, host, "ps", "-aq", "--filter", "label=clofix.waf_mode=vps")
	if err != nil || strings.TrimSpace(list) == "" {
		return
	}
	for _, id := range strings.Fields(list) {
		raw, err := m.runOn(ctx, host, "inspect", "--format",
			`{{.Name}}|{{.HostConfig.NetworkMode}}|{{range .Config.Env}}{{println .}}{{end}}`, id)
		if err != nil {
			continue
		}
		parts := strings.SplitN(raw, "|", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(parts[0]), "/")
		mode := strings.TrimSpace(parts[1])
		backend, port := "", ""
		for _, line := range strings.Split(parts[2], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "BACKEND=") {
				backend = strings.TrimPrefix(line, "BACKEND=")
			}
			if strings.HasPrefix(line, "PORT=") {
				port = strings.TrimPrefix(line, "PORT=")
			}
		}
		target := strings.TrimPrefix(name, "clofix-waf-")
		if sameContainerNetwork(mode, want) {
			// Keep custom error pages present across entrypoint regenerations.
			if _, err := m.runOn(ctx, host, "exec", "-u", "0", name, "test", "-f", "/var/www/clofix-html/clofix-waf-403.html"); err != nil {
				_, _ = m.writeWAFFiles(target, nil, nil)
				_ = m.applyEdgeWAFErrorPages(ctx, host, name, target)
			}
			continue
		}
		if name == "" || backend == "" || port == "" {
			continue
		}
		op := dock.Operation{
			WAFTargetID: target, WAFBackend: backend, WAFMode: "vps",
			WAFPorts: []dock.PortPublish{{HostPort: atoiPort(port), ContainerPort: 80, Protocol: "tcp"}},
		}
		if op.WAFPorts[0].HostPort < 1 {
			continue
		}
		_, _ = m.writeWAFFiles(target, nil, nil)
		if _, err := m.wafEnableVPS(ctx, op); err != nil {
			continue
		}
	}
}

func atoiPort(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func (m *Manager) wafEnable(ctx context.Context, op dock.Operation) (string, error) {
	if op.Container == nil || op.WAFTargetID == "" {
		return "", fmt.Errorf("container target required")
	}
	if len(op.WAFPorts) == 0 {
		return "", fmt.Errorf("host ports required for WAF edge")
	}
	if err := m.ensureWAFNetwork(ctx); err != nil {
		return "", err
	}
	target := labelName(op.WAFTargetID)
	proxy := wafProxyName(op.WAFTargetID)
	overrides, err := m.writeWAFFiles(op.WAFTargetID, op.WAFPolicy, op.WAFErrorPages)
	if err != nil {
		return "", err
	}
	var logs strings.Builder
	// Recreate app on the WAF network with no host publishes (edge proxy owns them).
	inner := *op.Container
	inner.Ports = nil
	inner.Networks = []string{"clofix-waf"}
	_, _ = m.run(ctx, "rm", "-f", proxy)
	_, _ = m.run(ctx, "rm", "-f", target)
	ci, err := m.createContainer(ctx, inner)
	if err != nil {
		return logs.String(), fmt.Errorf("recreate app behind WAF: %w", err)
	}
	logs.WriteString("recreated app without host ports\n")
	if _, err := m.run(ctx, "start", target); err != nil {
		return logs.String(), err
	}
	if _, err := m.pullImage(ctx, dockerWAFImage, nil); err != nil {
		logs.WriteString("pull waf image: " + err.Error() + "\n")
	}
	backend := op.WAFBackend
	if backend == "" {
		backend = "http://" + target + ":80"
	}
	args := []string{"run", "-d",
		"--name", proxy,
		"--label", "clofix.docker=1",
		"--label", "clofix.role=waf",
		"--label", "clofix.waf_for=" + op.WAFTargetID,
		"--network", "clofix-waf",
		"--restart", "unless-stopped",
		"-e", "BACKEND="+backend,
		"-e", "PORT=8080",
		"-e", "MODSEC_RULE_ENGINE=On",
		"-e", "BLOCKING_PARANOIA=1",
		"-v", overrides + ":/etc/modsecurity.d/owasp-crs/rules/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf:ro",
	}
	for _, p := range op.WAFPorts {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		args = append(args, "-p", fmt.Sprintf("%d:8080/%s", p.HostPort, proto))
	}
	args = append(args, dockerWAFImage)
	out, err := m.run(ctx, args...)
	logs.WriteString(out)
	if err != nil {
		// Best-effort restore original publishes if WAF fails to start.
		_, _ = m.run(ctx, "rm", "-f", target)
		_, _ = m.createContainer(ctx, *op.Container)
		_, _ = m.run(ctx, "start", target)
		return logs.String(), err
	}
	_ = ci
	// Wait briefly for nginx entrypoint before injecting error pages.
	time.Sleep(2 * time.Second)
	if err := m.applyEdgeWAFErrorPages(ctx, m.wafEdgeDockerHost(), proxy, op.WAFTargetID); err != nil {
		logs.WriteString("error pages: " + err.Error() + "\n")
	} else {
		logs.WriteString("applied custom WAF error pages\n")
	}
	return logs.String(), nil
}

func (m *Manager) wafDisable(ctx context.Context, op dock.Operation) (string, error) {
	if op.Container == nil || op.WAFTargetID == "" {
		return "", fmt.Errorf("container target required")
	}
	target := labelName(op.WAFTargetID)
	proxy := wafProxyName(op.WAFTargetID)
	var logs strings.Builder
	if out, err := m.run(ctx, "rm", "-f", proxy); err != nil {
		logs.WriteString(out + "\n")
	} else {
		logs.WriteString("removed waf proxy\n")
	}
	_, _ = m.run(ctx, "rm", "-f", target)
	if _, err := m.createContainer(ctx, *op.Container); err != nil {
		return logs.String(), err
	}
	if _, err := m.run(ctx, "start", target); err != nil {
		return logs.String(), err
	}
	logs.WriteString("restored app with host ports\n")
	return logs.String(), nil
}

func (m *Manager) wafUpdate(ctx context.Context, op dock.Operation) (string, error) {
	if op.WAFTargetID == "" {
		return "", fmt.Errorf("waf target required")
	}
	overrides, err := m.writeWAFFiles(op.WAFTargetID, op.WAFPolicy, op.WAFErrorPages)
	if err != nil {
		return "", err
	}
	proxy := wafProxyName(op.WAFTargetID)
	host := m.host
	if op.WAFMode != "vps" {
		host = m.wafEdgeDockerHost()
	}
	var logs strings.Builder
	if raw, err := os.ReadFile(overrides); err == nil {
		_ = m.dockerWriteFile(ctx, host, proxy, "/etc/modsecurity.d/owasp-crs/rules/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf", raw)
	}
	if err := m.applyEdgeWAFErrorPages(ctx, host, proxy, op.WAFTargetID); err != nil {
		logs.WriteString("error pages: " + err.Error() + "\n")
	} else {
		logs.WriteString("applied custom WAF error pages\n")
	}
	// Restart so CRS rule overrides are reloaded.
	out, err := m.runOn(ctx, host, "restart", proxy)
	logs.WriteString(out)
	if err != nil {
		return logs.String(), err
	}
	time.Sleep(2 * time.Second)
	_ = m.applyEdgeWAFErrorPages(ctx, host, proxy, op.WAFTargetID)
	return logs.String(), nil
}

// wafEdgeDockerHost prefers the worker-local DinD socket so --network host shares the
// FileVPS bridge namespace (cfbr0) and can reach guest IPs. Falls back to product host.
func (m *Manager) wafEdgeDockerHost() string {
	const dind = "unix:///var/run/clofix-docker/docker.sock"
	if _, err := os.Stat("/var/run/clofix-docker/docker.sock"); err == nil {
		return dind
	}
	return m.host
}

func (m *Manager) runOn(ctx context.Context, dockerHost string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "docker", args...)
	env := os.Environ()
	outEnv := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			continue
		}
		outEnv = append(outEnv, e)
	}
	c.Env = append(outEnv, "DOCKER_HOST="+dockerHost)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

// wafEnableVPS puts OWASP CRS in front of a FileVPS HTTP publish (gateway).
// The proxy shares the worker container network namespace (product Docker) so it can
// bind the published host port and reach the guest on cfbr0 — no guest install.
func (m *Manager) wafEnableVPS(ctx context.Context, op dock.Operation) (string, error) {
	if op.WAFTargetID == "" || op.WAFBackend == "" || len(op.WAFPorts) == 0 {
		return "", fmt.Errorf("vps WAF requires target, backend, and host port")
	}
	// Product Docker host (not DinD): DinD bridge cannot reach FileVPS guests.
	host := m.host
	proxy := wafProxyName(op.WAFTargetID)
	overrides, err := m.writeWAFFiles(op.WAFTargetID, op.WAFPolicy, op.WAFErrorPages)
	if err != nil {
		return "", err
	}
	var logs strings.Builder
	if err := m.ensureWAFImage(ctx, host, &logs); err != nil {
		return logs.String(), err
	}
	netMode, err := m.wafVPSNetworkMode()
	if err != nil {
		return "", err
	}
	_, _ = m.runOn(ctx, host, "rm", "-f", proxy)
	listen := op.WAFPorts[0].HostPort
	if listen < 1 || listen > 65535 {
		return "", fmt.Errorf("invalid WAF listen port")
	}
	// Overrides path is on the worker FS; product Docker on Desktop may not see it.
	// Prefer embedding policy via a named volume under state when path is not shareable —
	// for lab the worker state dir is not mounted into Desktop containers, so skip bind
	// when the path is not visible to the product daemon (best-effort).
	args := []string{"run", "-d",
		"--name", proxy,
		"--network", netMode,
		"--restart", "unless-stopped",
		"--label", "clofix.docker=1",
		"--label", "clofix.role=waf",
		"--label", "clofix.waf_mode=vps",
		"--label", "clofix.waf_for=" + op.WAFTargetID,
		"-e", "BACKEND="+op.WAFBackend,
		"-e", fmt.Sprintf("PORT=%d", listen),
		"-e", "MODSEC_RULE_ENGINE=On",
		"-e", "BLOCKING_PARANOIA=1",
	}
	if m.productDockerCanMount(overrides) {
		args = append(args, "-v", overrides+":/etc/modsecurity.d/owasp-crs/rules/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf:ro")
	} else {
		logs.WriteString("note: CRS overrides not mounted (worker path not visible to product Docker)\n")
	}
	args = append(args, dockerWAFImage)
	out, err := m.runOn(ctx, host, args...)
	logs.WriteString(out)
	if err != nil {
		return logs.String(), err
	}
	logs.WriteString(fmt.Sprintf("\nvps WAF gateway %s → %s on :%d (%s)\n", proxy, op.WAFBackend, listen, netMode))
	// Entrypoint generates nginx conf from PORT; wait then inject CloFix error pages.
	time.Sleep(2 * time.Second)
	if err := m.applyEdgeWAFErrorPages(ctx, host, proxy, op.WAFTargetID); err != nil {
		logs.WriteString("error pages: " + err.Error() + "\n")
	} else {
		logs.WriteString("applied custom WAF error pages\n")
	}
	return logs.String(), nil
}

func (m *Manager) wafDisableVPS(ctx context.Context, op dock.Operation) (string, error) {
	if op.WAFTargetID == "" {
		return "", fmt.Errorf("waf target required")
	}
	host := m.host
	proxy := wafProxyName(op.WAFTargetID)
	out, err := m.runOn(ctx, host, "rm", "-f", proxy)
	if err != nil {
		return out, err
	}
	return "removed vps WAF gateway\n" + out, nil
}

var (
	wafAccessRE = regexp.MustCompile(`^(\S+)\s+\S+\s+\S+\s+\[[^\]]+\]\s+"(\S+)\s+([^"]*?)\s+HTTP/[^"]*"\s+(\d+)`)
	wafAttackTagRE = regexp.MustCompile(`(?i)(?:^|/)(attack-[a-z0-9_-]+|anomaly-evaluation|OWASP_CRS/[A-Z0-9_-]+)`)
)

func (m *Manager) wafStats(ctx context.Context, op dock.Operation) (string, any, error) {
	if op.WAFTargetID == "" {
		return "", nil, fmt.Errorf("waf target required")
	}
	proxy := wafProxyName(op.WAFTargetID)
	host := m.host
	if op.WAFMode != "vps" {
		host = m.wafEdgeDockerHost()
	}
	tail := op.LogTail
	if tail <= 0 {
		tail = 4000
	}
	out, err := m.runOn(ctx, host, "logs", "--tail", strconv.Itoa(tail), proxy)
	stats := parseWAFStats(out, proxy)
	if err != nil {
		stats.Error = err.Error()
		// Container missing → empty stats, not a hard failure for UI.
		if strings.Contains(err.Error(), "No such container") || strings.Contains(strings.ToLower(err.Error()), "no such container") {
			return out, stats, nil
		}
		return out, stats, err
	}
	return fmt.Sprintf("parsed %d audit/access samples from %s\n", stats.Sampled, proxy), stats, nil
}

func parseWAFStats(raw, proxy string) dock.WAFStats {
	stats := dock.WAFStats{Proxy: proxy, At: time.Now().UTC(), TopIPs: []dock.WAFCount{}, TopURIs: []dock.WAFCount{}, TopAttacks: []dock.WAFCount{}, Recent: []dock.WAFEvent{}}
	ipN, uriN, atkN := map[string]int{}, map[string]int{}, map[string]int{}
	seenUID := map[string]bool{}
	lines := strings.Split(raw, "\n")
	hasAudit := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), `{"transaction"`) || strings.HasPrefix(strings.TrimSpace(line), `{"transaction":`) {
			hasAudit = true
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), "{") && strings.Contains(line, `"transaction"`) {
			hasAudit = true
			break
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") && strings.Contains(line, `"transaction"`) {
			var tx struct {
				Transaction struct {
					ClientIP      string `json:"client_ip"`
					TimeStamp     string `json:"time_stamp"`
					UniqueID      string `json:"unique_id"`
					IsInterrupted bool   `json:"is_interrupted"`
					Request       struct {
						Method string `json:"method"`
						URI    string `json:"uri"`
					} `json:"request"`
					Response struct {
						HTTPCode int `json:"http_code"`
					} `json:"response"`
					Messages []struct {
						Message string `json:"message"`
						Details struct {
							RuleID string   `json:"ruleId"`
							Tags   []string `json:"tags"`
						} `json:"details"`
					} `json:"messages"`
				} `json:"transaction"`
			}
			if json.Unmarshal([]byte(line), &tx) != nil {
				continue
			}
			t := tx.Transaction
			if t.UniqueID != "" {
				if seenUID[t.UniqueID] {
					continue
				}
				seenUID[t.UniqueID] = true
			}
			stats.Sampled++
			stats.Hits++
			ip := t.ClientIP
			if ip != "" {
				ipN[ip]++
			}
			uri := t.Request.URI
			if uri != "" {
				uriN[uri]++
			}
			blocked := t.IsInterrupted || t.Response.HTTPCode == 403 || t.Response.HTTPCode == 406 || t.Response.HTTPCode == 429
			if blocked {
				stats.Blocks++
			}
			attackLabel, ruleID, msg := "", "", ""
			var protocolLabel string
			for _, mm := range t.Messages {
				if msg == "" {
					msg = mm.Message
				}
				if ruleID == "" {
					ruleID = mm.Details.RuleID
				}
				for _, tag := range mm.Details.Tags {
					low := strings.ToLower(strings.TrimSpace(tag))
					if !strings.HasPrefix(low, "attack-") {
						continue
					}
					if low == "attack-protocol" {
						if protocolLabel == "" {
							protocolLabel = low
						}
						continue
					}
					// Prefer concrete classes (lfi, xss, sqli, …) over protocol noise.
					attackLabel = low
					if mm.Details.RuleID != "" {
						ruleID = mm.Details.RuleID
					}
					if mm.Message != "" {
						msg = mm.Message
					}
					break
				}
				if attackLabel != "" {
					break
				}
			}
			if attackLabel == "" {
				attackLabel = protocolLabel
			}
			isAttack := blocked || (attackLabel != "" && attackLabel != "attack-protocol")
			if isAttack {
				stats.Attacks++
				key := attackLabel
				if key == "" {
					key = "blocked"
				}
				atkN[key]++
			}
			stats.Recent = append(stats.Recent, dock.WAFEvent{
				Time: t.TimeStamp, IP: ip, Method: t.Request.Method, URI: uri,
				Status: t.Response.HTTPCode, Blocked: blocked, Attack: attackLabel, Message: msg, RuleID: ruleID,
			})
			continue
		}
		if hasAudit {
			continue // avoid double-counting access/error lines when audit JSON is present
		}
		if m := wafAccessRE.FindStringSubmatch(line); len(m) == 5 {
			status, _ := strconv.Atoi(m[4])
			ip, method, uri := m[1], m[2], m[3]
			stats.Sampled++
			stats.Hits++
			ipN[ip]++
			uriN[uri]++
			blocked := status >= 400
			if blocked {
				stats.Blocks++
			}
			stats.Recent = append(stats.Recent, dock.WAFEvent{IP: ip, Method: method, URI: uri, Status: status, Blocked: blocked})
			continue
		}
		if strings.Contains(line, "ModSecurity: Access denied") {
			stats.Sampled++
			stats.Blocks++
			stats.Attacks++
			atkN["modsecurity-deny"]++
			ev := dock.WAFEvent{Blocked: true, Attack: "modsecurity-deny", Message: truncate(line, 240)}
			if i := strings.Index(line, "client: "); i >= 0 {
				rest := line[i+8:]
				if j := strings.IndexByte(rest, ','); j > 0 {
					ev.IP = strings.TrimSpace(rest[:j])
					ipN[ev.IP]++
				}
			}
			if i := strings.Index(line, `[uri "`); i >= 0 {
				rest := line[i+6:]
				if j := strings.IndexByte(rest, '"'); j > 0 {
					ev.URI = rest[:j]
					uriN[ev.URI]++
				}
			}
			stats.Recent = append(stats.Recent, ev)
		}
	}
	stats.UniqueIPs = len(ipN)
	stats.TopIPs = topCounts(ipN, 10)
	stats.TopURIs = topCounts(uriN, 10)
	stats.TopAttacks = topCounts(atkN, 10)
	if n := len(stats.Recent); n > 1 {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			stats.Recent[i], stats.Recent[j] = stats.Recent[j], stats.Recent[i]
		}
	}
	if len(stats.Recent) > 40 {
		stats.Recent = stats.Recent[:40]
	}
	_ = wafAttackTagRE
	return stats
}

func topCounts(m map[string]int, n int) []dock.WAFCount {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v == list[j].v {
			return list[i].k < list[j].k
		}
		return list[i].v > list[j].v
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]dock.WAFCount, len(list))
	for i, x := range list {
		out[i] = dock.WAFCount{Key: x.k, Count: x.v}
	}
	return out
}

func (m *Manager) wafVPSNetworkMode() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CLOFIX_WAF_NETWORK_CONTAINER")); v != "" {
		name := strings.TrimPrefix(v, "container:")
		// Prefer resolved container ID so heal compares equal to docker inspect NetworkMode.
		if id := m.resolveContainerID(context.Background(), m.host, name); id != "" {
			return "container:" + id, nil
		}
		if strings.HasPrefix(v, "container:") {
			return v, nil
		}
		return "container:" + v, nil
	}
	id := selfContainerID()
	if id == "" {
		return "", fmt.Errorf("cannot detect worker container id; set CLOFIX_WAF_NETWORK_CONTAINER")
	}
	return "container:" + id, nil
}

func (m *Manager) resolveContainerID(ctx context.Context, dockerHost, nameOrID string) string {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return ""
	}
	out, err := m.runOn(ctx, dockerHost, "inspect", "--format", "{{.Id}}", nameOrID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// sameContainerNetwork reports whether inspect NetworkMode refers to the same target as want
// (container:<id> vs container:<name> both count as equal).
func sameContainerNetwork(have, want string) bool {
	have, want = strings.TrimSpace(have), strings.TrimSpace(want)
	if have == want {
		return true
	}
	trim := func(s string) string {
		s = strings.TrimPrefix(s, "container:")
		if i := strings.IndexByte(s, ':'); i >= 0 { // container:id:name rare
			s = s[:i]
		}
		return s
	}
	h, w := trim(have), trim(want)
	if h == w {
		return true
	}
	// ID prefix match (short vs full).
	if len(h) >= 12 && len(w) >= 12 && (strings.HasPrefix(h, w) || strings.HasPrefix(w, h)) {
		return true
	}
	return false
}

func selfContainerID() string {
	candidates := []string{"/proc/self/cgroup", "/proc/1/cgroup"}
	re := regexp.MustCompile(`(?i)(?:docker-|cri-containerd-|/docker/|/cri-containerd/)([0-9a-f]{12,64})`)
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if m := re.FindSubmatch(raw); len(m) > 1 {
			return string(m[1])
		}
	}
	// cgroup v2 mountinfo fallback
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		re2 := regexp.MustCompile(`/docker/containers/([0-9a-f]{12,64})/`)
		if m := re2.FindSubmatch(raw); len(m) > 1 {
			return string(m[1])
		}
	}
	return strings.TrimSpace(os.Getenv("HOSTNAME"))
}

func (m *Manager) productDockerCanMount(path string) bool {
	// Desktop Docker cannot mount paths that exist only inside the worker container.
	// Skip bind-mounts unless explicitly allowed or path is under a shared host mount.
	if strings.TrimSpace(os.Getenv("CLOFIX_WAF_MOUNT_OVERRIDES")) == "1" {
		return true
	}
	return false
}

func (m *Manager) ensureWAFImage(ctx context.Context, dockerHost string, logs *strings.Builder) error {
	if _, err := m.runOn(ctx, dockerHost, "image", "inspect", dockerWAFImage); err == nil {
		return nil
	}
	if out, err := m.runOn(ctx, dockerHost, "pull", dockerWAFImage); err == nil {
		if logs != nil {
			logs.WriteString(out)
		}
		return nil
	}
	// DinD often lacks public DNS in lab; copy from the product Docker host if the image is there.
	if dockerHost == m.host {
		if out, err := m.runOn(ctx, m.host, "pull", dockerWAFImage); err != nil {
			if logs != nil {
				logs.WriteString(out)
			}
			return fmt.Errorf("pull WAF image: %w", err)
		}
		return nil
	}
	if _, err := m.runOn(ctx, m.host, "image", "inspect", dockerWAFImage); err != nil {
		if out, err := m.runOn(ctx, m.host, "pull", dockerWAFImage); err != nil {
			if logs != nil {
				logs.WriteString(out)
			}
			return fmt.Errorf("pull WAF image: %w", err)
		}
	}
	save := exec.CommandContext(ctx, "docker", "save", dockerWAFImage)
	save.Env = m.envWithDockerHost(m.host)
	load := exec.CommandContext(ctx, "docker", "load")
	load.Env = m.envWithDockerHost(dockerHost)
	var stderr bytes.Buffer
	load.Stderr = &stderr
	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	load.Stdin = pipe
	if err := load.Start(); err != nil {
		return err
	}
	if err := save.Start(); err != nil {
		_ = load.Process.Kill()
		return err
	}
	if err := save.Wait(); err != nil {
		_ = load.Process.Kill()
		return fmt.Errorf("docker save WAF image: %w", err)
	}
	if err := load.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("docker load WAF image: %s", msg)
	}
	if logs != nil {
		logs.WriteString("loaded WAF image into edge docker from product host\n")
	}
	return nil
}

func (m *Manager) envWithDockerHost(dockerHost string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "DOCKER_HOST="+dockerHost)
}
