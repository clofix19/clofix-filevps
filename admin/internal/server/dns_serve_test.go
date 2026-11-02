package server

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestAnswerDNSARecord(t *testing.T) {
	s, _ := newSrv(t)
	s.dns.Lock()
	s.dns.Records["web"] = dnsRecord{ID: "web", Name: "web.clofix.internal", Type: "A", Value: "127.0.0.1", TTL: 60, Source: "manual", Created: time.Now(), Updated: time.Now()}
	s.dns.Unlock()
	q := encodeDNSQuery("web.clofix.internal", 1)
	out, err := s.answerDNS(q)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(out[6:8]) != 1 {
		t.Fatalf("ancount=%d body=%x", binary.BigEndian.Uint16(out[6:8]), out)
	}
	if got := string(out[len(out)-4:]); got != string([]byte{127, 0, 0, 1}) {
		t.Fatalf("A rdata %v", []byte(got))
	}
}

func encodeDNSQuery(name string, qtype uint16) []byte {
	q := make([]byte, 12)
	q[0], q[1] = 0x12, 0x34
	q[2], q[3] = 0x01, 0x00
	q[4], q[5] = 0, 1
	q = append(q, encodeDNSName(name)...)
	var tail [4]byte
	binary.BigEndian.PutUint16(tail[0:2], qtype)
	binary.BigEndian.PutUint16(tail[2:4], 1)
	return append(q, tail[:]...)
}

func TestDNSHostEntriesFollowCNAME(t *testing.T) {
	s, m := newSrv(t)
	_ = m
	if _, ok := s.Store.(*store.MemoryStore); !ok {
		t.Fatal("expected memory store")
	}
	s.dns.Lock()
	s.dns.Records["a"] = dnsRecord{ID: "a", Name: "vps-1.vps.clofix.internal", Type: "A", Value: "10.231.0.2", TTL: 60}
	s.dns.Records["c"] = dnsRecord{ID: "c", Name: "web.clofix.internal", Type: "CNAME", Value: "vps-1.vps.clofix.internal.", TTL: 60}
	s.dns.Unlock()
	hosts := s.dnsHostEntries()
	found := false
	for _, h := range hosts {
		if h.Name == "web.clofix.internal" && h.IP == "10.231.0.2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CNAME not flattened: %+v", hosts)
	}
}

func TestNamedHTTPPortAndInternelAlias(t *testing.T) {
	s, _ := newSrv(t)
	s.dns.Lock()
	s.dns.Records["web"] = dnsRecord{ID: "web", Name: "web.clofix.internal", Type: "CNAME", Value: "vps-1.vps.clofix.internal.", Port: proto.HostHTTPPort(22001), TTL: 60, Source: "manual", Resource: "vps", ResourceID: "vps-1"}
	s.dns.Records["vps"] = dnsRecord{ID: "vps", Name: "vps-1.vps.clofix.internal", Type: "A", Value: "10.231.0.2", Port: 22000, TTL: 60, Source: "auto", Resource: "vps", ResourceID: "vps-1"}
	s.dns.Unlock()
	want := proto.HostHTTPPort(22001)
	port, ok := s.namedHTTPPort("web.clofix.internal")
	if !ok || port != want {
		t.Fatalf("named port=%d ok=%v want=%d", port, ok, want)
	}
	port, ok = s.namedHTTPPort("web.clofix.internel")
	if !ok || port != want {
		t.Fatalf("internel alias port=%d ok=%v want=%d", port, ok, want)
	}
	q := encodeDNSQuery("web.clofix.internel", 1)
	out, err := s.answerDNS(q)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(out[6:8]) == 0 {
		t.Fatal("internel DNS NXDOMAIN")
	}
}

func TestNamedHTTPPortCKSLab(t *testing.T) {
	s, _ := newSrv(t)
	s.dns.Lock()
	s.dns.Records["ing"] = dnsRecord{ID: "ing", Name: "nginx.ing.test-cluster.cks.clofix.internal", Type: "CNAME", Value: "cks-1.cks.clofix.internal.", Port: 8080, Resource: "cks", ResourceID: "cks-1"}
	s.dns.Records["custom"] = dnsRecord{ID: "custom", Name: "shahjahan.clofix.internal", Type: "CNAME", Value: "nginx.ing.test-cluster.cks.clofix.internal.", Port: 80, Resource: "cks", ResourceID: "cks-1/ingress/default/nginx"}
	s.dns.Records["node"] = dnsRecord{ID: "node", Name: "nodeport.clofix.internal", Type: "CNAME", Value: "cks-1.cks.clofix.internal.", Port: 30080, Resource: "cks", ResourceID: "cks-1"}
	s.dns.Unlock()
	port, ok := s.namedHTTPPort("shahjahan.clofix.internal")
	if !ok || port != 19080 {
		t.Fatalf("cks custom http port=%d ok=%v", port, ok)
	}
	port, ok = s.namedHTTPPort("nginx.ing.test-cluster.cks.clofix.internal")
	if !ok || port != 19080 {
		t.Fatalf("cks ingress lab url port=%d ok=%v", port, ok)
	}
	port, ok = s.namedHTTPPort("nodeport.clofix.internal")
	if !ok || port != 30080 {
		t.Fatalf("cks nodeport=%d ok=%v", port, ok)
	}
	t.Setenv("CLOFIX_KUBERNETES_LAB_HTTP_PORT", "19180")
	port, ok = s.namedHTTPPort("shahjahan.clofix.internal")
	if !ok || port != 19180 {
		t.Fatalf("cks lab env port=%d ok=%v", port, ok)
	}
}
