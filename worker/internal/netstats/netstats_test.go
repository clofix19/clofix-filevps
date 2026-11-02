package netstats

import "testing"

const sampleDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1200      10    0    0    0     0          0         0     1200      10    0    0    0     0       0          0
  eth0: 4000      20    0    0    0     0          0         0     8000      30    0    0    0     0       0          0
  cfh12ab: 100      1    0    0    0     0          0         0      50       1    0    0    0     0       0          0
  cfbr0: 300      2    0    0    0     0          0         0     300       2    0    0    0     0       0          0
 vethabc: 10       1    0    0    0     0          0         0      10       1    0    0    0     0       0          0
`

func TestParseProcNetDev(t *testing.T) {
	devs := ParseProcNetDev(sampleDev)
	if devs["eth0"].RxBytes != 4000 || devs["eth0"].TxBytes != 8000 {
		t.Fatalf("eth0: %+v", devs["eth0"])
	}
	if !IsUplink("eth0") || !IsUplink("ens3") || IsUplink("lo") || IsUplink("cfh12ab") || IsUplink("cfbr0") || IsUplink("vethabc") {
		t.Fatalf("uplink filter failed")
	}
	sum := SumUplink(devs)
	if sum.RxBytes != 4000 || sum.TxBytes != 8000 {
		t.Fatalf("uplink sum: %+v", sum)
	}
}

func TestRate(t *testing.T) {
	rx, tx := Rate(Counters{RxBytes: 1000, TxBytes: 2000}, Counters{RxBytes: 3000, TxBytes: 4000}, 2)
	if rx != 1000 || tx != 1000 {
		t.Fatalf("rx=%d tx=%d", rx, tx)
	}
	if r, x := Rate(Counters{RxBytes: 5000}, Counters{RxBytes: 1000}, 1); r != 0 || x != 0 {
		t.Fatalf("reset should be zero")
	}
}
