package kubernetes

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
)

func TestHostCheckFalseWhenBinariesMissing(t *testing.T) {
	t.Setenv("CLOFIX_KUBERNETES_FAKE", "")
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ready, versions, _, _ := manager.Inventory()
	if ready {
		t.Fatal("worker without kubeadm must not advertise Kubernetes")
	}
	if len(versions) != 0 {
		t.Fatalf("unexpected versions: %v", versions)
	}
}

func TestFakeExecutorInitApplyLogsAndRejectUnknown(t *testing.T) {
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager.Fake = true
	manager.Ready = true
	manager.Version = "1.31"
	clusterID := "cks-" + strings.Repeat("a", 32)
	nodeID := "ckn-" + strings.Repeat("b", 32)
	spec := k8s.Spec{ID: clusterID, Version: "1.31", InfrastructureMode: k8s.ModeDedicated, Role: k8s.RoleControlPlane, CPU: 1, RAMMB: 2048, DiskMB: 10240, SingleNode: true, AdvertiseAddress: "192.0.2.40"}
	initJob := k8s.Operation{JobID: "ckj-" + strings.Repeat("c", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionInit, Spec: spec}
	result := manager.Run(context.Background(), initJob)
	if result.State != "SUCCEEDED" || !strings.Contains(string(result.Data), "kubeconfig") {
		t.Fatalf("init: %+v", result)
	}
	apply := k8s.Operation{JobID: "ckj-" + strings.Repeat("d", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionApply, Spec: spec, YAML: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\n  namespace: default\n"}
	applied := manager.Run(context.Background(), apply)
	if applied.State != "SUCCEEDED" {
		t.Fatalf("apply: %+v", applied)
	}
	var inventory k8s.Inventory
	if json.Unmarshal(applied.Data, &inventory) != nil || len(inventory.Deployments) == 0 {
		t.Fatalf("apply inventory: %s", applied.Data)
	}
	logs := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("e", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionLogs, Spec: spec, Namespace: "default", Name: "demo-0"})
	if logs.State != "SUCCEEDED" || !strings.Contains(string(logs.Data), "demo-0") {
		t.Fatalf("logs: %+v", logs)
	}
	secret := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("7", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionApply, Spec: spec, YAML: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: app\n  namespace: default\nstringData:\n  token: x\n"})
	if secret.State != "SUCCEEDED" || !strings.Contains(string(secret.Data), `"kind":"Secret"`) || strings.Contains(string(secret.Data), "token: x") {
		t.Fatalf("secret inventory must store names only: %s", secret.Data)
	}
	deleted := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("8", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionDelete, Spec: spec, Kind: "Deployment", Name: "demo", Namespace: "default"})
	if deleted.State != "SUCCEEDED" || strings.Contains(string(deleted.Data), `"name":"demo"`) {
		t.Fatalf("delete: %+v", deleted)
	}
	unknown := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("f", 32), ClusterID: clusterID, NodeID: nodeID, Action: "shell", Spec: spec})
	if unknown.State != "FAILED" || unknown.Error == "" {
		t.Fatalf("unknown action must fail: %+v", unknown)
	}
	helm := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("1", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionHelm, Spec: spec, Release: "web", Chart: "demo/web", HelmAction: "install"})
	if helm.State != "SUCCEEDED" || !strings.Contains(string(helm.Data), "web") {
		t.Fatalf("helm: %+v", helm)
	}
	backup := manager.Run(context.Background(), k8s.Operation{JobID: "ckj-" + strings.Repeat("2", 32), ClusterID: clusterID, NodeID: nodeID, Action: k8s.ActionBackup, Spec: spec, BackupID: "ckb-" + strings.Repeat("3", 32)})
	if backup.State != "SUCCEEDED" || !strings.Contains(string(backup.Data), "snapshot") {
		t.Fatalf("backup: %+v", backup)
	}
}

func TestRealKubeadmSkippedWithoutOptIn(t *testing.T) {
	if os.Getenv("CLOFIX_K8S_E2E") != "1" {
		t.Skip("live kubeadm is not claimed; set CLOFIX_K8S_E2E=1 on a host with kubeadm to run it")
	}
}
