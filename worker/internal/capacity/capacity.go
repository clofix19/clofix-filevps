// Package capacity collects the worker's dynamic capacity matrix from /proc and /sys.
package capacity

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type Collector struct {
	StateDir    string
	HostReserve uint64

	mu              sync.Mutex
	prevBusy, prevT uint64
}

func parseMeminfo(s string) map[string]uint64 {
	m := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		if len(f) >= 3 && f[2] == "kB" {
			v *= 1024
		}
		m[strings.TrimSuffix(f[0], ":")] = v
	}
	return m
}

// parsePSI returns the "some avg10" value from a /proc/pressure/* file.
func parsePSI(s string) float64 {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, kv := range strings.Fields(line) {
			if strings.HasPrefix(kv, "avg10=") {
				v, _ := strconv.ParseFloat(strings.TrimPrefix(kv, "avg10="), 64)
				return v
			}
		}
	}
	return 0
}

// parseCPU returns busy and total jiffies from the first line of /proc/stat.
func parseCPU(line string) (busy, total uint64) {
	f := strings.Fields(line)
	if len(f) < 6 || f[0] != "cpu" {
		return 0, 0
	}
	for i, s := range f[1:] {
		v, _ := strconv.ParseUint(s, 10, 64)
		total += v
		if i == 3 || i == 4 { // idle, iowait
			continue
		}
		busy += v
	}
	return busy, total
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func (c *Collector) cpuUtil() float64 {
	first := strings.SplitN(read("/proc/stat"), "\n", 2)[0]
	busy, total := parseCPU(first)
	c.mu.Lock()
	defer c.mu.Unlock()
	db, dt := busy-c.prevBusy, total-c.prevT
	c.prevBusy, c.prevT = busy, total
	if dt == 0 || c.prevT == total && dt == total {
		return 0
	}
	return float64(db) / float64(dt)
}

// Collect builds a capacity snapshot. The committed values come from the instance manager.
func (c *Collector) Collect(committedRAM uint64, allocCPU float64, committedDisk uint64) scheduler.Capacity {
	mi := parseMeminfo(read("/proc/meminfo"))
	cp := scheduler.Capacity{
		RAMTotal:      mi["MemTotal"],
		HostReserve:   c.HostReserve,
		RAMCommitted:  committedRAM,
		MemAvailable:  mi["MemAvailable"],
		SwapTotal:     mi["SwapTotal"],
		SwapFree:      mi["SwapFree"],
		CPUCores:      float64(runtime.NumCPU()),
		CPUAllocated:  allocCPU,
		CPUUtil:       c.cpuUtil(),
		DiskCommitted: committedDisk,
		PSIMemSome10:  parsePSI(read("/proc/pressure/memory")),
		PSICPUSome10:  parsePSI(read("/proc/pressure/cpu")),
		PSIIOSome10:   parsePSI(read("/proc/pressure/io")),
	}
	if b, err := os.ReadFile("/sys/block/zram0/disksize"); err == nil {
		cp.ZRAMLogical, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	if b, err := os.ReadFile("/sys/block/zram0/mm_stat"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) >= 3 {
			cp.ZRAMCompressed, _ = strconv.ParseUint(fields[1], 10, 64)
			cp.ZRAMUsed, _ = strconv.ParseUint(fields[2], 10, 64)
		}
	}
	if swaps := read("/proc/swaps"); strings.Contains(swaps, "/dev/zram0") {
		cp.ZRAMActive = true
	}
	var st syscall.Statfs_t
	dir := c.StateDir
	if _, err := os.Stat(dir); err != nil {
		dir = "/"
	}
	if syscall.Statfs(dir, &st) == nil {
		cp.DiskTotal = uint64(st.Blocks) * uint64(st.Bsize)
		cp.DiskFree = uint64(st.Bavail) * uint64(st.Bsize)
	}
	return cp
}
