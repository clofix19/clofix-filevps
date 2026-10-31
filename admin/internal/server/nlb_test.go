package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNLBIPPool(t *testing.T) {
	ips, e := parseNLBPool([]string{"203.0.113.0/30", "203.0.113.2-203.0.113.4", "203.0.113.1"})
	if e != nil || strings.Join(ips, ",") != "203.0.113.1,203.0.113.2,203.0.113.3,203.0.113.4" {
		t.Fatal(ips, e)
	}
	for _, entry := range []string{"0.0.0.0", "224.0.0.1", "::1", "203.0.113.4-203.0.113.2", "10.0.0.0/8", "garbage"} {
		if _, e := parseNLBPool([]string{entry}); e == nil {
			t.Fatal("invalid pool accepted", entry)
		}
	}
}
func TestNLBAllocationPersistenceAndPoolChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, _ := newSrv(t)
	h := s.AdminHandler()
	if c := do(h, "PUT", "/api/load-balancers/settings", `{"ip_ranges":["203.0.113.1-203.0.113.2"]}`, "").Code; c != 401 {
		t.Fatal(c)
	}
	if c := do(h, "PUT", "/api/load-balancers/settings", `{"ip_ranges":["203.0.113.1-203.0.113.2"]}`, tok).Code; c != 200 {
		t.Fatal(c)
	}
	payload := strings.Replace(lbPayload, `"name":"Web pool"`, `"mode":"nlb","listen_port":80,"backend_port":80,"name":"Web pool"`, 1)
	for n := 0; n < 2; n++ {
		w := do(h, "POST", "/api/load-balancers", payload, tok)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var b loadBalancer
		json.Unmarshal(w.Body.Bytes(), &b)
		if b.PublicIP == "" {
			t.Fatal("IP not assigned")
		}
	}
	if c := do(h, "POST", "/api/load-balancers", payload, tok).Code; c != 409 {
		t.Fatal("exhausted pool accepted", c)
	}
	original := map[string]string{}
	for id, b := range s.balancers.Items {
		original[id] = b.PublicIP
	}
	if c := do(h, "PUT", "/api/load-balancers/settings", `{"ip_ranges":["203.0.113.9"]}`, tok).Code; c != 409 {
		t.Fatal("undersized pool accepted", c)
	}
	for id, ip := range original {
		if s.balancers.Items[id].PublicIP != ip {
			t.Fatal("rejected edit changed IP")
		}
	}
	if c := do(h, "PUT", "/api/load-balancers/settings", `{"ip_ranges":["203.0.113.9-203.0.113.10"]}`, tok).Code; c != 200 {
		t.Fatal(c)
	}
	used := map[string]bool{}
	for _, b := range s.balancers.Items {
		if used[b.PublicIP] || b.PublicIP != "203.0.113.9" && b.PublicIP != "203.0.113.10" {
			t.Fatal("invalid reassignment", b.PublicIP)
		}
		used[b.PublicIP] = true
	}
	loaded := loadBalancers(dir)
	if len(loaded.Network.IPRanges) != 1 || len(loaded.Items) != 2 {
		t.Fatal("IP settings not persisted")
	}
}
func TestNLBTCPForwardingAndListenerChanges(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/unchanged" || r.Header.Get("Authorization") != "app-auth" {
			t.Error("TCP proxy modified request")
		}
		w.Write([]byte("raw-tcp-backend"))
	}))
	defer upstream.Close()
	b := &loadBalancer{ID: "test-nlb", Mode: "nlb", PublicIP: "127.0.0.1", ListenPort: 0, Algorithm: "round_robin", Healthy: map[string]string{"backend": strings.TrimPrefix(upstream.URL, "http://")}, active: map[string]int{}}
	d := s.balancers
	d.Items[b.ID] = b
	d.Lock()
	s.reconcileNLBListeners()
	l, ok := d.listeners[b.ID]
	d.Unlock()
	if !ok {
		t.Fatal("no TCP listener", b.ListenerError)
	}
	defer func() {
		d.Lock()
		for _, l := range d.listeners {
			l.listener.Close()
		}
		d.Unlock()
	}()
	conn, e := net.Dial("tcp", l.listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /unchanged HTTP/1.0\r\nAuthorization: app-auth\r\n\r\n")
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.CloseWrite()
	}
	raw, e := io.ReadAll(conn)
	if e != nil || !strings.Contains(string(raw), "raw-tcp-backend") {
		t.Fatal(string(raw), e)
	}
	d.Lock()
	b.Deleting = true
	s.reconcileNLBListeners()
	_, exists := d.listeners[b.ID]
	d.Unlock()
	if exists {
		t.Fatal("deleting NLB still accepts connections")
	}
}

func TestNLBBindMapping(t *testing.T) {
	b := &loadBalancer{PublicIP: "127.0.0.1", ListenPort: 18080}
	t.Setenv("CLOFIX_NLB_LISTEN_MAP", `{"127.0.0.1:18080":"0.0.0.0:18080"}`)
	if address, e := nlbBindAddress(b); e != nil || address != "0.0.0.0:18080" {
		t.Fatal(address, e)
	}
	b.PublicIP = "203.0.113.7"
	if address, e := nlbBindAddress(b); e != nil || address != "203.0.113.7:18080" {
		t.Fatal("unexpected wildcard fallback", address, e)
	}
	t.Setenv("CLOFIX_NLB_LISTEN_MAP", `invalid`)
	if _, e := nlbBindAddress(b); e == nil {
		t.Fatal("invalid configuration accepted")
	}
}
