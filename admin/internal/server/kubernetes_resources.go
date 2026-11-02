package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/worker/pkg/kubernetes"
)

type kubernetesResourceRequest struct {
	YAML      string            `json:"yaml"`
	Action    string            `json:"action"`
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Replicas  int               `json:"replicas"`
	Image     string            `json:"image"`
	Port      int               `json:"port"`
	Type      string            `json:"type"`
	Storage   string            `json:"storage"`
	Data      map[string]string `json:"data"`
	Schedule  string            `json:"schedule"`
	Selector  string            `json:"selector"`
}

func (s *Server) getKubernetesCatalog(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	p := principalFrom(r)
	admin := p.Kind == "admin"
	allow := s.visibleAccountIDSet(p)
	lbs := []map[string]any{}
	if s.balancers != nil {
		s.balancers.Lock()
		for _, b := range s.balancers.Items {
			if b == nil || b.Deleting {
				continue
			}
			if !admin && !s.canAccessAccount(p, b.AccountID) {
				continue
			}
			lbs = append(lbs, map[string]any{"id": b.ID, "name": b.Name, "mode": b.Mode, "public_ip": b.PublicIP, "listen_port": b.ListenPort, "slug": b.Slug})
		}
		s.balancers.Unlock()
	}
	volumes, _ := s.Store.ListVolumes()
	volOut := []map[string]any{}
	for _, vol := range filterVolumes(volumes, allow, admin) {
		volOut = append(volOut, map[string]any{"id": vol.ID, "name": vol.Name, "state": vol.State, "limit_mb": vol.LimitMB})
	}
	efsOut := []map[string]any{}
	if s.efs != nil {
		s.efs.Lock()
		for _, share := range s.efs.Shares {
			if !admin && !s.canAccessAccount(p, share.AccountID) {
				continue
			}
			efsOut = append(efsOut, map[string]any{"id": share.ID, "name": share.Name, "connection_id": share.ConnectionID, "prefix": share.Prefix})
		}
		s.efs.Unlock()
	}
	s3Out := []map[string]any{}
	if s.S3 != nil {
		if conns, err := s.S3.ListConnections(); err == nil {
			for _, c := range conns {
				s3Out = append(s3Out, map[string]any{"id": c.ID, "name": c.Name, "bucket": c.Bucket, "endpoint": c.Endpoint, "region": c.Region, "provider": c.Provider})
			}
		}
	}
	instances, _ := s.Store.ListInstances()
	vpsOut := []map[string]any{}
	for _, inst := range filterInstances(instances, allow, admin) {
		vpsOut = append(vpsOut, map[string]any{"id": inst.ID, "name": inst.Spec.Hostname, "state": inst.State, "ssh_port": inst.Spec.SSHPort, "ip_address": inst.Spec.IPAddress})
	}
	writeJSON(w, 200, map[string]any{
		"cluster_id":     cluster.ID,
		"load_balancers": lbs,
		"volumes":        volOut,
		"efs":            efsOut,
		"s3":             s3Out,
		"vps":            vpsOut,
		"note":           "CKS attaches existing CloFix LB, volume, EFS, S3 and FileVPS resources. FileVPS guests cannot run kubelet.",
		"integrations":   cluster.Integrations,
	})
}

func (s *Server) listKubernetesIntegrations(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"integrations": cluster.Integrations})
}

func (s *Server) postKubernetesIntegration(w http.ResponseWriter, r *http.Request) {
	cluster, ok := s.loadKubernetesCluster(w, r)
	if !ok {
		return
	}
	if !kubernetesClusterReady(cluster) {
		fail(w, 409, "CloFix resources can be attached after the cluster is running")
		return
	}
	var in struct {
		Kind       string `json:"kind"`
		ResourceID string `json:"resource_id"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
		Selector   string `json:"selector"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		fail(w, 400, "invalid integration")
		return
	}
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.ResourceID = strings.TrimSpace(in.ResourceID)
	in.Namespace = strings.TrimSpace(in.Namespace)
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Selector = strings.TrimSpace(in.Selector)
	if in.Namespace == "" {
		in.Namespace = "default"
	}
	if in.ResourceID == "" {
		fail(w, 400, "resource_id is required")
		return
	}
	yaml, item, err := s.buildKubernetesIntegration(principalFrom(r), in.Kind, in.ResourceID, in.Namespace, in.Name, in.Selector)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	control, ok := kubernetesControlNode(cluster)
	if !ok {
		fail(w, 409, "control plane node is missing")
		return
	}
	op := kubernetes.Operation{Action: kubernetes.ActionApply, Spec: kubernetes.Spec{ID: cluster.ID, Version: cluster.Version, InfrastructureMode: cluster.InfrastructureMode, Role: kubernetes.RoleControlPlane, CPU: cluster.NodePool.CPU, RAMMB: cluster.NodePool.RAMMB, DiskMB: cluster.NodePool.DiskMB}, YAML: yaml, Namespace: item.Namespace, Name: item.K8sName, Kind: item.K8sKind}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	if s.kubernetesClusterBusyLocked(current.ID) {
		fail(w, 409, "cluster has an active operation")
		return
	}
	for _, existing := range current.Integrations {
		if existing.Kind == item.Kind && existing.ResourceID == item.ResourceID && existing.Namespace == item.Namespace {
			fail(w, 409, "that CloFix resource is already attached to this cluster")
			return
		}
	}
	before := s.kubernetesCheckpoint()
	item.Status = "PENDING"
	current.Integrations = append(current.Integrations, item)
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Attach "+item.Kind+" "+item.Name, "info")
	d.Clusters[current.ID] = current
	job, err := s.queueKubernetesLocked(current, control, kubernetes.ActionApply, op)
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(current.Owner, "kubernetes.integration", current.ID, item.Kind)
	writeJSON(w, 202, map[string]any{"job_id": job.ID, "integration": item})
}

func (s *Server) buildKubernetesIntegration(p iamPrincipal, kind, id, ns, name, selector string) (string, model.Integration, error) {
	admin := p.Kind == "admin"
	item := model.Integration{Kind: kind, ResourceID: id, Namespace: ns, Status: "PENDING"}
	switch kind {
	case "lb", "load-balancer", "loadbalancer":
		s.balancers.Lock()
		b := s.balancers.Items[id]
		s.balancers.Unlock()
		if b == nil || b.Deleting {
			return "", item, fmt.Errorf("load balancer not found")
		}
		if !admin && !s.canAccessAccount(p, b.AccountID) {
			return "", item, fmt.Errorf("load balancer not found")
		}
		item.Kind, item.Name, item.K8sKind = "lb", b.Name, "Service"
		item.K8sName = kubernetesObjectName(name, b.Slug, b.Name, id)
		if selector == "" {
			selector = item.K8sName
		}
		item.URL = clofixLBURL(b)
		yaml := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  annotations:
    clofix.io/load-balancer: %s
spec:
  type: LoadBalancer
  selector:
    app: %s
  ports:
  - name: http
    port: 80
    targetPort: 80
`, item.K8sName, ns, b.ID, selector)
		return yaml, item, nil
	case "volume":
		vol, err := s.Store.GetVolume(id)
		if err != nil {
			return "", item, fmt.Errorf("volume not found")
		}
		if !admin && !s.canAccessAccount(p, vol.AccountID) {
			return "", item, fmt.Errorf("volume not found")
		}
		item.Kind, item.Name, item.K8sKind = "volume", vol.Name, "PersistentVolumeClaim"
		item.K8sName = kubernetesObjectName(name, vol.Name, vol.ID)
		storage := "1Gi"
		if vol.LimitMB > 0 {
			storage = strconv.FormatInt(vol.LimitMB, 10) + "Mi"
		}
		yaml := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
  annotations:
    clofix.io/volume: %s
spec:
  capacity:
    storage: %s
  accessModes: ["ReadWriteOnce"]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: cks-clofix-volume
  hostPath:
    path: /var/lib/clofix-filevps/volumes/%s
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  annotations:
    clofix.io/volume: %s
spec:
  accessModes: ["ReadWriteOnce"]
  storageClassName: cks-clofix-volume
  resources:
    requests:
      storage: %s
`, item.K8sName, vol.ID, storage, vol.ID, item.K8sName, ns, vol.ID, storage)
		return yaml, item, nil
	case "efs":
		s.efs.Lock()
		share, ok := s.efs.Shares[id]
		s.efs.Unlock()
		if !ok {
			return "", item, fmt.Errorf("EFS share not found")
		}
		if !admin && !s.canAccessAccount(p, share.AccountID) {
			return "", item, fmt.Errorf("EFS share not found")
		}
		item.Kind, item.Name, item.K8sKind = "efs", share.Name, "PersistentVolumeClaim"
		item.K8sName = kubernetesObjectName(name, share.Name, share.ID)
		yaml := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
  annotations:
    clofix.io/efs: %s
spec:
  capacity:
    storage: 1Gi
  accessModes: ["ReadWriteMany"]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: cks-clofix-efs
  hostPath:
    path: /var/lib/clofix-filevps/efs/%s
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  annotations:
    clofix.io/efs: %s
spec:
  accessModes: ["ReadWriteMany"]
  storageClassName: cks-clofix-efs
  resources:
    requests:
      storage: 1Gi
`, item.K8sName, share.ID, share.ID, item.K8sName, ns, share.ID)
		return yaml, item, nil
	case "s3":
		conn, err := s.S3.GetConnection(id)
		if err != nil {
			return "", item, fmt.Errorf("S3 connection not found")
		}
		item.Kind, item.Name, item.K8sKind = "s3", conn.Name, "Secret"
		item.K8sName = kubernetesObjectName(name, conn.Name, conn.ID)
		yaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  annotations:
    clofix.io/s3: %s
type: Opaque
stringData:
  endpoint: %s
  bucket: %s
  region: %s
  prefix: %s
  aws_access_key_id: %s
  aws_secret_access_key: %s
`, item.K8sName, ns, conn.ID, yamlQuote(conn.Endpoint), yamlQuote(conn.Bucket), yamlQuote(conn.Region), yamlQuote(conn.Prefix), yamlQuote(conn.AccessKey), yamlQuote(conn.SecretKey))
		return yaml, item, nil
	case "vps":
		inst, err := s.Store.GetInstance(id)
		if err != nil {
			return "", item, fmt.Errorf("FileVPS not found")
		}
		if !admin && !s.canAccessAccount(p, inst.AccountID) {
			return "", item, fmt.Errorf("FileVPS not found")
		}
		item.Kind, item.Name, item.K8sKind = "vps", inst.Spec.Hostname, "Service"
		item.K8sName = kubernetesObjectName(name, inst.Spec.Hostname, inst.ID)
		host := strings.TrimSpace(inst.Spec.IPAddress)
		if host == "" {
			host = "127.0.0.1"
		}
		yaml := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  annotations:
    clofix.io/vps: %s
spec:
  ports:
  - name: http
    port: 80
    targetPort: 80
---
apiVersion: v1
kind: Endpoints
metadata:
  name: %s
  namespace: %s
subsets:
- addresses:
  - ip: %s
  ports:
  - name: http
    port: 80
`, item.K8sName, ns, inst.ID, item.K8sName, ns, host)
		return yaml, item, nil
	default:
		return "", item, fmt.Errorf("kind must be lb, volume, efs, s3 or vps")
	}
}

func clofixLBURL(b *loadBalancer) string {
	if b == nil {
		return ""
	}
	if b.PublicIP != "" && b.ListenPort > 0 {
		return fmt.Sprintf("http://%s:%d", b.PublicIP, b.ListenPort)
	}
	if b.Slug != "" {
		return "http://127.0.0.1/" + b.Slug
	}
	return "http://127.0.0.1:8080/cks-lab"
}

func kubernetesObjectName(parts ...string) string {
	for _, part := range parts {
		n := strings.ToLower(strings.TrimSpace(part))
		n = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			if r == '_' || r == '.' || r == ' ' {
				return '-'
			}
			return -1
		}, n)
		n = strings.Trim(n, "-")
		if kubernetes.Name.MatchString(n) {
			return n
		}
	}
	return "cks-res"
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func buildKubernetesResourceYAML(in kubernetesResourceRequest) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	name := strings.ToLower(strings.TrimSpace(in.Name))
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		ns = "default"
	}
	if !kubernetes.Name.MatchString(name) {
		return "", fmt.Errorf("name must be a DNS label")
	}
	if kind != "namespace" && !kubernetes.Name.MatchString(ns) {
		return "", fmt.Errorf("namespace must be a DNS label")
	}
	image := strings.TrimSpace(in.Image)
	if image == "" {
		image = "nginx:1.27-alpine"
	}
	if strings.ContainsAny(image, " \t\r\n") || strings.Contains(image, "..") {
		return "", fmt.Errorf("image must be a safe container reference")
	}
	replicas := in.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	port := in.Port
	if port <= 0 {
		port = 80
	}
	storage := strings.TrimSpace(in.Storage)
	if storage == "" {
		storage = "1Gi"
	}
	selector := strings.TrimSpace(in.Selector)
	if selector == "" {
		selector = name
	}
	svcType := strings.TrimSpace(in.Type)
	if svcType == "" {
		svcType = "ClusterIP"
	}
	switch kind {
	case "namespace":
		return fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", name), nil
	case "deployment":
		return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: %s
        ports:
        - containerPort: %d
`, name, ns, replicas, selector, selector, name, image, port), nil
	case "service":
		return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  type: %s
  selector:
    app: %s
  ports:
  - name: http
    port: %d
    targetPort: %d
`, name, ns, svcType, selector, port, port), nil
	case "secret":
		body := fmt.Sprintf("apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\n", name, ns)
		if len(in.Data) == 0 {
			return body, nil
		}
		body += "stringData:\n"
		for k, v := range in.Data {
			if strings.TrimSpace(k) == "" {
				continue
			}
			body += "  " + k + ": " + yamlQuote(v) + "\n"
		}
		return body, nil
	case "configmap":
		body := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\n", name, ns)
		if len(in.Data) == 0 {
			return body, nil
		}
		body += "data:\n"
		for k, v := range in.Data {
			if strings.TrimSpace(k) == "" {
				continue
			}
			body += "  " + k + ": " + yamlQuote(v) + "\n"
		}
		return body, nil
	case "persistentvolume", "pv":
		return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
spec:
  capacity:
    storage: %s
  accessModes: ["ReadWriteOnce"]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: cks-local-path
  hostPath:
    path: /var/lib/clofix-filevps/pvs/%s
`, name, storage, name), nil
	case "persistentvolumeclaim", "pvc":
		return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: %s
`, name, ns, storage), nil
	case "job":
		return fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: %s
        image: %s
`, name, ns, name, image), nil
	case "cronjob":
		schedule := strings.TrimSpace(in.Schedule)
		if schedule == "" {
			schedule = "0 * * * *"
		}
		return fmt.Sprintf(`apiVersion: batch/v1
kind: CronJob
metadata:
  name: %s
  namespace: %s
spec:
  schedule: %s
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
          - name: %s
            image: %s
`, name, ns, yamlQuote(schedule), name, image), nil
	case "ingress":
		return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
spec:
  rules:
  - http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: %s
            port:
              number: %d
`, name, ns, selector, port), nil
	case "serviceaccount":
		return fmt.Sprintf("apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: %s\n  namespace: %s\n", name, ns), nil
	case "daemonset":
		return fmt.Sprintf(`apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: %s
        ports:
        - containerPort: %d
`, name, ns, selector, selector, name, image, port), nil
	case "statefulset":
		return fmt.Sprintf(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s
  namespace: %s
spec:
  serviceName: %s
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: %s
        ports:
        - containerPort: %d
`, name, ns, selector, replicas, selector, selector, name, image, port), nil
	case "networkpolicy":
		return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
spec:
  podSelector:
    matchLabels:
      app: %s
  policyTypes: ["Ingress"]
`, name, ns, selector), nil
	case "hpa", "horizontalpodautoscaler":
		max := replicas
		if max < 2 {
			max = 3
		}
		target := selector
		if target == "" {
			target = name
		}
		return fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s
  namespace: %s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: 1
  maxReplicas: %d
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
`, name, ns, target, max), nil
	default:
		return "", fmt.Errorf("unsupported resource kind")
	}
}
