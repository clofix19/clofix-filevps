package kubernetes

// FileVPSCustomerWorkloads is false until compatibility, security and performance
// validation all pass. Shared-kernel FileVPS cannot host kubelet for customers.
const FileVPSCustomerWorkloads = false

type ValidationCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ValidationReport struct {
	CustomerWorkloads bool                 `json:"customer_workloads"`
	Ready             bool                 `json:"ready"`
	Compatibility     []CompatibilityIssue `json:"compatibility"`
	Security          []ValidationCheck    `json:"security"`
	Performance       []ValidationCheck    `json:"performance"`
	Summary           string               `json:"summary"`
}

// FileVPSValidation is the Phase 4 gate. It documents why FileVPS kubelet stays disabled.
func FileVPSValidation() ValidationReport {
	security := []ValidationCheck{
		{Name: "Kernel isolation", Status: "fail", Detail: "FileVPS shares the worker kernel. A kubelet in a guest could observe or influence sibling VPS cgroups."},
		{Name: "Capability drop", Status: "fail", Detail: "Required kubelet/CNI capabilities are dropped; raising them would weaken FileVPS isolation."},
		{Name: "Seccomp profile", Status: "fail", Detail: "The FileVPS seccomp filter denies mount, unshare, bpf and pivot_root used by container runtimes."},
		{Name: "Multi-tenant node isolation", Status: "fail", Detail: "One worker cannot safely co-locate untrusted Kubernetes nodes with FileVPS guests."},
	}
	performance := []ValidationCheck{
		{Name: "Nested kubelet CPU", Status: "fail", Detail: "Guests cannot create nested cgroup trees, so Kubernetes QoS and HPA signals are not trustworthy."},
		{Name: "Storage I/O isolation", Status: "fail", Detail: "OverlayFS writable layers share the host disk with no per-PVC IO controller inside the guest."},
		{Name: "Network datapath", Status: "fail", Detail: "veth/nftables FileVPS networking is not a CNI-compatible node datapath for customer pods."},
	}
	blocked := 0
	for _, item := range FileVPSReport {
		if item.Status != "pass" {
			blocked++
		}
	}
	return ValidationReport{
		CustomerWorkloads: FileVPSCustomerWorkloads,
		Ready:             false,
		Compatibility:     FileVPSReport,
		Security:          security,
		Performance:       performance,
		Summary:           "FileVPS kubelet is not enabled. Compatibility, security and performance validation failed; dedicated physical workers remain the only CKS node type.",
	}
}
