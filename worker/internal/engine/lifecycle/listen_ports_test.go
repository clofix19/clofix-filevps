package lifecycle

import "testing"

func TestParseProcNetListen(t *testing.T) {
	const table = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 11 1 00000000aaaaaaaa 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22 1 00000000bbbbbbbb 100 0 0 10 0
   2: 00000000:01BB 0A00000A:1234 01 00000000:00000000 00:00000000 00000000     0        0 33 1 00000000cccccccc 100 0 0 10 0
`
	ports := parseProcNetListen(table)
	if len(ports) != 2 || ports[0] != 80 || ports[1] != 8080 {
		t.Fatalf("%v", ports)
	}
	socks := parseProcNetListenSocks(table)
	if len(socks) != 2 || socks[0].Port != 80 || socks[0].Inode != 11 || socks[1].Port != 8080 || socks[1].Inode != 22 {
		t.Fatalf("%v", socks)
	}
}
