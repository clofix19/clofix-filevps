package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/proto"
)

const (
	metricsRetention   = 30 * 24 * time.Hour
	metricsMinInterval = 60 * time.Second
	metricsWarmInterval = 5 * time.Second
	metricsWarmPoints  = 24
)

// metricsPoint is one dated sample for a chart series (at = unix ms).
type metricsPoint map[string]any

type metricsHistoryStore struct {
	sync.Mutex
	file     string
	Series   map[string][]metricsPoint `json:"series"`
	db       *sql.DB
	lastSave time.Time
}

func loadMetricsHistory(dir string) *metricsHistoryStore {
	d := &metricsHistoryStore{
		file:   filepath.Join(dir, "metrics-history.json"),
		Series: map[string][]metricsPoint{},
	}
	if raw, err := os.ReadFile(d.file); err == nil {
		_ = json.Unmarshal(raw, d)
	}
	if d.Series == nil {
		d.Series = map[string][]metricsPoint{}
	}
	d.openDB()
	d.pruneLocked(time.Now())
	return d
}

func (d *metricsHistoryStore) openDB() {
	url := os.Getenv("CLOFIX_DB_URL")
	if url == "" {
		return
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cf_metrics_samples (
		id BIGSERIAL PRIMARY KEY,
		series TEXT NOT NULL,
		at TIMESTAMPTZ NOT NULL,
		sample JSONB NOT NULL,
		created TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS cf_metrics_samples_series_at ON cf_metrics_samples(series, at DESC)`)
	d.db = db
	d.hydrateFromDB()
}

func (d *metricsHistoryStore) hydrateFromDB() {
	if d.db == nil {
		return
	}
	cutoff := time.Now().Add(-metricsRetention)
	rows, err := d.db.Query(`SELECT series, at, sample FROM cf_metrics_samples WHERE at >= $1 ORDER BY series, at`, cutoff)
	if err != nil {
		return
	}
	defer rows.Close()
	fromDB := map[string][]metricsPoint{}
	for rows.Next() {
		var series string
		var at time.Time
		var raw []byte
		if rows.Scan(&series, &at, &raw) != nil {
			continue
		}
		p := metricsPoint{}
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		p["at"] = float64(at.UnixMilli())
		fromDB[series] = append(fromDB[series], p)
	}
	for series, pts := range fromDB {
		if len(pts) > len(d.Series[series]) {
			d.Series[series] = pts
		}
	}
}

func (d *metricsHistoryStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := d.file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.file); err != nil {
		return err
	}
	d.lastSave = time.Now()
	return nil
}

func (d *metricsHistoryStore) pruneLocked(now time.Time) {
	cut := float64(now.Add(-metricsRetention).UnixMilli())
	for series, pts := range d.Series {
		i := 0
		for i < len(pts) {
			at, _ := pts[i]["at"].(float64)
			if at >= cut {
				break
			}
			i++
		}
		if i > 0 {
			d.Series[series] = pts[i:]
		}
		if len(d.Series[series]) == 0 {
			delete(d.Series, series)
		}
	}
	if d.db != nil {
		_, _ = d.db.Exec(`DELETE FROM cf_metrics_samples WHERE at < $1`, now.Add(-metricsRetention))
	}
}

func (d *metricsHistoryStore) append(series string, sample metricsPoint) {
	if series == "" || sample == nil {
		return
	}
	now := time.Now().UTC()
	d.Lock()
	defer d.Unlock()
	pts := d.Series[series]
	interval := metricsMinInterval
	if len(pts) < metricsWarmPoints {
		interval = metricsWarmInterval
	}
	atMs := float64(now.UnixMilli())
	sample["at"] = atMs
	if n := len(pts); n > 0 {
		lastAt, _ := pts[n-1]["at"].(float64)
		if time.Duration(atMs-lastAt)*time.Millisecond < interval {
			pts[n-1] = sample
			d.Series[series] = pts
			if now.Sub(d.lastSave) > 10*time.Second {
				_ = d.saveLocked()
			}
			return
		}
	}
	pts = append(pts, sample)
	d.Series[series] = pts
	d.pruneLocked(now)
	_ = d.saveLocked()
	if d.db != nil {
		raw, _ := json.Marshal(sample)
		_, _ = d.db.Exec(`INSERT INTO cf_metrics_samples(series, at, sample) VALUES($1,$2,$3)`, series, now, raw)
	}
}

func (d *metricsHistoryStore) query(series string, from, to time.Time) []metricsPoint {
	d.Lock()
	defer d.Unlock()
	pts := d.Series[series]
	if len(pts) == 0 {
		return nil
	}
	fromMs := float64(from.UnixMilli())
	toMs := float64(to.UnixMilli())
	out := make([]metricsPoint, 0, len(pts))
	for _, p := range pts {
		at, _ := p["at"].(float64)
		if at >= fromMs && at <= toMs {
			cp := metricsPoint{}
			for k, v := range p {
				cp[k] = v
			}
			out = append(out, cp)
		}
	}
	return out
}

func (d *metricsHistoryStore) seriesNames() []string {
	d.Lock()
	defer d.Unlock()
	names := make([]string, 0, len(d.Series))
	for k := range d.Series {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (s *Server) metricsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/metrics/history", s.auth(s.getMetricsHistory))
	m.HandleFunc("GET /api/metrics/series", s.auth(s.listMetricsSeries))
}

func (s *Server) getMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if s.metricsHistory == nil {
		writeJSON(w, 200, map[string]any{"series": "", "points": []any{}})
		return
	}
	series := strings.TrimSpace(r.URL.Query().Get("series"))
	if series == "" {
		fail(w, 400, "series required")
		return
	}
	now := time.Now()
	from := now.Add(-time.Hour)
	to := now
	if v := r.URL.Query().Get("from"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = time.UnixMilli(ms)
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = time.UnixMilli(ms)
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	if !to.After(from) {
		to = from.Add(time.Hour)
	}
	points := s.metricsHistory.query(series, from, to)
	if points == nil {
		points = []metricsPoint{}
	}
	writeJSON(w, 200, map[string]any{
		"series": series,
		"from":   from.UTC(),
		"to":     to.UTC(),
		"points": points,
		"count":  len(points),
	})
}

func (s *Server) listMetricsSeries(w http.ResponseWriter, r *http.Request) {
	names := []string{}
	if s.metricsHistory != nil {
		names = s.metricsHistory.seriesNames()
	}
	writeJSON(w, 200, map[string]any{"series": names})
}

func (s *Server) recordFleetAndInstanceMetrics() {
	if s.metricsHistory == nil {
		return
	}
	instances, _ := s.Store.ListInstances()
	workers, _ := s.Store.ListWorkers()
	s.usageMu.RLock()
	usage := map[string]proto.InstanceMetrics{}
	for id, m := range s.usage {
		usage[id] = m
	}
	s.usageMu.RUnlock()

	now := time.Now()
	fleet := metricsPoint{
		"cpuUsed": 0.0, "cpuLimit": 0.0,
		"ramUsed": 0.0, "ramLimit": 0.0,
		"swapUsed": 0.0, "swapLimit": 0.0,
		"diskUsed": 0.0, "diskLimit": 0.0,
		"netIn": 0.0, "netOut": 0.0, "netBw": 0.0,
		"hostNetIn": 0.0, "hostNetOut": 0.0, "hostNetBw": 0.0,
	}
	for _, inst := range instances {
		if inst.State != proto.StateRunning {
			continue
		}
		m := usage[inst.ID]
		cpu := float64(inst.Spec.CPU)
		cpuUsed := cpu * (m.CPUPercent / 100)
		ramUsed := float64(m.MemoryCurrentBytes) / 1073741824
		ramLimit := float64(m.MemoryLimitBytes) / 1073741824
		if ramLimit == 0 {
			ramLimit = float64(inst.Spec.RAMMB) / 1024
		}
		swapUsed := float64(m.SwapCurrentBytes) / 1073741824
		swapLimit := float64(m.SwapLimitBytes) / 1073741824
		if swapLimit == 0 {
			swapMB := int64(512)
			if inst.Spec.SwapMB != nil {
				swapMB = *inst.Spec.SwapMB
			}
			swapLimit = float64(swapMB) / 1024
		}
		diskUsed := float64(m.DiskUsageBytes) / 1073741824
		diskLimit := float64(inst.Spec.DiskMB) / 1024
		netIn := float64(m.NetRxBps)
		netOut := float64(m.NetTxBps)

		fleet["cpuUsed"] = fleet["cpuUsed"].(float64) + cpuUsed
		fleet["cpuLimit"] = fleet["cpuLimit"].(float64) + cpu
		fleet["ramUsed"] = fleet["ramUsed"].(float64) + ramUsed
		fleet["ramLimit"] = fleet["ramLimit"].(float64) + ramLimit
		fleet["swapUsed"] = fleet["swapUsed"].(float64) + swapUsed
		fleet["swapLimit"] = fleet["swapLimit"].(float64) + swapLimit
		fleet["diskUsed"] = fleet["diskUsed"].(float64) + diskUsed
		fleet["diskLimit"] = fleet["diskLimit"].(float64) + diskLimit
		fleet["netIn"] = fleet["netIn"].(float64) + netIn
		fleet["netOut"] = fleet["netOut"].(float64) + netOut
		fleet["netBw"] = fleet["netBw"].(float64) + netIn + netOut

		s.metricsHistory.append("instance:"+inst.ID, metricsPoint{
			"cpuUsed": cpuUsed, "cpuLimit": cpu,
			"ramUsed": ramUsed, "ramLimit": ramLimit,
			"swapUsed": swapUsed, "swapLimit": swapLimit,
			"diskUsed": diskUsed, "diskLimit": diskLimit,
			"netIn": netIn, "netOut": netOut, "netBw": netIn + netOut,
		})
	}
	for _, wk := range workers {
		if wk.Status != "active" {
			continue
		}
		if now.Sub(wk.LastHeartbeat) > time.Minute {
			continue
		}
		inn := float64(wk.Capacity.NetRxBps)
		out := float64(wk.Capacity.NetTxBps)
		fleet["hostNetIn"] = fleet["hostNetIn"].(float64) + inn
		fleet["hostNetOut"] = fleet["hostNetOut"].(float64) + out
		fleet["hostNetBw"] = fleet["hostNetBw"].(float64) + inn + out
	}
	s.metricsHistory.append("fleet", fleet)
}

func (s *Server) recordDockerMetrics() {
	if s.metricsHistory == nil || s.docker == nil {
		return
	}
	d := s.docker
	d.mu.Lock()
	var cpuUsed, ramUsed, ramLimit, diskUsed float64
	var netRx, netTx uint64
	running, total := 0, 0
	for _, st := range d.WorkerStatus {
		for _, c := range st.Containers {
			total++
			state := strings.ToLower(c.State)
			if strings.Contains(state, "run") {
				running++
			}
			cpuUsed += c.CPUPct / 100
			ramUsed += float64(c.MemBytes) / 1073741824
			ramLimit += float64(c.MemLimit) / 1073741824
			diskUsed += float64(c.DiskBytes) / 1073741824
			netRx += c.NetRx
			netTx += c.NetTx
		}
	}
	if total == 0 {
		total = len(d.Containers)
		for _, c := range d.Containers {
			st := strings.ToLower(c.State)
			if strings.Contains(st, "run") {
				running++
			}
		}
	}
	d.mu.Unlock()
	cpuLimit := float64(total)
	if cpuLimit < cpuUsed {
		cpuLimit = cpuUsed
	}
	if cpuLimit < 1 {
		cpuLimit = 1
	}
	if ramLimit < ramUsed {
		ramLimit = ramUsed
	}
	if ramLimit == 0 {
		ramLimit = 0.25
	}
	diskLimit := ramLimit
	if diskLimit < diskUsed {
		diskLimit = diskUsed
	}
	s.metricsHistory.append("docker", metricsPoint{
		"cpuUsed": cpuUsed, "cpuLimit": cpuLimit,
		"ramUsed": ramUsed, "ramLimit": ramLimit,
		"diskUsed": diskUsed, "diskLimit": diskLimit,
		"containersRunning": float64(running), "containersTotal": float64(total),
		"netIn": 0.0, "netOut": 0.0, "netBw": 0.0,
		"netRxBytes": float64(netRx), "netTxBytes": float64(netTx),
	})
}

func (s *Server) recordLoadBalancerMetrics() {
	if s.metricsHistory == nil || s.balancers == nil {
		return
	}
	type snap struct {
		id      string
		point   metricsPoint
	}
	var snaps []snap
	s.balancers.Lock()
	for _, b := range s.balancers.Items {
		healthy := 0
		for _, st := range b.Healthy {
			if st != "" && st != "down" && st != "unhealthy" {
				healthy++
			}
		}
		snaps = append(snaps, snap{id: b.ID, point: metricsPoint{
			"replicas":  float64(len(b.Instances)),
			"min":       float64(b.Min),
			"max":       float64(b.Max),
			"healthy":   float64(healthy),
			"hits":      float64(b.Hits1m),
			"cpu":       0.0,
			"targetCPU": float64(b.TargetCPU),
			"latency":   float64(b.AvgLatencyMS),
		}})
	}
	s.balancers.Unlock()
	for _, sn := range snaps {
		s.metricsHistory.append("lb:"+sn.id, sn.point)
	}
}

func (s *Server) recordWAFMetrics(id string, stats *dock.WAFStats) {
	if s.metricsHistory == nil || id == "" || stats == nil {
		return
	}
	at := stats.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.metricsHistory.append("waf:"+id, metricsPoint{
		"hits":       float64(stats.Hits),
		"blocks":     float64(stats.Blocks),
		"attacks":    float64(stats.Attacks),
		"unique_ips": float64(stats.UniqueIPs),
		"sampled_at": float64(at.UnixMilli()),
	})
}

func (s *Server) recordAllMetricsSnapshots() {
	s.recordFleetAndInstanceMetrics()
	s.recordDockerMetrics()
	s.recordLoadBalancerMetrics()
}
