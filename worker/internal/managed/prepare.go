//go:build linux

package managed

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

const defaultTemplateBuilder = "/usr/local/lib/clofix/build-managed-templates.sh"

var prepareOnce sync.Once

// MissingTemplates returns catalog IDs that do not yet have a published start script.
func (m *Manager) MissingTemplates() []string {
	out := []string{}
	for _, t := range proto.ManagedTemplates {
		start := filepath.Join(m.Templates, t.ID, "app", "start")
		if f, err := os.Stat(start); err != nil || !f.Mode().IsRegular() || f.Mode()&0111 == 0 {
			out = append(out, t.ID)
		}
	}
	return out
}

// PrepareMissingInBackground builds unpublished Quick App templates once per process.
// It requires the packaged build-managed-templates.sh helper and debootstrap on the worker.
func (m *Manager) PrepareMissingInBackground(ctx context.Context) {
	prepareOnce.Do(func() {
		go m.prepareMissing(ctx)
	})
}

func (m *Manager) prepareMissing(ctx context.Context) {
	missing := m.MissingTemplates()
	if len(missing) == 0 {
		return
	}
	builder := os.Getenv("CLOFIX_MANAGED_TEMPLATE_BUILDER")
	if builder == "" {
		builder = defaultTemplateBuilder
	}
	if _, err := os.Stat(builder); err != nil {
		log.Printf("managed templates: %d missing (%s); install builder at %s to auto-prepare", len(missing), strings.Join(missing, ", "), builder)
		return
	}
	if _, err := exec.LookPath("debootstrap"); err != nil {
		log.Printf("managed templates: debootstrap not installed; skipping auto-prepare for %s", strings.Join(missing, ", "))
		return
	}
	log.Printf("managed templates: auto-preparing %s", strings.Join(missing, ", "))
	cmd := exec.CommandContext(ctx, "/bin/sh", builder)
	cmd.Env = append(os.Environ(),
		"CLOFIX_STATE_DIR="+filepath.Dir(m.Templates),
		"CLOFIX_MANAGED_TEMPLATES="+strings.Join(missing, " "),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	started := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("managed templates: auto-prepare finished with error after %s: %v", time.Since(started).Round(time.Second), err)
		return
	}
	still := m.MissingTemplates()
	if len(still) == 0 {
		log.Printf("managed templates: all templates ready after %s", time.Since(started).Round(time.Second))
		return
	}
	log.Printf("managed templates: still missing after auto-prepare: %s", strings.Join(still, ", "))
}
