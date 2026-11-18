package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Listener struct {
	Local string `json:"local"`
	State string `json:"state"`
	Proto string `json:"proto"`
}

type Status struct {
	Listeners     []Listener `json:"listeners"`
	NewListeners  []string   `json:"new_listeners"`
	ConnSample    int        `json:"connection_rows"`
	BaselinePath  string     `json:"baseline_path"`
	Note          string     `json:"note"`
}

func Inventory(dataDir string) (Status, error) {
	st := Status{
		Note: "Guest-visible /proc/net only; host netns isolation is host-worker responsibility",
	}
	st.Listeners = append(st.Listeners, readListeners("/proc/net/tcp", "tcp")...)
	st.Listeners = append(st.Listeners, readListeners("/proc/net/tcp6", "tcp6")...)
	st.ConnSample = countRows("/proc/net/tcp") + countRows("/proc/net/tcp6")

	basePath := filepath.Join(dataDir, "network-listeners.json")
	st.BaselinePath = basePath
	cur := map[string]bool{}
	for _, l := range st.Listeners {
		cur[l.Proto+":"+l.Local] = true
	}
	prev := map[string]bool{}
	if b, err := os.ReadFile(basePath); err == nil {
		_ = json.Unmarshal(b, &prev)
		for k := range cur {
			if !prev[k] {
				st.NewListeners = append(st.NewListeners, k)
			}
		}
	}
	_ = os.MkdirAll(dataDir, 0o750)
	raw, _ := json.MarshalIndent(cur, "", "  ")
	_ = os.WriteFile(basePath+".tmp", raw, 0o640)
	_ = os.Rename(basePath+".tmp", basePath)
	_ = os.WriteFile(filepath.Join(dataDir, "network-last-scan"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o640)
	return st, nil
}

func readListeners(path, proto string) []Listener {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Listener
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if f[3] != "0A" {
			continue
		}
		out = append(out, Listener{Local: f[1], State: "LISTEN", Proto: proto})
	}
	return out
}

func countRows(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		n++
	}
	return n
}
