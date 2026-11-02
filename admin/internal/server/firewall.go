package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

type firewallPolicy struct {
	Restrict bool                 `json:"restrict"`
	Rules    []proto.FirewallRule `json:"rules,omitempty"`
}

type firewallStore struct {
	sync.Mutex
	Workers   map[string]firewallPolicy `json:"workers"`
	Instances map[string]firewallPolicy `json:"instances"`
	file      string
}

func loadFirewall(dir string) *firewallStore {
	d := &firewallStore{file: filepath.Join(dir, "firewall.json")}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid firewall.json")
		}
	}
	if d.Workers == nil {
		d.Workers = map[string]firewallPolicy{}
	}
	if d.Instances == nil {
		d.Instances = map[string]firewallPolicy{}
	}
	return d
}

func (d *firewallStore) save() error {
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

func (d *firewallStore) policy(kind, id string) firewallPolicy {
	d.Lock()
	defer d.Unlock()
	if kind == "worker" {
		return d.Workers[id]
	}
	return d.Instances[id]
}

func (d *firewallStore) put(kind, id string, policy firewallPolicy) error {
	d.Lock()
	defer d.Unlock()
	if kind == "worker" {
		d.Workers[id] = policy
	} else {
		d.Instances[id] = policy
	}
	return d.save()
}

func (d *firewallStore) dropInstance(id string) {
	d.Lock()
	defer d.Unlock()
	if _, ok := d.Instances[id]; !ok {
		return
	}
	delete(d.Instances, id)
	_ = d.save()
}

func (s *Server) firewallRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/firewall", s.auth(s.getFirewall))
	m.HandleFunc("PUT /api/firewall/workers/{id}", s.auth(s.putWorkerFirewall))
	m.HandleFunc("PUT /api/firewall/instances/{id}", s.auth(s.putInstanceFirewall))
}

func (s *Server) getFirewall(w http.ResponseWriter, r *http.Request) {
	workers, _ := s.Store.ListWorkers()
	instances, _ := s.Store.ListInstances()
	workerOut := make([]map[string]any, 0, len(workers))
	for _, wk := range workers {
		p := s.firewall.policy("worker", wk.ID)
		workerOut = append(workerOut, map[string]any{
			"id": wk.ID, "hostname": wk.Hostname, "status": wk.Status,
			"restrict": p.Restrict, "rules": p.Rules,
		})
	}
	sort.Slice(workerOut, func(i, j int) bool {
		return workerOut[i]["id"].(string) < workerOut[j]["id"].(string)
	})
	instanceOut := make([]map[string]any, 0, len(instances))
	for _, inst := range instances {
		p := s.firewall.policy("instance", inst.ID)
		vpcID := inst.Spec.VPCID
		if vpcID == "" {
			vpcID = vpcDefaultID
		}
		instanceOut = append(instanceOut, map[string]any{
			"id": inst.ID, "worker_id": inst.WorkerID, "state": inst.State,
			"vpc_id": vpcID, "vpc_cidr": s.vpcCIDR(vpcID), "ip": inst.Spec.IPAddress,
			"maps": instancePortMaps(inst), "restrict": p.Restrict, "rules": p.Rules,
		})
	}
	sort.Slice(instanceOut, func(i, j int) bool {
		return instanceOut[i]["id"].(string) < instanceOut[j]["id"].(string)
	})
	ranges := make([]map[string]any, 0, len(proto.CloFixHostPortRanges))
	names := []string{"FileVPS SSH", "FileVPS HTTP", "FileVPS HTTPS", "Custom port maps", "Managed app gateways", "Database TLS"}
	for i, r := range proto.CloFixHostPortRanges {
		label := "CloFix ports"
		if i < len(names) {
			label = names[i]
		}
		ranges = append(ranges, map[string]any{"from": r[0], "to": r[1], "name": label})
	}
	writeJSON(w, 200, map[string]any{"workers": workerOut, "instances": instanceOut, "host_ports": ranges})
}

func (s *Server) putWorkerFirewall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	if _, err := s.Store.GetWorker(id); err != nil {
		fail(w, http.StatusNotFound, "worker not found")
		return
	}
	policy, err := decodeFirewallPolicy(r, true)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.firewall.put("worker", id, policy); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	spec := proto.FirewallSpec{Scope: "host", Restrict: policy.Restrict, Rules: policy.Rules}
	if err := s.Store.QueueOp(id, proto.Operation{
		ID: "fw/host/" + id + "/" + randHex(4), Action: proto.ActionUpdateFirewall, WorkerID: id, Firewall: &spec,
	}); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "firewall.worker", id, map[bool]string{true: "restrict", false: "open"}[policy.Restrict])
	writeJSON(w, 200, map[string]any{"id": id, "restrict": policy.Restrict, "rules": policy.Rules, "queued": true})
}

func (s *Server) putInstanceFirewall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !apiInstanceID.MatchString(id) {
		fail(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	policy, err := decodeFirewallPolicy(r, false)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.firewall.put("instance", id, policy); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	spec := proto.FirewallSpec{Scope: "vps", InstanceID: id, GuestIP: inst.Spec.IPAddress, Restrict: policy.Restrict, Rules: policy.Rules}
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: id + "/" + proto.ActionUpdateFirewall + "/" + randHex(4), Action: proto.ActionUpdateFirewall, InstanceID: id, WorkerID: inst.WorkerID, Firewall: &spec,
	}); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "firewall.instance", id, map[bool]string{true: "restrict", false: "open"}[policy.Restrict])
	writeJSON(w, 200, map[string]any{"id": id, "restrict": policy.Restrict, "rules": policy.Rules, "queued": true})
}

func decodeFirewallPolicy(r *http.Request, host bool) (firewallPolicy, error) {
	var in firewallPolicy
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&in); err != nil {
		return in, err
	}
	spec := proto.FirewallSpec{Restrict: in.Restrict, Rules: in.Rules}
	if err := proto.NormalizeFirewallSpec(&spec, host); err != nil {
		return in, err
	}
	return firewallPolicy{Restrict: spec.Restrict, Rules: spec.Rules}, nil
}

func (s *Server) firewallsForWorker(workerID string) []proto.FirewallSpec {
	out := []proto.FirewallSpec{}
	p := s.firewall.policy("worker", workerID)
	out = append(out, proto.FirewallSpec{Scope: "host", Restrict: p.Restrict, Rules: p.Rules})
	instances, err := s.Store.ListInstances()
	if err != nil {
		return out
	}
	for _, inst := range instances {
		if inst.WorkerID != workerID {
			continue
		}
		ip := s.firewall.policy("instance", inst.ID)
		out = append(out, proto.FirewallSpec{
			Scope: "vps", InstanceID: inst.ID, GuestIP: inst.Spec.IPAddress, Restrict: ip.Restrict, Rules: ip.Rules,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}

func (s *Server) vpcCIDR(id string) string {
	if s.vpcs == nil {
		return vpcDefaultCIDR
	}
	s.vpcs.Lock()
	defer s.vpcs.Unlock()
	if v, ok := s.vpcs.VPCs[id]; ok && strings.TrimSpace(v.CIDR) != "" {
		return v.CIDR
	}
	return vpcDefaultCIDR
}

func instancePortMaps(inst store.Instance) []map[string]any {
	out := []map[string]any{}
	if inst.Spec.SSHPort > 0 {
		out = append(out,
			map[string]any{"protocol": "tcp", "guest_port": 22, "host_port": inst.Spec.SSHPort, "managed": true},
			map[string]any{"protocol": "tcp", "guest_port": 80, "host_port": proto.HostHTTPPort(inst.Spec.SSHPort), "managed": true},
			map[string]any{"protocol": "tcp", "guest_port": 443, "host_port": proto.HostHTTPSPort(inst.Spec.SSHPort), "managed": true},
		)
	}
	for _, m := range inst.Spec.PortMappings {
		out = append(out, map[string]any{"protocol": m.Protocol, "guest_port": m.GuestPort, "host_port": m.HostPort, "managed": false})
	}
	return out
}
