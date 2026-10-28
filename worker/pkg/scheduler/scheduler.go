// Package scheduler implements automatic VPS placement (spec section 6).
// It is pure logic: no I/O, so it is shared by the worker binary and the controller.
package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Capacity is a worker's dynamic capacity snapshot. Bytes unless noted.
type Capacity struct {
	RAMTotal      uint64  `json:"ram_total"`
	HostReserve   uint64  `json:"host_reserve"`
	RAMCommitted  uint64  `json:"ram_committed"` // sum of instance memory.max limits
	MemAvailable  uint64  `json:"mem_available"` // kernel MemAvailable
	SwapTotal     uint64  `json:"swap_total"`
	SwapFree      uint64  `json:"swap_free"`
	SwapCommitted uint64  `json:"swap_committed"`
	CPUCores      float64 `json:"cpu_cores"`
	CPUAllocated  float64 `json:"cpu_allocated"`
	CPUUtil       float64 `json:"cpu_util"` // 0..1
	DiskTotal     uint64  `json:"disk_total"`
	DiskFree      uint64  `json:"disk_free"`
	DiskCommitted uint64  `json:"disk_committed"`
	// ZRAMLogical is informational only. It is NEVER counted as physical RAM.
	ZRAMLogical    uint64  `json:"zram_logical"`
	ZRAMUsed       uint64  `json:"zram_used"`
	ZRAMCompressed uint64  `json:"zram_compressed"`
	ZRAMActive     bool    `json:"zram_active"`
	PSIMemSome10   float64 `json:"psi_mem_some10"` // percent 0..100
	PSICPUSome10   float64 `json:"psi_cpu_some10"`
	PSIIOSome10    float64 `json:"psi_io_some10"`
}

type Candidate struct {
	ID            string
	Active        bool
	Draining      bool
	LastHeartbeat time.Time
	Capacity      Capacity
}

type Request struct {
	RAM  uint64
	Swap uint64
	Disk uint64
	CPU  float64
}

type Strategy string

const (
	LeastLoaded Strategy = "least-loaded" // spread: highest score wins
	BinPacking  Strategy = "bin-packing"  // dense: lowest eligible score wins
)

type Weights struct{ W1, W2, W3, W4 float64 }

type Config struct {
	Strategy         Strategy
	Weights          Weights
	MaxRAMCommit     float64 // committed/usable allowed (1.0 = no RAM overcommit)
	MaxCPUOvercommit float64 // allocated/cores allowed
	MaxDiskCommit    float64 // committed/total allowed
	MaxPSIMem        float64 // workers above this memory PSI are excluded
	MaxHeartbeatAge  time.Duration
	MinScore         float64 // queue instead of placing below this score
}

func DefaultConfig() Config {
	return Config{
		Strategy:         LeastLoaded,
		Weights:          Weights{W1: 0.4, W2: 0.25, W3: 0.2, W4: 0.15},
		MaxRAMCommit:     1.0,
		MaxCPUOvercommit: 1.0,
		MaxDiskCommit:    1.0,
		MaxPSIMem:        20,
		MaxHeartbeatAge:  60 * time.Second,
		MinScore:         0,
	}
}

type Decision struct {
	WorkerID string
	Score    float64
	Explain  []string // placement insights: why each worker was chosen or rejected
}

var ErrNoCapacity = errors.New("no worker has safe capacity for this request")

func usableRAM(c Capacity) uint64 {
	if c.HostReserve >= c.RAMTotal {
		return 0
	}
	return c.RAMTotal - c.HostReserve
}

// Score implements the spec equation:
// w1*(1-RAMcommitted/(RAMtotal-Rhost)) + w2*(1-CPUutil) + w3*(1-DiskUsed/DiskTotal) - w4*PSImem
func Score(c Capacity, w Weights) float64 {
	ramFrac := 1.0
	if u := usableRAM(c); u > 0 {
		ramFrac = float64(c.RAMCommitted) / float64(u)
	}
	diskFrac := 1.0
	if c.DiskTotal > 0 {
		diskFrac = float64(c.DiskTotal-min64(c.DiskFree, c.DiskTotal)) / float64(c.DiskTotal)
	}
	return w.W1*(1-ramFrac) + w.W2*(1-c.CPUUtil) + w.W3*(1-diskFrac) - w.W4*(c.PSIMemSome10/100)
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// reject returns a reason when the worker fails a hard constraint, else "".
func reject(c Candidate, r Request, cfg Config, now time.Time) string {
	cp := c.Capacity
	switch {
	case !c.Active:
		return "not active"
	case c.Draining:
		return "draining"
	case now.Sub(c.LastHeartbeat) > cfg.MaxHeartbeatAge:
		return "stale heartbeat"
	case cp.PSIMemSome10 > cfg.MaxPSIMem:
		return fmt.Sprintf("memory pressure %.1f%% > %.1f%%", cp.PSIMemSome10, cfg.MaxPSIMem)
	case float64(cp.RAMCommitted+r.RAM) > float64(usableRAM(cp))*cfg.MaxRAMCommit:
		return "RAM commit limit"
	case r.RAM > cp.MemAvailable:
		return "insufficient MemAvailable"
	case cp.SwapCommitted > cp.SwapTotal || r.Swap > cp.SwapTotal-cp.SwapCommitted:
		return "insufficient swap capacity"
	case r.Swap > cp.SwapFree:
		return "insufficient free host swap"
	case r.Disk > cp.DiskFree:
		return "insufficient free disk"
	case float64(cp.DiskCommitted+r.Disk) > float64(cp.DiskTotal)*cfg.MaxDiskCommit:
		return "disk commit limit"
	case cp.CPUAllocated+r.CPU > cp.CPUCores*cfg.MaxCPUOvercommit:
		return "CPU allocation limit"
	}
	return ""
}

// Place picks a worker or returns ErrNoCapacity (caller should queue the request).
func Place(cands []Candidate, r Request, cfg Config, now time.Time) (Decision, error) {
	type scored struct {
		id    string
		score float64
	}
	var ok []scored
	var explain []string
	for _, c := range cands {
		if why := reject(c, r, cfg, now); why != "" {
			explain = append(explain, fmt.Sprintf("%s: rejected (%s)", c.ID, why))
			continue
		}
		s := Score(c.Capacity, cfg.Weights)
		ok = append(ok, scored{c.ID, s})
		explain = append(explain, fmt.Sprintf("%s: eligible, score %.3f", c.ID, s))
	}
	if len(ok) == 0 {
		return Decision{Explain: explain}, ErrNoCapacity
	}
	sort.Slice(ok, func(i, j int) bool {
		if ok[i].score != ok[j].score {
			if cfg.Strategy == BinPacking {
				return ok[i].score < ok[j].score
			}
			return ok[i].score > ok[j].score
		}
		return ok[i].id < ok[j].id
	})
	best := ok[0]
	if best.score < cfg.MinScore {
		return Decision{Explain: explain}, ErrNoCapacity
	}
	explain = append(explain, fmt.Sprintf("selected %s using %s", best.id, cfg.Strategy))
	return Decision{WorkerID: best.id, Score: best.score, Explain: explain}, nil
}
