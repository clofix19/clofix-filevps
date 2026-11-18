package response

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/clofix/clofix-linux/internal/config"
)

type Category string

const (
	ReadOnly         Category = "READ_ONLY"
	SafeMutation     Category = "SAFE_MUTATION"
	ApprovalRequired Category = "APPROVAL_REQUIRED"
	HighRisk         Category = "HIGH_RISK"
	Destructive      Category = "DESTRUCTIVE"
)

type Action struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Category  Category  `json:"category"`
	Summary   string    `json:"summary"`
	Approved  bool      `json:"approved"`
	Executed  bool      `json:"executed"`
	Evidence  string    `json:"evidence"`
	Result    string    `json:"result"`
}

type Engine struct {
	DataDir string
	Cfg     config.AutoPilotConfig
}

func (e *Engine) Decide(category Category, summary string) (Action, error) {
	act := Action{
		ID:       fmt.Sprintf("%d", time.Now().UnixNano()),
		Time:     time.Now().UTC(),
		Category: category,
		Summary:  summary,
	}
	if !e.Cfg.Enabled {
		act.Result = "autopilot disabled — recorded only"
		return act, e.record(act)
	}
	switch category {
	case ReadOnly:
		act.Approved = true
		act.Executed = true
		act.Result = "read-only observation allowed"
	case SafeMutation:
		if e.Cfg.AllowSafeMutation {
			act.Approved = true
			act.Executed = true
			act.Result = "safe mutation permitted by policy"
		} else {
			act.Category = ApprovalRequired
			act.Result = "safe mutation blocked pending policy enable"
		}
	case HighRisk, Destructive, ApprovalRequired:
		act.Approved = false
		act.Executed = false
		act.Result = "approval required — not auto-executed"
		if category == Destructive && e.Cfg.AllowDestructive {
			act.Result = "destructive allowed by policy but still requires explicit approval API"
		}
		if category == HighRisk && e.Cfg.AllowHighRisk {
			act.Result = "high-risk allowed by policy but still requires explicit approval API"
		}
	}
	return act, e.record(act)
}

func (e *Engine) record(act Action) error {
	dir := filepath.Join(e.DataDir, "autopilot")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	raw, _ := json.Marshal(act)
	act.Evidence = string(raw)
	path := filepath.Join(dir, act.ID+".json")
	return os.WriteFile(path, raw, 0o640)
}

func (e *Engine) Status() (map[string]any, error) {
	dir := filepath.Join(e.DataDir, "autopilot")
	entries, _ := os.ReadDir(dir)
	return map[string]any{
		"enabled": e.Cfg.Enabled,
		"actions_recorded": len(entries),
		"allow_safe_mutation": e.Cfg.AllowSafeMutation,
		"allow_high_risk": e.Cfg.AllowHighRisk,
		"allow_destructive": e.Cfg.AllowDestructive,
	}, nil
}
