# CloFix Kubernetes Service (CKS)

CKS provisions kubeadm clusters on **dedicated physical CloFix workers**. It reuses controller IAM, tenant `account_id` ownership, the capacity scheduler, mTLS heartbeats, encrypted application secrets, Billing live-resource inventory and company license quotas.

It does **not** run Kubernetes inside FileVPS guests. Shared-kernel FileVPS cannot host kubelet (seccomp, dropped `CAP_SYS_ADMIN`, no cgroup delegation).

## Production-grade path

1. On each Ubuntu worker host run [`scripts/install-cks-worker.sh`](../scripts/install-cks-worker.sh) (containerd, kubeadm, kubelet, kubectl, helm, etcdctl).
2. **Never** set `CLOFIX_KUBERNETES_FAKE=1` on production workers.
3. Enroll/restart `clofix-worker` so capacity reports `kubernetes_ready` with real versions.
4. After verifying HA create (3/5 CP) and etcd backup/restore on those hosts, set `CLOFIX_K8S_E2E=1` on the **controller**.
5. `GET /api/v1/kubernetes` then reports `production_ready: true` when at least one non-lab ready worker is present and E2E is enabled.

Local Compose may still set `CLOFIX_KUBERNETES_FAKE=1` for UI demos without kubeadm. That lab executor is not production Kubernetes.

## What is implemented

| Area | Behaviour |
|---|---|
| Clusters | Create/list/status, kubeconfig download, delete protection |
| Node pools | Default pool plus extra worker pools, scale up/down |
| Workloads | kubectl apply / scale / restart / logs |
| Helm | install / upgrade / uninstall |
| Storage | CKS `local-path` StorageClass / PVC YAML |
| Ingress | nginx IngressClass |
| RBAC | Role / RoleBinding YAML |
| Autoscaling | HPA YAML and node-pool min/max |
| Backups | Encrypted etcd snapshots (etcdctl on host) |
| HA | 3 or 5 stacked control-plane nodes with `--upload-certs` |
| Upgrades | kubeadm rolling upgrade (control plane, then workers) |
| Billing | CPU/RAM/disk for all live nodes; etcd backups as backup GB |
| License | `max_kubernetes_clusters` / `max_kubernetes_nodes` / `max_kubernetes_node_pools` |

Console: `/kubernetes` (alias `/cks`). API: `/api/v1/kubernetes`.

## Private API access

kube-apiserver stays on the worker. Download kubeconfig from the console (`GET /api/v1/kubernetes/clusters/{id}/kubeconfig`). Reach the API over VPN, a bastion on the worker network, or an operator mTLS tunnel. Compose does not publish 6443, 10250 or 2379. CloFix does not open a public kube-apiserver by default.

## FileVPS nodes — still gated

`GET /api/v1/kubernetes/compatibility` returns the FileVPS validation report. `infrastructure_mode=filevps` remains HTTP 409. Customer workloads on FileVPS nodes are not enabled.

## License

Unspecified CKS limits stay **unlimited** so existing signed plans keep working. Set `0` on the company license server to exclude CKS, or a positive cap to enforce it. CKS etcd snapshots count toward `max_backups` and storage bytes.

## Threat notes

- Tenant isolation is owner-scoped. Cross-tenant get/delete/kubeconfig/helm/backup is 404.
- Workers never expose a shell API. Only allowlisted kubeadm/kubectl/helm/etcdctl verbs run.
- Prefer one customer cluster per dedicated worker reservation; do not co-locate untrusted clusters without an isolation review.
- Kubeconfig AAD `clofix-kubernetes-kubeconfig-v1`. Job payloads AAD `kubernetes:{job-id}`. Backup snapshots AAD `kubernetes:backup:{id}`.

## Unsupported

- Cilium CNI (Flannel remains the default)
- FileVPS kubelet nodes
- Pod exec / interactive terminal
- Public kube-apiserver
