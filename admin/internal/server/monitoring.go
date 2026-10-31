package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type alertChannel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Webhook string `json:"webhook,omitempty"`
	Enabled bool   `json:"enabled"`
}
type healthMonitor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	Enabled   bool   `json:"enabled"`
	Threshold int    `json:"threshold"`
}
type healthState struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Status   string    `json:"status"`
	Detail   string    `json:"detail"`
	Failures int       `json:"failures"`
	Checked  time.Time `json:"checked"`
}
type alertDelivery struct {
	Attempts int       `json:"attempts"`
	Status   string    `json:"status"`
	Next     time.Time `json:"next"`
}
type healthEvent struct {
	ID         string                   `json:"id"`
	MonitorID  string                   `json:"monitor_id"`
	Name       string                   `json:"name"`
	Status     string                   `json:"status"`
	At         time.Time                `json:"at"`
	Deliveries map[string]alertDelivery `json:"deliveries"`
}
type monitoringStore struct {
	sync.Mutex
	Channels map[string]alertChannel  `json:"channels"`
	Monitors map[string]healthMonitor `json:"monitors"`
	States   map[string]healthState   `json:"states"`
	Events   []healthEvent            `json:"events"`
	file     string
	Error    string `json:"error,omitempty"`
}

func loadMonitoring(dir string) *monitoringStore {
	d := &monitoringStore{file: filepath.Join(dir, "monitoring.json")}
	if raw, e := os.ReadFile(d.file); e == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid monitoring.json")
		}
	}
	if d.Channels == nil {
		d.Channels = map[string]alertChannel{}
	}
	if d.Monitors == nil {
		d.Monitors = map[string]healthMonitor{}
	}
	if d.States == nil {
		d.States = map[string]healthState{}
	}
	return d
}
func (d *monitoringStore) save() error {
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
func (s *Server) monitoringRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/monitoring", s.auth(s.getMonitoring))
	m.HandleFunc("POST /api/monitoring/channels", s.auth(s.saveAlertChannel))
	m.HandleFunc("PUT /api/monitoring/channels/{id}", s.auth(s.saveAlertChannel))
	m.HandleFunc("DELETE /api/monitoring/channels/{id}", s.auth(s.deleteMonitoringResource))
	m.HandleFunc("POST /api/monitoring/monitors", s.auth(s.saveHealthMonitor))
	m.HandleFunc("PUT /api/monitoring/monitors/{id}", s.auth(s.saveHealthMonitor))
	m.HandleFunc("DELETE /api/monitoring/monitors/{id}", s.auth(s.deleteMonitoringResource))
	m.HandleFunc("POST /api/monitoring/channels/{id}/test", s.auth(s.testAlertChannel))
}
func (s *Server) getMonitoring(w http.ResponseWriter, r *http.Request) {
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	channels := []map[string]any{}
	for _, c := range d.Channels {
		channels = append(channels, map[string]any{"id": c.ID, "name": c.Name, "type": c.Type, "enabled": c.Enabled, "configured": c.Webhook != ""})
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i]["name"].(string) < channels[j]["name"].(string) })
	monitors := []healthMonitor{}
	for _, m := range d.Monitors {
		monitors = append(monitors, m)
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].Name < monitors[j].Name })
	states := []healthState{}
	for _, state := range d.States {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	writeJSON(w, 200, map[string]any{"channels": channels, "monitors": monitors, "states": states, "events": d.Events, "error": d.Error, "interval_seconds": 30})
}
func validMonitorHTTP(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func (s *Server) saveAlertChannel(w http.ResponseWriter, r *http.Request) {
	var c alertChannel
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&c) != nil || strings.TrimSpace(c.Name) == "" || len(c.Name) > 80 || (c.Type != "slack" && c.Type != "webhook") {
		fail(w, 400, "name and channel type (slack or webhook) required")
		return
	}
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	c.ID = r.PathValue("id")
	old, exists := d.Channels[c.ID]
	if c.ID != "" && !exists {
		fail(w, 404, "channel not found")
		return
	}
	if c.Webhook == "" && exists {
		c.Webhook = old.Webhook
	}
	if !validMonitorHTTP(c.Webhook) {
		fail(w, 400, "valid HTTP/HTTPS webhook URL required, without user credentials or fragment")
		return
	}
	if c.Type == "slack" {
		u, _ := url.Parse(c.Webhook)
		if u.Scheme != "https" || (u.Hostname() != "hooks.slack.com" && u.Hostname() != "hooks.slack-gov.com") || u.Port() != "" || !strings.HasPrefix(u.Path, "/services/") {
			fail(w, 400, "use an official Slack incoming webhook URL")
			return
		}
	}
	if !exists {
		if len(d.Channels) >= 20 {
			fail(w, 409, "maximum 20 channels")
			return
		}
		c.ID = "chn-" + randHex(8)
	}
	d.Channels[c.ID] = c
	if e := d.save(); e != nil {
		if exists {
			d.Channels[c.ID] = old
		} else {
			delete(d.Channels, c.ID)
		}
		fail(w, 500, "could not save channel")
		return
	}
	s.Store.Audit("admin", "monitoring.channel.save", c.ID, "ok")
	writeJSON(w, 200, map[string]string{"id": c.ID})
}
func (s *Server) saveHealthMonitor(w http.ResponseWriter, r *http.Request) {
	var m healthMonitor
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&m) != nil || strings.TrimSpace(m.Name) == "" || len(m.Name) > 80 || m.Threshold < 1 || m.Threshold > 10 {
		fail(w, 400, "name and failure threshold 1–10 required")
		return
	}
	m.Target = strings.TrimSpace(m.Target)
	if m.Type == "http" {
		if !validMonitorHTTP(m.Target) {
			fail(w, 400, "valid HTTP/HTTPS target required")
			return
		}
	} else if m.Type == "tcp" {
		host, port, e := net.SplitHostPort(m.Target)
		n, err := strconv.Atoi(port)
		if e != nil || host == "" || err != nil || n < 1 || n > 65535 {
			fail(w, 400, "TCP target must be host:port")
			return
		}
	} else if m.Type == "ping" {
		if !validPingTarget(m.Target) {
			fail(w, 400, "ping target must be an IP address or hostname, without URL or port")
			return
		}
	} else {
		fail(w, 400, "monitor type must be http, tcp or ping")
		return
	}
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	m.ID = r.PathValue("id")
	old, exists := d.Monitors[m.ID]
	if m.ID != "" && !exists {
		fail(w, 404, "monitor not found")
		return
	}
	if !exists {
		if len(d.Monitors) >= 100 {
			fail(w, 409, "maximum 100 monitors")
			return
		}
		m.ID = "mon-" + randHex(8)
	}
	d.Monitors[m.ID] = m
	if e := d.save(); e != nil {
		if exists {
			d.Monitors[m.ID] = old
		} else {
			delete(d.Monitors, m.ID)
		}
		fail(w, 500, "could not save monitor")
		return
	}
	delete(d.States, m.ID)
	s.Store.Audit("admin", "monitoring.monitor.save", m.ID, "ok")
	writeJSON(w, 200, m)
}
func (s *Server) deleteMonitoringResource(w http.ResponseWriter, r *http.Request) {
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	id := r.PathValue("id")
	channel := strings.Contains(r.URL.Path, "/channels/")
	c, ce := d.Channels[id]
	m, me := d.Monitors[id]
	if channel && !ce || !channel && !me {
		fail(w, 404, "resource not found")
		return
	}
	if channel {
		delete(d.Channels, id)
	} else {
		delete(d.Monitors, id)
	}
	if e := d.save(); e != nil {
		if channel {
			d.Channels[id] = c
		} else {
			d.Monitors[id] = m
		}
		fail(w, 500, "could not delete resource")
		return
	}
	if !channel {
		delete(d.States, id)
	}
	s.Store.Audit("admin", "monitoring.delete", id, "ok")
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func sendHealthAlert(ctx context.Context, c alertChannel, event healthEvent) string {
	message := fmt.Sprintf("CloFix health alert: %s is %s (%s)", event.Name, event.Status, event.At.UTC().Format(time.RFC3339))
	payload := map[string]any{"text": message}
	if c.Type == "webhook" {
		payload["event"] = event.Status
		payload["monitor_id"] = event.MonitorID
		payload["name"] = event.Name
		payload["timestamp"] = event.At
		payload["event_id"] = event.ID
	}
	raw, _ := json.Marshal(payload)
	req, e := http.NewRequestWithContext(ctx, "POST", c.Webhook, bytes.NewReader(raw))
	if e != nil {
		return "invalid webhook configuration"
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(req)
	if e != nil {
		return "webhook connection failed or timed out"
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Sprintf("webhook returned HTTP %d", response.StatusCode)
	}
	return "delivered"
}
func (s *Server) testAlertChannel(w http.ResponseWriter, r *http.Request) {
	if e := s.checkQuota("notification-channels", 0); e != nil {
		fail(w, 403, e.Error())
		return
	}
	d := s.monitoring
	d.Lock()
	c, ok := d.Channels[r.PathValue("id")]
	d.Unlock()
	if !ok {
		fail(w, 404, "channel not found")
		return
	}
	result := sendHealthAlert(r.Context(), c, healthEvent{Name: "Test notification", Status: "TEST", At: time.Now().UTC()})
	s.Store.Audit("admin", "monitoring.channel.test", c.ID, result)
	if result != "delivered" {
		fail(w, 502, result)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "test notification delivered"})
}
func checkHealthTarget(ctx context.Context, m healthMonitor) healthState {
	state := healthState{ID: m.ID, Name: m.Name, Kind: m.Type, Status: "UP", Checked: time.Now().UTC()}
	if !m.Enabled {
		state.Status = "PAUSED"
		state.Detail = "Monitoring disabled"
		return state
	}
	if m.Type == "ping" {
		if !validPingTarget(m.Target) {
			state.Status = "ERROR"
			state.Detail = "Invalid ping target"
			return state
		}
		binary, e := exec.LookPath("ping")
		if e != nil {
			state.Status = "ERROR"
			state.Detail = "ICMP ping tool unavailable on controller"
			return state
		}
		timeout := "2"
		if runtime.GOOS == "darwin" {
			timeout = "2000"
		}
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		output, e := exec.CommandContext(probeCtx, binary, "-n", "-c", "1", "-W", timeout, m.Target).CombinedOutput()
		if e == nil {
			state.Detail = "ICMP reply received"
			return state
		}
		text := strings.ToLower(string(output))
		state.Status = "DOWN"
		state.Detail = "No ICMP reply or host could not be resolved"
		if strings.Contains(text, "permission") || strings.Contains(text, "operation not permitted") {
			state.Status = "ERROR"
			state.Detail = "Controller lacks ICMP ping permissions"
		}
		return state
	}
	if m.Type == "tcp" {
		conn, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", m.Target)
		if e != nil {
			state.Status = "DOWN"
			state.Detail = "TCP connection failed or timed out"
		} else {
			conn.Close()
			state.Detail = "TCP reachable"
		}
		return state
	}
	req, e := http.NewRequestWithContext(ctx, "GET", m.Target, nil)
	if e != nil {
		state.Status = "DOWN"
		state.Detail = "Invalid target"
		return state
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(req)
	if e != nil {
		state.Status = "DOWN"
		state.Detail = "HTTP connection failed or timed out"
		return state
	}
	response.Body.Close()
	state.Detail = fmt.Sprintf("HTTP %d", response.StatusCode)
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		state.Status = "DOWN"
	}
	return state
}
func (s *Server) RunHealthScheduler(ctx context.Context) {
	s.runHealthChecks(ctx)
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.runHealthChecks(ctx)
		}
	}
}
func (s *Server) runHealthChecks(ctx context.Context) {
	d := s.monitoring
	d.Lock()
	monitors := []healthMonitor{}
	for _, m := range d.Monitors {
		monitors = append(monitors, m)
	}
	d.Unlock()
	results := []healthState{}
	workers, e := s.Store.ListWorkers()
	if e == nil {
		for _, worker := range workers {
			if worker.Status != "active" {
				continue
			}
			state := healthState{ID: "worker:" + worker.ID, Name: "Worker " + worker.ID, Kind: "worker", Status: "UP", Detail: "Heartbeat current", Checked: time.Now().UTC()}
			if time.Since(worker.LastHeartbeat) > time.Minute {
				state.Status = "DOWN"
				state.Detail = "No heartbeat in the last minute"
			}
			results = append(results, state)
		}
	}
	s.balancers.Lock()
	for _, b := range s.balancers.Items {
		if b.Deleting {
			continue
		}
		state := healthState{ID: "lb:" + b.ID, Name: b.Name, Kind: "load-balancer", Status: "UP", Checked: time.Now().UTC(), Detail: fmt.Sprintf("%d healthy backends", len(b.Healthy))}
		if len(b.Healthy) == 0 || b.Mode == "nlb" && b.ListenerError != "" {
			state.Status = "DOWN"
			if time.Since(b.LastScale) < 2*time.Minute {
				state.Status = "PENDING"
			}
			if b.ListenerError != "" {
				state.Detail = "NLB listener unavailable"
			}
		}
		results = append(results, state)
	}
	s.balancers.Unlock()
	jobs := make(chan healthMonitor, len(monitors))
	output := make(chan healthState, len(monitors))
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				output <- checkHealthTarget(ctx, m)
			}
		}()
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].ID < monitors[j].ID })
	plan, licenseErr := s.licensedPlan()
	limit := quotaLimit(plan, "health-checks")
	for index, m := range monitors {
		if licenseErr != nil || limit >= 0 && int64(index) >= limit {
			output <- healthState{ID: m.ID, Name: m.Name, Kind: m.Type, Status: "PAUSED", Detail: "License health-check quota unavailable or exceeded", Checked: time.Now().UTC()}
			continue
		}
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	close(output)
	for state := range output {
		results = append(results, state)
	}
	d.Lock()
	seen := map[string]bool{}
	for _, state := range results {
		threshold := 2
		if strings.HasPrefix(state.ID, "mon-") {
			current, ok := d.Monitors[state.ID]
			var snapshot healthMonitor
			for _, m := range monitors {
				if m.ID == state.ID {
					snapshot = m
					break
				}
			}
			if !ok || current != snapshot {
				continue
			}
			threshold = current.Threshold
		}
		seen[state.ID] = true
		d.recordState(state, threshold)
	}
	for id, state := range d.States {
		if !seen[id] {
			state.Status = "PAUSED"
			state.Detail = "Resource removed or not currently monitored"
			d.States[id] = state
		}
	}
	if e = d.save(); e != nil {
		d.Error = "Could not persist monitoring state"
	} else {
		d.Error = ""
	}
	d.Unlock()
	s.deliverHealthEvents(ctx)
}
func (d *monitoringStore) recordState(state healthState, threshold int) {
	old := d.States[state.ID]
	if state.Status == "DOWN" {
		state.Failures = old.Failures + 1
		if state.Failures < threshold {
			state.Status = old.Status
			if state.Status == "" {
				state.Status = "PENDING"
			}
		}
	}
	d.States[state.ID] = state
	if state.Status == old.Status || state.Status != "DOWN" && !(state.Status == "UP" && old.Status == "DOWN") {
		return
	}
	event := healthEvent{ID: "evt-" + randHex(8), MonitorID: state.ID, Name: state.Name, Status: state.Status, At: state.Checked, Deliveries: map[string]alertDelivery{}}
	for id, c := range d.Channels {
		if c.Enabled {
			event.Deliveries[id] = alertDelivery{Status: "pending"}
		}
	}
	d.Events = append(d.Events, event)
	if len(d.Events) > 100 {
		d.Events = d.Events[len(d.Events)-100:]
	}
}
func (s *Server) deliverHealthEvents(ctx context.Context) {
	if e := s.checkQuota("notification-channels", 0); e != nil {
		return
	}
	d := s.monitoring
	type job struct {
		event   healthEvent
		channel alertChannel
	}
	jobs := []job{}
	d.Lock()
	for _, event := range d.Events {
		for id, delivery := range event.Deliveries {
			channel, ok := d.Channels[id]
			if ok && channel.Enabled && delivery.Status != "delivered" && delivery.Attempts < 5 && !time.Now().Before(delivery.Next) {
				jobs = append(jobs, job{event, channel})
			}
		}
	}
	d.Unlock()
	if len(jobs) > 8 {
		jobs = jobs[:8]
	}
	for _, job := range jobs {
		result := sendHealthAlert(ctx, job.channel, job.event)
		d.Lock()
		for i := range d.Events {
			if d.Events[i].ID == job.event.ID {
				delivery := d.Events[i].Deliveries[job.channel.ID]
				delivery.Attempts++
				delivery.Status = result
				delivery.Next = time.Now().Add(time.Duration(1<<delivery.Attempts) * 30 * time.Second)
				d.Events[i].Deliveries[job.channel.ID] = delivery
				break
			}
		}
		if e := d.save(); e != nil {
			d.Error = "Could not persist alert delivery state"
		}
		d.Unlock()
	}
}

func validPingTarget(target string) bool {
	if net.ParseIP(target) != nil {
		return true
	}
	if len(target) == 0 || len(target) > 253 {
		return false
	}
	for _, label := range strings.Split(target, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
