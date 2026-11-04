package docker

import "testing"

func TestParseWAFStatsAudit(t *testing.T) {
	raw := `172.21.0.1 - - [04/Nov/2026:16:38:08 +0000] "HEAD / HTTP/1.1" 200 0 "-" "curl/8.7.1" "-"
{"transaction":{"client_ip":"1.2.3.4","time_stamp":"Wed Nov  4 16:38:08 2026","unique_id":"a1","is_interrupted":false,"request":{"method":"HEAD","uri":"/"},"response":{"http_code":200},"messages":[{"message":"Host header","details":{"ruleId":"920350","tags":["attack-protocol"]}}]}}
{"transaction":{"client_ip":"1.2.3.4","time_stamp":"Wed Nov  4 16:38:09 2026","unique_id":"a2","is_interrupted":true,"request":{"method":"GET","uri":"/.env"},"response":{"http_code":403},"messages":[{"message":"Restricted File Access Attempt","details":{"ruleId":"930130","tags":["attack-lfi","OWASP_CRS"]}},{"message":"Inbound Anomaly","details":{"ruleId":"949110","tags":["anomaly-evaluation"]}}]}}
`
	stats := parseWAFStats(raw, "clofix-waf-demo")
	if stats.Hits != 2 {
		t.Fatalf("hits=%d want 2", stats.Hits)
	}
	if stats.Blocks != 1 {
		t.Fatalf("blocks=%d want 1", stats.Blocks)
	}
	if stats.Attacks != 1 {
		t.Fatalf("attacks=%d want 1 (blocked lfi only)", stats.Attacks)
	}
	if stats.UniqueIPs != 1 {
		t.Fatalf("unique_ips=%d want 1", stats.UniqueIPs)
	}
	if len(stats.TopAttacks) == 0 || stats.TopAttacks[0].Key != "attack-lfi" {
		t.Fatalf("top attacks unexpected: %+v", stats.TopAttacks)
	}
	if len(stats.Recent) < 1 || stats.Recent[0].Attack != "attack-lfi" {
		t.Fatalf("recent[0] attack=%q want attack-lfi", stats.Recent[0].Attack)
	}
}
