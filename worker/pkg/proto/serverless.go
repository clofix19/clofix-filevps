package proto

import (
	"errors"
	"math"
	"path"
	"regexp"
	"strings"
	"time"
)

// Execution is a bounded, network-isolated invocation; it never creates a VPS.
var ExecutionRuntimes = []string{"python", "go", "nodejs", "php", "java", "ruby", "bun", "deno"}
var executionEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type Execution struct {
	Format              string            `json:"format,omitempty"`
	Entry               string            `json:"entry,omitempty"`
	Args                []string          `json:"args,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	InstallDependencies bool              `json:"install_dependencies,omitempty"`

	Deadline time.Time `json:"deadline,omitempty"`
	Runtime  string    `json:"runtime"`
	Artifact []byte    `json:"artifact"`
	Input    string    `json:"input"`
	RAMMB    int64     `json:"ram_mb"`
	CPU      float64   `json:"cpu"`
	Timeout  int       `json:"timeout_seconds"`
}
type ExecutionResult struct {
	OutputAvailable bool   `json:"output_available"`
	OutputZIP       []byte `json:"output_zip,omitempty"`
	OutputError     string `json:"output_error,omitempty"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	Cancelled       bool   `json:"cancelled"`
	TimedOut        bool   `json:"timed_out"`
	Truncated       bool   `json:"truncated"`
}

func ValidateExecution(e Execution) error {
	if !e.Deadline.IsZero() && time.Now().After(e.Deadline) {
		return errors.New("execution expired in queue")
	}
	supported := false
	for _, r := range ExecutionRuntimes {
		if r == e.Runtime {
			supported = true
		}
	}
	if !supported {
		return errors.New("unsupported runtime")
	}
	if e.Format != "" && e.Format != "file" && e.Format != "zip" {
		return errors.New("format must be file or zip")
	}
	if e.Entry != "" && (!SafeExecutionPath(e.Entry) || strings.HasPrefix(e.Entry, ".clofix")) {
		return errors.New("invalid entrypoint")
	}
	if len(e.Args) > 32 {
		return errors.New("at most 32 arguments allowed")
	}
	for _, a := range e.Args {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return errors.New("invalid argument")
		}
	}
	if err := ValidateExecutionEnv(e.Env); err != nil {
		return err
	}
	if len(e.Artifact) == 0 || len(e.Artifact) > 4<<20 {
		return errors.New("artifact must contain 1 byte to 4 MiB")
	}
	if len(e.Input) > 64<<10 {
		return errors.New("input exceeds 64 KiB")
	}
	if e.RAMMB < 32 || e.RAMMB > 2048 || math.IsNaN(e.CPU) || math.IsInf(e.CPU, 0) || e.CPU < 0.1 || e.CPU > 2 || e.Timeout < 1 || e.Timeout > 120 {
		return errors.New("limits: RAM 32–2048 MiB, CPU 0.1–2, timeout 1–120 seconds")
	}
	return nil
}

func SafeExecutionPath(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "/") && path.Clean(name) == name && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00")
}
func ValidateExecutionEnv(env map[string]string) error {
	if len(env) > 64 {
		return errors.New("at most 64 environment variables allowed")
	}
	for k, v := range env {
		if !executionEnvKey.MatchString(k) || len(v) > 4096 || strings.ContainsRune(v, 0) {
			return errors.New("invalid environment variable")
		}
		if k == "PATH" || k == "HOME" || strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "CLOFIX_") {
			return errors.New("reserved environment variable")
		}
	}
	return nil
}
func ExecutionCommand(e Execution) []string {
	entry := e.Entry
	if entry == "" {
		entry = "main"
	}
	p := "/app/" + entry
	var args []string
	switch e.Runtime {
	case "python":
		args = []string{"/usr/bin/python3", "-s", "-u", "-B", p}
	case "go":
		args = []string{p}
	case "nodejs":
		args = []string{"/usr/bin/node", p}
	case "php":
		args = []string{"/usr/bin/php", p}
	case "java":
		args = []string{"/usr/bin/java", "-jar", p}
	case "ruby":
		args = []string{"/usr/bin/ruby", p}
	case "bun":
		args = []string{"/usr/local/bin/bun", "run", p}
	case "deno":
		args = []string{"/usr/local/bin/deno", "run", "--cached-only", "--ext=ts", "--no-prompt", "--allow-read=/app,/tmp,/output", "--allow-write=/tmp,/output", "--allow-env", p}
	}
	return append(args, e.Args...)
}

type ExecutionProgress struct {
	OpID      string `json:"op_id"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

func ExecutionExecutable(runtime string) string {
	switch runtime {
	case "python":
		return "usr/bin/python3"
	case "go":
		return "usr/bin/env"
	case "nodejs":
		return "usr/bin/node"
	case "php":
		return "usr/bin/php"
	case "java":
		return "usr/bin/java"
	case "ruby":
		return "usr/bin/ruby"
	case "bun":
		return "usr/local/bin/bun"
	case "deno":
		return "usr/local/bin/deno"
	}
	return ""
}
