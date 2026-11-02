package server

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

// ServeDNS answers A/AAAA/CNAME/TXT for the controller zone on UDP and TCP.
// Bind failures are logged; the admin API stays up.
func (s *Server) ServeDNS(addr string) {
	if addr == "" {
		addr = envOr("CLOFIX_DNS_LISTEN", ":53")
	}
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Printf("DNS listen %s: %v", addr, err)
		return
	}
	defer udp.Close()
	log.Printf("DNS zone %s on %s", s.dnsZoneDomain(), addr)
	go s.serveDNSTCP(addr)
	buf := make([]byte, 4096)
	for {
		n, from, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		out, err := s.answerDNS(buf[:n])
		if err != nil || len(out) == 0 {
			continue
		}
		_, _ = udp.WriteTo(out, from)
	}
}

func (s *Server) serveDNSTCP(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return
	}
	defer ln.Close()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			var hdr [2]byte
			if _, err := io.ReadFull(conn, hdr[:]); err != nil {
				return
			}
			need := int(binary.BigEndian.Uint16(hdr[:]))
			if need < 12 || need > 4096 {
				return
			}
			in := make([]byte, need)
			if _, err := io.ReadFull(conn, in); err != nil {
				return
			}
			out, err := s.answerDNS(in)
			if err != nil || len(out) == 0 || len(out) > 65535 {
				return
			}
			var pre [2]byte
			binary.BigEndian.PutUint16(pre[:], uint16(len(out)))
			_, _ = conn.Write(append(pre[:], out...))
		}(c)
	}
}

func (s *Server) dnsZoneDomain() string {
	s.dns.Lock()
	defer s.dns.Unlock()
	return s.dns.Zone.Domain
}

func (s *Server) dnsHostEntries() []proto.DNSHost {
	_ = s.refreshDNS()
	s.dns.Lock()
	defer s.dns.Unlock()
	byName := map[string][]dnsRecord{}
	for _, rec := range s.dns.Records {
		byName[rec.Name] = append(byName[rec.Name], rec)
	}
	seen := map[string]bool{}
	var out []proto.DNSHost
	for _, rec := range s.dns.Records {
		name, ip := rec.Name, rec.Value
		switch rec.Type {
		case "A":
		case "CNAME":
			ip = ""
			target := strings.ToLower(strings.TrimSuffix(rec.Value, "."))
			for _, t := range byName[target] {
				if t.Type == "A" {
					ip = t.Value
					break
				}
			}
		default:
			continue
		}
		if ip == "" || ip == "127.0.0.1" || ip == "::1" || seen[name+"|"+ip] {
			continue
		}
		seen[name+"|"+ip] = true
		out = append(out, proto.DNSHost{Name: name, IP: ip})
	}
	return out
}

func (s *Server) lookupDNS(qname, qtype string) []dnsRecord {
	_ = s.refreshDNS()
	qname = canonicalCloFixName(qname)
	s.dns.Lock()
	defer s.dns.Unlock()
	var out []dnsRecord
	for _, rec := range s.dns.Records {
		if rec.Name != qname {
			continue
		}
		if rec.Type == qtype || (qtype == "A" && rec.Type == "CNAME") {
			out = append(out, rec)
		}
	}
	if qtype == "A" {
		seen := map[string]bool{}
		var followed []dnsRecord
		queue := append([]dnsRecord{}, out...)
		for len(queue) > 0 && len(followed) < 8 {
			rec := queue[0]
			queue = queue[1:]
			if rec.Type != "CNAME" {
				continue
			}
			target := strings.ToLower(strings.TrimSuffix(rec.Value, "."))
			if target == "" || seen[target] {
				continue
			}
			seen[target] = true
			for _, t := range s.dns.Records {
				if t.Name != target {
					continue
				}
				followed = append(followed, t)
				if t.Type == "CNAME" {
					queue = append(queue, t)
				}
			}
		}
		out = append(out, followed...)
	}
	return out
}

func (s *Server) answerDNS(in []byte) ([]byte, error) {
	qname, qtype, qclass, rest, err := parseDNSQuery(in)
	if err != nil {
		return nil, err
	}
	_ = rest
	if qclass != 1 {
		return dnsReply(in, 4, nil, 0)
	}
	typ := dnsTypeName(qtype)
	if typ == "" {
		return dnsReply(in, 0, nil, 0)
	}
	answers := s.lookupDNS(qname, typ)
	if len(answers) == 0 {
		return dnsReply(in, 3, nil, 0)
	}
	var rr []byte
	ok := 0
	for _, rec := range answers {
		if rec.Type == "A" && strings.HasPrefix(rec.Value, "10.231.") {
			rec.Value = "127.0.0.1"
		}
		enc, err := encodeDNSRecord(rec)
		if err != nil {
			continue
		}
		rr = append(rr, enc...)
		ok++
	}
	if ok == 0 {
		return dnsReply(in, 3, nil, 0)
	}
	return dnsReply(in, 0, rr, ok)
}

func parseDNSQuery(in []byte) (name string, qtype, qclass uint16, question []byte, err error) {
	if len(in) < 12 {
		return "", 0, 0, nil, fmt.Errorf("short query")
	}
	qd := binary.BigEndian.Uint16(in[4:6])
	if qd == 0 {
		return "", 0, 0, nil, fmt.Errorf("no question")
	}
	off := 12
	name, off, err = decodeDNSName(in, off)
	if err != nil {
		return "", 0, 0, nil, err
	}
	if off+4 > len(in) {
		return "", 0, 0, nil, fmt.Errorf("short question")
	}
	qtype = binary.BigEndian.Uint16(in[off : off+2])
	qclass = binary.BigEndian.Uint16(in[off+2 : off+4])
	return name, qtype, qclass, in[12 : off+4], nil
}

func decodeDNSName(msg []byte, off int) (string, int, error) {
	var labels []string
	start := off
	jumps := 0
	for {
		if off >= len(msg) {
			return "", start, fmt.Errorf("name overflow")
		}
		n := int(msg[off])
		if n == 0 {
			off++
			break
		}
		if n&0xC0 == 0xC0 {
			if off+1 >= len(msg) {
				return "", start, fmt.Errorf("bad pointer")
			}
			ptr := int(binary.BigEndian.Uint16(msg[off:off+2]) & 0x3FFF)
			off += 2
			if jumps == 0 {
				start = off
			}
			jumps++
			if jumps > 10 {
				return "", start, fmt.Errorf("pointer loop")
			}
			part, _, err := decodeDNSName(msg, ptr)
			if err != nil {
				return "", start, err
			}
			if part != "" {
				labels = append(labels, part)
			}
			if jumps == 1 {
				return strings.Join(labels, "."), start, nil
			}
			return strings.Join(labels, "."), off, nil
		}
		off++
		if n == 0 || off+n > len(msg) {
			return "", start, fmt.Errorf("bad label")
		}
		labels = append(labels, strings.ToLower(string(msg[off:off+n])))
		off += n
	}
	return strings.Join(labels, "."), off, nil
}

func encodeDNSName(name string) []byte {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	var out []byte
	if name != "" {
		for _, label := range strings.Split(name, ".") {
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
	}
	return append(out, 0)
}

func encodeDNSRecord(rec dnsRecord) ([]byte, error) {
	typ := dnsTypeCode(rec.Type)
	if typ == 0 {
		return nil, fmt.Errorf("unsupported type")
	}
	name := encodeDNSName(rec.Name)
	ttl := rec.TTL
	if ttl < 1 {
		ttl = 60
	}
	var rdata []byte
	switch rec.Type {
	case "A":
		ip := net.ParseIP(rec.Value).To4()
		if ip == nil {
			return nil, fmt.Errorf("bad A")
		}
		rdata = ip
	case "AAAA":
		ip := net.ParseIP(rec.Value)
		if ip == nil || ip.To4() != nil {
			return nil, fmt.Errorf("bad AAAA")
		}
		rdata = ip.To16()
	case "CNAME":
		rdata = encodeDNSName(strings.TrimSuffix(rec.Value, "."))
	case "TXT":
		v := rec.Value
		if len(v) > 255 {
			v = v[:255]
		}
		rdata = append([]byte{byte(len(v))}, v...)
	default:
		return nil, fmt.Errorf("unsupported type")
	}
	out := append([]byte{}, name...)
	var hdr [10]byte
	binary.BigEndian.PutUint16(hdr[0:2], typ)
	binary.BigEndian.PutUint16(hdr[2:4], 1)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(ttl))
	binary.BigEndian.PutUint16(hdr[8:10], uint16(len(rdata)))
	out = append(out, hdr[:]...)
	return append(out, rdata...), nil
}

func dnsReply(query []byte, rcode int, answers []byte, ancount int) ([]byte, error) {
	if len(query) < 12 {
		return nil, fmt.Errorf("short query")
	}
	_, _, _, question, err := parseDNSQuery(query)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 12, 12+len(question)+len(answers))
	copy(out[0:2], query[0:2])
	flags := binary.BigEndian.Uint16(query[2:4])
	rd := flags & 0x0100
	binary.BigEndian.PutUint16(out[2:4], 0x8400|rd|uint16(rcode&0xF))
	binary.BigEndian.PutUint16(out[4:6], 1)
	binary.BigEndian.PutUint16(out[6:8], uint16(ancount))
	out = append(out, question...)
	out = append(out, answers...)
	return out, nil
}

func dnsTypeName(code uint16) string {
	switch code {
	case 1:
		return "A"
	case 5:
		return "CNAME"
	case 16:
		return "TXT"
	case 28:
		return "AAAA"
	default:
		return ""
	}
}

func dnsTypeCode(name string) uint16 {
	switch name {
	case "A":
		return 1
	case "CNAME":
		return 5
	case "TXT":
		return 16
	case "AAAA":
		return 28
	default:
		return 0
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
