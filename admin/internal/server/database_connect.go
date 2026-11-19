package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/database"
	k8smodel "github.com/clofix/filevps/admin/internal/kubernetes"
	k8s "github.com/clofix/filevps/worker/pkg/kubernetes"
)

var secretNameOK = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type k8sDatabaseAttach struct {
	Database string
	Username string
	Password string
	Env      map[string]string
}

func databaseURL(engine, host string, port int, name, user, password string) string {
	u := url.URL{Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + name, User: url.UserPassword(user, password)}
	q := u.Query()
	switch engine {
	case "postgresql":
		u.Scheme = "postgres"
		q.Set("sslmode", "verify-ca")
	case "mongodb":
		u.Scheme = "mongodb"
		q.Set("tls", "true")
		q.Set("authSource", "admin")
	case "redis":
		u.Scheme = "rediss"
		u.Path = "/0"
		q.Set("prefix", name)
	default:
		u.Scheme = "mysql"
		q.Set("ssl-mode", "VERIFY_CA")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// databaseVPCHost is the FileVPS bridge gateway. Managed databases listen on the
// worker host port band; VPC peers reach them via this address and Spec.Port.
func databaseVPCHost(model.Project) string { return "10.231.0.1" }

func databaseConnectionEnv(project model.Project, database, user, password string) map[string]string {
	vpcHost := databaseVPCHost(project)
	env := map[string]string{
		"DB_ENGINE":      project.Spec.Engine,
		"DB_HOST":        project.Spec.Host,
		"DB_PORT":        strconv.Itoa(project.Spec.Port),
		"DB_VPC_HOST":    vpcHost,
		"DB_VPC_PORT":    strconv.Itoa(project.Spec.Port),
		"DB_NAME":        database,
		"DB_USER":        user,
		"DB_PASSWORD":    password,
		"DATABASE_URL":   databaseURL(project.Spec.Engine, project.Spec.Host, project.Spec.Port, database, user, password),
		"DATABASE_VPC_URL": databaseURL(project.Spec.Engine, vpcHost, project.Spec.Port, database, user, password),
	}
	if project.CA != "" {
		env["DB_CA"] = project.CA
	}
	if project.Spec.Engine == "redis" {
		env["REDIS_URL"] = env["DATABASE_URL"]
		env["REDIS_PREFIX"] = database
	}
	return env
}

func databaseEnvFile(env map[string]string) string {
	order := []string{"DB_ENGINE", "DB_HOST", "DB_PORT", "DB_VPC_HOST", "DB_VPC_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DATABASE_URL", "DATABASE_VPC_URL", "DB_CA", "REDIS_URL", "REDIS_PREFIX"}
	var b strings.Builder
	seen := map[string]bool{}
	for _, key := range order {
		if value, ok := env[key]; ok {
			fmt.Fprintf(&b, "%s=%s\n", key, strings.ReplaceAll(value, "\n", `\n`))
			seen[key] = true
		}
	}
	for key, value := range env {
		if !seen[key] {
			fmt.Fprintf(&b, "%s=%s\n", key, strings.ReplaceAll(value, "\n", `\n`))
		}
	}
	return b.String()
}

func (s *Server) lookupReadyDatabase(r *http.Request, projectID, database, username string) (model.Project, error) {
	if s.databases == nil {
		return model.Project{}, fmt.Errorf("managed databases unavailable")
	}
	s.databases.Lock()
	defer s.databases.Unlock()
	project, ok := s.databases.Projects[projectID]
	if !ok || !canAccessApplication(r, project.Owner) {
		return model.Project{}, fmt.Errorf("database instance not found")
	}
	if project.State != "READY" {
		return model.Project{}, fmt.Errorf("database instance is not ready")
	}
	if database != "" && !project.Databases[database] {
		return model.Project{}, fmt.Errorf("logical database not found")
	}
	if username != "" && project.Users[username] == nil {
		return model.Project{}, fmt.Errorf("database user not found")
	}
	return project, nil
}

func (s *Server) createDatabaseConnectionSecret(owner, name, description string, env map[string]string) (map[string]any, error) {
	if s.secrets == nil {
		return nil, fmt.Errorf("secrets manager unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" || !secretNameOK.MatchString(name) {
		return nil, fmt.Errorf("secret name must start with a letter and use up to 64 letters, digits, _ or -")
	}
	plain, err := encodeSecretPlain("key_value", "", env)
	if err != nil {
		return nil, err
	}
	cipherText, err := s.encryptVaultSecret(plain)
	if err != nil {
		return nil, fmt.Errorf("cannot encrypt secret")
	}
	now := time.Now().UTC()
	id := "sec-" + randHex(8)
	sec := vaultSecret{
		ID: id, Name: name, Description: strings.TrimSpace(description), Type: "key_value",
		Ciphertext: cipherText, Owner: owner, Created: now, Updated: now, Version: 1,
	}
	d := s.secrets
	d.Lock()
	for _, existing := range d.Secrets {
		if existing.Owner == owner && strings.EqualFold(existing.Name, name) {
			d.Unlock()
			return nil, fmt.Errorf("secret name already exists in this account")
		}
	}
	d.Secrets[id] = sec
	err = d.save()
	d.Unlock()
	if err != nil {
		return nil, err
	}
	out := publicSecret(sec)
	out["values"] = env
	out["warning"] = "Secret stored in Secrets Manager. Use sec:" + name + ":DB_HOST (and related keys) from Docker, Applications, or sync to CKS."
	return out, nil
}

func defaultDBSecretName(projectID, database string) string {
	short := strings.TrimPrefix(projectID, "dbp-")
	if len(short) > 8 {
		short = short[:8]
	}
	name := "db-" + short + "-" + database
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	if !secretNameOK.MatchString(name) {
		return "db-" + short
	}
	return name
}

func (s *Server) kubernetesClusterByID(id string) (k8smodel.Cluster, bool) {
	if s.kubernetes == nil {
		return k8smodel.Cluster{}, false
	}
	s.kubernetes.Lock()
	defer s.kubernetes.Unlock()
	cluster, ok := s.kubernetes.Clusters[id]
	return cluster, ok
}

func (s *Server) attachDatabaseToCluster(r *http.Request, clusterID, projectID, namespace, name string, attach k8sDatabaseAttach) (k8smodel.Integration, string, error) {
	cluster, ok := s.kubernetesClusterByID(clusterID)
	if !ok || !canAccessApplication(r, cluster.Owner) {
		return k8smodel.Integration{}, "", fmt.Errorf("cluster not found")
	}
	if !kubernetesClusterReady(cluster) {
		return k8smodel.Integration{}, "", fmt.Errorf("CloFix resources can be attached after the cluster is running")
	}
	control, ok := kubernetesControlNode(cluster)
	if !ok {
		return k8smodel.Integration{}, "", fmt.Errorf("control plane node is missing")
	}
	if namespace == "" {
		namespace = "default"
	}
	yaml, item, err := s.buildKubernetesIntegration(principalFrom(r), "database", projectID, namespace, name, "", attach)
	if err != nil {
		return k8smodel.Integration{}, "", err
	}
	op := k8s.Operation{
		Action: k8s.ActionApply,
		Spec: k8s.Spec{
			ID: cluster.ID, Version: cluster.Version, InfrastructureMode: cluster.InfrastructureMode,
			Role: k8s.RoleControlPlane, CPU: cluster.NodePool.CPU, RAMMB: cluster.NodePool.RAMMB, DiskMB: cluster.NodePool.DiskMB,
		},
		YAML: yaml, Namespace: item.Namespace, Name: item.K8sName, Kind: item.K8sKind,
	}
	d := s.kubernetes
	d.Lock()
	defer d.Unlock()
	current := d.Clusters[cluster.ID]
	if s.kubernetesClusterBusyLocked(current.ID) {
		return k8smodel.Integration{}, "", fmt.Errorf("cluster has an active operation")
	}
	for _, existing := range current.Integrations {
		if existing.Kind == item.Kind && existing.ResourceID == item.ResourceID && existing.Namespace == item.Namespace {
			return k8smodel.Integration{}, "", fmt.Errorf("that CloFix database is already attached to this cluster namespace")
		}
	}
	before := s.kubernetesCheckpoint()
	item.Status = "PENDING"
	current.Integrations = append(current.Integrations, item)
	current.Updated = time.Now().UTC()
	current.Events = appendClusterEvent(current.Events, "Attach database "+item.Name, "info")
	d.Clusters[current.ID] = current
	job, err := s.queueKubernetesLocked(current, control, k8s.ActionApply, op)
	if err == nil {
		err = s.saveKubernetes(before)
	}
	if err != nil {
		return k8smodel.Integration{}, "", err
	}
	return item, job.ID, nil
}

func (s *Server) connectDatabaseProject(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	projectID := r.PathValue("id")
	var in struct {
		Target      string `json:"target"`
		Database    string `json:"database"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		Name        string `json:"name"`
		ClusterID   string `json:"cluster_id"`
		Namespace   string `json:"namespace"`
		Description string `json:"description"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || json.Unmarshal(raw, &in) != nil {
		fail(w, 400, "invalid connect request")
		return
	}
	in.Target = strings.ToLower(strings.TrimSpace(in.Target))
	in.Database = strings.TrimSpace(in.Database)
	in.Username = strings.TrimSpace(in.Username)
	in.Name = strings.TrimSpace(in.Name)
	in.ClusterID = strings.TrimSpace(in.ClusterID)
	in.Namespace = strings.TrimSpace(in.Namespace)
	if in.Namespace == "" {
		in.Namespace = "default"
	}
	switch in.Target {
	case "env", "secret", "vps", "docker", "application", "cks":
	default:
		fail(w, 400, "target must be env, secret, vps, docker, application, or cks")
		return
	}
	if in.Database == "" || in.Username == "" || in.Password == "" {
		fail(w, 400, "database, username and password are required")
		return
	}
	if len(in.Password) < 16 || len(in.Password) > 128 || strings.ContainsAny(in.Password, "\x00\r\n") {
		fail(w, 400, "database password must be 16-128 characters")
		return
	}
	project, err := s.lookupReadyDatabase(r, projectID, in.Database, in.Username)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	env := databaseConnectionEnv(project, in.Database, in.Username, in.Password)
	out := map[string]any{
		"target":       in.Target,
		"project_id":   project.Spec.ID,
		"project_name": project.Name,
		"database":     in.Database,
		"username":     in.Username,
		"env":          env,
		"env_file":     databaseEnvFile(env),
		"uri":          env["DATABASE_URL"],
		"note":         "Clients must use TLS. Download the instance CA or use DB_CA from the secret/env bundle.",
	}
	owner := applicationOwner(r)
	secretName := in.Name
	if secretName == "" {
		secretName = defaultDBSecretName(project.Spec.ID, in.Database)
	}
	switch in.Target {
	case "env":
		out["usage"] = "Copy env_file into VPS, Docker, Application, or local clients. Prefer Secrets Manager for long-lived credentials."
	case "secret", "vps", "docker", "application":
		desc := in.Description
		if desc == "" {
			desc = fmt.Sprintf("Managed database %s/%s for %s", project.Name, in.Database, in.Target)
		}
		secret, err := s.createDatabaseConnectionSecret(owner, secretName, desc, env)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		out["secret"] = secret
		switch in.Target {
		case "vps":
			out["usage"] = "Open Secrets Manager, reveal this secret, and on the VPS prefer DB_VPC_HOST:DB_VPC_PORT (or DATABASE_VPC_URL) plus ca.pem from DB_CA. Public DB_HOST is for external clients."
		case "docker":
			out["usage"] = "In Docker env use sec:" + secretName + ":DB_HOST (and DB_PORT, DB_VPC_HOST, DB_VPC_PORT, DB_NAME, DB_USER, DB_PASSWORD, DATABASE_URL, DB_CA)."
		case "application":
			out["usage"] = "In Application env use {{secret:" + secretName + ":DB_HOST}} style refs, or paste env_file. Prefer DB_VPC_HOST from FileVPS; public DB_HOST:DB_PORT from outside the VPC."
		default:
			out["usage"] = "Secret stored. Reference keys with sec:" + secretName + ":KEY or sync to CKS."
		}
		s.auditAccount(r, owner, "database.connect."+in.Target, project.Spec.ID, secretName, nil)
	case "cks":
		if in.ClusterID == "" {
			fail(w, 400, "cluster_id is required for CKS connect")
			return
		}
		secret, err := s.createDatabaseConnectionSecret(owner, secretName, "CKS database credentials for "+project.Name+"/"+in.Database, env)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		out["secret"] = secret
		item, jobID, err := s.attachDatabaseToCluster(r, in.ClusterID, project.Spec.ID, in.Namespace, in.Name, k8sDatabaseAttach{
			Database: in.Database, Username: in.Username, Password: in.Password, Env: env,
		})
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		out["integration"] = item
		out["job_id"] = jobID
		out["usage"] = "CKS Secret includes DB_* keys and ca.pem. Use envFrom.secretRef and mount ca.pem in workloads."
		s.auditAccount(r, owner, "database.connect.cks", project.Spec.ID, in.ClusterID, nil)
	}
	writeJSON(w, 200, out)
}
