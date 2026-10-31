package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

// The balancer owns only instances it creates. Configuration and membership survive restarts.
type loadBalancer struct {
	Domains       []string           `json:"domains"`
	Mode          string             `json:"mode"`
	PublicIP      string             `json:"public_ip"`
	ListenPort    int                `json:"listen_port"`
	BackendPort   int                `json:"backend_port"`
	ListenerError string             `json:"listener_error,omitempty"`
	ID            string             `json:"id"`
	Slug          string             `json:"slug"`
	BackendStatus map[string]string  `json:"backend_status"`
	Name          string             `json:"name"`
	Algorithm     string             `json:"algorithm"`
	Min           int                `json:"min_vps"`
	Max           int                `json:"max_vps"`
	TargetCPU     float64            `json:"target_cpu"`
	HealthPath    string             `json:"health_path"`
	Spec          proto.InstanceSpec `json:"spec"`
	Instances     []string           `json:"instances"`
	Deleting      bool               `json:"deleting"`
	Error         string             `json:"error,omitempty"`
	LastScale     time.Time          `json:"last_scale"`
	Healthy       map[string]string  `json:"healthy"`
	sequence      uint64
	active        map[string]int
	latency       map[string]time.Duration
}
type loadBalancerStore struct {
	sync.Mutex
	Network   nlbSettings `json:"network"`
	listeners map[string]nlbListener
	Items     map[string]*loadBalancer `json:"items"`
	file      string
}

func loadBalancers(dir string) *loadBalancerStore {
	d := &loadBalancerStore{Items: map[string]*loadBalancer{}, file: filepath.Join(dir, "load-balancers.json")}
	if b, e := os.ReadFile(d.file); e == nil {
		if json.Unmarshal(b, d) != nil {
			panic("invalid load-balancers.json")
		}
	}
	if d.Items == nil {
		d.Items = map[string]*loadBalancer{}
	}
	for _, b := range d.Items {
		b.Healthy = map[string]string{}
		b.active = map[string]int{}
		b.latency = map[string]time.Duration{}
	}
	return d
}
func (d *loadBalancerStore) save() error {
	if e := os.MkdirAll(filepath.Dir(d.file), 0700); e != nil {
		return e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if e = os.WriteFile(d.file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(d.file+".tmp", d.file)
}

var lbSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var lbAlgorithms = map[string]bool{"round_robin": true, "least_connections": true, "least_response_time": true, "ip_hash": true, "uri_hash": true, "random": true, "power_of_two": true}

func (s *Server) loadBalancerRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/load-balancers/settings", s.auth(s.getNLBSettings))
	m.HandleFunc("PUT /api/load-balancers/settings", s.auth(s.saveNLBSettings))
	m.HandleFunc("GET /api/load-balancers", s.auth(s.listLoadBalancers))
	m.HandleFunc("POST /api/load-balancers", s.auth(s.saveLoadBalancer))
	m.HandleFunc("PUT /api/load-balancers/{id}", s.auth(s.saveLoadBalancer))
	m.HandleFunc("DELETE /api/load-balancers/{id}", s.auth(s.deleteLoadBalancer))
	// Public HTTP application traffic; management remains authenticated.
	m.HandleFunc("/lb/{id}/{path...}", s.proxyLoadBalancer)
}
func (s *Server) listLoadBalancers(w http.ResponseWriter, r *http.Request) {
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	out := []*loadBalancer{}
	for _, b := range d.Items {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, out)
}
func (s *Server) saveLoadBalancer(w http.ResponseWriter, r *http.Request) {
	var b loadBalancer
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	if b.Mode == "alb" {
		b.Mode = "http"
	}
	if b.Mode == "" {
		b.Mode = "http"
	}
	if b.Mode != "http" && b.Mode != "nlb" {
		fail(w, 400, "mode must be http or nlb")
		return
	}
	if b.Mode == "nlb" {
		if b.ListenPort < 1 || b.ListenPort > 65535 || (b.BackendPort != 22 && b.BackendPort != 80 && b.BackendPort != 443) {
			fail(w, 400, "TCP listen port must be 1–65535; backend port must be 22, 80 or 443")
			return
		}
		if b.Algorithm == "uri_hash" || b.Algorithm == "least_response_time" {
			fail(w, 400, "select a TCP-compatible algorithm")
			return
		}
	}
	b.Name = strings.TrimSpace(b.Name)
	b.Slug = strings.TrimSpace(b.Slug)
	if b.Slug != "" && (!lbSlug.MatchString(b.Slug) || strings.HasPrefix(b.Slug, "lb-")) {
		fail(w, 400, "URL name must use lowercase letters, numbers and hyphens, maximum 63 characters; lb- is reserved")
		return
	}
	if b.Name == "" || len(b.Name) > 80 || !lbAlgorithms[b.Algorithm] || b.Min < 1 || b.Max < b.Min || b.Max > 100 || b.TargetCPU < 10 || b.TargetCPU > 95 {
		fail(w, 400, "name, supported algorithm, 1 ≤ min ≤ max ≤ 100 and target CPU 10–95 are required")
		return
	}
	if p, e := s.licensedPlan(); e != nil {
		fail(w, 403, e.Error())
		return
	} else if b.Max > p.MaxVPS {
		fail(w, 403, "maximum VPS exceeds the licensed VPS quota")
		return
	}
	if b.HealthPath == "" {
		b.HealthPath = "/"
	}
	u, e := url.ParseRequestURI(b.HealthPath)
	if e != nil || !strings.HasPrefix(b.HealthPath, "/") || strings.HasPrefix(b.HealthPath, "//") || u.Host != "" {
		fail(w, 400, "health path must be a local HTTP path")
		return
	}
	b.Spec.IPAddress = ""
	b.Spec.SSHPort = 0
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	id := r.PathValue("id")
	old := d.Items[id]
	if id != "" && (old == nil || old.Deleting) {
		fail(w, 404, "active load balancer not found")
		return
	}
	if b.Spec.BackupID != "" {
		s.usageMu.RLock()
		backup, ok := s.backups[b.Spec.BackupID]
		s.usageMu.RUnlock()
		if ok && backup.State == "READY" {
			b.Spec.Image = proto.ImageRef{}
			b.Spec.Image.Name, b.Spec.Image.Version = restoreImage(backup.Image)
		} else if old != nil && old.Spec.BackupID == b.Spec.BackupID {
			// Metadata may be absent until a worker heartbeat after restart.
			// Retain the trusted template; actual scale-out still requires READY.
			b.Spec.Image = old.Spec.Image
		} else {
			fail(w, 400, "select a ready VPS backup")
			return
		}
	}
	if !validSSHPublicKey(b.Spec.SSHAuthorizedKey) || b.Spec.Image.Name == "" || b.Spec.Image.Version == "" || b.Spec.RAMMB < 32 || b.Spec.CPU <= 0 || b.Spec.DiskMB <= 0 || b.Spec.DeleteProtection || len(b.Spec.PortMappings) > 0 || len(b.Spec.StorageMounts) > 0 || b.Spec.S3ConnectionID != "" || b.Spec.S3MountPath != "" || b.Spec.SwapMB != nil && *b.Spec.SwapMB < 0 {
		fail(w, 400, "valid image, SSH public key and resources required; shared mounts, custom ports and delete protection are unsupported")
		return
	}
	uniqueDomains := map[string]bool{}
	domains := []string{}
	if b.Mode == "nlb" && len(b.Domains) > 0 {
		fail(w, 400, "custom domain routing is available for ALB only")
		return
	}
	if len(b.Domains) > 20 {
		fail(w, 400, "maximum 20 ALB domains")
		return
	}
	for _, domain := range b.Domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if !validALBDomain(domain) {
			fail(w, 400, "enter valid domain names without scheme, port or path")
			return
		}
		if !uniqueDomains[domain] {
			domains = append(domains, domain)
			uniqueDomains[domain] = true
		}
	}
	b.Domains = domains
	for otherID, other := range d.Items {
		if otherID == id {
			continue
		}
		for _, domain := range other.Domains {
			if uniqueDomains[domain] {
				fail(w, 409, "domain is already assigned to another ALB")
				return
			}
		}
	}
	for otherID, other := range d.Items {
		if b.Slug != "" && otherID != id && other.Slug == b.Slug {
			fail(w, 409, "URL name is already in use")
			return
		}
	}
	b.PublicIP = ""
	b.ListenerError = ""
	if b.Mode == "nlb" {
		if old != nil && old.Mode == "nlb" {
			b.PublicIP = old.PublicIP
		} else {
			b.PublicIP = d.nextIP(id)
		}
		if b.PublicIP == "" {
			fail(w, 409, "configure an allocated IP pool with a free address in NLB network settings first")
			return
		}
	}
	b.BackendStatus = map[string]string{}
	b.ID = id
	b.Instances = []string{}
	b.Healthy = map[string]string{}
	b.active = map[string]int{}
	b.latency = map[string]time.Duration{}
	if old != nil {
		b.Instances = old.Instances
		b.Healthy = old.Healthy
		b.BackendStatus = old.BackendStatus
		b.active = old.active
		b.latency = old.latency
		b.LastScale = old.LastScale
	} else {
		b.ID = "lb-" + randHex(16)
	}
	if old != nil && (old.Mode != b.Mode || old.BackendPort != b.BackendPort) {
		b.Healthy = map[string]string{}
	}
	d.Items[b.ID] = &b
	if e := d.save(); e != nil {
		if old != nil {
			d.Items[b.ID] = old
		} else {
			delete(d.Items, b.ID)
		}
		fail(w, 500, e.Error())
		return
	}
	s.Store.Audit("admin", "load-balancer.save", b.ID, "ok")
	writeJSON(w, 200, &b)
}
func (s *Server) deleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	b := d.Items[r.PathValue("id")]
	if b == nil {
		fail(w, 404, "load balancer not found")
		return
	}
	previous := b.Deleting
	b.Deleting = true
	if e := d.save(); e != nil {
		b.Deleting = previous
		fail(w, 500, e.Error())
		return
	}
	s.Store.Audit("admin", "load-balancer.delete", b.ID, "queued")
	writeJSON(w, 202, map[string]string{"status": "deleting balancer and owned VPS instances"})
}

// Internal lifecycle calls share the same validation, placement and queueing as manual VPS actions.
type lifecycleResponse struct {
	header http.Header
	bytes.Buffer
	code int
}

func (w *lifecycleResponse) Header() http.Header { return w.header }
func (w *lifecycleResponse) WriteHeader(n int)   { w.code = n }
func (s *Server) lbLifecycle(action, id string, spec proto.InstanceSpec) (string, error) {
	payload := map[string]any{"spec": spec}
	if action == "create" && spec.BackupID != "" {
		s.usageMu.RLock()
		backup, ok := s.backups[spec.BackupID]
		s.usageMu.RUnlock()
		if !ok || backup.State != "READY" {
			return "", fmt.Errorf("selected VPS backup is unavailable or not ready")
		}
		payload["backup_id"] = backup.ID
		payload["worker_id"] = backup.WorkerID
	}
	raw, _ := json.Marshal(payload)
	r, _ := http.NewRequest("POST", "/api/instances", bytes.NewReader(raw))
	r.SetPathValue("id", id)
	r = r.WithContext(context.WithValue(r.Context(), quotaLockKey{}, true))
	w := &lifecycleResponse{header: http.Header{}}
	if action == "create" {
		s.createInstance(w, r)
	} else {
		s.instanceOp(proto.ActionDelete)(w, r)
	}
	if w.code >= 400 {
		return "", fmt.Errorf("%s", strings.TrimSpace(w.String()))
	}
	if action == "create" {
		var result struct {
			Instance struct {
				ID string `json:"id"`
			} `json:"instance"`
		}
		if e := json.Unmarshal(w.Bytes(), &result); e != nil {
			return "", e
		}
		return result.Instance.ID, nil
	}
	return id, nil
}
func (s *Server) RunLoadBalancerScheduler(ctx context.Context) {
	s.reconcileLoadBalancers()
	defer func() {
		s.balancers.Lock()
		defer s.balancers.Unlock()
		for _, l := range s.balancers.listeners {
			l.listener.Close()
		}
		s.balancers.listeners = nil
	}()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcileLoadBalancers()
		}
	}
}
func (s *Server) reconcileLoadBalancers() {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	for id, b := range d.Items {
		b.Error = ""
		ids := []string{}
		running := 0
		cpu := 0.0
		metrics := 0
		pending := false
		type healthResult struct{ id, address, status string }
		checks := make(chan healthResult, len(b.Instances))
		checkCount := 0
		b.Healthy = map[string]string{}
		b.BackendStatus = map[string]string{}
		for _, iid := range b.Instances {
			i, e := s.Store.GetInstance(iid)
			if e != nil {
				if e != store.ErrNotFound {
					ids = append(ids, iid)
					b.Error = e.Error()
				}
				continue
			}
			ids = append(ids, iid)
			b.BackendStatus[iid] = i.State
			if b.Deleting || len(ids) > b.Max || i.State == proto.StateFailed {
				if i.State != proto.StateDeleting {
					if _, e = s.lbLifecycle("delete", iid, proto.InstanceSpec{}); e != nil {
						b.Error = e.Error()
					}
				}
				continue
			}
			if i.State != proto.StateRunning {
				pending = true
				continue
			}
			running++
			wk, e := s.Store.GetWorker(i.WorkerID)
			if e != nil || wk.Status != "active" || time.Since(wk.LastHeartbeat) > time.Minute {
				b.BackendStatus[iid] = "Worker unavailable or heartbeat stale"
				continue
			}
			s.usageMu.RLock()
			m, ok := s.usage[iid]
			s.usageMu.RUnlock()
			if ok && i.Spec.CPU > 0 {
				cpu += m.CPUPercent / i.Spec.CPU
				metrics++
			}
			host := wk.Hostname
			if wk.Capacity.ManagedPublicHost != "" {
				host = wk.Capacity.ManagedPublicHost
			}
			if override := os.Getenv("CLOFIX_LB_WORKER_HOST"); override != "" {
				host = override
			}
			if b.Mode == "nlb" {
				port := i.Spec.SSHPort
				switch b.BackendPort {
				case 80:
					port += 1000
				case 443:
					port += 1100
				}
				address := net.JoinHostPort(host, strconv.Itoa(port))
				checkCount++
				go func(iid, address string) {
					result := healthResult{id: iid}
					conn, e := net.DialTimeout("tcp", address, 2*time.Second)
					if e == nil {
						conn.Close()
						result.address = address
						result.status = "TCP reachable"
					} else {
						result.status = e.Error()
					}
					checks <- result
				}(iid, address)
				continue
			}
			address := "http://" + net.JoinHostPort(host, strconv.Itoa(i.Spec.SSHPort+1000))
			checkCount++
			healthHost := ""
			if len(b.Domains) > 0 {
				healthHost = b.Domains[0]
			}
			go func(iid, address, path, healthHost string) {
				result := healthResult{id: iid}
				request, reqErr := http.NewRequest("GET", address+path, nil)
				if reqErr != nil {
					result.status = reqErr.Error()
					checks <- result
					return
				}
				if healthHost != "" {
					request.Host = healthHost
				}
				response, e := client.Do(request)
				if e == nil {
					response.Body.Close()
					result.status = fmt.Sprintf("HTTP %d", response.StatusCode)
					if response.StatusCode >= 200 && response.StatusCode < 400 {
						result.address = address
					}
				}
				if e != nil {
					result.status = e.Error()
				}
				checks <- result
			}(iid, address, b.HealthPath, healthHost)
		}
		for n := 0; n < checkCount; n++ {
			result := <-checks
			b.BackendStatus[result.id] = result.status
			if result.address != "" {
				b.Healthy[result.id] = result.address
			}
		}

		b.Instances = ids
		if b.Deleting {
			if len(ids) == 0 {
				delete(d.Items, id)
			}
			continue
		}
		if b.active == nil {
			b.active = map[string]int{}
			b.latency = map[string]time.Duration{}
		}
		if plan, e := s.licensedPlan(); e != nil {
			b.Error = e.Error()
			continue
		} else if len(d.Items) > plan.MaxLB {
			b.Error = "Load balancer license quota exceeded; autoscaling paused"
			continue
		}
		desired := len(ids)
		if desired < b.Min {
			desired = b.Min
		} else if !pending && running == len(ids) && metrics == running && running > 0 && time.Since(b.LastScale) >= 2*time.Minute {
			avg := cpu / float64(metrics)
			if avg > b.TargetCPU && desired < b.Max {
				desired++
			} else if avg < b.TargetCPU*0.5 && desired > b.Min && len(b.Healthy) == running {
				desired--
			}
		}
		// One operation per pass bounds resource reservation and allows heartbeat feedback.
		if desired > len(ids) {
			iid, e := s.lbLifecycle("create", "", b.Spec)
			if e != nil {
				b.Error = e.Error()
			} else {
				b.Instances = append(b.Instances, iid)
				b.LastScale = time.Now()
			}
		} else if desired < len(ids) {
			iid := ids[len(ids)-1]
			if b.active[iid] == 0 {
				if _, e := s.lbLifecycle("delete", iid, proto.InstanceSpec{}); e != nil {
					b.Error = e.Error()
				} else {
					delete(b.Healthy, iid)
					b.LastScale = time.Now()
				}
			}
		}
	}
	s.reconcileNLBListeners()
	if e := d.save(); e != nil {
		for _, b := range d.Items {
			b.Error = "could not persist load balancer state: " + e.Error()
		}
	}
}
func (s *Server) proxyLoadBalancer(w http.ResponseWriter, r *http.Request) {
	d := s.balancers
	d.Lock()
	key := r.PathValue("id")
	b := d.Items[key]
	if b == nil {
		for _, candidate := range d.Items {
			if candidate.Slug != "" && candidate.Slug == key {
				b = candidate
				break
			}
		}
	}
	if b == nil || b.Deleting {
		d.Unlock()
		fail(w, 404, "load balancer not found")
		return
	}
	if b.Mode == "nlb" {
		d.Unlock()
		fail(w, 400, "this is a TCP NLB; connect to its allocated IP and listener port")
		return
	}
	ids := []string{}
	for id := range b.Healthy {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		d.Unlock()
		fail(w, 503, "no healthy backend")
		return
	}
	index := int(b.sequence % uint64(len(ids)))
	b.sequence++
	switch b.Algorithm {
	case "random":
		index = rand.Intn(len(ids))
	case "ip_hash", "uri_hash":
		key := r.URL.Path
		if b.Algorithm == "ip_hash" {
			key, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		h := fnv.New64a()
		h.Write([]byte(key))
		index = int(h.Sum64() % uint64(len(ids)))
	case "least_connections", "least_response_time":
		for j := range ids {
			if b.Algorithm == "least_connections" && b.active[ids[j]] < b.active[ids[index]] || b.Algorithm == "least_response_time" && b.latency[ids[j]] < b.latency[ids[index]] {
				index = j
			}
		}
	case "power_of_two":
		index = rand.Intn(len(ids))
		other := rand.Intn(len(ids))
		if b.active[ids[other]] < b.active[ids[index]] {
			index = other
		}
	}
	iid := ids[index]
	target, _ := url.Parse(b.Healthy[iid])
	b.active[iid]++
	d.Unlock()
	start := time.Now()
	defer func() {
		d.Lock()
		defer d.Unlock()
		b.active[iid]--
		elapsed := time.Since(start)
		b.latency[iid] = (b.latency[iid]*3 + elapsed) / 4
	}()
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Path = "/" + r.PathValue("path")
		req.URL.RawPath = ""
		req.Host = target.Host
		if r.PathValue("alb_domain") == "true" {
			req.Host = r.Host
		}
		if req.Header.Get("Authorization") == "Bearer "+s.AdminToken {
			req.Header.Del("Authorization")
		}
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("Forwarded")
	}
	proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second, DisableKeepAlives: true}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		d.Lock()
		delete(b.Healthy, iid)
		d.Unlock()
		fail(w, 502, "backend unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func validALBDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (s *Server) routeALBDomain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		d := s.balancers
		d.Lock()
		id := ""
		for _, b := range d.Items {
			if b.Mode == "nlb" {
				continue
			}
			for _, domain := range b.Domains {
				if host == domain {
					id = b.ID
					break
				}
			}
			if id != "" {
				break
			}
		}
		d.Unlock()
		if id == "" {
			next.ServeHTTP(w, r)
			return
		}
		r.SetPathValue("id", id)
		r.SetPathValue("path", strings.TrimPrefix(r.URL.Path, "/"))
		r.SetPathValue("alb_domain", "true")
		s.proxyLoadBalancer(w, r)
	})
}
