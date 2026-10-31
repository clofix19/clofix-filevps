// Package s3pool runs this worker's SeaweedFS volume server when the controller
// supplied a private master address at enrollment.
package s3pool

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type Volume struct {
	cfg     config.Config
	cmd     *exec.Cmd
	mu      sync.RWMutex
	dataDir string
	online  bool
}

func New(cfg config.Config) *Volume { return &Volume{cfg: cfg} }

func (v *Volume) Start(ctx context.Context) {
	if strings.TrimSpace(v.cfg.S3MasterAddress) == "" {
		return
	}
	if err := ValidateMaster(v.cfg.S3MasterAddress); err != nil {
		log.Printf("S3 pool: %v", err)
		return
	}
	weed, err := exec.LookPath("weed")
	if err != nil {
		log.Printf("S3 pool: master configured but `weed` is missing; install the SeaweedFS worker binary to contribute storage")
		return
	}
	dir := v.cfg.S3DataDir
	if dir == "" {
		dir = filepath.Join(config.StateDir(), "s3-volume")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("S3 pool: create data directory: %v", err)
		return
	}
	advertise := v.cfg.S3AdvertiseAddress
	if advertise == "" {
		advertise = routeAddress(v.cfg.S3MasterAddress)
	}
	if advertise == "" {
		log.Printf("S3 pool: could not determine private advertise IP; set s3_advertise_address in worker.json")
		return
	}
	// A zero max makes SeaweedFS derive a small slot count from disk capacity.
	// Those slots can be consumed by the default collection before S3's
	// per-bucket collection gets a volume. Keep plenty of slots available and
	// let minFreeSpacePercent enforce the actual disk safety limit.
	args := []string{"volume", "-dir=" + dir, "-master=" + v.cfg.S3MasterAddress, "-ip=" + advertise, "-port=8080", "-port.grpc=18080", "-max=1000", "-minFreeSpacePercent=5", "-publicUrl=" + net.JoinHostPort(advertise, "8080")}
	v.cmd = exec.CommandContext(ctx, weed, args...)
	v.cmd.Stdout, v.cmd.Stderr = os.Stdout, os.Stderr
	if err := v.cmd.Start(); err != nil {
		log.Printf("S3 pool: start volume server: %v", err)
		return
	}
	v.mu.Lock()
	v.dataDir, v.online = dir, true
	v.mu.Unlock()
	log.Printf("S3 pool: volume server started at %s:8080, directory %s", advertise, dir)
	go func() {
		if err := v.cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("S3 pool: volume server exited: %v", err)
		}
		v.mu.Lock()
		v.online = false
		v.mu.Unlock()
	}()
}

func (v *Volume) Capacity() scheduler.Capacity {
	v.mu.RLock()
	dir, online := v.dataDir, v.online
	v.mu.RUnlock()
	if !online {
		return scheduler.Capacity{}
	}
	return diskCapacity(dir)
}

func diskCapacity(dir string) scheduler.Capacity {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return scheduler.Capacity{}
	}
	total := uint64(st.Blocks) * uint64(st.Bsize)
	free := uint64(st.Bavail) * uint64(st.Bsize)
	reserve := total / 20 // SeaweedFS volume server is configured to keep 5% free.
	if free > reserve {
		free -= reserve
	} else {
		free = 0
	}
	return scheduler.Capacity{S3Node: true, S3DiskTotal: total, S3DiskFree: free}
}

func routeAddress(master string) string {
	host, _, err := net.SplitHostPort(master)
	if err != nil {
		host = master
	}
	c, err := net.DialTimeout("udp", net.JoinHostPort(host, "9333"), 2*time.Second)
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

func ValidateMaster(address string) error {
	if address == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("invalid SeaweedFS master address %q: expected host:port", address)
	}
	return nil
}
