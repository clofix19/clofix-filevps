package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

const (
	vpcDefaultID     = "vpc-default"
	vpcDefaultName   = "Default VPC"
	vpcDefaultCIDR   = "10.231.0.0/16"
	vpcDefaultGW     = "10.231.0.1"
	vpcManagedAppNet = "10.231.128.0/24"
)

type vpc struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CIDR      string    `json:"cidr"`
	Gateway   string    `json:"gateway"`
	Default   bool      `json:"default"`
	AccountID string    `json:"account_id,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

type vpcView struct {
	vpc
	Used     int `json:"used"`
	Capacity int `json:"capacity"`
}

type vpcStore struct {
	sync.Mutex
	VPCs map[string]vpc `json:"vpcs"`
	file string
}

func loadVPCs(dir string) *vpcStore {
	d := &vpcStore{file: filepath.Join(dir, "vpcs.json")}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			d.VPCs = nil
		}
	}
	d.ensure()
	return d
}

func (d *vpcStore) ensure() {
	if d.VPCs == nil {
		d.VPCs = map[string]vpc{}
	}
	now := time.Now().UTC()
	if v, ok := d.VPCs[vpcDefaultID]; !ok {
		d.VPCs[vpcDefaultID] = vpc{ID: vpcDefaultID, Name: vpcDefaultName, CIDR: vpcDefaultCIDR, Gateway: vpcDefaultGW, Default: true, Created: now, Updated: now}
	} else {
		v.ID = vpcDefaultID
		v.CIDR = vpcDefaultCIDR
		v.Gateway = vpcDefaultGW
		v.Default = true
		if strings.TrimSpace(v.Name) == "" {
			v.Name = vpcDefaultName
		}
		d.VPCs[vpcDefaultID] = v
	}
}

func (d *vpcStore) save() error {
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

func (s *Server) vpcRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/vpcs", s.auth(s.listVPCs))
	m.HandleFunc("POST /api/vpcs", s.auth(s.createVPC))
	m.HandleFunc("PUT /api/vpcs/{id}", s.auth(s.updateVPC))
	m.HandleFunc("DELETE /api/vpcs/{id}", s.auth(s.deleteVPC))
}

func (s *Server) listVPCs(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	views := s.vpcViews()
	if p.Kind != "admin" {
		filtered := make([]vpcView, 0, len(views))
		for _, v := range views {
			if v.Default || s.canAccessAccount(p, v.AccountID) {
				filtered = append(filtered, v)
			}
		}
		views = filtered
	}
	writeJSON(w, 200, map[string]any{"vpcs": views})
}

func (s *Server) createVPC(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		CIDR string `json:"cidr"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	name, err := normalizeVPCName(in.Name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	block, err := parseExtraVPCCidr(in.CIDR)
	if err != nil {
		fail(w, statusForVPCError(err), err.Error())
		return
	}
	d := s.vpcs
	d.Lock()
	defer d.Unlock()
	d.ensure()
	if err = vpcCIDRFits(block, d.VPCs, ""); err != nil {
		fail(w, statusForVPCError(err), err.Error())
		return
	}
	now := time.Now().UTC()
	v := vpc{ID: "vpc-" + randHex(8), Name: name, CIDR: block.String(), Gateway: vpcDefaultGW, AccountID: s.stampAccount(principalFrom(r)), Created: now, Updated: now}
	d.VPCs[v.ID] = v
	if err = d.save(); err != nil {
		delete(d.VPCs, v.ID)
		fail(w, 500, "could not save VPC")
		return
	}
	s.Store.Audit(applicationOwner(r), "vpc.create", v.ID, v.CIDR)
	writeJSON(w, 201, s.vpcViewLocked(v))
}

func (s *Server) updateVPC(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name string `json:"name"`
		CIDR string `json:"cidr"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid json")
		return
	}
	name, err := normalizeVPCName(in.Name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := s.vpcs
	d.Lock()
	defer d.Unlock()
	d.ensure()
	v, ok := d.VPCs[id]
	if !ok {
		fail(w, 404, "VPC not found")
		return
	}
	cidr := strings.TrimSpace(in.CIDR)
	if cidr != "" && cidr != v.CIDR {
		if v.Default {
			fail(w, 409, "the default VPC CIDR cannot be changed")
			return
		}
		if s.vpcInUseLocked(id) {
			fail(w, 409, "CIDR can be changed only when the VPC has no VPS, database, or managed app addresses")
			return
		}
		block, parseErr := parseExtraVPCCidr(cidr)
		if parseErr != nil {
			fail(w, statusForVPCError(parseErr), parseErr.Error())
			return
		}
		if err = vpcCIDRFits(block, d.VPCs, id); err != nil {
			fail(w, statusForVPCError(err), err.Error())
			return
		}
		v.CIDR = block.String()
	}
	v.Name = name
	v.Updated = time.Now().UTC()
	d.VPCs[id] = v
	if err = d.save(); err != nil {
		fail(w, 500, "could not save VPC")
		return
	}
	s.Store.Audit(applicationOwner(r), "vpc.update", v.ID, v.CIDR)
	writeJSON(w, 200, s.vpcViewLocked(v))
}

func (s *Server) deleteVPC(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.vpcs
	d.Lock()
	defer d.Unlock()
	d.ensure()
	v, ok := d.VPCs[id]
	if !ok {
		fail(w, 404, "VPC not found")
		return
	}
	if v.Default {
		fail(w, 409, "the default VPC cannot be deleted")
		return
	}
	if s.vpcInUseLocked(id) {
		fail(w, 409, "VPC still has VPS, database, or managed app addresses")
		return
	}
	delete(d.VPCs, id)
	if err := d.save(); err != nil {
		d.VPCs[id] = v
		fail(w, 500, "could not save VPC")
		return
	}
	s.Store.Audit(applicationOwner(r), "vpc.delete", id, v.CIDR)
	writeJSON(w, 200, map[string]string{"id": id, "state": "deleted"})
}

func (s *Server) lookupVPC(id string) (vpc, error) {
	s.vpcs.Lock()
	defer s.vpcs.Unlock()
	s.vpcs.ensure()
	if id == "" {
		id = vpcDefaultID
	}
	v, ok := s.vpcs.VPCs[id]
	if !ok {
		return vpc{}, fmt.Errorf("VPC not found")
	}
	return v, nil
}

func (s *Server) resolveVPCID(id string) (string, error) {
	v, err := s.lookupVPC(id)
	if err != nil {
		return "", err
	}
	return v.ID, nil
}

func (s *Server) reserveNetwork(spec *proto.InstanceSpec, instances []store.Instance) bool {
	v, err := s.lookupVPC(spec.VPCID)
	if err != nil {
		return false
	}
	spec.VPCID = v.ID
	usedIPs, usedPorts := s.usedGuestIPs(instances), map[int]bool{}
	for _, instance := range instances {
		usedPorts[instance.Spec.SSHPort] = true
	}
	skip := s.skipNets(v)
	if v.Default {
		for slot := 0; slot < sshPortCount; slot++ {
			ip, port := fmt.Sprintf("10.231.0.%d", slot+2), sshPortBase+slot
			if usedIPs[ip] || usedPorts[port] || ipInNets(ip, skip) {
				continue
			}
			spec.IPAddress, spec.SSHPort = ip, port
			return true
		}
		for _, ip := range vpcHostIPs(mustCIDR(vpcDefaultCIDR), skip) {
			if strings.HasPrefix(ip, "10.231.0.") {
				continue
			}
			if usedIPs[ip] {
				continue
			}
			for slot := 0; slot < sshPortCount; slot++ {
				port := sshPortBase + slot
				if usedPorts[port] {
					continue
				}
				spec.IPAddress, spec.SSHPort = ip, port
				return true
			}
			return false
		}
		return false
	}
	for _, ip := range vpcHostIPs(mustCIDR(v.CIDR), skip) {
		if usedIPs[ip] {
			continue
		}
		for slot := 0; slot < sshPortCount; slot++ {
			port := sshPortBase + slot
			if usedPorts[port] {
				continue
			}
			spec.IPAddress, spec.SSHPort = ip, port
			return true
		}
		return false
	}
	return false
}

func (s *Server) usedGuestIPs(instances []store.Instance) map[string]bool {
	used := map[string]bool{}
	for _, instance := range instances {
		if instance.Spec.IPAddress != "" {
			used[instance.Spec.IPAddress] = true
		}
	}
	if s.applications != nil {
		s.applications.Lock()
		for _, app := range s.applications.Managed {
			if app.Spec.Address != "" {
				used[app.Spec.Address] = true
			}
		}
		s.applications.Unlock()
	}
	return used
}

func (s *Server) skipNets(v vpc) []*net.IPNet {
	out := []*net.IPNet{mustCIDR(vpcManagedAppNet)}
	s.vpcs.Lock()
	defer s.vpcs.Unlock()
	for _, other := range s.vpcs.VPCs {
		if other.ID == v.ID || other.Default {
			continue
		}
		if n := mustCIDR(other.CIDR); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) vpcViews() []vpcView {
	s.vpcs.Lock()
	defer s.vpcs.Unlock()
	s.vpcs.ensure()
	out := make([]vpcView, 0, len(s.vpcs.VPCs))
	for _, v := range s.vpcs.VPCs {
		out = append(out, s.vpcViewLocked(v))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) vpcViewLocked(v vpc) vpcView {
	skip := []*net.IPNet{mustCIDR(vpcManagedAppNet)}
	if v.Default {
		for _, other := range s.vpcs.VPCs {
			if other.Default {
				continue
			}
			if n := mustCIDR(other.CIDR); n != nil {
				skip = append(skip, n)
			}
		}
	}
	hosts := vpcHostIPs(mustCIDR(v.CIDR), skip)
	return vpcView{vpc: v, Used: s.vpcUseCountLocked(v.ID), Capacity: len(hosts)}
}

func (s *Server) vpcInUseLocked(id string) bool {
	return s.vpcUseCountLocked(id) > 0
}

func (s *Server) vpcUseCountLocked(id string) int {
	n := 0
	instances, _ := s.Store.ListInstances()
	for _, inst := range instances {
		if s.resourceVPCIDLocked(inst.Spec.VPCID, inst.Spec.IPAddress) == id {
			n++
		}
	}
	if s.applications != nil {
		s.applications.Lock()
		for _, app := range s.applications.Managed {
			if s.resourceVPCIDLocked(app.Spec.VPCID, app.Spec.Address) == id {
				n++
			}
		}
		s.applications.Unlock()
	}
	if s.databases != nil {
		s.databases.Lock()
		for _, project := range s.databases.Projects {
			if s.resourceVPCIDLocked(project.Spec.VPCID, "") == id {
				n++
			}
		}
		s.databases.Unlock()
	}
	return n
}

func (s *Server) resourceVPCIDLocked(vpcID, ip string) string {
	if vpcID != "" {
		if _, ok := s.vpcs.VPCs[vpcID]; ok {
			return vpcID
		}
	}
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return vpcDefaultID
	}
	for _, v := range s.vpcs.VPCs {
		if v.Default {
			continue
		}
		if n := mustCIDR(v.CIDR); n != nil && n.Contains(parsed) {
			return v.ID
		}
	}
	return vpcDefaultID
}

func normalizeVPCName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", fmt.Errorf("VPC name is required (max 80 characters)")
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return "", fmt.Errorf("VPC name is invalid")
	}
	return name, nil
}

func parseExtraVPCCidr(raw string) (*net.IPNet, error) {
	ip, n, err := net.ParseCIDR(strings.TrimSpace(raw))
	if err != nil || ip == nil || ip.To4() == nil || n == nil {
		return nil, fmt.Errorf("enter an IPv4 CIDR such as 10.231.20.0/24")
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 16 || ones > 24 {
		return nil, fmt.Errorf("CIDR prefix must be /24 to /16")
	}
	if !rfc1918Net(n) {
		return nil, fmt.Errorf("CIDR must be a private IPv4 block")
	}
	parent := mustCIDR(vpcDefaultCIDR)
	if !cidrContainsNet(parent, n) {
		return nil, fmt.Errorf("extra VPC CIDR must be a subnet of %s", vpcDefaultCIDR)
	}
	if ones == 16 {
		return nil, fmt.Errorf("CIDR overlaps another VPC")
	}
	gw := net.ParseIP(vpcDefaultGW).To4()
	if n.Contains(gw) {
		return nil, fmt.Errorf("CIDR cannot include the FileVPS gateway %s", vpcDefaultGW)
	}
	return n, nil
}

func vpcCIDRFits(block *net.IPNet, existing map[string]vpc, skipID string) error {
	managed := mustCIDR(vpcManagedAppNet)
	if cidrOverlap(block, managed) {
		return fmt.Errorf("CIDR overlaps the reserved managed-app range %s", vpcManagedAppNet)
	}
	for id, v := range existing {
		if id == skipID {
			continue
		}
		other := mustCIDR(v.CIDR)
		if other == nil {
			continue
		}
		if v.Default {
			if !cidrContainsNet(other, block) || cidrEqual(other, block) {
				return fmt.Errorf("CIDR overlaps another VPC")
			}
			continue
		}
		if cidrOverlap(block, other) {
			return fmt.Errorf("CIDR overlaps another VPC")
		}
	}
	return nil
}

func statusForVPCError(err error) int {
	msg := err.Error()
	if strings.Contains(msg, "overlaps") {
		return 409
	}
	return 400
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		return nil
	}
	return n
}

func rfc1918Net(n *net.IPNet) bool {
	for _, raw := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if p := mustCIDR(raw); p != nil && cidrContainsNet(p, n) {
			return true
		}
	}
	return false
}

func cidrContainsNet(parent, child *net.IPNet) bool {
	if parent == nil || child == nil {
		return false
	}
	last := cidrLast(child)
	return parent.Contains(child.IP) && parent.Contains(last)
}

func cidrOverlap(a, b *net.IPNet) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func cidrEqual(a, b *net.IPNet) bool {
	return a != nil && b != nil && a.String() == b.String()
}

func cidrLast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := n.Mask
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func ipInNets(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n != nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func vpcHostIPs(n *net.IPNet, skip []*net.IPNet) []string {
	if n == nil {
		return nil
	}
	ip := n.IP.To4()
	if ip == nil {
		return nil
	}
	mask := binary.BigEndian.Uint32(net.IP(n.Mask).To4())
	base := binary.BigEndian.Uint32(ip) & mask
	size := ^mask
	out := []string{}
	for i := uint32(1); i < size; i++ {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], base+i)
		if b[3] < 2 {
			continue
		}
		addr := net.IP(b[:]).String()
		if ipInNets(addr, skip) {
			continue
		}
		out = append(out, addr)
	}
	return out
}
