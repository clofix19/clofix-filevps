# CloFix Kubernetes Service (CKS) — Phase 2

Current CKS behaviour, including node pools, Helm, HA, backups and license quotas, is documented in [cks.md](cks.md). This page is the Phase 2 MVP note.

CKS provisions kubeadm clusters on **dedicated physical CloFix workers**. It reuses controller IAM, tenant `account_id` ownership, the capacity scheduler, mTLS heartbeats, and encrypted application secrets. It does **not** run Kubernetes inside FileVPS guests and is **not** production-ready HA Kubernetes.

## Architecture

Tenant console `/kubernetes` and REST `/api/v1/kubernetes` create a cluster snapshot (`cf_kubernetes_state`). The controller places one Kubernetes node per CloFix worker that advertised `kubernetes_ready`. Heartbeats inject `kubernetes-job` operations. The worker fetches the sealed job from `GET /v1/kubernetes/jobs/{id}` over mTLS and runs a fixed verb: `init`, `join`, `reset`, `apply`, `get`, `logs`, `scale`, `restart`.

The Kubernetes API stays on the worker. Compose does not publish 6443, 10250 or 2379. Console workload actions go through the worker job channel, not a public kube-apiserver.

Kubeconfig is stored with AES-GCM using `application-secrets.key` and AAD `clofix-kubernetes-kubeconfig-v1`. Job payloads use AAD `kubernetes:{job-id}`. Cross-tenant get/delete/kubeconfig returns 404.

## FileVPS compatibility report

FileVPS guests share the worker kernel. Nested kubelet/containerd is structurally blocked:

| Requirement | FileVPS guest today |
|---|---|
| Privileged mounts / unshare | Seccomp deny (`mount`, `unshare`, `bpf`, `pivot_root`) |
| CAP_SYS_ADMIN / CAP_NET_ADMIN | Dropped |
| `/sys` and cgroup delegation | `/sys` not mounted; `CLONE_NEWCGROUP` hides the host tree |
| Nested containerd / kubelet | Not a VM; not a supported product mode |

`infrastructure_mode=filevps` returns HTTP 409 with this report. The create form disables FileVPS. Do not treat FileVPS as a Kubernetes node until a later phase ships a passing compatibility test.

## Worker install (host Ubuntu, not the Compose image)

The development Compose worker image does **not** include kubeadm. Production workers are host-installed Ubuntu packages. On those hosts:

```sh
# containerd + kubeadm + kubelet + kubectl for a supported 1.31 or 1.32 minor
# then restart clofix-worker so Capacity.kubernetes_ready becomes true
sudo systemctl restart clofix-worker
```

Optional advertise address: `CLOFIX_KUBERNETES_PUBLIC_HOST`. Optional fake executor for labs without kubeadm: `CLOFIX_KUBERNETES_FAKE=1` (never enable for customer production).

CNI for this phase is Flannel. Cilium, CSI, Helm, HA etcd, upgrades and backups are out of scope.

## Threat notes

- Tenant isolation is owner-scoped (`applicationOwner` / account id). IAM `kubernetes:read` and `kubernetes:manage` are distinct from `instances:*`.
- Workers never expose an unrestricted shell API. Only allowlisted kubectl/kubeadm verbs run.
- Join tokens live in encrypted join material and are stripped from job results after use.
- This phase does not claim multi-tenant node isolation beyond “one customer cluster on dedicated workers with scheduler RAM/CPU/disk reservation.” Do not co-locate untrusted clusters on one worker without a later isolation review.

## Unsupported in Phase 2

- HA control plane / stacked etcd / API load balancing
- Cilium CNI
- CSI persistent volumes and S3-as-block PVC
- Helm applications
- Cluster and node-pool autoscaling
- etcd / workload backup and restore
- FileVPS kubelet nodes
- Kubernetes version upgrades
- Pod exec / interactive terminal
- License-plan cluster quotas (billing inventory is present; signed license limits are not)
- Production-readiness certification

Automated tests cover API auth, tenant isolation, FileVPS rejection, placement of unready workers, encrypted job payloads, fake kubeadm provisioning and kubeconfig download. Live kubeadm is not claimed unless `CLOFIX_K8S_E2E=1` is set on a host that actually has kubeadm.
