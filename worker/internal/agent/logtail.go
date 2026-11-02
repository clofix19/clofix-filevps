package agent

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/logtail"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func (a *Agent) setLogSources(items []proto.LogSourceSpec) {
	a.mu.Lock()
	a.logSources = append([]proto.LogSourceSpec(nil), items...)
	a.mu.Unlock()
}

func (a *Agent) collectLogTails() []proto.LogTail {
	a.mu.Lock()
	specs := append([]proto.LogSourceSpec(nil), a.logSources...)
	a.mu.Unlock()
	if len(specs) == 0 {
		return nil
	}
	pids := map[string]int{}
	for _, st := range a.Engine.Statuses() {
		pids[st.ID] = st.Pid
	}
	merged := map[string]*proto.LogTail{}
	add := func(id, text, err string) {
		item := merged[id]
		if item == nil {
			item = &proto.LogTail{ID: id}
			merged[id] = item
		}
		if err != "" && item.Error == "" {
			item.Error = err
		}
		if text == "" {
			return
		}
		if item.Text != "" {
			item.Text += "\n"
		}
		item.Text += text
		item.Bytes = len(item.Text)
	}
	for _, spec := range specs {
		if spec.ID == "" {
			continue
		}
		switch spec.Kind {
		case "vps", "lb":
			for _, id := range strings.Split(spec.ResourceID, ",") {
				id = strings.TrimSpace(id)
				pid := pids[id]
				if pid <= 0 {
					add(spec.ID, "", "VPS is not running")
					continue
				}
				path, err := logtail.GuestPath(pid, spec.Path)
				if err != nil {
					add(spec.ID, "", err.Error())
					continue
				}
				data := logtail.TailFile(path, logtail.MaxBytes)
				if len(data) == 0 {
					add(spec.ID, "", "log file not found")
					continue
				}
				add(spec.ID, string(data), "")
			}
		case "database":
			a.tailProject(spec, filepath.Join(config.StateDir(), "databases"), "clofix-"+spec.ResourceID, add)
		case "managed-app":
			a.tailProject(spec, filepath.Join(config.StateDir(), "managed-apps"), "", add)
		case "worker":
			if err := logtail.AllowHostPath(spec.Path); err != nil {
				add(spec.ID, "", err.Error())
				continue
			}
			data := logtail.TailFile(spec.Path, logtail.MaxBytes)
			if len(data) == 0 {
				add(spec.ID, "", "log file not found")
				continue
			}
			add(spec.ID, string(data), "")
		default:
			add(spec.ID, "", "unknown log source kind")
		}
	}
	out := make([]proto.LogTail, 0, len(merged))
	for _, item := range merged {
		if len(item.Text) > logtail.MaxBytes {
			item.Text = item.Text[len(item.Text)-logtail.MaxBytes:]
			item.Bytes = len(item.Text)
		}
		out = append(out, *item)
	}
	return out
}

func (a *Agent) tailProject(spec proto.LogSourceSpec, root, dockerName string, add func(id, text, err string)) {
	path, err := logtail.Confine(filepath.Join(root, spec.ResourceID), spec.Path)
	if err != nil {
		add(spec.ID, "", err.Error())
		return
	}
	if path == "" && dockerName != "" {
		text, derr := dockerLogs(dockerName)
		if derr != nil {
			add(spec.ID, "", derr.Error())
			return
		}
		add(spec.ID, text, "")
		return
	}
	if path == "" {
		path, err = logtail.Confine(filepath.Join(root, spec.ResourceID), "app.log")
		if err != nil {
			add(spec.ID, "", err.Error())
			return
		}
	}
	data := logtail.TailFile(path, logtail.MaxBytes)
	if len(data) == 0 {
		add(spec.ID, "", "log file not found")
		return
	}
	add(spec.ID, string(data), "")
}

func dockerLogs(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "200", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	text := out.String()
	if len(text) > logtail.MaxBytes {
		text = text[len(text)-logtail.MaxBytes:]
	}
	return text, nil
}
