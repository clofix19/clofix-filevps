package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Severity string

const (
	Info     Severity = "info"
	Low      Severity = "low"
	Medium   Severity = "medium"
	High     Severity = "high"
	Critical Severity = "critical"
)

type Event struct {
	ID        string            `json:"id"`
	Time      time.Time         `json:"time"`
	Source    string            `json:"source"`
	Type      string            `json:"type"`
	Severity  Severity          `json:"severity"`
	Message   string            `json:"message"`
	Data      map[string]any    `json:"data,omitempty"`
	Host      string            `json:"host,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dataDir, "events.jsonl")}, nil
}

func (s *Store) Append(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = ev.Time.Format("20060102T150405.000000000")
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(ev)
}

func (s *Store) Tail(n int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := splitNonEmpty(string(b))
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]Event, 0, len(lines))
	for _, ln := range lines {
		var ev Event
		if json.Unmarshal([]byte(ln), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

func splitNonEmpty(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
