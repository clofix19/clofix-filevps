package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// s3PoolSettings controls SeaweedFS replication guidance and soft tenant quotas.
type s3PoolSettings struct {
	sync.Mutex
	path               string
	DefaultReplication string           `json:"default_replication"` // 000 lab, 001 production dual-copy
	TLSGatewayHint     string           `json:"tls_gateway_hint"`
	TenantQuotaGB      map[string]int64 `json:"tenant_quota_gb"` // account_id -> soft cap
}

func loadS3PoolSettings(dir string) *s3PoolSettings {
	d := &s3PoolSettings{
		path: filepath.Join(dir, "s3-pool-settings.json"),
		DefaultReplication: envOr("S3_DEFAULT_REPLICATION", "000"),
		TLSGatewayHint: "Terminate TLS in front of the S3 gateway (8333). Do not expose master ports 9333/19333 publicly.",
		TenantQuotaGB: map[string]int64{},
	}
	b, err := os.ReadFile(d.path)
	if err == nil {
		_ = json.Unmarshal(b, d)
	}
	if d.TenantQuotaGB == nil {
		d.TenantQuotaGB = map[string]int64{}
	}
	if d.DefaultReplication == "" {
		d.DefaultReplication = "000"
	}
	return d
}

func (d *s3PoolSettings) save() error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

func (s *Server) s3PoolRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/storage/s3-pool", s.auth(s.getS3PoolSettings))
	m.HandleFunc("PUT /api/storage/s3-pool", s.auth(s.saveS3PoolSettings))
}

func (s *Server) getS3PoolSettings(w http.ResponseWriter, r *http.Request) {
	d := s.s3Pool
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, map[string]any{
		"default_replication": d.DefaultReplication,
		"tls_gateway_hint":    d.TLSGatewayHint,
		"tenant_quota_gb":     d.TenantQuotaGB,
		"note":                "Apply replication by setting S3_DEFAULT_REPLICATION on the s3pool service and restarting it when enough volume servers are online.",
	})
}

func (s *Server) saveS3PoolSettings(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "S3 pool settings require administrator access")
		return
	}
	var in struct {
		DefaultReplication string           `json:"default_replication"`
		TenantQuotaGB      map[string]int64 `json:"tenant_quota_gb"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		fail(w, 400, "invalid S3 pool settings")
		return
	}
	switch in.DefaultReplication {
	case "000", "001", "010", "100", "011", "101", "110", "200", "020", "002":
	case "":
		in.DefaultReplication = "000"
	default:
		fail(w, 400, "unsupported SeaweedFS replication code")
		return
	}
	d := s.s3Pool
	d.Lock()
	defer d.Unlock()
	d.DefaultReplication = in.DefaultReplication
	if in.TenantQuotaGB != nil {
		d.TenantQuotaGB = in.TenantQuotaGB
	}
	if err := d.save(); err != nil {
		fail(w, 500, "could not save S3 pool settings")
		return
	}
	writeJSON(w, 200, map[string]any{
		"default_replication": d.DefaultReplication,
		"tenant_quota_gb":     d.TenantQuotaGB,
		"note":                "Controller soft quotas are recorded. Restart s3pool with S3_DEFAULT_REPLICATION=" + d.DefaultReplication + " to apply cluster replication.",
	})
}
