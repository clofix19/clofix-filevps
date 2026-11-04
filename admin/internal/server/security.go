package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

type securityFinding struct {
	ID     string `json:"id"`
	Scope  string `json:"scope"`
	Asset  string `json:"asset"`
	Delta  int    `json:"delta"`
	Reason string `json:"reason"`
}

type securityScoreBlock struct {
	Score    int               `json:"score"`
	Findings []securityFinding `json:"findings"`
}

type securityAlertsSummary struct {
	Open24h int `json:"open_24h"`
	Total   int `json:"total"`
}

type securityPosture struct {
	ProtectionOn    int  `json:"protection_on"`
	ProtectionOff   int  `json:"protection_off"`
	DeleteProtected int  `json:"delete_protected"`
	SSHBlocked      int  `json:"ssh_blocked"`
	WAFInstalled    int  `json:"waf_installed"`
	CustomForwards  int  `json:"custom_forwards"`
	ActiveWorkers   int  `json:"active_workers"`
	InactiveWorkers int  `json:"inactive_workers"`
	UserNSEnabled   bool `json:"user_ns_enabled"`
	UserNSKnown     bool `json:"user_ns_known"`
}

func (s *Server) securityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/security/overview", s.auth(s.getSecurityOverview))
	mux.HandleFunc("GET /api/audit", s.auth(s.listAudit))
	s.securityCenterRoutes(mux)
	s.advancedProtectionRoutes(mux)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.Store.ListAudit(limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not list audit events")
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": entries})
}

func (s *Server) getSecurityOverview(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	instances, _ := s.Store.ListInstances()
	if instances == nil {
		instances = []store.Instance{}
	}
	instances = filterInstances(instances, s.visibleAccountIDSet(p), p.Kind == "admin")
	workers, _ := s.Store.ListWorkers()
	if workers == nil {
		workers = []store.Worker{}
	}

	s.usageMu.RLock()
	installs := map[string]map[string]string{}
	for id, m := range s.serviceInstalls {
		cp := map[string]string{}
		for k, v := range m {
			cp[k] = v
		}
		installs[id] = cp
	}
	s.usageMu.RUnlock()

	global := computeSecurityScore(instances, workers, installs, s.recentLogAlertEvents(24*time.Hour))
	byAccount := map[string]securityScoreBlock{}
	byVPS := map[string]securityScoreBlock{}
	byWorker := map[string]securityScoreBlock{}

	accountIDs := map[string]bool{}
	for _, inst := range instances {
		aid := inst.AccountID
		if aid == "" {
			aid = "platform"
		}
		accountIDs[aid] = true
		byVPS[inst.ID] = computeSecurityScore([]store.Instance{inst}, nil, installs, nil)
	}
	for aid := range accountIDs {
		subset := make([]store.Instance, 0)
		for _, inst := range instances {
			cur := inst.AccountID
			if cur == "" {
				cur = "platform"
			}
			if cur == aid {
				subset = append(subset, inst)
			}
		}
		byAccount[aid] = computeSecurityScore(subset, nil, installs, nil)
	}
	for _, wk := range workers {
		byWorker[wk.ID] = computeSecurityScore(nil, []store.Worker{wk}, nil, nil)
	}

	posture := securityPostureFrom(instances, workers, installs)
	alerts := securityAlertsSummary{Open24h: len(s.recentLogAlertEvents(24 * time.Hour)), Total: len(s.allLogAlertEvents())}

	cat := advancedProtectionCatalog()
	live, partial, planned := 0, 0, 0
	for _, f := range cat {
		switch f.Status {
		case "live", "satisfied_by_ro":
			live++
		case "partial":
			partial++
		default:
			planned++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"score":        global.Score,
		"findings":     global.Findings,
		"global":       global,
		"accounts":     byAccount,
		"vps":          byVPS,
		"workers":      byWorker,
		"alerts":       alerts,
		"posture":      posture,
		"filevps":      s.fileVPSProtectionPosture(instances, installs),
		"worker_posture": s.workerProtectionPosture(workers),
		"network_exposure": s.networkExposure(instances),
		"protection_catalog_summary": map[string]int{"live_or_ro": live, "partial": partial, "planned": planned, "total": len(cat)},
		"deep_links":   securityDeepLinks(),
		"formula":      securityScoreFormula(),
		"generated_at": time.Now().UTC(),
	})
}

func securityDeepLinks() []map[string]string {
	return []map[string]string{
		{"label": "Firewall", "href": "/firewall"},
		{"label": "IAM", "href": "/iam"},
		{"label": "Logs", "href": "/logs"},
		{"label": "Health Checks", "href": "/health-checks"},
		{"label": "Databases", "href": "/databases"},
		{"label": "Kubernetes", "href": "/kubernetes"},
	}
}

func securityScoreFormula() []map[string]any {
	return []map[string]any{
		{"id": "protection_off", "delta": -4, "cap": -20, "reason": "Protection Mode off"},
		{"id": "delete_protection_off", "delta": -1, "cap": -8, "reason": "Delete Protection off"},
		{"id": "http_without_waf", "delta": -5, "cap": -20, "reason": "HTTP/HTTPS without WAF"},
		{"id": "extra_port_without_waf", "delta": -3, "cap": -12, "reason": "Extra port forward without WAF"},
		{"id": "inactive_worker", "delta": -10, "cap": 0, "reason": "Inactive worker"},
		{"id": "user_ns_lab", "delta": -15, "cap": -15, "reason": "user_ns=false lab posture"},
		{"id": "log_alert_24h", "delta": -8, "cap": -24, "reason": "Open log alert-event (24h)"},
	}
}

func computeSecurityScore(instances []store.Instance, workers []store.Worker, installs map[string]map[string]string, alerts []logAlertEvent) securityScoreBlock {
	findings := []securityFinding{}
	addCap := func(bucket *int, cap, delta int, id, scope, asset, reason string) {
		if cap < 0 && *bucket+delta < cap {
			delta = cap - *bucket
		}
		if delta == 0 {
			return
		}
		*bucket += delta
		findings = append(findings, securityFinding{ID: id, Scope: scope, Asset: asset, Delta: delta, Reason: reason})
	}

	protSum, delSum, httpSum, portSum, alertSum := 0, 0, 0, 0, 0
	for _, inst := range instances {
		sp := inst.Spec
		protOn := sp.ProtectionMode == nil || *sp.ProtectionMode
		if !protOn {
			addCap(&protSum, -20, -4, "protection_off:"+inst.ID, "vps", inst.ID, "Protection Mode is off")
		}
		if !sp.DeleteProtection {
			addCap(&delSum, -8, -1, "delete_protection_off:"+inst.ID, "vps", inst.ID, "Delete Protection is off")
		}
		wafOK := installs[inst.ID]["owasp-waf"] == "INSTALLED"
		if !wafOK {
			addCap(&httpSum, -20, -5, "http_without_waf:"+inst.ID, "vps", inst.ID, "HTTP/HTTPS published without OWASP CRS WAF")
			for _, m := range sp.PortMappings {
				addCap(&portSum, -12, -3, "extra_port_without_waf:"+inst.ID+":"+strconv.Itoa(m.HostPort), "vps", inst.ID,
					"Extra port forward without WAF (guest "+strconv.Itoa(m.GuestPort)+")")
			}
		}
	}
	for _, wk := range workers {
		if wk.Status != "active" {
			findings = append(findings, securityFinding{
				ID: "inactive_worker:" + wk.ID, Scope: "worker", Asset: wk.ID, Delta: -10,
				Reason: "Worker status is " + wk.Status,
			})
		}
	}
	labSeen := false
	for _, wk := range workers {
		if wk.Status != "active" {
			continue
		}
		// Capacity.UserNS is false for lab Compose posture; true once worker reports user namespaces.
		// Only score when at least one capacity heartbeat has been seen (non-zero RAM).
		if wk.Capacity.RAMTotal == 0 {
			continue
		}
		if !wk.Capacity.UserNS && !labSeen {
			labSeen = true
			findings = append(findings, securityFinding{
				ID: "user_ns_lab", Scope: "platform", Asset: wk.ID, Delta: -15,
				Reason: "Detectable user_ns=false lab posture on worker " + wk.ID,
			})
		}
	}
	for _, ev := range alerts {
		addCap(&alertSum, -24, -8, "log_alert:"+ev.ID, "alert", ev.SourceID, "Log alert in last 24h: "+ev.Name)
	}

	score := 100
	for _, f := range findings {
		score += f.Delta
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	if findings == nil {
		findings = []securityFinding{}
	}
	return securityScoreBlock{Score: score, Findings: findings}
}

func securityPostureFrom(instances []store.Instance, workers []store.Worker, installs map[string]map[string]string) securityPosture {
	p := securityPosture{}
	userNSTrue, userNSFalse := false, false
	for _, inst := range instances {
		sp := inst.Spec
		if sp.ProtectionMode == nil || *sp.ProtectionMode {
			p.ProtectionOn++
		} else {
			p.ProtectionOff++
		}
		if sp.DeleteProtection {
			p.DeleteProtected++
		}
		if sp.SSHLoginBlocked != nil && *sp.SSHLoginBlocked {
			p.SSHBlocked++
		}
		if installs[inst.ID]["owasp-waf"] == "INSTALLED" {
			p.WAFInstalled++
		}
		p.CustomForwards += len(sp.PortMappings)
	}
	for _, wk := range workers {
		if wk.Status == "active" {
			p.ActiveWorkers++
			if wk.Capacity.RAMTotal > 0 {
				if wk.Capacity.UserNS {
					userNSTrue = true
				} else {
					userNSFalse = true
				}
			}
		} else {
			p.InactiveWorkers++
		}
	}
	p.UserNSKnown = userNSTrue || userNSFalse
	p.UserNSEnabled = userNSTrue && !userNSFalse
	return p
}

func (s *Server) allLogAlertEvents() []logAlertEvent {
	if s.logs == nil {
		return nil
	}
	s.logs.Lock()
	defer s.logs.Unlock()
	return append([]logAlertEvent(nil), s.logs.Events...)
}

func (s *Server) recentLogAlertEvents(window time.Duration) []logAlertEvent {
	cutoff := time.Now().Add(-window)
	out := []logAlertEvent{}
	for _, ev := range s.allLogAlertEvents() {
		if ev.At.After(cutoff) {
			out = append(out, ev)
		}
	}
	return out
}

func supportedModeControls() []string {
	return []string{"protection_mode", "ssh_login_blocked", "delete_protection"}
}

func isSupportedModeControl(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, c := range supportedModeControls() {
		if c == name {
			return true
		}
	}
	return false
}
