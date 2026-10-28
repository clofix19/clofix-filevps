package capacity

import "testing"

func TestParseMeminfo(t *testing.T) {
	m := parseMeminfo("MemTotal:       4000 kB\nMemAvailable:   1000 kB\nHugePages_Total: 0\n")
	if m["MemTotal"] != 4000*1024 || m["MemAvailable"] != 1000*1024 {
		t.Fatalf("%v", m)
	}
}

func TestParsePSI(t *testing.T) {
	s := "some avg10=12.50 avg60=1.00 avg300=0.00 total=1\nfull avg10=3.00 avg60=0 avg300=0 total=1\n"
	if v := parsePSI(s); v != 12.5 {
		t.Fatalf("got %v", v)
	}
}

func TestParseCPU(t *testing.T) {
	busy, total := parseCPU("cpu  100 0 50 800 50 0 0 0 0 0")
	if total != 1000 || busy != 150 {
		t.Fatalf("busy=%d total=%d", busy, total)
	}
}
