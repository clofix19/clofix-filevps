// Package agent is the worker's control loop: mTLS heartbeat, capacity reporting, operation execution.
package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/internal/capacity"
	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/engine/lifecycle"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type Agent struct {
	Cfg    config.Config
	Engine *lifecycle.Engine
	Col    *capacity.Collector

	hc      *http.Client
	mu      sync.Mutex
	results []proto.OpResult
	seen    map[string]bool
	ops     chan proto.Operation
}

func New(cfg config.Config, eng *lifecycle.Engine) (*Agent, error) {
	sec := config.SecurityDir()
	pair, err := tls.LoadX509KeyPair(filepath.Join(sec, "worker.crt"), filepath.Join(sec, "worker.key"))
	if err != nil {
		return nil, fmt.Errorf("worker is not enrolled (run: clofix-worker enroll): %w", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(sec, "ca.pem"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return &Agent{
		Cfg: cfg, Engine: eng,
		Col: &capacity.Collector{StateDir: config.StateDir(), HostReserve: uint64(cfg.HostReserveMB) << 20},
		hc: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{pair}, RootCAs: pool, MinVersion: tls.VersionTLS13}}},
		seen: map[string]bool{}, ops: make(chan proto.Operation, 64),
	}, nil
}

func (a *Agent) capacity() scheduler.Capacity {
	ram, swap, cpu, disk := a.Engine.Committed()
	cp := a.Col.Collect(ram, cpu, disk)
	cp.SwapCommitted = swap
	return cp
}

// Run heartbeats until ctx is cancelled. Operations execute serially in a separate goroutine so a slow
// image pull never blocks heartbeats. The controller being unreachable never stops running instances.
func (a *Agent) Run(ctx context.Context) {
	go a.executor(ctx)
	t := time.NewTicker(time.Duration(a.Cfg.HeartbeatSeconds) * time.Second)
	defer t.Stop()
	for {
		if err := a.beat(); err != nil {
			log.Printf("heartbeat: %v (instances keep running)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) beat() error {
	a.mu.Lock()
	res := a.results
	a.results = nil
	a.mu.Unlock()
	body, _ := json.Marshal(proto.Heartbeat{Version: Version, Capacity: a.capacity(), Instances: a.Engine.Statuses(), Volumes: a.Engine.VolumeStatuses(), Results: res})
	resp, err := a.hc.Post(a.Cfg.ControllerURL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		a.requeue(res)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		a.requeue(res)
		return fmt.Errorf("controller returned %d", resp.StatusCode)
	}
	var hr proto.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return err
	}
	for _, op := range hr.Operations {
		select {
		case a.ops <- op:
		default:
			a.addResult(proto.OpResult{OpID: op.ID, Error: "worker operation queue full"})
		}
	}
	return nil
}

func (a *Agent) requeue(r []proto.OpResult) {
	a.mu.Lock()
	a.results = append(r, a.results...)
	a.mu.Unlock()
}

func (a *Agent) addResult(r proto.OpResult) {
	a.mu.Lock()
	a.results = append(a.results, r)
	a.mu.Unlock()
}

func (a *Agent) executor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case op := <-a.ops:
			a.mu.Lock()
			dup := a.seen[op.ID]
			a.seen[op.ID] = true // replay protection / idempotency by operation id
			a.mu.Unlock()
			if dup {
				a.addResult(proto.OpResult{OpID: op.ID, OK: true})
				continue
			}
			err := a.admit(op)
			var volumeFile *proto.VolumeFileResult
			if err == nil && op.VolumeFile != nil {
				result, fileErr := a.Engine.ManageVolumeFile(*op.VolumeFile)
				volumeFile, err = &result, fileErr
			} else if err == nil {
				err = a.Engine.Apply(op)
			}
			r := proto.OpResult{OpID: op.ID, OK: err == nil}
			r.VolumeFile = volumeFile
			if err != nil {
				r.Error = err.Error()
				log.Printf("op %s %s: %v", op.ID, op.Action, err)
			}
			a.addResult(r)
		}
	}
}

// admit re-checks hard capacity constraints locally before creating an instance, using the same
// scheduler logic as the controller, so a stale placement decision can never overcommit this worker.
func (a *Agent) admit(op proto.Operation) error {
	if op.Action != proto.ActionCreate || op.Spec == nil {
		return nil
	}
	cp := a.capacity()
	// the controller already reserved this request in its view; our own numbers do not yet include it
	_, err := scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: cp}},
		scheduler.Request{RAM: uint64(op.Spec.RAMMB) << 20, Swap: uint64(proto.SwapLimitMB(*op.Spec)) << 20, Disk: uint64(op.Spec.DiskMB) << 20, CPU: op.Spec.CPU},
		scheduler.DefaultConfig(), time.Now())
	if errors.Is(err, scheduler.ErrNoCapacity) {
		return errors.New("worker admission check failed: insufficient safe capacity")
	}
	return err
}
