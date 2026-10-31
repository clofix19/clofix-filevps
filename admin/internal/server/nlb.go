package server

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type nlbSettings struct {
	IPRanges []string `json:"ip_ranges"`
}
type nlbListener struct {
	address  string
	listener net.Listener
}

// Pools are provider-allocated addresses already configured/routed to this host.
// Private and loopback addresses are accepted for local deployments.
func parseNLBPool(entries []string) ([]string, error) {
	set := map[netip.Addr]bool{}
	add := func(ip netip.Addr) error {
		if !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
			return fmt.Errorf("use unicast IPv4 addresses")
		}
		set[ip] = true
		if len(set) > 4096 {
			return fmt.Errorf("IP pool exceeds 4096 addresses")
		}
		return nil
	}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, e := netip.ParsePrefix(entry)
			if e != nil || !prefix.Addr().Is4() {
				return nil, fmt.Errorf("invalid IPv4 CIDR: %s", entry)
			}
			prefix = prefix.Masked()
			if prefix.Bits() < 20 {
				return nil, fmt.Errorf("each CIDR must be /20 or smaller")
			}
			count := 1 << uint(32-prefix.Bits())
			ip := prefix.Addr()
			for i := 0; i < count; i++ {
				if prefix.Bits() >= 31 || (i != 0 && i != count-1) {
					if e = add(ip); e != nil {
						return nil, e
					}
				}
				ip = ip.Next()
			}
		} else if left, right, ok := strings.Cut(entry, "-"); ok {
			start, e := netip.ParseAddr(strings.TrimSpace(left))
			end, e2 := netip.ParseAddr(strings.TrimSpace(right))
			if e != nil || e2 != nil || !start.Is4() || !end.Is4() || start.Compare(end) > 0 {
				return nil, fmt.Errorf("invalid IPv4 range: %s", entry)
			}
			for ip := start; ; ip = ip.Next() {
				if e = add(ip); e != nil {
					return nil, e
				}
				if ip == end {
					break
				}
			}
		} else {
			ip, e := netip.ParseAddr(entry)
			if e != nil {
				return nil, fmt.Errorf("invalid IPv4 address: %s", entry)
			}
			if e = add(ip); e != nil {
				return nil, e
			}
		}
	}
	ips := make([]netip.Addr, 0, len(set))
	for ip := range set {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
	out := []string{}
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out, nil
}
func (s *Server) getNLBSettings(w http.ResponseWriter, r *http.Request) {
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, d.Network)
}
func (s *Server) saveNLBSettings(w http.ResponseWriter, r *http.Request) {
	var settings nlbSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&settings) != nil {
		fail(w, 400, "invalid IP pool settings")
		return
	}
	ips, e := parseNLBPool(settings.IPRanges)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	d := s.balancers
	d.Lock()
	defer d.Unlock()
	allowed := map[string]bool{}
	for _, ip := range ips {
		allowed[ip] = true
	}
	used := map[string]bool{}
	assigned := map[string]string{}
	ids := []string{}
	for id, b := range d.Items {
		if b.Mode == "nlb" {
			ids = append(ids, id)
			if allowed[b.PublicIP] && !used[b.PublicIP] {
				assigned[id] = b.PublicIP
				used[b.PublicIP] = true
			}
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if assigned[id] != "" {
			continue
		}
		for _, ip := range ips {
			if !used[ip] {
				assigned[id] = ip
				used[ip] = true
				break
			}
		}
		if assigned[id] == "" {
			fail(w, 409, "pool needs at least one address per existing NLB; delete unused NLBs before shrinking the pool")
			return
		}
	}
	previous := d.Network
	oldIPs := map[string]string{}
	for id, ip := range assigned {
		oldIPs[id] = d.Items[id].PublicIP
		d.Items[id].PublicIP = ip
	}
	d.Network = settings
	if e = d.save(); e != nil {
		d.Network = previous
		for id, ip := range oldIPs {
			d.Items[id].PublicIP = ip
		}
		fail(w, 500, e.Error())
		return
	}
	s.Store.Audit("admin", "load-balancer.ip-pool", "settings", "saved")
	writeJSON(w, 200, settings)
}
func (d *loadBalancerStore) nextIP(exclude string) string {
	ips, _ := parseNLBPool(d.Network.IPRanges)
	used := map[string]bool{}
	for id, b := range d.Items {
		if id != exclude && b.Mode == "nlb" {
			used[b.PublicIP] = true
		}
	}
	for _, ip := range ips {
		if !used[ip] {
			return ip
		}
	}
	return ""
}

// Explicit NAT mapping for container deployments. Unlisted endpoints bind directly.
func nlbBindAddress(b *loadBalancer) (string, error) {
	public := net.JoinHostPort(b.PublicIP, strconv.Itoa(b.ListenPort))
	raw := os.Getenv("CLOFIX_NLB_LISTEN_MAP")
	if raw == "" {
		return public, nil
	}
	mappings := map[string]string{}
	if e := json.Unmarshal([]byte(raw), &mappings); e != nil {
		return "", fmt.Errorf("invalid CLOFIX_NLB_LISTEN_MAP: %w", e)
	}
	if target, ok := mappings[public]; ok {
		host, port, e := net.SplitHostPort(target)
		n, parseErr := strconv.Atoi(port)
		if e != nil || net.ParseIP(host) == nil || parseErr != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid NLB bind mapping for %s", public)
		}
		return target, nil
	}
	return public, nil
}

// Called under the balancer lock. Listener failures remain visible and retry on the next pass.
func (s *Server) reconcileNLBListeners() {
	d := s.balancers
	if d.listeners == nil {
		d.listeners = map[string]nlbListener{}
	}
	for id, l := range d.listeners {
		b := d.Items[id]
		expected := ""
		if b != nil {
			expected, _ = nlbBindAddress(b)
		}
		if b == nil || b.Deleting || b.Mode != "nlb" || l.address != expected {
			l.listener.Close()
			delete(d.listeners, id)
		}
	}
	for id, b := range d.Items {
		if b.Mode != "nlb" || b.Deleting {
			continue
		}
		if _, running := d.listeners[id]; !running {
			plan, e := s.licensedPlan()
			if e != nil || len(d.Items) > plan.MaxLB {
				b.ListenerError = "License required or load balancer quota exceeded"
				continue
			}
		}
		if b.PublicIP == "" {
			b.PublicIP = d.nextIP(id)
		}
		if b.PublicIP == "" {
			b.ListenerError = "Configure an allocated IP pool with a free address"
			continue
		}
		address, err := nlbBindAddress(b)
		if err != nil {
			b.ListenerError = err.Error()
			continue
		}
		if _, ok := d.listeners[id]; ok {
			continue
		}
		listener, e := net.Listen("tcp4", address)
		if e != nil {
			b.ListenerError = "Cannot listen on " + address + ": " + e.Error()
			continue
		}
		b.ListenerError = ""
		d.listeners[id] = nlbListener{address: address, listener: listener}
		go s.acceptNLB(id, listener)
	}
}
func (s *Server) acceptNLB(id string, l net.Listener) {
	for {
		conn, e := l.Accept()
		if e != nil {
			return
		}
		go s.forwardNLB(id, conn)
	}
}
func (s *Server) forwardNLB(id string, client net.Conn) {
	defer client.Close()
	d := s.balancers
	d.Lock()
	b := d.Items[id]
	if b == nil || b.Deleting || b.Mode != "nlb" {
		d.Unlock()
		return
	}
	ids := []string{}
	for id := range b.Healthy {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		d.Unlock()
		return
	}
	index := int(b.sequence % uint64(len(ids)))
	b.sequence++
	switch b.Algorithm {
	case "random":
		index = rand.Intn(len(ids))
	case "ip_hash":
		host, _, _ := net.SplitHostPort(client.RemoteAddr().String())
		h := fnv.New64a()
		h.Write([]byte(host))
		index = int(h.Sum64() % uint64(len(ids)))
	case "least_connections":
		for j := range ids {
			if b.active[ids[j]] < b.active[ids[index]] {
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
	backendID := ids[index]
	address := b.Healthy[backendID]
	b.active[backendID]++
	d.Unlock()
	defer func() { d.Lock(); b.active[backendID]--; d.Unlock() }()
	backend, e := net.DialTimeout("tcp", address, 5*time.Second)
	if e != nil {
		d.Lock()
		delete(b.Healthy, backendID)
		d.Unlock()
		return
	}
	defer backend.Close()
	// Bound idle connections while allowing active streams, including half-closed TCP sessions.
	copyStream := func(dst, src net.Conn) {
		reader := &nlbReader{Conn: src}
		io.Copy(&nlbWriter{Conn: dst}, reader)
		if tcp, ok := dst.(*net.TCPConn); ok {
			tcp.CloseWrite()
		}
	}
	done := make(chan struct{})
	go func() { copyStream(backend, client); close(done) }()
	copyStream(client, backend)
	<-done
}

type nlbReader struct{ net.Conn }

func (r *nlbReader) Read(p []byte) (int, error) {
	r.SetReadDeadline(time.Now().Add(5 * time.Minute))
	return r.Conn.Read(p)
}

type nlbWriter struct{ net.Conn }

func (w *nlbWriter) Write(p []byte) (int, error) {
	w.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	return w.Conn.Write(p)
}
