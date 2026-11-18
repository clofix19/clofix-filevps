package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Mode string

const (
	ModeStandard   Mode = "standard"
	ModeStrict     Mode = "strict"
	ModeImmutable  Mode = "immutable"
)

type State struct {
	Mode              Mode `json:"mode"`
	AutoPilotEnabled  bool `json:"autopilot_enabled"`
	ImmutableActive   bool `json:"immutable_active"`
	ZeroTrustActive   bool `json:"zero_trust_active"`
	ProcessTerminate  bool `json:"process_terminate"`
	AggressiveEgress  bool `json:"aggressive_egress"`
	AutoQuarantine    bool `json:"auto_quarantine"`
}

func DefaultState() State {
	return State{Mode: ModeStandard}
}

func Path(dataDir string) string {
	return filepath.Join(dataDir, "policy-state.json")
}

func Load(dataDir string) (State, error) {
	st := DefaultState()
	b, err := os.ReadFile(Path(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

func Save(dataDir string, st State) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(dataDir), raw, 0o640)
}

func Enable(dataDir string, mode Mode) (State, error) {
	st, err := Load(dataDir)
	if err != nil {
		return st, err
	}
	st.Mode = mode
	switch mode {
	case ModeStandard:
		st.ProcessTerminate = false
		st.ZeroTrustActive = false
		st.ImmutableActive = false
		st.AggressiveEgress = false
		st.AutoQuarantine = false
	case ModeStrict:
		// Still no destructive defaults
		st.ProcessTerminate = false
		st.AggressiveEgress = false
		st.AutoQuarantine = false
		st.ZeroTrustActive = false
		st.ImmutableActive = false
	case ModeImmutable:
		st.ImmutableActive = true // marker; host-worker enforces
	}
	return st, Save(dataDir, st)
}

func List() []map[string]string {
	return []map[string]string{
		{"mode": string(ModeStandard), "description": "Safe baseline assessments; no disruptive auto-response"},
		{"mode": string(ModeStrict), "description": "Stricter alerting; disruptive controls still opt-in"},
		{"mode": string(ModeImmutable), "description": "Requests host-enforced immutable lower; maintenance mode required for writes"},
	}
}
