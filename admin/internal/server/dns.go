package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	databaseState "github.com/clofix/filevps/admin/internal/database"
	kubernetesState "github.com/clofix/filevps/admin/internal/kubernetes"
	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/proto"
)

type dnsZone struct {
	Domain         string `json:"domain"`
	TTL            int    `json:"ttl"`
	PublishAddress string `json:"publish_address,omitempty"`
	AutoVPS        bool   `json:"auto_vps"`
	AutoLB         bool   `json:"auto_lb"`
	AutoCKS        bool   `json:"auto_cks"`
	AutoDB         bool   `json:"auto_db"`
	AutoApp        bool   `json:"auto_app"`
	AutoWorker     bool   `json:"auto_worker"`
}
type dnsRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Value       string    `json:"value"`
	Port        int       `json:"port,omitempty"`
	TTL         int       `json:"ttl"`
	Source      string    `json:"source"`
	Resource    string    `json:"resource"`
	ResourceID  string    `json:"resource_id,omitempty"`
	Description string    `json:"description,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}
type dnsStore struct {
	sync.Mutex
	Zone    dnsZone              `json:"zone"`
	Records map[string]dnsRecord `json:"records"`
	file    string
}

func loadDNS(dir string) *dnsStore {
	d := &dnsStore{file: filepath.Join(dir, "dns.json")}
	first := true
	raw, err := os.ReadFile(d.file)
	if err == nil {
		if json.Unmarshal(raw, d) != nil {
			d.Records = nil
		} else {
			first = d.Zone.Domain == ""
		}
	}
	if first {
		d.Zone.AutoVPS, d.Zone.AutoLB, d.Zone.AutoCKS, d.Zone.AutoDB, d.Zone.AutoApp, d.Zone.AutoWorker = true, true, true, true, true, true
	} else if err == nil && !strings.Contains(string(raw), `"auto_cks"`) {
		d.Zone.AutoCKS = true
	}
	d.ensure()
	return d
}
func (d *dnsStore) ensure() {
	if d.Records == nil {
		d.Records = map[string]dnsRecord{}
	}
	if !validDNSZone(d.Zone.Domain) {
		d.Zone.Domain = "clofix.internal"
	}
	if d.Zone.TTL < 30 || d.Zone.TTL > 86400 {
		d.Zone.TTL = 60
	}
	if d.Zone.PublishAddress != "" && net.ParseIP(d.Zone.PublishAddress).To4() == nil {
		d.Zone.PublishAddress = ""
	}
}
func (d *dnsStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) dnsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/dns", s.auth(s.getDNS))
	m.HandleFunc("PUT /api/dns/zone", s.auth(s.saveDNSZone))
	m.HandleFunc("POST /api/dns/records", s.auth(s.createDNSRecord))
	m.HandleFunc("PUT /api/dns/records/{id}", s.auth(s.updateDNSRecord))
	m.HandleFunc("DELETE /api/dns/records/{id}", s.auth(s.deleteDNSRecord))
	m.HandleFunc("GET /api/dns/zonefile", s.auth(s.downloadDNSZoneFile))
}

func (s *Server) getDNS(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshDNS(); err != nil {
		fail(w, 500, "could not refresh DNS records")
		return
	}
	targets := s.dnsTargets()
	d := s.dns
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, dnsView(d, targets))
}
func (s *Server) saveDNSZone(w http.ResponseWriter, r *http.Request) {
	var in dnsZone
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(in.Domain, ".")))
	in.PublishAddress = strings.TrimSpace(in.PublishAddress)
	if !validDNSZone(in.Domain) {
		fail(w, 400, "enter a DNS zone such as clofix.internal or example.com")
		return
	}
	if in.TTL < 30 || in.TTL > 86400 {
		fail(w, 400, "TTL must be between 30 and 86400 seconds")
		return
	}
	if in.PublishAddress != "" && net.ParseIP(in.PublishAddress).To4() == nil {
		fail(w, 400, "publish address must be an IPv4 address")
		return
	}
	d := s.dns
	d.Lock()
	d.Zone = in
	d.ensure()
	err := d.save()
	d.Unlock()
	if err != nil {
		fail(w, 500, "could not save DNS zone")
		return
	}
	if err = s.refreshDNS(); err != nil {
		fail(w, 500, "could not refresh DNS records")
		return
	}
	targets := s.dnsTargets()
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, dnsView(d, targets))
}

type dnsRecordInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Port        int    `json:"port"`
	TTL         int    `json:"ttl"`
	Description string `json:"description"`
	Resource    string `json:"resource"`
	ResourceID  string `json:"resource_id"`
}

func decodeDNSRecordInput(w http.ResponseWriter, r *http.Request) (dnsRecordInput, bool) {
	var in dnsRecordInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return in, false
	}
	return in, true
}
func (s *Server) applyDNSRecordTarget(in *dnsRecordInput) (int, error) {
	in.Resource = strings.ToLower(strings.TrimSpace(in.Resource))
	in.ResourceID = strings.TrimSpace(in.ResourceID)
	if in.Resource == "" {
		in.Resource = "manual"
	}
	if in.Resource == "manual" {
		return 0, nil
	}
	target, ok := dnsFindTarget(s.dnsTargets(), in.Resource, in.ResourceID)
	if !ok {
		return 404, fmt.Errorf("selected VPS, load balancer, CKS cluster, database, app or worker was not found")
	}
	if in.Value == "" {
		in.Type, in.Value = target.Type, target.Value
	}
	if in.Port == 0 {
		in.Port = target.Port
	}
	if in.Description == "" {
		in.Description = target.Label
	}
	return 0, nil
}

func (s *Server) createDNSRecord(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeDNSRecordInput(w, r)
	if !ok {
		return
	}
	if err := s.refreshDNS(); err != nil {
		fail(w, 500, "could not refresh DNS records")
		return
	}
	if code, err := s.applyDNSRecordTarget(&in); err != nil {
		fail(w, code, err.Error())
		return
	}
	d := s.dns
	d.Lock()
	defer d.Unlock()
	name := dnsAbsoluteName(in.Name, d.Zone.Domain)
	rec, err := newManualDNSRecord(name, in.Type, in.Value, in.Port, in.TTL, in.Description, in.Resource, in.ResourceID, d.Zone.TTL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	for _, existing := range d.Records {
		if existing.Name == rec.Name && existing.Type == rec.Type {
			fail(w, 409, "a record with this name and type already exists")
			return
		}
	}
	d.Records[rec.ID] = rec
	if err = d.save(); err != nil {
		delete(d.Records, rec.ID)
		fail(w, 500, "could not save DNS record")
		return
	}
	writeJSON(w, 200, map[string]any{"record": rec})
}
func (s *Server) updateDNSRecord(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	in, ok := decodeDNSRecordInput(w, r)
	if !ok {
		return
	}
	if err := s.refreshDNS(); err != nil {
		fail(w, 500, "could not refresh DNS records")
		return
	}
	if code, err := s.applyDNSRecordTarget(&in); err != nil {
		fail(w, code, err.Error())
		return
	}
	d := s.dns
	d.Lock()
	defer d.Unlock()
	existing, found := d.Records[id]
	if !found {
		fail(w, 404, "DNS record not found")
		return
	}
	if existing.Source == "auto" {
		fail(w, 409, "automatic records follow live inventory and cannot be edited")
		return
	}
	name := dnsAbsoluteName(in.Name, d.Zone.Domain)
	rec, err := newManualDNSRecord(name, in.Type, in.Value, in.Port, in.TTL, in.Description, in.Resource, in.ResourceID, d.Zone.TTL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	for _, other := range d.Records {
		if other.ID != id && other.Name == rec.Name && other.Type == rec.Type {
			fail(w, 409, "a record with this name and type already exists")
			return
		}
	}
	rec.ID = existing.ID
	rec.Created = existing.Created
	if rec.Created.IsZero() {
		rec.Created = rec.Updated
	}
	d.Records[id] = rec
	if err = d.save(); err != nil {
		d.Records[id] = existing
		fail(w, 500, "could not save DNS record")
		return
	}
	writeJSON(w, 200, map[string]any{"record": rec})
}
func (s *Server) deleteDNSRecord(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	d := s.dns
	d.Lock()
	defer d.Unlock()
	rec, ok := d.Records[id]
	if !ok {
		fail(w, 404, "DNS record not found")
		return
	}
	if rec.Source == "auto" {
		fail(w, 409, "automatic records follow live inventory and cannot be deleted")
		return
	}
	delete(d.Records, id)
	if err := d.save(); err != nil {
		d.Records[id] = rec
		fail(w, 500, "could not delete DNS record")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": id})
}
func (s *Server) downloadDNSZoneFile(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshDNS(); err != nil {
		fail(w, 500, "could not refresh DNS records")
		return
	}
	d := s.dns
	d.Lock()
	defer d.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.Zone.Domain+`.zone"`)
	w.Write([]byte(dnsZoneFile(d)))
}

func dnsView(d *dnsStore, targets []dnsTarget) map[string]any {
	records := make([]dnsRecord, 0, len(d.Records))
	auto, manual := 0, 0
	for _, rec := range d.Records {
		records = append(records, rec)
		if rec.Source == "auto" {
			auto++
		} else {
			manual++
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Name == records[j].Name {
			return records[i].Type < records[j].Type
		}
		return records[i].Name < records[j].Name
	})
	return map[string]any{
		"zone":    d.Zone,
		"records": records,
		"targets": targets,
		"overview": map[string]any{
			"domain":          d.Zone.Domain,
			"records":         len(records),
			"automatic":       auto,
			"manual":          manual,
			"publish_address": d.Zone.PublishAddress,
		},
		"note": "Automatic A and CNAME records are created from live VPS, load balancer, CKS, database, managed app and worker inventory. Attached CloFix load balancers also get CKS aliases. This controller stores the zone; it does not listen on port 53 or publish names on the public Internet.",
	}
}

func (s *Server) refreshDNS() error {
	instances, _ := s.Store.ListInstances()
	workers, _ := s.Store.ListWorkers()
	var balancers []loadBalancer
	if s.balancers != nil {
		s.balancers.Lock()
		for _, b := range s.balancers.Items {
			if b != nil && !b.Deleting {
				balancers = append(balancers, *b)
			}
		}
		s.balancers.Unlock()
	}
	var projects []databaseState.Project
	if s.databases != nil && s.databaseError == nil {
		s.databases.Lock()
		for _, p := range s.databases.Projects {
			projects = append(projects, p)
		}
		s.databases.Unlock()
	}
	var apps []managedApp
	if s.applications != nil {
		s.applications.Lock()
		for _, a := range s.applications.Managed {
			apps = append(apps, a)
		}
		s.applications.Unlock()
	}
	d := s.dns
	d.Lock()
	defer d.Unlock()
	d.ensure()
	wanted := map[string]dnsRecord{}
	now := time.Now().UTC()
	add := func(id, name, typ, value, resource, resourceID, desc string, port int) {
		if name == "" || value == "" {
			return
		}
		wanted[id] = dnsRecord{ID: id, Name: name, Type: typ, Value: value, Port: port, TTL: d.Zone.TTL, Source: "auto", Resource: resource, ResourceID: resourceID, Description: desc, Created: now, Updated: now}
	}
	zone := d.Zone.Domain
	if d.Zone.AutoVPS {
		for _, inst := range instances {
			if inst.State == "DELETING" {
				continue
			}
			host := dnsJoin(dnsLabel(inst.ID), "vps", zone)
			if ip := dnsIPv4(inst.Spec.IPAddress); ip != "" {
				add("dns-auto-vps-"+inst.ID, host, "A", ip, "vps", inst.ID, "FileVPS guest address", inst.Spec.SSHPort)
			}
			if alias := dnsLabel(inst.Spec.Hostname); alias != "" && alias != dnsLabel(inst.ID) {
				add("dns-auto-vps-host-"+inst.ID, dnsJoin(alias, "vps", zone), "CNAME", host+".", "vps", inst.ID, "FileVPS hostname alias", inst.Spec.SSHPort)
			}
		}
	}
	if d.Zone.AutoLB {
		for _, b := range balancers {
			kind := "lb"
			if b.Mode != "nlb" {
				kind = "alb"
			}
			host := dnsJoin(dnsLabel(b.ID), kind, zone)
			target := b.PublicIP
			desc := "Load balancer allocated address"
			if target == "" {
				target = d.Zone.PublishAddress
				desc = "Load balancer using zone publish address"
			}
			if target == "" && b.Mode != "nlb" {
				target = "127.0.0.1"
				desc = "Load balancer HTTP on this controller"
			}
			if target == "" {
				for _, upstream := range b.Healthy {
					if h := dnsHostFromURL(upstream); h != "" {
						target, desc = h, "Load balancer backend host"
						break
					}
				}
			}
			port := b.ListenPort
			if port == 0 && b.Mode != "nlb" {
				port = 80
			}
			if rec := dnsAddressRecord(target); rec.Type != "" {
				add("dns-auto-lb-"+b.ID, host, rec.Type, rec.Value, "lb", b.ID, desc, port)
			}
			if alias := dnsLabel(b.Slug); alias != "" && alias != dnsLabel(b.ID) {
				add("dns-auto-lb-slug-"+b.ID, dnsJoin(alias, kind, zone), "CNAME", host+".", "lb", b.ID, "Load balancer URL name", port)
			}
			if alias := dnsLabel(b.Name); alias != "" && alias != dnsLabel(b.ID) && alias != dnsLabel(b.Slug) {
				add("dns-auto-lb-name-"+b.ID, dnsJoin(alias, kind, zone), "CNAME", host+".", "lb", b.ID, "Load balancer name", port)
			}
		}
	}
	if d.Zone.AutoCKS {
		for _, c := range s.dnsKubernetesClusters() {
			host := dnsJoin(dnsLabel(c.ID), "cks", zone)
			typ, value, port, lbID, lbKind := cksDNSAddress(c, d.Zone, balancers)
			if typ == "" || value == "" {
				continue
			}
			add("dns-auto-cks-"+c.ID, host, typ, value, "cks", c.ID, "CKS cluster (EKS-style)", port)
			if alias := dnsLabel(c.Name); alias != "" && alias != dnsLabel(c.ID) {
				add("dns-auto-cks-name-"+c.ID, dnsJoin(alias, "cks", zone), "CNAME", host+".", "cks", c.ID, "CKS cluster name", port)
			}
			clusterName := dnsLabel(c.Name)
			if clusterName == "" {
				clusterName = dnsLabel(c.ID)
			}
			for _, svc := range c.Inventory.Services {
				if !cksDNSWorkloadVisible(svc) {
					continue
				}
				svcPort := cksWorkloadDefaultPort(svc)
				if svcPort == 0 {
					svcPort = port
				}
				add("dns-auto-cks-svc-"+c.ID+"-"+svc.Namespace+"-"+svc.Name, dnsJoin(dnsLabel(svc.Name), "svc", clusterName, "cks", zone), "CNAME", host+".", "cks", c.ID, "CKS Service "+svc.Name, svcPort)
			}
			for _, ing := range c.Inventory.Ingresses {
				if !cksDNSWorkloadVisible(ing) {
					continue
				}
				ingPort := cksWorkloadDefaultPort(ing)
				if ingPort == 0 {
					ingPort = port
				}
				add("dns-auto-cks-ing-"+c.ID+"-"+ing.Namespace+"-"+ing.Name, dnsJoin(dnsLabel(ing.Name), "ing", clusterName, "cks", zone), "CNAME", host+".", "cks", c.ID, "CKS Ingress "+ing.Name, ingPort)
			}
			if lbID != "" {
				lbHost := dnsJoin(dnsLabel(lbID), lbKind, zone)
				for _, integ := range c.Integrations {
					kind := strings.ToLower(integ.Kind)
					if kind != "lb" && kind != "load-balancer" && kind != "loadbalancer" {
						continue
					}
					if integ.ResourceID != lbID {
						continue
					}
					svc := dnsLabel(integ.K8sName)
					if svc == "" {
						svc = dnsLabel(integ.Name)
					}
					if svc == "" || svc == dnsLabel(c.ID) || svc == dnsLabel(c.Name) {
						continue
					}
					add("dns-auto-cks-lb-"+c.ID+"-"+lbID, dnsJoin(svc, dnsLabel(c.Name), "cks", zone), "CNAME", lbHost+".", "cks", c.ID, "CKS attached load balancer", port)
				}
			}
		}
	}
	if d.Zone.AutoDB {
		for _, p := range projects {
			host := dnsJoin(dnsLabel(p.Spec.ID), "db", zone)
			if rec := dnsAddressRecord(p.Spec.Host); rec.Type != "" {
				add("dns-auto-db-"+p.Spec.ID, host, rec.Type, rec.Value, "db", p.Spec.ID, "Managed database "+p.Spec.Engine, p.Spec.Port)
			}
			if alias := dnsLabel(p.Name); alias != "" && alias != dnsLabel(p.Spec.ID) {
				add("dns-auto-db-name-"+p.Spec.ID, dnsJoin(alias, "db", zone), "CNAME", host+".", "db", p.Spec.ID, "Managed database name", p.Spec.Port)
			}
		}
	}
	if d.Zone.AutoApp {
		for _, a := range apps {
			host := dnsJoin(dnsLabel(a.ID), "app", zone)
			target := dnsHostFromURL(a.URL)
			if target == "" {
				target = a.Spec.Address
			}
			port := a.Spec.Port
			if port == 0 {
				port = dnsPortFromURL(a.URL)
			}
			if rec := dnsAddressRecord(target); rec.Type != "" {
				add("dns-auto-app-"+a.ID, host, rec.Type, rec.Value, "app", a.ID, "Managed application", port)
			} else if ip := dnsIPv4(a.Spec.Address); ip != "" {
				add("dns-auto-app-"+a.ID, host, "A", ip, "app", a.ID, "Managed application guest address", port)
			}
			if alias := dnsLabel(a.Name); alias != "" && alias != dnsLabel(a.ID) {
				add("dns-auto-app-name-"+a.ID, dnsJoin(alias, "app", zone), "CNAME", host+".", "app", a.ID, "Managed application name", port)
			}
		}
		if s.docker != nil {
			s.docker.mu.Lock()
			for _, app := range s.docker.Applications {
				host := dnsJoin(dnsLabel(app.ID), "docker", zone)
				port := 0
				if len(app.Spec.Ports) > 0 {
					port = app.Spec.Ports[0].HostPort
					if port == 0 {
						port = app.Spec.Ports[0].ContainerPort
					}
				}
				wkHost := ""
				for _, wk := range workers {
					if wk.ID == app.WorkerID {
						wkHost = wk.Hostname
						break
					}
				}
				if rec := dnsAddressRecord(wkHost); rec.Type != "" {
					add("dns-auto-docker-"+app.ID, host, rec.Type, rec.Value, "docker", app.ID, "Docker application", port)
				}
				if alias := dnsLabel(app.Name); alias != "" && alias != dnsLabel(app.ID) {
					add("dns-auto-docker-name-"+app.ID, dnsJoin(alias, "docker", zone), "CNAME", host+".", "docker", app.ID, "Docker application name", port)
				}
				if domain := strings.ToLower(strings.TrimSpace(app.Domain)); domain != "" && validDNSName(domain) {
					if rec := dnsAddressRecord(wkHost); rec.Type != "" {
						add("dns-auto-docker-domain-"+app.ID, domain, rec.Type, rec.Value, "docker", app.ID, "Docker application domain", port)
					}
				}
			}
			s.docker.mu.Unlock()
		}
	}
	if d.Zone.AutoWorker {
		for _, wk := range workers {
			if rec := dnsAddressRecord(wk.Hostname); rec.Type != "" {
				add("dns-auto-worker-"+wk.ID, dnsJoin(dnsLabel(wk.ID), "worker", zone), rec.Type, rec.Value, "worker", wk.ID, "Worker hostname", 0)
			}
		}
	}
	for id, rec := range d.Records {
		if rec.Source != "auto" {
			continue
		}
		next, ok := wanted[id]
		if !ok {
			delete(d.Records, id)
			continue
		}
		next.Created = rec.Created
		if rec.Created.IsZero() {
			next.Created = now
		}
		d.Records[id] = next
		delete(wanted, id)
	}
	for id, rec := range wanted {
		if existing, ok := d.Records[id]; ok && existing.Source != "auto" {
			continue
		}
		d.Records[id] = rec
	}
	return d.save()
}

func newManualDNSRecord(name, typ, value string, port, ttl int, desc, resource, resourceID string, defaultTTL int) (dnsRecord, error) {
	typ = strings.ToUpper(strings.TrimSpace(typ))
	value = strings.TrimSpace(value)
	desc = strings.TrimSpace(desc)
	resource = strings.ToLower(strings.TrimSpace(resource))
	if resource == "" {
		resource = "manual"
	}
	if !validDNSName(name) {
		return dnsRecord{}, fmt.Errorf("enter a valid DNS name")
	}
	if ttl == 0 {
		ttl = defaultTTL
	}
	if ttl < 30 || ttl > 86400 {
		return dnsRecord{}, fmt.Errorf("TTL must be between 30 and 86400 seconds")
	}
	if port < 0 || port > 65535 {
		return dnsRecord{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if len(desc) > 160 {
		return dnsRecord{}, fmt.Errorf("description is too long")
	}
	switch resource {
	case "manual", "vps", "lb", "cks", "db", "app", "docker", "worker":
	default:
		return dnsRecord{}, fmt.Errorf("select FileVPS, load balancer, CKS cluster, database, managed app, Docker app, worker or manual")
	}
	if resource != "manual" && resourceID == "" {
		return dnsRecord{}, fmt.Errorf("select a %s target", resource)
	}
	switch typ {
	case "A":
		if dnsIPv4(value) == "" {
			return dnsRecord{}, fmt.Errorf("A records require an IPv4 address")
		}
		value = dnsIPv4(value)
	case "AAAA":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return dnsRecord{}, fmt.Errorf("AAAA records require an IPv6 address")
		}
		value = ip.String()
	case "CNAME":
		value = strings.ToLower(strings.TrimSuffix(value, "."))
		if !validDNSName(value) {
			return dnsRecord{}, fmt.Errorf("CNAME records require a hostname")
		}
		value += "."
	case "TXT":
		if value == "" || len(value) > 255 {
			return dnsRecord{}, fmt.Errorf("TXT records must be 1 to 255 characters")
		}
	default:
		return dnsRecord{}, fmt.Errorf("supported record types are A, AAAA, CNAME and TXT")
	}
	now := time.Now().UTC()
	return dnsRecord{ID: "dns-" + randHex(8), Name: name, Type: typ, Value: value, Port: port, TTL: ttl, Source: "manual", Resource: resource, ResourceID: resourceID, Description: desc, Created: now, Updated: now}, nil
}

func dnsZoneFile(d *dnsStore) string {
	records := make([]dnsRecord, 0, len(d.Records))
	for _, rec := range d.Records {
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Name == records[j].Name {
			return records[i].Type < records[j].Type
		}
		return records[i].Name < records[j].Name
	})
	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN %s.\n$TTL %d\n", d.Zone.Domain, d.Zone.TTL)
	fmt.Fprintf(&b, "@\tIN\tSOA\tns.%s. hostmaster.%s. ( %d 3600 600 86400 %d )\n", d.Zone.Domain, d.Zone.Domain, time.Now().UTC().Unix(), d.Zone.TTL)
	fmt.Fprintf(&b, "@\tIN\tNS\tns.%s.\n", d.Zone.Domain)
	if ip := dnsIPv4(d.Zone.PublishAddress); ip != "" {
		fmt.Fprintf(&b, "ns\tIN\tA\t%s\n", ip)
	}
	for _, rec := range records {
		name := rec.Name
		if name == d.Zone.Domain {
			name = "@"
		} else if strings.HasSuffix(name, "."+d.Zone.Domain) {
			name = strings.TrimSuffix(name, "."+d.Zone.Domain)
		}
		fmt.Fprintf(&b, "%s\t%d\tIN\t%s\t%s\n", name, rec.TTL, rec.Type, rec.Value)
		if rec.Port > 0 && rec.Type != "TXT" {
			service := rec.Resource
			if service == "" || service == "manual" {
				service = "tcp"
			}
			target := rec.Name + "."
			if rec.Type == "CNAME" {
				target = rec.Value
				if !strings.HasSuffix(target, ".") {
					target += "."
				}
			}
			fmt.Fprintf(&b, "_%s._tcp.%s\t%d\tIN\tSRV\t0 0 %d %s\n", service, name, rec.TTL, rec.Port, target)
		}
	}
	fmt.Fprintf(&b, "; CloFix controller zone. Also served on UDP/TCP port 53.\n")
	return b.String()
}

func dnsAbsoluteName(name, zone string) string {
	name = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
	if name == "" || name == "@" {
		return zone
	}
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}
func dnsJoin(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, ".")
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ".")
}
func dnsLabel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == ' ':
			b.WriteByte('-')
		}
	}
	label := strings.Trim(b.String(), "-")
	for strings.Contains(label, "--") {
		label = strings.ReplaceAll(label, "--", "-")
	}
	if len(label) > 63 {
		label = label[:63]
	}
	if label == "" || !unicode.IsLetter(rune(label[0])) && !unicode.IsDigit(rune(label[0])) {
		return ""
	}
	return label
}
func dnsIPv4(v string) string {
	ip := net.ParseIP(strings.TrimSpace(v))
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.To4().String()
}
func dnsAddressRecord(v string) dnsRecord {
	v = strings.TrimSpace(v)
	if ip := dnsIPv4(v); ip != "" {
		return dnsRecord{Type: "A", Value: ip}
	}
	if ip := net.ParseIP(v); ip != nil && ip.To4() == nil {
		return dnsRecord{Type: "AAAA", Value: ip.String()}
	}
	host := strings.ToLower(strings.TrimSuffix(v, "."))
	if validDNSName(host) {
		return dnsRecord{Type: "CNAME", Value: host + "."}
	}
	return dnsRecord{}
}
func dnsHostFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return u.Hostname()
}
func dnsPortFromURL(raw string) int {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Port() == "" {
		return 0
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

type dnsTargetPort struct {
	Port      int    `json:"port"`
	Label     string `json:"label"`
	Protocol  string `json:"protocol,omitempty"`
	GuestPort int    `json:"guest_port,omitempty"`
}
type dnsTarget struct {
	Resource string          `json:"resource"`
	ID       string          `json:"id"`
	Label    string          `json:"label"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Value    string          `json:"value"`
	Port     int             `json:"port"`
	Ports    []dnsTargetPort `json:"ports,omitempty"`
}

func dnsPortOption(port, guest int, protocol, note string) dnsTargetPort {
	if port < 1 || port > 65535 {
		return dnsTargetPort{}
	}
	if protocol == "" {
		protocol = "tcp"
	}
	label := strings.ToUpper(protocol) + " " + strconv.Itoa(guest) + " → localhost:" + strconv.Itoa(port)
	if note != "" {
		label += " · " + note
	}
	return dnsTargetPort{Port: port, Label: label, Protocol: protocol, GuestPort: guest}
}
func dnsSinglePort(port int, note string) []dnsTargetPort {
	item := dnsPortOption(port, port, "tcp", note)
	if item.Port == 0 {
		return nil
	}
	return []dnsTargetPort{item}
}
func dnsAppendPort(out []dnsTargetPort, item dnsTargetPort) []dnsTargetPort {
	if item.Port == 0 {
		return out
	}
	for _, existing := range out {
		if existing.Port == item.Port && existing.Protocol == item.Protocol {
			return out
		}
	}
	return append(out, item)
}
func cksDNSWorkloadVisible(item k8s.Workload) bool {
	name := strings.ToLower(strings.TrimSpace(item.Name))
	ns := strings.ToLower(strings.TrimSpace(item.Namespace))
	if name == "" {
		return false
	}
	if ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" {
		return false
	}
	return name != "kubernetes"
}
func cksWorkloadDefaultPort(item k8s.Workload) int {
	if item.Port > 0 {
		return item.Port
	}
	if p := dnsPortFromURL(item.URL); p > 0 {
		return p
	}
	if item.NodePort > 0 {
		return item.NodePort
	}
	for _, port := range item.Ports {
		if port > 0 {
			return port
		}
	}
	if strings.EqualFold(item.Kind, "Ingress") || strings.Contains(strings.ToLower(item.URL), "http") {
		return 80
	}
	return 0
}
func cksWorkloadPorts(item k8s.Workload, note string) []dnsTargetPort {
	out := []dnsTargetPort{}
	add := func(port int, label string) {
		out = dnsAppendPort(out, dnsPortOption(port, port, "tcp", label))
	}
	if item.Port > 0 {
		add(item.Port, note)
	}
	for _, port := range item.Ports {
		add(port, note)
	}
	if item.NodePort > 0 {
		add(item.NodePort, note+" NodePort")
	}
	if p := dnsPortFromURL(item.URL); p > 0 {
		add(p, note+" URL")
	}
	kind := strings.ToLower(item.Kind)
	if kind == "ingress" || strings.Contains(kind, "ingress") {
		add(80, note+" HTTP")
		add(443, note+" HTTPS")
	}
	if kind == "service" && item.Port == 0 {
		add(80, note+" HTTP")
	}
	return out
}
func cksDNSPorts(c kubernetesState.Cluster, bals []loadBalancer, zone dnsZone) (int, []dnsTargetPort) {
	_, _, publish, _, _ := cksDNSAddress(c, zone, bals)
	out := []dnsTargetPort{}
	add := func(port int, note string) {
		out = dnsAppendPort(out, dnsPortOption(port, port, "tcp", note))
	}
	if publish == 0 {
		publish = 80
	}
	add(publish, "CKS HTTP")
	add(80, "HTTP")
	add(443, "HTTPS")
	lab := false
	for _, item := range append(append([]k8s.Workload{}, c.Inventory.Services...), c.Inventory.Ingresses...) {
		if strings.Contains(strings.ToLower(item.URL), "cks-lab") || dnsPortFromURL(item.URL) == 8080 {
			lab = true
		}
		note := item.Kind
		if note == "" {
			note = "CKS"
		}
		note += " " + item.Name
		for _, p := range cksWorkloadPorts(item, note) {
			out = dnsAppendPort(out, p)
		}
	}
	if lab {
		add(8080, "CKS lab proxy /cks-lab")
		add(19080, "CKS lab HTTP")
	}
	for _, integ := range c.Integrations {
		kind := strings.ToLower(integ.Kind)
		if kind != "lb" && kind != "load-balancer" && kind != "loadbalancer" {
			continue
		}
		for _, b := range bals {
			if b.ID != integ.ResourceID {
				continue
			}
			listen := b.ListenPort
			if listen == 0 && b.Mode != "nlb" {
				listen = 80
			}
			add(listen, "Attached LB listen")
			if b.BackendPort > 0 {
				add(b.BackendPort, "Attached LB backend")
			}
		}
	}
	return publish, out
}
func dnsVPSPorts(spec proto.InstanceSpec) []dnsTargetPort {
	out := make([]dnsTargetPort, 0, 3+len(spec.PortMappings))
	add := func(item dnsTargetPort) {
		if item.Port == 0 {
			return
		}
		for _, existing := range out {
			if existing.Port == item.Port && existing.Protocol == item.Protocol {
				return
			}
		}
		out = append(out, item)
	}
	add(dnsPortOption(spec.SSHPort, 22, "tcp", "SSH"))
	if spec.SSHPort > 0 {
		add(dnsPortOption(proto.HostHTTPPort(spec.SSHPort), 80, "tcp", "HTTP"))
		add(dnsPortOption(proto.HostHTTPSPort(spec.SSHPort), 443, "tcp", "HTTPS"))
	}
	for _, mapping := range spec.PortMappings {
		add(dnsPortOption(mapping.HostPort, mapping.GuestPort, mapping.Protocol, "Port mapping"))
	}
	return out
}

func (s *Server) dnsKubernetesClusters() []kubernetesState.Cluster {
	if s.kubernetes == nil {
		return nil
	}
	s.kubernetes.Lock()
	defer s.kubernetes.Unlock()
	out := make([]kubernetesState.Cluster, 0, len(s.kubernetes.Clusters))
	for _, c := range s.kubernetes.Clusters {
		if c.State == "DELETING" || c.State == "DELETED" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func cksDNSAddress(c kubernetesState.Cluster, zone dnsZone, balancers []loadBalancer) (typ, value string, port int, lbID, lbKind string) {
	for _, integ := range c.Integrations {
		kind := strings.ToLower(integ.Kind)
		if kind != "lb" && kind != "load-balancer" && kind != "loadbalancer" {
			continue
		}
		for _, b := range balancers {
			if b.ID != integ.ResourceID {
				continue
			}
			target := b.PublicIP
			if target == "" {
				target = zone.PublishAddress
			}
			if target == "" && b.Mode != "nlb" {
				target = "127.0.0.1"
			}
			rec := dnsAddressRecord(target)
			if rec.Type == "" {
				continue
			}
			port = b.ListenPort
			if port == 0 && b.Mode != "nlb" {
				port = 80
			}
			lbKind = "lb"
			if b.Mode != "nlb" {
				lbKind = "alb"
			}
			return rec.Type, rec.Value, port, b.ID, lbKind
		}
	}
	target := zone.PublishAddress
	if target == "" {
		target = "127.0.0.1"
	}
	rec := dnsAddressRecord(target)
	if rec.Type == "" {
		return "", "", 0, "", ""
	}
	return rec.Type, rec.Value, 80, "", ""
}

func (s *Server) dnsTargets() []dnsTarget {
	s.dns.Lock()
	zone := s.dns.Zone
	s.dns.Unlock()
	out := []dnsTarget{}
	instances, _ := s.Store.ListInstances()
	for _, inst := range instances {
		if inst.State == "DELETING" {
			continue
		}
		ip := dnsIPv4(inst.Spec.IPAddress)
		if ip == "" {
			continue
		}
		name := dnsJoin(dnsLabel(inst.ID), "vps", zone.Domain)
		ports := dnsVPSPorts(inst.Spec)
		port := inst.Spec.SSHPort
		if port == 0 && len(ports) > 0 {
			port = ports[0].Port
		}
		out = append(out, dnsTarget{Resource: "vps", ID: inst.ID, Label: inst.ID, Name: name, Type: "CNAME", Value: name + ".", Port: port, Ports: ports})
	}
	if s.balancers != nil {
		s.balancers.Lock()
		for _, b := range s.balancers.Items {
			if b == nil || b.Deleting {
				continue
			}
			kind := "lb"
			if b.Mode != "nlb" {
				kind = "alb"
			}
			target := b.PublicIP
			if target == "" {
				target = zone.PublishAddress
			}
			if target == "" && b.Mode != "nlb" {
				target = "127.0.0.1"
			}
			if target == "" {
				for _, upstream := range b.Healthy {
					if h := dnsHostFromURL(upstream); h != "" {
						target = h
						break
					}
				}
			}
			rec := dnsAddressRecord(target)
			if rec.Type == "" {
				continue
			}
			port := b.ListenPort
			if port == 0 && b.Mode != "nlb" {
				port = 80
			}
			name := dnsJoin(dnsLabel(b.ID), kind, zone.Domain)
			label := b.Name
			if label == "" {
				label = b.ID
			}
			ports := dnsSinglePort(port, "Listen")
			if b.BackendPort > 0 && b.BackendPort != port {
				ports = append(ports, dnsPortOption(b.BackendPort, b.BackendPort, "tcp", "Backend"))
			}
			out = append(out, dnsTarget{Resource: "lb", ID: b.ID, Label: label, Name: name, Type: "CNAME", Value: name + ".", Port: port, Ports: ports})
		}
		s.balancers.Unlock()
	}
	for _, c := range s.dnsKubernetesClusters() {
		var bals []loadBalancer
		if s.balancers != nil {
			s.balancers.Lock()
			for _, b := range s.balancers.Items {
				if b != nil && !b.Deleting {
					bals = append(bals, *b)
				}
			}
			s.balancers.Unlock()
		}
		typ, _, _, _, _ := cksDNSAddress(c, zone, bals)
		if typ == "" {
			continue
		}
		name := dnsJoin(dnsLabel(c.ID), "cks", zone.Domain)
		label := c.Name
		if label == "" {
			label = c.ID
		}
		port, ports := cksDNSPorts(c, bals, zone)
		out = append(out, dnsTarget{Resource: "cks", ID: c.ID, Label: label + " · cluster", Name: name, Type: "CNAME", Value: name + ".", Port: port, Ports: ports})
		clusterName := dnsLabel(c.Name)
		if clusterName == "" {
			clusterName = dnsLabel(c.ID)
		}
		for _, svc := range c.Inventory.Services {
			if !cksDNSWorkloadVisible(svc) {
				continue
			}
			svcPorts := cksWorkloadPorts(svc, "Service "+svc.Name)
			if len(svcPorts) == 0 {
				svcPorts = ports
			}
			svcPort := cksWorkloadDefaultPort(svc)
			if svcPort == 0 {
				svcPort = port
			}
			svcName := dnsJoin(dnsLabel(svc.Name), "svc", clusterName, "cks", zone.Domain)
			out = append(out, dnsTarget{Resource: "cks", ID: c.ID + "/service/" + svc.Namespace + "/" + svc.Name, Label: svc.Name + " · Service", Name: svcName, Type: "CNAME", Value: svcName + ".", Port: svcPort, Ports: svcPorts})
		}
		for _, ing := range c.Inventory.Ingresses {
			if !cksDNSWorkloadVisible(ing) {
				continue
			}
			ingPorts := cksWorkloadPorts(ing, "Ingress "+ing.Name)
			if len(ingPorts) == 0 {
				ingPorts = ports
			}
			ingPort := cksWorkloadDefaultPort(ing)
			if ingPort == 0 {
				ingPort = port
			}
			ingName := dnsJoin(dnsLabel(ing.Name), "ing", clusterName, "cks", zone.Domain)
			out = append(out, dnsTarget{Resource: "cks", ID: c.ID + "/ingress/" + ing.Namespace + "/" + ing.Name, Label: ing.Name + " · Ingress", Name: ingName, Type: "CNAME", Value: ingName + ".", Port: ingPort, Ports: ingPorts})
		}
	}
	if s.databases != nil && s.databaseError == nil {
		s.databases.Lock()
		for _, p := range s.databases.Projects {
			if dnsAddressRecord(p.Spec.Host).Type == "" {
				continue
			}
			name := dnsJoin(dnsLabel(p.Spec.ID), "db", zone.Domain)
			label := p.Name
			if label == "" {
				label = p.Spec.ID
			}
			out = append(out, dnsTarget{Resource: "db", ID: p.Spec.ID, Label: label, Name: name, Type: "CNAME", Value: name + ".", Port: p.Spec.Port, Ports: dnsSinglePort(p.Spec.Port, "Database")})
		}
		s.databases.Unlock()
	}
	if s.applications != nil {
		s.applications.Lock()
		for _, a := range s.applications.Managed {
			target := dnsHostFromURL(a.URL)
			if target == "" {
				target = a.Spec.Address
			}
			if dnsAddressRecord(target).Type == "" && dnsIPv4(a.Spec.Address) == "" {
				continue
			}
			port := a.Spec.Port
			if port == 0 {
				port = dnsPortFromURL(a.URL)
			}
			name := dnsJoin(dnsLabel(a.ID), "app", zone.Domain)
			label := a.Name
			if label == "" {
				label = a.ID
			}
			out = append(out, dnsTarget{Resource: "app", ID: a.ID, Label: label, Name: name, Type: "CNAME", Value: name + ".", Port: port, Ports: dnsSinglePort(port, "Application")})
		}
		s.applications.Unlock()
	}
	if s.docker != nil {
		s.docker.mu.Lock()
		for _, a := range s.docker.Applications {
			name := dnsJoin(dnsLabel(a.ID), "docker", zone.Domain)
			label := a.Name
			if label == "" {
				label = a.ID
			}
			port := 0
			if len(a.Spec.Ports) > 0 {
				port = a.Spec.Ports[0].HostPort
				if port == 0 {
					port = a.Spec.Ports[0].ContainerPort
				}
			}
			out = append(out, dnsTarget{Resource: "docker", ID: a.ID, Label: label + " · Docker", Name: name, Type: "CNAME", Value: name + ".", Port: port, Ports: dnsSinglePort(port, "Docker")})
		}
		s.docker.mu.Unlock()
	}
	workers, _ := s.Store.ListWorkers()
	for _, wk := range workers {
		if dnsAddressRecord(wk.Hostname).Type == "" {
			continue
		}
		name := dnsJoin(dnsLabel(wk.ID), "worker", zone.Domain)
		out = append(out, dnsTarget{Resource: "worker", ID: wk.ID, Label: wk.ID, Name: name, Type: "CNAME", Value: name + ".", Port: 0})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resource == out[j].Resource {
			return out[i].Label < out[j].Label
		}
		return out[i].Resource < out[j].Resource
	})
	return out
}
func dnsFindTarget(targets []dnsTarget, resource, id string) (dnsTarget, bool) {
	for _, item := range targets {
		if item.Resource == resource && item.ID == id {
			return item, true
		}
	}
	return dnsTarget{}, false
}
func validDNSZone(zone string) bool {
	return validDNSName(zone) && strings.Contains(zone, ".")
}
func validDNSName(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" || len(name) > 253 || strings.Contains(name, "..") || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}
