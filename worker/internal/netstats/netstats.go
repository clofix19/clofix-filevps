// Package netstats reads Linux interface counters for FileVPS guests and worker hosts.
package netstats

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Counters are cumulative interface byte counts from the kernel.
type Counters struct {
	RxBytes uint64
	TxBytes uint64
}

var skipPrefixes = []string{
	"lo", "sit", "dummy", "docker", "br-", "veth", "cfh", "cfp", "cfbr",
	"virbr", "cni", "flannel", "cali", "tun", "tap", "wg", "kube",
	"gre", "ip6tnl", "ip6gre", "erspan",
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func readUint(path string) (uint64, bool) {
	s := strings.TrimSpace(readFile(path))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil
}

// ParseProcNetDev parses /proc/net/dev text into per-interface counters.
func ParseProcNetDev(s string) map[string]Counters {
	out := map[string]Counters{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.Contains(line, ":") || strings.HasPrefix(line, "Inter-") || strings.HasPrefix(line, "face") {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[8], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		out[strings.TrimSpace(name)] = Counters{RxBytes: rx, TxBytes: tx}
	}
	return out
}

// IsUplink reports whether a host interface is treated as worker uplink traffic.
// Virtual FileVPS, bridge, and container links are excluded so guest traffic is
// not double-counted with the host NIC that actually leaves the machine.
func IsUplink(name string) bool {
	if name == "" {
		return false
	}
	for _, prefix := range skipPrefixes {
		if name == prefix || strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// SumUplink totals counters for host uplink interfaces.
func SumUplink(devs map[string]Counters) Counters {
	var sum Counters
	for name, c := range devs {
		if !IsUplink(name) {
			continue
		}
		sum.RxBytes += c.RxBytes
		sum.TxBytes += c.TxBytes
	}
	return sum
}

// HostUplink reads the worker host's uplink IN/OUT byte counters.
func HostUplink() Counters {
	return SumUplink(ParseProcNetDev(readFile("/proc/net/dev")))
}

// SysInterface reads /sys counters for a single interface.
func SysInterface(name string) (Counters, bool) {
	if name == "" {
		return Counters{}, false
	}
	rx, okRx := readUint("/sys/class/net/" + name + "/statistics/rx_bytes")
	tx, okTx := readUint("/sys/class/net/" + name + "/statistics/tx_bytes")
	if !okRx || !okTx {
		return Counters{}, false
	}
	return Counters{RxBytes: rx, TxBytes: tx}, true
}

// Guest returns guest-direction counters: IN is traffic delivered to the VPS,
// OUT is traffic sent by the VPS. Prefer the guest namespace eth0 view; fall
// back to the host veth with RX/TX swapped.
func Guest(pid int, hostIf string) (Counters, bool) {
	if pid > 0 {
		devs := ParseProcNetDev(readFile(fmt.Sprintf("/proc/%d/net/dev", pid)))
		if c, ok := devs["eth0"]; ok {
			return c, true
		}
	}
	if c, ok := SysInterface(hostIf); ok {
		return Counters{RxBytes: c.TxBytes, TxBytes: c.RxBytes}, true
	}
	return Counters{}, false
}

// Rate returns bytes/sec between two cumulative samples. A counter reset yields 0.
func Rate(prev, now Counters, elapsedSec float64) (rxBps, txBps uint64) {
	if elapsedSec <= 0 || now.RxBytes < prev.RxBytes || now.TxBytes < prev.TxBytes {
		return 0, 0
	}
	return uint64(float64(now.RxBytes-prev.RxBytes) / elapsedSec), uint64(float64(now.TxBytes-prev.TxBytes) / elapsedSec)
}
