package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

// protectionFeature is one row from the Advanced Protection feature list (#1–72 + mgmt).
type protectionFeature struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Name     string `json:"name"`
	Status   string `json:"status"` // live|partial|planned|satisfied_by_ro
	Priority string `json:"priority,omitempty"`
	Note     string `json:"note,omitempty"`
}

func advancedProtectionCatalog() []protectionFeature {
	return []protectionFeature{
		// FileVPS
		{1, "filevps", "Root Filesystem Read-Only", "live", "existing", "Protection Mode remounts guest root read-only"},
		{2, "filevps", "System File Write Protection", "satisfied_by_ro", "P0", "Covered when Protection Mode is on (Overlay upper RO)"},
		{3, "filevps", "Critical Directory Protection", "satisfied_by_ro", "P0", "/etc /bin /sbin /usr /boot /lib* under RO root"},
		{4, "filevps", "Unauthorized File Deletion Protection", "satisfied_by_ro", "P0", "Root RO blocks deletes on protected tree"},
		{5, "filevps", "File Modification Lock", "satisfied_by_ro", "P0", "Root RO blocks modification of system paths"},
		{6, "filevps", "Executable File Protection", "partial", "P1", "RO root; FIM detects worker path hash changes"},
		{7, "filevps", "Package Install/Remove Protection", "live", "P1", "install-service requires Protection Mode off"},
		{8, "filevps", "Configuration Change Protection", "satisfied_by_ro", "P0", "RO root covers /etc when Protection Mode on"},
		{9, "filevps", "Kernel Parameter Protection", "partial", "P0", "Seccomp denylist; sysctl writes limited in guest"},
		{10, "filevps", "Privilege Escalation Protection", "partial", "P0", "User NS (prod) + dropped caps + seccomp"},
		{11, "filevps", "Process Execution Restrictions", "partial", "P1", "Seccomp + no_new_privs"},
		{12, "filevps", "Service Stop/Restart Protection", "planned", "P1", ""},
		{13, "filevps", "Cron Job Modification Protection", "satisfied_by_ro", "P1", "Cron dirs under RO root when protected"},
		{14, "filevps", "Startup Script Protection", "satisfied_by_ro", "P1", "Init scripts under RO root when protected"},
		{15, "filevps", "Binary Integrity Protection", "partial", "P1", "FIM baselines; write-block only via RO mount"},
		// Access
		{16, "access", "SSH Login Block", "live", "", "Per-VPS ssh_login_blocked"},
		{17, "access", "SSH Key-Only Authentication", "live", "", "PasswordAuthentication off via password_auth=false"},
		{18, "access", "SSH Brute-Force Protection", "partial", "", "Auth failures → detections; suggest firewall deny"},
		{19, "access", "Root SSH Login Restriction", "partial", "", "Key-only uses prohibit-password"},
		{20, "access", "SSH IP Allowlist", "live", "", "Use Firewall VPS rules on mapped SSH port"},
		{21, "access", "Temporary SSH Access", "live", "", "POST /api/security/unlock clears SSH block for TTL"},
		{22, "access", "MFA for Console Access", "live", "", "Profile TOTP MFA"},
		{23, "access", "Suspicious Login Detection", "partial", "", "Log alerts + detection refresh"},
		{24, "access", "Session Timeout", "partial", "", "Controller session expiry"},
		{25, "access", "Privileged Command Approval", "live", "", "Approval queue for protection-off / mode apply / quarantine"},
		// Network
		{26, "network", "Inbound Firewall Protection", "live", "", "Worker + VPS nftables CIDR rules"},
		{27, "network", "Outbound Firewall Protection", "planned", "", "Egress direction not in nft path yet"},
		{28, "network", "Port Exposure Protection", "live", "", "Exposure scanner + custom forward score"},
		{29, "network", "Cross-VPS Network Isolation", "live", "", "Bridge-isolated veth + default-drop forward"},
		{30, "network", "Suspicious IP Auto-Block", "partial", "", "Suggest-only deny rule; requires approval"},
		{31, "network", "Connection Rate Limiting", "planned", "", ""},
		{32, "network", "Network Scan Protection", "planned", "", ""},
		{33, "network", "DNS Filtering", "planned", "", ""},
		{34, "network", "Network Quarantine", "live", "", "tpl-quarantine + restrict firewall apply"},
		{35, "network", "Network Policy Change Lock", "partial", "", "Approval gate when AutoPilot=approval"},
		// App
		{36, "app", "OWASP CRS WAF", "live", "", "owasp-waf service"},
		{37, "app", "SQL Injection Protection", "live", "", "CRS-backed category"},
		{38, "app", "XSS Attack Protection", "live", "", "CRS-backed category"},
		{39, "app", "RCE Attempt Filtering", "live", "", "CRS-backed category"},
		{40, "app", "LFI/RFI Protection", "live", "", "CRS-backed category"},
		{41, "app", "HTTP Rate Limiting", "planned", "", ""},
		{42, "app", "Bot Detection and Mitigation", "planned", "", ""},
		{43, "app", "Application Directory Lock", "partial", "", "Protection Mode + limited forwards"},
		{44, "app", "Web Shell Detection", "partial", "", "WAF + log detections"},
		{45, "app", "Upload File Scanning", "planned", "", ""},
		{46, "app", "Application Secret Protection", "planned", "", ""},
		{47, "app", "Application Deployment Lock", "live", "", "install-service blocked while Protection on"},
		// Worker
		{48, "worker", "User Namespace Enforcement", "partial", "", "Prod default true; Compose lab false"},
		{49, "worker", "Seccomp System Call Filtering", "live", "", ""},
		{50, "worker", "Linux Capability Restriction", "live", "", ""},
		{51, "worker", "AppArmor/SELinux Profiles", "planned", "", "No per-guest LSM yet"},
		{52, "worker", "Host Filesystem Isolation", "live", "", "Namespaces + OverlayFS"},
		{53, "worker", "Kernel Module Loading Restrictions", "planned", "", ""},
		{54, "worker", "Worker Runtime Integrity", "partial", "", "FIM baselines"},
		{55, "worker", "CPU/RAM/PID Resource Protection", "live", "", "cgroup v2"},
		{56, "worker", "Disk I/O Resource Protection", "partial", "", "Disk commit limits"},
		{57, "worker", "Worker Enrollment Protection", "live", "", "mTLS + single-use tokens"},
		{58, "worker", "Worker Maintenance Lock", "live", "", "Drain blocks new placement"},
		{59, "worker", "Untrusted Workload Isolation", "partial", "", "Shared-kernel limits documented"},
		// DB/Storage
		{60, "database", "Database TLS Enforcement", "partial", "", "Scored via CA presence"},
		{61, "database", "Database Public Access Block", "partial", "", "Scored via host exposure"},
		{62, "database", "Database User Permission Protection", "planned", "", ""},
		{63, "database", "Destructive Query Approval", "partial", "", "Approval queue type destructive_db"},
		{64, "database", "Database Backup Encryption", "partial", "", "Encrypted key on READY backups"},
		{65, "database", "Database Credential Rotation", "planned", "", ""},
		{66, "storage", "Volume Delete Protection", "live", "", "volume.delete_protection"},
		{67, "storage", "S3 Bucket Access Protection", "partial", "", "IAM + connection credentials"},
		{68, "storage", "S3 Object Versioning", "planned", "", "Objectstore has no versioning yet"},
		{69, "storage", "Immutable Backup / Object Lock", "planned", "", ""},
		{70, "storage", "Storage Encryption at Rest", "partial", "", "Depends on backing volume"},
		{71, "storage", "Snapshot Delete Protection", "planned", "", ""},
		{72, "storage", "Backup Integrity Verification", "partial", "", "SHA256 on DB backups"},
		// Mgmt (unnumbered in list)
		{100, "mgmt", "Protection Profiles", "live", "", "Security Modes templates"},
		{101, "mgmt", "Enable/Disable Per-VPS", "live", "", ""},
		{102, "mgmt", "Protection Scheduler", "live", "", "Binding schedule + tick"},
		{103, "mgmt", "Temporary Unlock", "live", "", "/api/security/unlock"},
		{104, "mgmt", "Approval Workflow", "live", "", "/api/security/approvals"},
		{105, "mgmt", "Policy Inheritance", "live", "", "vps>worker>project>account"},
		{106, "mgmt", "Dry-Run", "live", "", ""},
		{107, "mgmt", "Rollback", "live", "", "/api/security/rollback"},
		{108, "mgmt", "Protection Audit Logs", "live", "", "cf_audit + GET /api/audit"},
		{109, "mgmt", "Protection Alerts", "partial", "", "Log alert-events hub"},
		{110, "mgmt", "Fleet Protection", "live", "", "Mode apply multi-instance"},
		{111, "mgmt", "Protection Compliance", "live", "", "Compliance export + catalog"},
	}
}

func fileVPSCriticalPaths() []string {
	return []string{"/etc", "/bin", "/sbin", "/usr", "/boot", "/lib", "/lib64", "/var/spool/cron"}
}

func (s *Server) getProtectionCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"features":        advancedProtectionCatalog(),
		"critical_paths":  fileVPSCriticalPaths(),
		"protection_note": "Protection Mode (RO root) is orthogonal to Security Modes policies.",
	})
}

func (s *Server) fileVPSProtectionPosture(instances []store.Instance, installs map[string]map[string]string) []map[string]any {
	out := make([]map[string]any, 0, len(instances))
	for _, inst := range instances {
		prot := inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode
		paths := map[string]string{}
		for _, p := range fileVPSCriticalPaths() {
			if prot {
				paths[p] = "ro_enforced"
			} else {
				paths[p] = "writable"
			}
		}
		out = append(out, map[string]any{
			"instance_id":             inst.ID,
			"state":                   inst.State,
			"protection_mode":         prot,
			"root_ro":                 prot,
			"package_install_locked":  prot,
			"config_change_locked":    prot,
			"critical_paths":          paths,
			"waf":                     installs[inst.ID]["owasp-waf"],
			"ssh_login_blocked":       inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked,
			"password_auth":           inst.Spec.PasswordAuth,
			"ssh_key_only":            !inst.Spec.PasswordAuth && inst.Spec.SSHAuthorizedKey != "",
			"delete_protection":       inst.Spec.DeleteProtection,
			"satisfied_by_ro_features": []int{2, 3, 4, 5, 8, 13, 14},
		})
	}
	return out
}

func (s *Server) crsCategoryMap(instances []store.Instance) []map[string]any {
	// Map list #37-40 to CRS-backed categories used by OWASPProtection toggles.
	cats := []struct {
		Feature int
		Key     string
		Label   string
	}{
		{37, "sql", "SQL Injection (CRS)"},
		{38, "xss", "XSS (CRS)"},
		{39, "rce", "RCE (CRS)"},
		{40, "lfi", "LFI/RFI (CRS)"},
	}
	out := []map[string]any{}
	for _, inst := range instances {
		waf := s.serviceInstalls[inst.ID]["owasp-waf"] == "INSTALLED"
		policy := inst.Spec.OWASPProtection
		for _, c := range cats {
			enabled := waf
			if policy != nil {
				if v, ok := policy[c.Key]; ok {
					enabled = waf && v
				}
			}
			status := "planned"
			if waf {
				status = "live"
				if policy != nil {
					if v, ok := policy[c.Key]; ok && !v {
						status = "disabled"
					}
				}
			}
			out = append(out, map[string]any{
				"feature_id": c.Feature, "instance_id": inst.ID, "category": c.Key,
				"label": c.Label, "waf_installed": waf, "enabled": enabled, "status": status,
			})
		}
	}
	return out
}

func (s *Server) networkExposure(instances []store.Instance) []map[string]any {
	out := []map[string]any{}
	for _, inst := range instances {
		sp := inst.Spec
		ports := []map[string]any{
			{"kind": "managed", "protocol": "tcp", "guest_port": 22, "host_port": sp.SSHPort},
			{"kind": "managed", "protocol": "tcp", "guest_port": 80, "host_port": sp.SSHPort + 1000},
			{"kind": "managed", "protocol": "tcp", "guest_port": 443, "host_port": sp.SSHPort + 1100},
		}
		for _, m := range sp.PortMappings {
			ports = append(ports, map[string]any{
				"kind": "custom", "protocol": m.Protocol, "guest_port": m.GuestPort, "host_port": m.HostPort,
			})
		}
		fw := s.firewall.policy("instance", inst.ID)
		out = append(out, map[string]any{
			"instance_id": inst.ID, "ports": ports, "custom_forwards": len(sp.PortMappings),
			"firewall_restrict": fw.Restrict, "firewall_rules": len(fw.Rules),
			"egress_status": "planned",
		})
	}
	return out
}

func (s *Server) workerProtectionPosture(workers []store.Worker) []map[string]any {
	out := []map[string]any{}
	for _, wk := range workers {
		out = append(out, map[string]any{
			"worker_id":         wk.ID,
			"status":            wk.Status,
			"maintenance_lock":  wk.Status == "draining",
			"user_ns":           wk.Capacity.UserNS,
			"user_ns_known":     wk.Capacity.RAMTotal > 0,
			"seccomp":           "live",
			"capabilities":      "restricted",
			"cgroup":            "live",
			"apparmor_selinux":  "planned",
			"kernel_modules":    "planned",
			"enrollment":        "mtls",
			"placement_blocked": wk.Status == "draining" || wk.Status == "revoked",
		})
	}
	return out
}

// --- Phase G: unlock / approvals / rollback / inheritance ---

type protectionUnlock struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instance_id"`
	Control      string    `json:"control"` // protection_mode|ssh_login_blocked
	Expires      time.Time `json:"expires"`
	RestoreValue bool      `json:"restore_value"`
	CreatedBy    string    `json:"created_by"`
	Created      time.Time `json:"created"`
}

type protectionApproval struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"` // protection_off|mode_apply|quarantine|destructive_db|firewall_deny
	Status    string         `json:"status"` // pending|approved|rejected|applied
	Payload   map[string]any `json:"payload"`
	Requested string         `json:"requested_by"`
	DecidedBy string         `json:"decided_by,omitempty"`
	Created   time.Time      `json:"created"`
	Updated   time.Time      `json:"updated"`
	Note      string         `json:"note,omitempty"`
}

type protectionRollback struct {
	TargetID   string         `json:"target_id"`
	PolicyID   string         `json:"policy_id"`
	Version    int            `json:"version"`
	Controls   map[string]any `json:"controls"`
	AppliedAt  time.Time      `json:"applied_at"`
	AppliedBy  string         `json:"applied_by"`
}

func (s *Server) advancedProtectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/security/protection-catalog", s.auth(s.getProtectionCatalog))
	mux.HandleFunc("GET /api/security/filevps-posture", s.auth(s.getFileVPSPosture))
	mux.HandleFunc("GET /api/security/network-exposure", s.auth(s.getNetworkExposure))
	mux.HandleFunc("GET /api/security/worker-posture", s.auth(s.getWorkerPosture))
	mux.HandleFunc("GET /api/security/app-crs", s.auth(s.getAppCRS))
	mux.HandleFunc("POST /api/security/unlock", s.auth(s.createProtectionUnlock))
	mux.HandleFunc("GET /api/security/unlocks", s.auth(s.listProtectionUnlocks))
	mux.HandleFunc("GET /api/security/approvals", s.auth(s.listProtectionApprovals))
	mux.HandleFunc("POST /api/security/approvals", s.auth(s.createProtectionApproval))
	mux.HandleFunc("POST /api/security/approvals/{id}/decide", s.auth(s.decideProtectionApproval))
	mux.HandleFunc("GET /api/security/rollback/{targetID}", s.auth(s.getProtectionRollback))
	mux.HandleFunc("POST /api/security/rollback/{targetID}", s.auth(s.applyProtectionRollback))
	mux.HandleFunc("POST /api/security/quarantine/{id}", s.auth(s.applyNetworkQuarantine))
	mux.HandleFunc("POST /api/security/suggest-deny", s.auth(s.suggestFirewallDeny))
	mux.HandleFunc("GET /api/security/resolve-policy", s.auth(s.resolveInheritedPolicy))
	mux.HandleFunc("POST /api/security/scheduler/tick", s.auth(s.tickProtectionScheduler))
	mux.HandleFunc("GET /api/security/storage-protection", s.auth(s.getStorageProtection))
	mux.HandleFunc("POST /api/instances/{id}/ssh-key-only", s.auth(s.setSSHKeyOnly))
	mux.HandleFunc("POST /api/volumes/{id}/delete-protection", s.auth(s.setVolumeDeleteProtection))
}

func (s *Server) getFileVPSPosture(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	instances, _ := s.Store.ListInstances()
	instances = filterInstances(instances, s.visibleAccountIDSet(p), p.Kind == "admin")
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
	writeJSON(w, 200, map[string]any{
		"instances":      s.fileVPSProtectionPosture(instances, installs),
		"critical_paths": fileVPSCriticalPaths(),
		"features":       filterCatalogDomain("filevps"),
	})
}

func filterCatalogDomain(domain string) []protectionFeature {
	out := []protectionFeature{}
	for _, f := range advancedProtectionCatalog() {
		if f.Domain == domain {
			out = append(out, f)
		}
	}
	return out
}

func (s *Server) getNetworkExposure(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	instances, _ := s.Store.ListInstances()
	instances = filterInstances(instances, s.visibleAccountIDSet(p), p.Kind == "admin")
	writeJSON(w, 200, map[string]any{
		"exposures": s.networkExposure(instances),
		"features":  filterCatalogDomain("network"),
		"egress":    "planned",
	})
}

func (s *Server) getWorkerPosture(w http.ResponseWriter, r *http.Request) {
	workers, _ := s.Store.ListWorkers()
	writeJSON(w, 200, map[string]any{
		"workers":  s.workerProtectionPosture(workers),
		"features": filterCatalogDomain("worker"),
	})
}

func (s *Server) getAppCRS(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	instances, _ := s.Store.ListInstances()
	instances = filterInstances(instances, s.visibleAccountIDSet(p), p.Kind == "admin")
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	writeJSON(w, 200, map[string]any{
		"crs_map":  s.crsCategoryMap(instances),
		"features": filterCatalogDomain("app"),
		"note":     "SQLi/XSS/RCE/LFI are CRS-backed; not separate CloFix engines",
	})
}

func (s *Server) createProtectionUnlock(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstanceID string `json:"instance_id"`
		Control    string `json:"control"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil || in.InstanceID == "" {
		fail(w, 400, "instance_id and control required")
		return
	}
	if in.Control != "protection_mode" && in.Control != "ssh_login_blocked" {
		fail(w, 400, "control must be protection_mode or ssh_login_blocked")
		return
	}
	if in.TTLSeconds <= 0 || in.TTLSeconds > 86400 {
		in.TTLSeconds = 3600
	}
	if s.requiresApproval() {
		fail(w, 403, "AutoPilot approval mode: create an approval request instead of direct unlock")
		return
	}
	inst, err := s.Store.GetInstance(in.InstanceID)
	if err != nil {
		fail(w, 404, "instance not found")
		return
	}
	var restore bool
	switch in.Control {
	case "protection_mode":
		restore = inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode
		off := false
		inst.Spec.ProtectionMode = &off
	case "ssh_login_blocked":
		restore = inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked
		off := false
		inst.Spec.SSHLoginBlocked = &off
	}
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, 500, "could not save unlock")
		return
	}
	spec := inst.Spec
	action := proto.ActionUpdateProtection
	if in.Control == "ssh_login_blocked" {
		action = proto.ActionUpdateSSHLogin
	}
	_ = s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: inst.ID + "/" + action + "/" + randHex(4), Action: action, InstanceID: inst.ID, Spec: &spec,
	})
	u := protectionUnlock{
		ID: "unl-" + randHex(6), InstanceID: in.InstanceID, Control: in.Control,
		Expires: time.Now().UTC().Add(time.Duration(in.TTLSeconds) * time.Second),
		RestoreValue: restore, CreatedBy: principalFrom(r).Name, Created: time.Now().UTC(),
	}
	d := s.securityCenter
	d.Lock()
	if d.Unlocks == nil {
		d.Unlocks = map[string]protectionUnlock{}
	}
	d.Unlocks[u.ID] = u
	_ = d.save()
	d.Unlock()
	s.Store.Audit(principalFrom(r).Name, "security.unlock", in.InstanceID, in.Control)
	writeJSON(w, 200, u)
}

func (s *Server) listProtectionUnlocks(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]protectionUnlock, 0, len(d.Unlocks))
	for _, u := range d.Unlocks {
		list = append(list, u)
	}
	writeJSON(w, 200, map[string]any{"unlocks": list})
}

func (s *Server) requiresApproval() bool {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	return d.AutoPilot.Mode == "approval"
}

func (s *Server) listProtectionApprovals(w http.ResponseWriter, r *http.Request) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	list := make([]protectionApproval, 0, len(d.Approvals))
	for _, a := range d.Approvals {
		list = append(list, a)
	}
	writeJSON(w, 200, map[string]any{"approvals": list})
}

func (s *Server) createProtectionApproval(w http.ResponseWriter, r *http.Request) {
	var in protectionApproval
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || in.Type == "" {
		fail(w, 400, "type and payload required")
		return
	}
	in.ID = "apr-" + randHex(6)
	in.Status = "pending"
	in.Requested = principalFrom(r).Name
	in.Created = time.Now().UTC()
	in.Updated = in.Created
	if in.Payload == nil {
		in.Payload = map[string]any{}
	}
	d := s.securityCenter
	d.Lock()
	if d.Approvals == nil {
		d.Approvals = map[string]protectionApproval{}
	}
	d.Approvals[in.ID] = in
	_ = d.save()
	d.Unlock()
	writeJSON(w, 200, in)
}

func (s *Server) decideProtectionApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Decision string `json:"decision"` // approve|reject
		Apply    bool   `json:"apply"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		fail(w, 400, "decision required")
		return
	}
	d := s.securityCenter
	d.Lock()
	apr, ok := d.Approvals[id]
	if !ok {
		d.Unlock()
		fail(w, 404, "approval not found")
		return
	}
	if apr.Status != "pending" {
		d.Unlock()
		fail(w, 409, "approval already decided")
		return
	}
	if in.Decision == "reject" {
		apr.Status = "rejected"
	} else if in.Decision == "approve" {
		apr.Status = "approved"
	} else {
		d.Unlock()
		fail(w, 400, "decision must be approve or reject")
		return
	}
	apr.DecidedBy = principalFrom(r).Name
	apr.Updated = time.Now().UTC()
	d.Approvals[id] = apr
	_ = d.save()
	d.Unlock()

	if apr.Status == "approved" && in.Apply {
		if err := s.executeApprovedProtection(apr); err != nil {
			fail(w, 500, err.Error())
			return
		}
		d.Lock()
		apr.Status = "applied"
		apr.Updated = time.Now().UTC()
		d.Approvals[id] = apr
		_ = d.save()
		d.Unlock()
	}
	writeJSON(w, 200, apr)
}

func (s *Server) executeApprovedProtection(apr protectionApproval) error {
	switch apr.Type {
	case "quarantine":
		id, _ := apr.Payload["instance_id"].(string)
		if id == "" {
			return errStr("instance_id required")
		}
		return s.quarantineInstance(id, principalFromName(apr.DecidedBy))
	case "mode_apply":
		policyID, _ := apr.Payload["policy_id"].(string)
		rawIDs, _ := apr.Payload["instance_ids"].([]any)
		ids := make([]string, 0, len(rawIDs))
		for _, v := range rawIDs {
			if s, ok := v.(string); ok {
				ids = append(ids, s)
			}
		}
		if policyID == "" || len(ids) == 0 {
			return errStr("policy_id and instance_ids required")
		}
		return s.applyModeByID(policyID, ids, apr.DecidedBy)
	case "protection_off":
		id, _ := apr.Payload["instance_id"].(string)
		inst, err := s.Store.GetInstance(id)
		if err != nil {
			return err
		}
		off := false
		inst.Spec.ProtectionMode = &off
		if err := s.Store.SaveInstance(inst); err != nil {
			return err
		}
		spec := inst.Spec
		return s.Store.QueueOp(inst.WorkerID, proto.Operation{
			ID: id + "/" + proto.ActionUpdateProtection + "/" + randHex(4),
			Action: proto.ActionUpdateProtection, InstanceID: id, Spec: &spec,
		})
	case "destructive_db":
		// Record-only gate; actual DB APIs check pending approvals by job id later.
		return nil
	case "firewall_deny":
		return s.applySuggestedDeny(apr.Payload)
	default:
		return errStr("unsupported approval type")
	}
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errStr(s string) error    { return strErr(s) }
func principalFromName(name string) string {
	if name == "" {
		return "admin"
	}
	return name
}

func (s *Server) getProtectionRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("targetID")
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	rb, ok := d.Rollbacks[id]
	if !ok {
		fail(w, 404, "no rollback snapshot")
		return
	}
	writeJSON(w, 200, rb)
}

func (s *Server) applyProtectionRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("targetID")
	d := s.securityCenter
	d.Lock()
	rb, ok := d.Rollbacks[id]
	d.Unlock()
	if !ok {
		fail(w, 404, "no rollback snapshot")
		return
	}
	if err := s.applyModeControlsToInstance(id, rb.Controls, principalFrom(r).Name); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(principalFrom(r).Name, "security.rollback", id, rb.PolicyID)
	writeJSON(w, 200, map[string]any{"rolled_back": true, "snapshot": rb})
}

func (s *Server) saveRollbackSnapshot(targetID, policyID, actor string, version int, controls map[string]any) {
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	if d.Rollbacks == nil {
		d.Rollbacks = map[string]protectionRollback{}
	}
	// Keep previous applied controls as rollback target.
	prev := map[string]any{}
	if inst, err := s.Store.GetInstance(targetID); err == nil {
		prev["protection_mode"] = inst.Spec.ProtectionMode == nil || *inst.Spec.ProtectionMode
		prev["ssh_login_blocked"] = inst.Spec.SSHLoginBlocked != nil && *inst.Spec.SSHLoginBlocked
		prev["delete_protection"] = inst.Spec.DeleteProtection
	}
	d.Rollbacks[targetID] = protectionRollback{
		TargetID: targetID, PolicyID: policyID, Version: version,
		Controls: prev, AppliedAt: time.Now().UTC(), AppliedBy: actor,
	}
	_ = d.save()
}

func (s *Server) applyModeControlsToInstance(id string, controls map[string]any, actor string) error {
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		return err
	}
	previous := inst
	for key, raw := range controls {
		if !isSupportedModeControl(key) {
			continue
		}
		want, ok := raw.(bool)
		if !ok {
			continue
		}
		switch key {
		case "protection_mode":
			inst.Spec.ProtectionMode = &want
		case "ssh_login_blocked":
			inst.Spec.SSHLoginBlocked = &want
		case "delete_protection":
			inst.Spec.DeleteProtection = want
		}
	}
	if err := s.Store.SaveInstance(inst); err != nil {
		return err
	}
	spec := inst.Spec
	for key := range controls {
		if !isSupportedModeControl(key) {
			continue
		}
		action := ""
		switch key {
		case "protection_mode":
			action = proto.ActionUpdateProtection
		case "ssh_login_blocked":
			action = proto.ActionUpdateSSHLogin
		case "delete_protection":
			action = proto.ActionUpdateDeleteProtection
		}
		if action != "" {
			if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
				ID: id + "/" + action + "/" + randHex(4), Action: action, InstanceID: id, Spec: &spec,
			}); err != nil {
				_ = s.Store.SaveInstance(previous)
				return err
			}
		}
	}
	_ = actor
	return nil
}

func (s *Server) applyModeByID(policyID string, ids []string, actor string) error {
	d := s.securityCenter
	d.Lock()
	pol, ok := d.Policies[policyID]
	d.Unlock()
	if !ok {
		return errStr("policy not found")
	}
	for _, id := range ids {
		s.saveRollbackSnapshot(id, policyID, actor, pol.Version, pol.Controls)
		if err := s.applyModeControlsToInstance(id, pol.Controls, actor); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) quarantineInstance(id, actor string) error {
	on, block, del := true, true, true
	controls := map[string]any{"protection_mode": on, "ssh_login_blocked": block, "delete_protection": del}
	s.saveRollbackSnapshot(id, "tpl-quarantine", actor, 1, controls)
	if err := s.applyModeControlsToInstance(id, controls, actor); err != nil {
		return err
	}
	// Restrict firewall (inbound quarantine).
	if s.firewall != nil {
		_ = s.firewall.put("instance", id, firewallPolicy{Restrict: true, Rules: nil})
		inst, err := s.Store.GetInstance(id)
		if err == nil {
			_ = s.Store.QueueOp(inst.WorkerID, proto.Operation{
				ID: id + "/" + proto.ActionUpdateFirewall + "/" + randHex(4),
				Action: proto.ActionUpdateFirewall, InstanceID: id,
				Firewall: &proto.FirewallSpec{Scope: "vps", InstanceID: id, GuestIP: inst.Spec.IPAddress, Restrict: true},
			})
		}
	}
	return nil
}

func (s *Server) applyNetworkQuarantine(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.requiresApproval() {
		fail(w, 403, "AutoPilot approval mode: submit type=quarantine approval")
		return
	}
	if err := s.quarantineInstance(id, principalFrom(r).Name); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Store.Audit(principalFrom(r).Name, "security.quarantine", id, "ok")
	writeJSON(w, 200, map[string]any{"quarantined": true, "instance_id": id})
}

func (s *Server) suggestFirewallDeny(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstanceID string `json:"instance_id"`
		Source     string `json:"source"`
		Port       int    `json:"port"`
		Protocol   string `json:"protocol"`
		Submit     bool   `json:"submit_approval"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil || in.InstanceID == "" || in.Source == "" {
		fail(w, 400, "instance_id and source CIDR required")
		return
	}
	if in.Protocol == "" {
		in.Protocol = "tcp"
	}
	suggestion := map[string]any{
		"action": "deny", "protocol": in.Protocol, "port": in.Port, "source": in.Source,
		"instance_id": in.InstanceID, "status": "suggest_only",
		"note": "Auto-block is suggest-only; approve to apply",
	}
	if in.Submit || s.requiresApproval() {
		apr := protectionApproval{
			ID: "apr-" + randHex(6), Type: "firewall_deny", Status: "pending",
			Payload: suggestion, Requested: principalFrom(r).Name,
			Created: time.Now().UTC(), Updated: time.Now().UTC(),
		}
		d := s.securityCenter
		d.Lock()
		if d.Approvals == nil {
			d.Approvals = map[string]protectionApproval{}
		}
		d.Approvals[apr.ID] = apr
		_ = d.save()
		d.Unlock()
		suggestion["approval_id"] = apr.ID
	}
	writeJSON(w, 200, suggestion)
}

func (s *Server) applySuggestedDeny(payload map[string]any) error {
	id, _ := payload["instance_id"].(string)
	source, _ := payload["source"].(string)
	protoName, _ := payload["protocol"].(string)
	port, _ := payload["port"].(float64)
	if id == "" || source == "" {
		return errStr("invalid deny payload")
	}
	pol := s.firewall.policy("instance", id)
	pol.Rules = append(pol.Rules, proto.FirewallRule{
		Action: "deny", Protocol: protoName, Port: int(port), Source: source,
	})
	if err := s.firewall.put("instance", id, pol); err != nil {
		return err
	}
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		return err
	}
	return s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: id + "/" + proto.ActionUpdateFirewall + "/" + randHex(4),
		Action: proto.ActionUpdateFirewall, InstanceID: id,
		Firewall: &proto.FirewallSpec{Scope: "vps", InstanceID: id, GuestIP: inst.Spec.IPAddress, Restrict: pol.Restrict, Rules: pol.Rules},
	})
}

func (s *Server) resolveInheritedPolicy(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		fail(w, 400, "instance_id required")
		return
	}
	inst, err := s.Store.GetInstance(instanceID)
	if err != nil {
		fail(w, 404, "instance not found")
		return
	}
	d := s.securityCenter
	d.Lock()
	defer d.Unlock()
	order := []struct{ scope, id string }{
		{"vps", inst.ID},
		{"worker", inst.WorkerID},
		{"project", inst.AccountID},
		{"account", inst.AccountID},
	}
	var matched *securityBinding
	var policy *securityPolicy
	for _, o := range order {
		for _, b := range d.Bindings {
			if b.Scope == o.scope && b.TargetID == o.id {
				if pol, ok := d.Policies[b.PolicyID]; ok {
					bb, pp := b, pol
					matched, policy = &bb, &pp
					break
				}
			}
		}
		if matched != nil {
			break
		}
	}
	writeJSON(w, 200, map[string]any{
		"instance_id": instanceID,
		"resolution":  "vps>worker>project>account",
		"binding":     matched,
		"policy":      policy,
	})
}

func (s *Server) tickProtectionScheduler(w http.ResponseWriter, r *http.Request) {
	// Honor bindings with schedule == "always" or empty minute match "*"; simple ops tick.
	d := s.securityCenter
	d.Lock()
	bindings := make([]securityBinding, 0, len(d.Bindings))
	for _, b := range d.Bindings {
		bindings = append(bindings, b)
	}
	d.Unlock()
	applied := []map[string]any{}
	now := time.Now().UTC()
	for _, b := range bindings {
		if b.Schedule == "" {
			continue
		}
		// Supported: "always" or cron-like "hourly" shorthand for lab.
		if b.Schedule != "always" && b.Schedule != "hourly" {
			applied = append(applied, map[string]any{"binding_id": b.ID, "status": "skipped", "note": "unsupported schedule expression"})
			continue
		}
		if b.Schedule == "hourly" && now.Minute() > 5 {
			applied = append(applied, map[string]any{"binding_id": b.ID, "status": "skipped", "note": "outside hourly window"})
			continue
		}
		targets := []string{}
		if b.Scope == "vps" {
			targets = []string{b.TargetID}
		} else if b.Scope == "worker" {
			instances, _ := s.Store.ListInstances()
			for _, inst := range instances {
				if inst.WorkerID == b.TargetID {
					targets = append(targets, inst.ID)
				}
			}
		} else if b.Scope == "account" || b.Scope == "project" {
			instances, _ := s.Store.ListInstances()
			for _, inst := range instances {
				if inst.AccountID == b.TargetID {
					targets = append(targets, inst.ID)
				}
			}
		}
		if err := s.applyModeByID(b.PolicyID, targets, "scheduler"); err != nil {
			applied = append(applied, map[string]any{"binding_id": b.ID, "status": "error", "error": err.Error()})
			continue
		}
		applied = append(applied, map[string]any{"binding_id": b.ID, "status": "applied", "targets": targets})
	}
	// Expire temporary unlocks.
	restored := s.expireProtectionUnlocks()
	writeJSON(w, 200, map[string]any{"applied": applied, "unlocks_restored": restored, "at": now})
}

func (s *Server) expireProtectionUnlocks() []string {
	d := s.securityCenter
	d.Lock()
	now := time.Now().UTC()
	expired := []protectionUnlock{}
	for id, u := range d.Unlocks {
		if !u.Expires.After(now) {
			expired = append(expired, u)
			delete(d.Unlocks, id)
		}
	}
	_ = d.save()
	d.Unlock()
	out := []string{}
	for _, u := range expired {
		inst, err := s.Store.GetInstance(u.InstanceID)
		if err != nil {
			continue
		}
		switch u.Control {
		case "protection_mode":
			v := u.RestoreValue
			inst.Spec.ProtectionMode = &v
		case "ssh_login_blocked":
			v := u.RestoreValue
			inst.Spec.SSHLoginBlocked = &v
		}
		_ = s.Store.SaveInstance(inst)
		spec := inst.Spec
		action := proto.ActionUpdateProtection
		if u.Control == "ssh_login_blocked" {
			action = proto.ActionUpdateSSHLogin
		}
		_ = s.Store.QueueOp(inst.WorkerID, proto.Operation{
			ID: u.InstanceID + "/" + action + "/" + randHex(4), Action: action, InstanceID: u.InstanceID, Spec: &spec,
		})
		out = append(out, u.ID)
	}
	return out
}

func (s *Server) setSSHKeyOnly(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, 400, "enabled must be true or false")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, 404, "instance not found")
		return
	}
	// enabled=true means key-only → PasswordAuth false
	wantPassword := !*in.Enabled
	if inst.Spec.PasswordAuth == wantPassword {
		writeJSON(w, 200, map[string]any{"id": id, "ssh_key_only": *in.Enabled, "unchanged": true})
		return
	}
	if *in.Enabled && inst.Spec.SSHAuthorizedKey == "" {
		fail(w, 400, "ssh_authorized_key required before enabling key-only auth")
		return
	}
	previous := inst
	inst.Spec.PasswordAuth = wantPassword
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, 500, "could not save")
		return
	}
	spec := inst.Spec
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: id + "/" + proto.ActionUpdatePasswordAuth + "/" + randHex(4),
		Action: proto.ActionUpdatePasswordAuth, InstanceID: id, Spec: &spec,
	}); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, 500, "could not queue key-only update")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "instance.ssh_key_only", id, strconv.FormatBool(*in.Enabled))
	writeJSON(w, 202, map[string]any{"id": id, "ssh_key_only": *in.Enabled, "password_auth": wantPassword, "queued": true})
}

func (s *Server) setVolumeDeleteProtection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, 400, "enabled must be true or false")
		return
	}
	vol, err := s.Store.GetVolume(id)
	if err != nil {
		fail(w, 404, "volume not found")
		return
	}
	vol.DeleteProtection = *in.Enabled
	if err := s.Store.SaveVolume(vol); err != nil {
		fail(w, 500, "could not save volume")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "volume.delete_protection", id, strconv.FormatBool(*in.Enabled))
	writeJSON(w, 200, map[string]any{"id": id, "delete_protection": vol.DeleteProtection})
}

func (s *Server) getStorageProtection(w http.ResponseWriter, r *http.Request) {
	volumes, _ := s.Store.ListVolumes()
	volOut := []map[string]any{}
	for _, v := range volumes {
		volOut = append(volOut, map[string]any{
			"id": v.ID, "name": v.Name, "delete_protection": v.DeleteProtection, "state": v.State,
		})
	}
	dbScore, dbFindings := s.databaseSecurityPanel()
	writeJSON(w, 200, map[string]any{
		"volumes": volOut,
		"databases": map[string]any{"score": dbScore, "findings": dbFindings},
		"s3": map[string]any{
			"versioning": "planned", "object_lock": "planned",
			"note": "Objectstore does not expose versioning/object-lock yet",
		},
		"features": append(filterCatalogDomain("database"), filterCatalogDomain("storage")...),
	})
}
