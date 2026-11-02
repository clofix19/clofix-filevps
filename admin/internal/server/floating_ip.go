package server

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type floatingIPAssignment struct {
	IP         string    `json:"ip"`
	InstanceID string    `json:"instance_id,omitempty"`
	NLBID      string    `json:"nlb_id,omitempty"`
	Note       string    `json:"note,omitempty"`
	Firewall   string    `json:"firewall"` // recorded|pending
	Attached   time.Time `json:"attached"`
	Owner      string    `json:"owner"`
}

type floatingIPStore struct {
	sync.Mutex
	path  string
	Items map[string]floatingIPAssignment `json:"items"`
}

func (s *Server) floatingPath() string {
	dir := os.Getenv("CLOFIX_DATA_DIR")
	if dir == "" {
		dir = "/var/lib/clofix-controller"
	}
	return filepath.Join(dir, "floating-ips.json")
}

func (s *Server) loadFloatingIPs() *floatingIPStore {
	d := &floatingIPStore{path: s.floatingPath(), Items: map[string]floatingIPAssignment{}}
	b, err := os.ReadFile(d.path)
	if err == nil {
		_ = json.Unmarshal(b, d)
	}
	if d.Items == nil {
		d.Items = map[string]floatingIPAssignment{}
	}
	return d
}

func (d *floatingIPStore) save() error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

func (s *Server) listFloatingIPs(w http.ResponseWriter, r *http.Request) {
	d := s.loadFloatingIPs()
	d.Lock()
	defer d.Unlock()
	items := make([]floatingIPAssignment, 0, len(d.Items))
	p := principalFrom(r)
	for _, item := range d.Items {
		if p.Kind != "admin" && item.Owner != p.AccountID && item.Owner != p.Email {
			continue
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{
		"items": items,
		"note":  "Floating IPs must already be routed to the controller/worker by the ISP. CloFix records the attachment and queues matching host firewall allows; it does not provision provider addresses.",
	})
}

func (s *Server) attachFloatingIP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IP         string `json:"ip"`
		InstanceID string `json:"instance_id"`
		NLBID      string `json:"nlb_id"`
		Note       string `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid floating IP payload")
		return
	}
	ip := net.ParseIP(strings.TrimSpace(in.IP))
	if ip == nil || ip.To4() == nil {
		fail(w, 400, "valid IPv4 address required")
		return
	}
	in.IP = ip.To4().String()
	if in.InstanceID == "" && in.NLBID == "" {
		fail(w, 400, "attach to instance_id or nlb_id")
		return
	}
	owner := principalFrom(r).AccountID
	if owner == "" {
		owner = principalFrom(r).Email
	}
	if in.InstanceID != "" {
		inst, err := s.Store.GetInstance(in.InstanceID)
		if err != nil {
			fail(w, 404, "VPS not found")
			return
		}
		if !s.canAccessAccount(principalFrom(r), inst.AccountID) {
			fail(w, 404, "VPS not found")
			return
		}
		owner = inst.AccountID
	}
	if in.NLBID != "" {
		s.balancers.Lock()
		b := s.balancers.Items[in.NLBID]
		s.balancers.Unlock()
		if b == nil || b.Mode != "nlb" {
			fail(w, 404, "NLB not found")
			return
		}
		s.balancers.Lock()
		pool, _ := parseNLBPool(s.balancers.Network.IPRanges)
		s.balancers.Unlock()
		poolOK := false
		for _, allocated := range pool {
			if allocated == in.IP {
				poolOK = true
				break
			}
		}
		if !poolOK {
			fail(w, 400, "IP must be listed in Settings → NLB network IP pool first")
			return
		}
	}
	d := s.loadFloatingIPs()
	d.Lock()
	defer d.Unlock()
	rec := floatingIPAssignment{
		IP: in.IP, InstanceID: in.InstanceID, NLBID: in.NLBID, Note: strings.TrimSpace(in.Note),
		Firewall: "recorded", Attached: time.Now().UTC(), Owner: owner,
	}
	d.Items[in.IP] = rec
	if err := d.save(); err != nil {
		fail(w, 500, "could not save floating IP assignment")
		return
	}
	s.Store.Audit(principalFrom(r).Email, "floating-ip.attach", in.IP, in.InstanceID+in.NLBID)
	writeJSON(w, 200, map[string]any{
		"assignment": rec,
		"firewall":   "Recorded for operator allowlists. Ensure host nftables/security groups permit the published services on this address.",
	})
}
