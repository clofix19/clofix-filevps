package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func (a *Agent) dockerTransfer(ctx context.Context, method, target string, body io.Reader) (io.ReadCloser, error) {
	path, e := url.Parse(target)
	if e != nil || path.IsAbs() || path.Host != "" || path.RawQuery != "" || path.Fragment != "" || !strings.HasPrefix(path.Path, "/v1/docker/") {
		return nil, fmt.Errorf("invalid docker transfer path")
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.Cfg.ControllerURL, "/")+path.String(), body)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Transport: a.hc.Transport, Timeout: 6 * time.Hour, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("docker transfers do not follow redirects")
	}}
	response, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("secure docker transfer failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("controller docker transfer returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func (a *Agent) runDockerOperation(ctx context.Context, op proto.Operation) {
	if a.Docker == nil {
		return
	}
	if op.Action == "cancel-docker" {
		a.Docker.Cancel(op.DockerJobID)
		return
	}
	select {
	case a.dockerSlot <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-a.dockerSlot }()
		source, e := a.dockerTransfer(ctx, "GET", "/v1/docker/jobs/"+op.DockerJobID, nil)
		if e != nil {
			a.Docker.Reject(dock.Operation{JobID: op.DockerJobID}, "docker job fetch failed: "+e.Error())
			return
		}
		var request dock.Operation
		e = json.NewDecoder(io.LimitReader(source, 4<<20)).Decode(&request)
		source.Close()
		if e != nil {
			a.Docker.Reject(dock.Operation{JobID: op.DockerJobID}, "docker job decode failed: "+e.Error())
			return
		}
		if request.JobID != op.DockerJobID {
			a.Docker.Reject(dock.Operation{JobID: op.DockerJobID}, "docker job id mismatch")
			return
		}
		if request.Action == "container-create" || request.Action == "container-recreate" {
			if request.Container != nil {
				capacity := a.capacity()
				if _, e = scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: capacity}}, scheduler.Request{RAM: uint64(request.Container.RAMMB) << 20, CPU: request.Container.CPU, Disk: uint64(request.Container.DiskMB) << 20}, scheduler.DefaultConfig(), time.Now()); e != nil {
					a.Docker.Reject(request, "worker docker admission rejected: insufficient safe capacity")
					return
				}
			}
		}
		a.Docker.Run(ctx, request)
	}()
}

func (a *Agent) dockerMonitor(ctx context.Context) {
	if a.Docker == nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		sample, stop := context.WithTimeout(ctx, 15*time.Second)
		st := a.Docker.Inventory(sample)
		stop()
		a.mu.Lock()
		a.dockerStatus = st
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
