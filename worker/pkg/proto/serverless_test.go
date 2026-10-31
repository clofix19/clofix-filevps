package proto

import (
	"math"
	"testing"
	"time"
)

func TestExecutionBounds(t *testing.T) {
	valid := Execution{Runtime: "python", Artifact: []byte("print(1)"), RAMMB: 128, CPU: 0.5, Timeout: 30}
	if err := ValidateExecution(valid); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Execution){
		"arbitrary runtime":  func(e *Execution) { e.Runtime = "shell" },
		"empty artifact":     func(e *Execution) { e.Artifact = nil },
		"oversized artifact": func(e *Execution) { e.Artifact = make([]byte, (4<<20)+1) },
		"oversized input":    func(e *Execution) { e.Input = string(make([]byte, (64<<10)+1)) },
		"low memory":         func(e *Execution) { e.RAMMB = 1 },
		"high memory":        func(e *Execution) { e.RAMMB = 4096 },
		"zero CPU":           func(e *Execution) { e.CPU = 0 },
		"NaN CPU":            func(e *Execution) { e.CPU = math.NaN() },
		"infinite CPU":       func(e *Execution) { e.CPU = math.Inf(1) },
		"unbounded timeout":  func(e *Execution) { e.Timeout = 0 },
		"expired queue":      func(e *Execution) { e.Deadline = time.Now().Add(-time.Second) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := valid
			change(&e)
			if ValidateExecution(e) == nil {
				t.Fatal("invalid execution accepted")
			}
		})
	}
}

func TestApplicationRuntimeCommandsAndPaths(t *testing.T) {
	for _, name := range ExecutionRuntimes {
		e := Execution{Runtime: name, Entry: "src/main", Artifact: []byte("x"), RAMMB: 128, CPU: 0.5, Timeout: 10}
		if err := ValidateExecution(e); err != nil {
			t.Fatal(name, err)
		}
		if len(ExecutionCommand(e)) == 0 {
			t.Fatal("missing runtime command", name)
		}
	}
	for _, entry := range []string{"../main", "/main", "a/../../main", `a\main`, ".clofix-env.json"} {
		e := Execution{Runtime: "python", Entry: entry, Artifact: []byte("x"), RAMMB: 128, CPU: 0.5, Timeout: 10}
		if ValidateExecution(e) == nil {
			t.Fatal("unsafe entrypoint", entry)
		}
	}
	for _, key := range []string{"LD_PRELOAD", "PATH", "HOME", "CLOFIX_ADMIN_TOKEN", "BAD=KEY"} {
		if ValidateExecutionEnv(map[string]string{key: "x"}) == nil {
			t.Fatal("reserved environment", key)
		}
	}
}
