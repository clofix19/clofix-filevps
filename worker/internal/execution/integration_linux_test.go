package execution

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/clofix/filevps/worker/internal/engine/namespaces"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "fx-init" {
		if err := namespaces.RunInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// Opt in on a provisioned Linux worker with a small Linux helper executable.
func TestNativeExecutionIntegration(t *testing.T) {
	helper := os.Getenv("CLOFIX_EXECUTION_TEST_BINARY")
	if helper == "" {
		t.Skip("requires Linux worker runtime and CLOFIX_EXECUTION_TEST_BINARY")
	}
	artifact, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	e := proto.Execution{Runtime: "go", Artifact: artifact, RAMMB: 128, CPU: 0.5, Timeout: 10, Env: map[string]string{"TEST_SECRET": "super-secret-test"}}
	result, err := Run(context.Background(), e)
	if err != nil {
		t.Fatalf("native run: %v; stderr: %s", err, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "uid=65534") || strings.Contains(result.Stdout, "super-secret-test") {
		t.Fatalf("isolation/redaction: %s", result.Stdout)
	}
	if len(result.OutputZIP) == 0 || result.OutputError != "" {
		t.Fatalf("output collection: %s", result.OutputError)
	}
	e.Args = []string{"sleep"}
	e.Timeout = 1
	result, err = Run(context.Background(), e)
	if err == nil || !result.TimedOut {
		t.Fatalf("timeout not enforced: %+v %v", result, err)
	}
}
