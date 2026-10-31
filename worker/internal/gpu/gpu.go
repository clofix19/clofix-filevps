// Package gpu reports NVIDIA devices visible to the worker process. It does not
// assign devices to guests or enforce per-guest GPU/VRAM limits.
package gpu

import (
	"context"
	"encoding/csv"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Device struct {
	Index          int    `json:"index"`
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	MemoryTotal    uint64 `json:"memory_total_bytes"`
	MemoryUsed     uint64 `json:"memory_used_bytes"`
	MemoryFree     uint64 `json:"memory_free_bytes"`
	UtilizationPct uint64 `json:"utilization_percent"`
}

// Detect queries nvidia-smi with a short timeout. Missing tooling or GPUs is a
// normal state for CPU-only workers and is reported as an empty inventory.
func Detect() []Device {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,uuid,name,memory.total,memory.used,memory.free,utilization.gpu", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return []Device{}
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.TrimLeadingSpace = true
	var devices []Device
	for {
		fields, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(fields) != 7 {
			continue
		}
		index, e1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		if e1 != nil || strings.TrimSpace(fields[1]) == "" {
			continue
		}
		device := Device{Index: index, UUID: strings.TrimSpace(fields[1]), Name: strings.TrimSpace(fields[2])}
		device.MemoryTotal = mib(fields[3])
		device.MemoryUsed = mib(fields[4])
		device.MemoryFree = mib(fields[5])
		device.UtilizationPct, _ = strconv.ParseUint(strings.TrimSpace(fields[6]), 10, 64)
		devices = append(devices, device)
	}
	if devices == nil {
		return []Device{}
	}
	return devices
}

func mib(value string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return n * 1024 * 1024
}
