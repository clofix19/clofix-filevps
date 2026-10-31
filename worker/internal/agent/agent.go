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
	database "github.com/clofix/filevps/worker/internal/database"
	"github.com/clofix/filevps/worker/internal/execution"
	"github.com/clofix/filevps/worker/internal/managed"
	db "github.com/clofix/filevps/worker/pkg/database"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/internal/capacity"
	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/engine/lifecycle"
	"github.com/clofix/filevps/worker/internal/s3pool"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type Agent struct {
	Database         *database.Manager
	databaseSlot     chan struct{}
	databaseStatuses []db.Status
	databaseEngines  map[string]string
	databaseProjects []string
	Managed          *managed.Manager
	Cfg              config.Config
	Engine           *lifecycle.Engine
	Col              *capacity.Collector
	S3Pool           *s3pool.Volume

	hc                  *http.Client
	mu                  sync.Mutex
	executionCancel     context.CancelFunc
	executionOpID       string
	cancelledExecutions map[string]bool
	executionProgress   map[string]proto.ExecutionProgress
	runningExecution    *proto.Execution
	results             []proto.OpResult
	seen                map[string]bool
	ops                 chan proto.Operation
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
	manager := managed.New(config.StateDir())
	if err := manager.Load(); err != nil {
		return nil, fmt.Errorf("managed apps: %w", err)
	}
	databases, err := database.New(config.StateDir())
	if err != nil {
		return nil, fmt.Errorf("databases: %w", err)
	}
	engines, projects := databases.Inventory()
	agent := &Agent{Database: databases, databaseSlot: make(chan struct{}, 1), databaseEngines: engines, databaseProjects: projects, Managed: manager,
		Cfg: cfg, Engine: eng,
		S3Pool: s3pool.New(cfg),
		Col:    &capacity.Collector{StateDir: config.StateDir(), HostReserve: uint64(cfg.HostReserveMB) << 20},
		hc: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{pair}, RootCAs: pool, MinVersion: tls.VersionTLS13}}},
		seen: map[string]bool{}, ops: make(chan proto.Operation, 64),
	}
	databases.Transfer = agent.databaseTransfer
	return agent, nil
}

func (a *Agent) capacity() scheduler.Capacity {
	// A local database daemon may become ready after the worker process starts
	// (notably in the development Compose stack). Retry discovery until at least
	// one trusted engine is available instead of caching an empty startup probe.
	a.mu.Lock()
	databaseDiscoveryPending := len(a.databaseEngines) == 0
	a.mu.Unlock()
	if databaseDiscoveryPending && a.Database != nil {
		engines, projects := a.Database.Inventory()
		if len(engines) > 0 {
			a.mu.Lock()
			a.databaseEngines = engines
			a.databaseProjects = projects
			a.mu.Unlock()
		}
	}
	ram, swap, cpu, disk := a.Engine.Committed()
	managedRAM, managedCPU, managedDisk := a.Managed.Reserved()
	if a.Database != nil {
		databaseRAM, databaseCPU, databaseDisk := a.Database.Reserved()
		managedRAM += databaseRAM
		managedCPU += databaseCPU
		managedDisk += databaseDisk
	}
	ram += managedRAM
	cpu += managedCPU
	disk += managedDisk
	a.mu.Lock()
	if a.runningExecution != nil {
		ram += uint64(a.runningExecution.RAMMB) << 20
		cpu += a.runningExecution.CPU
	}
	a.mu.Unlock()
	cp := a.Col.Collect(ram, cpu, disk)
	runtimeDir := execution.RuntimeDirectory()
	for _, name := range proto.ExecutionRuntimes {
		if execution.RuntimeReady(filepath.Join(runtimeDir, name), name) {
			cp.ServerlessRuntimes = append(cp.ServerlessRuntimes, name)
		}
	}
	cp.Serverless = len(cp.ServerlessRuntimes) > 0
	cp.ManagedTemplates = a.Managed.ReadyTemplates()
	cp.ManagedPublicHost = os.Getenv("CLOFIX_APP_PUBLIC_HOST")
	a.mu.Lock()
	cp.DatabaseEngines = a.databaseEngines
	cp.DatabaseProjects = append([]string{}, a.databaseProjects...)
	a.mu.Unlock()
	cp.DatabasePublicHost = os.Getenv("CLOFIX_DATABASE_PUBLIC_HOST")
	cp.SwapCommitted = swap
	if pool := a.S3Pool.Capacity(); pool.S3Node {
		cp.S3Node, cp.S3DiskTotal, cp.S3DiskFree = pool.S3Node, pool.S3DiskTotal, pool.S3DiskFree
	}
	return cp
}

// Run heartbeats until ctx is cancelled. Operations execute serially in a separate goroutine so a slow
// image pull never blocks heartbeats. The controller being unreachable never stops running instances.
func (a *Agent) Run(ctx context.Context) {
	a.S3Pool.Start(ctx)
	go a.databaseMonitor(ctx)
	go a.executor(ctx)
	t := time.NewTicker(time.Duration(a.Cfg.HeartbeatSeconds) * time.Second)
	defer t.Stop()
	for {
		a.Managed.Reconcile()
		if err := a.beat(ctx); err != nil {
			log.Printf("heartbeat: %v (instances keep running)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) beat(ctx context.Context) error {
	a.mu.Lock()
	progress := []proto.ExecutionProgress{}
	for _, p := range a.executionProgress {
		progress = append(progress, p)
	}
	statuses := append([]db.Status{}, a.databaseStatuses...)
	res := a.results
	a.results = nil
	a.mu.Unlock()
	body, _ := json.Marshal(proto.Heartbeat{DatabaseResults: a.Database.Results(), DatabaseStatuses: statuses, ManagedApps: a.Managed.Statuses(), ExecutionProgress: progress, Version: Version, Capacity: a.capacity(), Instances: a.Engine.Statuses(), Volumes: a.Engine.VolumeStatuses(), Backups: a.Engine.BackupStatuses(), Results: res})
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
	if hr.S3MasterAddress != "" && a.Cfg.S3MasterAddress == "" {
		a.Cfg.S3MasterAddress = hr.S3MasterAddress
		if a.Cfg.S3DataDir == "" {
			a.Cfg.S3DataDir = filepath.Join(config.StateDir(), "s3-volume")
		}
		if err := a.Cfg.Save(); err != nil {
			log.Printf("S3 pool: save worker configuration: %v", err)
		} else {
			a.S3Pool = s3pool.New(a.Cfg)
			a.S3Pool.Start(ctx)
		}
	}
	a.Database.Acknowledge(hr.DatabaseAcknowledged)
	for _, op := range hr.Operations {
		if op.Action == "database-job" || op.Action == "cancel-database" {
			a.runDatabaseOperation(ctx, op)
			continue
		}
		if op.Action == "cancel-execution" {
			a.mu.Lock()
			if a.cancelledExecutions == nil {
				a.cancelledExecutions = map[string]bool{}
			}
			if a.executionOpID == op.InstanceID && a.executionCancel != nil {
				a.executionCancel()
			} else {
				a.cancelledExecutions[op.InstanceID] = true
			}
			a.mu.Unlock()
			a.addResult(proto.OpResult{OpID: op.ID, OK: true})
			continue
		}
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
			if op.Action == "managed-app" {
				if op.Managed != nil && (op.ServiceAction == "deploy" || op.ServiceAction == "start" || op.ServiceAction == "restart") && a.Managed.NeedsReservation(op.Managed.ID) {
					cp := a.capacity()
					_, err := scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: cp}}, scheduler.Request{RAM: uint64(op.Managed.RAMMB) << 20, CPU: op.Managed.CPU}, scheduler.DefaultConfig(), time.Now())
					if err != nil {
						a.addResult(proto.OpResult{OpID: op.ID, Error: "managed application admission: insufficient capacity"})
						continue
					}
				}
				err := a.Managed.Apply(op)
				result := proto.OpResult{OpID: op.ID, OK: err == nil}
				if err != nil {
					result.Error = err.Error()
				}
				a.addResult(result)
				continue
			}
			if op.Action == "execute" && op.Execution != nil {
				cp := a.capacity()
				_, admissionErr := scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: cp}}, scheduler.Request{RAM: uint64(op.Execution.RAMMB) << 20, CPU: op.Execution.CPU}, scheduler.DefaultConfig(), time.Now())
				if admissionErr != nil {
					a.addResult(proto.OpResult{OpID: op.ID, Error: "insufficient execution capacity: " + admissionErr.Error()})
					continue
				}
				a.mu.Lock()
				a.runningExecution = op.Execution
				runCtx, cancelRun := context.WithCancel(ctx)
				a.executionCancel = cancelRun
				a.executionOpID = op.ID
				if a.cancelledExecutions[op.ID] {
					cancelRun()
					delete(a.cancelledExecutions, op.ID)
				}
				a.mu.Unlock()
				result, runErr := execution.RunWithProgress(runCtx, *op.Execution, func(p proto.ExecutionProgress) {
					p.OpID = op.ID
					a.mu.Lock()
					if a.executionProgress == nil {
						a.executionProgress = map[string]proto.ExecutionProgress{}
					}
					a.executionProgress[op.ID] = p
					a.mu.Unlock()
				})
				cancelRun()
				a.mu.Lock()
				delete(a.executionProgress, op.ID)
				a.runningExecution = nil
				a.executionCancel = nil
				a.executionOpID = ""
				a.mu.Unlock()
				r := proto.OpResult{OpID: op.ID, OK: runErr == nil, Execution: &result}
				if runErr != nil {
					r.Error = runErr.Error()
				}
				a.addResult(r)
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
