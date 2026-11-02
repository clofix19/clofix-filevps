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

	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func (a *Agent) kubernetesTransfer(ctx context.Context, method, target string, body io.Reader) (io.ReadCloser, error) {
	path, e := url.Parse(target)
	if e != nil || path.IsAbs() || path.Host != "" || path.RawQuery != "" || path.Fragment != "" || !strings.HasPrefix(path.Path, "/v1/kubernetes/") {
		return nil, fmt.Errorf("invalid kubernetes transfer path")
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.Cfg.ControllerURL, "/")+path.String(), body)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Transport: a.hc.Transport, Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("kubernetes transfers do not follow redirects")
	}}
	response, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("secure kubernetes transfer failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("controller kubernetes transfer returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func (a *Agent) runKubernetesOperation(ctx context.Context, op proto.Operation) {
	if a.Kubernetes == nil {
		return
	}
	select {
	case a.kubernetesSlot <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-a.kubernetesSlot }()
		source, e := a.kubernetesTransfer(ctx, "GET", "/v1/kubernetes/jobs/"+op.KubernetesJobID, nil)
		if e != nil {
			return
		}
		var request k8s.Operation
		e = json.NewDecoder(io.LimitReader(source, 2<<20)).Decode(&request)
		source.Close()
		if e != nil || request.JobID != op.KubernetesJobID {
			return
		}
		if !a.Kubernetes.Fake && (request.Action == k8s.ActionInit || request.Action == k8s.ActionJoin) {
			capacity := a.capacity()
			if _, e = scheduler.Place([]scheduler.Candidate{{ID: "self", Active: true, LastHeartbeat: time.Now(), Capacity: capacity}}, scheduler.Request{RAM: uint64(request.Spec.RAMMB) << 20, CPU: request.Spec.CPU, Disk: uint64(request.Spec.DiskMB) << 20}, scheduler.DefaultConfig(), time.Now()); e != nil {
				a.Kubernetes.Reject(request, "worker kubernetes admission rejected: insufficient safe capacity")
				return
			}
		}
		a.Kubernetes.Run(ctx, request)
	}()
}
