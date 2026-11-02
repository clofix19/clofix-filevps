package kubernetes

import "testing"

func TestVersionSupportAndFileVPSReport(t *testing.T) {
	if !VersionSupported("1.31") || !VersionSupported("v1.32.4") || VersionSupported("1.28") {
		t.Fatal("version allowlist mismatch")
	}
	if NormalizeVersion("v1.31.8") != "1.31" {
		t.Fatal(NormalizeVersion("v1.31.8"))
	}
	if len(FileVPSReport) == 0 {
		t.Fatal("compatibility report required")
	}
	spec := Spec{ID: "cks-" + "0123456789abcdef0123456789abcdef", Version: "1.31", InfrastructureMode: ModeFileVPS, Role: RoleControlPlane, CPU: 1, RAMMB: 2048, DiskMB: 10240}
	if spec.Validate() == nil {
		t.Fatal("filevps spec must not validate")
	}
	report := FileVPSValidation()
	if report.Ready || report.CustomerWorkloads || report.Summary == "" {
		t.Fatal("FileVPS customer workloads must stay disabled")
	}
}
