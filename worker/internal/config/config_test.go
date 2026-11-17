package config

import (
	"os"
	"testing"
)

func TestResolveInstallationIDPrecedence(t *testing.T) {
	t.Setenv("CLOFIX_INSTALLATION_ID", "")
	BakedInstallationID = "ctl-baked"
	defer func() { BakedInstallationID = "" }()

	if got := ResolveInstallationID(Config{}); got != "ctl-baked" {
		t.Fatalf("baked: got %q", got)
	}
	if got := ResolveInstallationID(Config{InstallationID: "ctl-file"}); got != "ctl-file" {
		t.Fatalf("file: got %q", got)
	}
	t.Setenv("CLOFIX_INSTALLATION_ID", "ctl-env")
	if got := ResolveInstallationID(Config{InstallationID: "ctl-file"}); got != "ctl-env" {
		t.Fatalf("env: got %q", got)
	}
	_ = os.Unsetenv("CLOFIX_INSTALLATION_ID")
}
