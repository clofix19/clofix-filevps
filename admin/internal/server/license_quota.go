package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type quotaLockKey struct{}
type quotaEntry struct {
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Unit     string `json:"unit"`
	Included bool   `json:"included"`
}

func (s *Server) licensedPlan() (licensePlan, error) {
	d := s.license
	d.Lock()
	defer d.Unlock()
	if len(d.EncryptedKey) == 0 || !d.Grant.Valid || d.Grant.InstallationID != d.InstallationID {
		return licensePlan{}, fmt.Errorf("valid license required; open Settings > License")
	}
	if !time.Now().Before(d.Grant.Expires) {
		return licensePlan{}, fmt.Errorf("license expired; renew and validate the license")
	}
	if d.LastCheck.IsZero() || time.Since(d.LastCheck) > 10*time.Minute {
		return licensePlan{}, fmt.Errorf("license validation is stale; validate the license before allocating resources")
	}
	return d.Grant.Plan, nil
}
func quotaLimit(p licensePlan, resource string) int64 {
	var optional *int
	switch resource {
	case "database-projects":
		optional = p.MaxDatabaseProjects
	case "databases":
		optional = p.MaxDatabases
	case "database-users":
		optional = p.MaxDatabaseUsers
	case "database-schedules":
		optional = p.MaxDatabaseSchedules
	case "instances":
		return int64(p.MaxVPS)
	case "workers":
		return int64(p.MaxWorkers)
	case "load-balancers":
		return int64(p.MaxLB)
	case "health-checks":
		optional = p.MaxHealthChecks
	case "backups":
		optional = p.MaxBackups
	case "volumes":
		optional = p.MaxVolumes
	case "storage":
		optional = p.MaxStorageGB
	case "s3-connections":
		optional = p.MaxS3Connections
	case "applications":
		optional = p.MaxApplications
	case "managed-apps":
		optional = p.MaxManagedApps
	case "notification-channels":
		optional = p.MaxNotificationChannels
	case "iam-users":
		optional = p.MaxIAMUsers
	case "iam-groups":
		optional = p.MaxIAMGroups
	case "iam-policies":
		optional = p.MaxIAMPolicies
	case "access-keys":
		optional = p.MaxAccessKeys
	default:
		return 0
	}
	if optional == nil {
		return 0
	}
	return int64(*optional)
}
func (s *Server) quotaUsed(resource string) (int64, error) {
	switch resource {
	case "database-projects", "databases", "database-users", "database-schedules":
		return s.databaseQuotaUsed(resource), nil
	case "instances":
		items, e := s.Store.ListInstances()
		return int64(len(items)), e
	case "workers":
		items, e := s.Store.ListWorkers()
		n := int64(0)
		for _, w := range items {
			if w.Status != "revoked" {
				n++
			}
		}
		return n, e
	case "volumes":
		items, e := s.Store.ListVolumes()
		return int64(len(items)), e
	case "load-balancers":
		s.balancers.Lock()
		defer s.balancers.Unlock()
		return int64(len(s.balancers.Items)), nil
	case "health-checks", "notification-channels":
		s.monitoring.Lock()
		defer s.monitoring.Unlock()
		if resource == "health-checks" {
			return int64(len(s.monitoring.Monitors)), nil
		}
		return int64(len(s.monitoring.Channels)), nil
	case "applications", "managed-apps":
		s.applications.Lock()
		defer s.applications.Unlock()
		if resource == "applications" {
			return int64(len(s.applications.Apps)), nil
		}
		return int64(len(s.applications.Managed)), nil
	case "backups":
		s.usageMu.RLock()
		defer s.usageMu.RUnlock()
		return int64(len(s.backups)) + s.databaseQuotaUsed("backups"), nil
	case "s3-connections":
		items, e := s.S3.ListConnections()
		return int64(len(items)), e
	case "iam-users":
		items, e := s.Store.ListIAMUsers()
		return int64(len(items)), e
	case "iam-groups":
		items, e := s.Store.ListIAMGroups()
		return int64(len(items)), e
	case "iam-policies":
		items, e := s.Store.ListIAMPolicies()
		return int64(len(items)), e
	case "access-keys":
		users, e := s.Store.ListIAMUsers()
		if e != nil {
			return 0, e
		}
		n := int64(0)
		for _, u := range users {
			keys, e := s.Store.ListIAMAccessKeys(u.ID)
			if e != nil {
				return 0, e
			}
			n += int64(len(keys))
		}
		return n, nil
	}
	return 0, fmt.Errorf("unknown quota resource")
}
func (s *Server) checkQuota(resource string, delta int64) error {
	p, e := s.licensedPlan()
	if e != nil {
		return e
	}
	limit := quotaLimit(p, resource)
	if limit < 0 {
		return nil
	}
	used, e := s.quotaUsed(resource)
	if e != nil {
		return fmt.Errorf("could not read %s usage", resource)
	}
	if delta > 0 && used > limit-delta || delta <= 0 && used > limit {
		return fmt.Errorf("license quota exceeded for %s: used %d, limit %d, requested +%d", resource, used, limit, delta)
	}
	return nil
}
func (s *Server) storageUsed() (int64, error) {
	instances, e := s.Store.ListInstances()
	if e != nil {
		return 0, e
	}
	volumes, e := s.Store.ListVolumes()
	if e != nil {
		return 0, e
	}
	used := s.databaseStorageUsed()
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	for _, i := range instances {
		reserved := i.Spec.DiskMB << 20
		actual := s.usage[i.ID].DiskUsageBytes
		if actual > uint64(reserved) {
			reserved = int64(actual)
		}
		used += reserved
	}
	for _, v := range volumes {
		reserved := v.LimitMB << 20
		if v.UsageBytes > uint64(reserved) {
			reserved = int64(v.UsageBytes)
		}
		used += reserved
	}
	for _, b := range s.backups {
		reserved := int64(b.SizeBytes)
		if b.State == "CREATING" && b.DiskMB<<20 > reserved {
			reserved = b.DiskMB << 20
		}
		used += reserved
	}
	return used, nil
}
func (s *Server) checkStorage(delta int64, unbounded bool) error {
	if delta < 0 {
		return nil
	}
	p, e := s.licensedPlan()
	if e != nil {
		return e
	}
	limit := quotaLimit(p, "storage")
	if limit < 0 {
		return nil
	}
	if unbounded {
		return fmt.Errorf("finite storage license requires a bounded volume size")
	}
	used, e := s.storageUsed()
	if e != nil {
		return fmt.Errorf("could not read allocated storage")
	}
	capacity := limit << 30
	if used > capacity || delta > capacity-used {
		return fmt.Errorf("license storage quota exceeded: allocated %d bytes, limit %d GiB", used, limit)
	}
	return nil
}
func quotaCreation(r *http.Request) string {
	if r.Method != http.MethodPost {
		return ""
	}
	path := r.URL.Path
	switch path {
	case "/api/volumes":
		return "volumes"
	case "/api/load-balancers":
		return "load-balancers"
	case "/api/applications":
		return "applications"
	case "/api/managed-apps":
		return "managed-apps"
	case "/api/monitoring/monitors":
		return "health-checks"
	case "/api/monitoring/channels":
		return "notification-channels"
	case "/api/storage/connections":
		return "s3-connections"
	case "/api/iam/users":
		return "iam-users"
	case "/api/iam/groups":
		return "iam-groups"
	case "/api/iam/policies":
		return "iam-policies"
	}
	if strings.HasPrefix(path, "/api/instances/") && strings.HasSuffix(path, "/backups") {
		return "backups"
	}
	if strings.HasPrefix(path, "/api/iam/users/") && strings.HasSuffix(path, "/keys") {
		return "access-keys"
	}
	return ""
}
func quotaOperation(r *http.Request) string {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/instances/"):
		return "instances"
	case strings.HasPrefix(p, "/api/load-balancers/"):
		return "load-balancers"
	case strings.HasPrefix(p, "/api/applications/") || strings.HasPrefix(p, "/api/functions/"):
		return "applications"
	case strings.HasPrefix(p, "/api/managed-apps/"):
		return "managed-apps"
	case strings.HasPrefix(p, "/api/volumes/"):
		return "volumes"
	case strings.HasPrefix(p, "/api/monitoring/monitors/"):
		return "health-checks"
	case strings.HasPrefix(p, "/api/monitoring/channels/"):
		return "notification-channels"
	case strings.HasPrefix(p, "/api/storage/connections/"):
		return "s3-connections"
	case strings.HasPrefix(p, "/api/iam/users/"):
		return "iam-users"
	case strings.HasPrefix(p, "/api/iam/groups/"):
		return "iam-groups"
	case strings.HasPrefix(p, "/api/iam/policies/"):
		return "iam-policies"
	}
	return ""
}
func licenseCleanup(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/managed-apps/") && strings.HasSuffix(r.URL.Path, "/delete") || r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/stop") || strings.HasSuffix(r.URL.Path, "/cancel") || strings.HasSuffix(r.URL.Path, "/detach") || strings.HasPrefix(r.URL.Path, "/api/workers/") && (strings.HasSuffix(r.URL.Path, "/revoke") || strings.HasSuffix(r.URL.Path, "/drain"))
}
func (s *Server) licenseMutation(w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || strings.HasPrefix(r.URL.Path, "/api/license") {
		h(w, r)
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	r = r.WithContext(context.WithValue(r.Context(), quotaLockKey{}, true))
	if !licenseCleanup(r) {
		if _, e := s.licensedPlan(); e != nil {
			fail(w, 403, e.Error())
			return
		}
		if strings.Contains(r.URL.Path, "/file") || strings.Contains(r.URL.Path, "/object") || strings.HasSuffix(r.URL.Path, "/folders") || strings.HasSuffix(r.URL.Path, "/folder") {
			if p, e := s.licensedPlan(); e != nil || quotaLimit(p, "storage") == 0 {
				fail(w, 403, "storage is not included in the current license")
				return
			}
			if e := s.checkStorage(0, false); e != nil {
				fail(w, 403, e.Error())
				return
			}
		}
		resource := quotaCreation(r)
		delta := int64(1)
		if resource == "" {
			resource = quotaOperation(r)
			delta = 0
		}
		if parent := quotaOperation(r); delta == 1 && parent != "" && parent != resource {
			if e := s.checkQuota(parent, 0); e != nil {
				fail(w, 403, e.Error())
				return
			}
		}
		if resource != "" {
			if e := s.checkQuota(resource, delta); e != nil {
				fail(w, 403, e.Error())
				return
			}
		}
	}
	h(w, r)
}
func (s *Server) licenseUsage(w http.ResponseWriter, r *http.Request) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	p, e := s.licensedPlan()
	out := map[string]quotaEntry{}
	resources := []string{"database-projects", "databases", "database-users", "database-schedules", "instances", "workers", "load-balancers", "health-checks", "backups", "volumes", "s3-connections", "applications", "managed-apps", "notification-channels", "iam-users", "iam-groups", "iam-policies", "access-keys"}
	for _, resource := range resources {
		used, err := s.quotaUsed(resource)
		if err != nil {
			fail(w, 503, "could not read resource usage")
			return
		}
		limit := quotaLimit(p, resource)
		out[resource] = quotaEntry{Used: used, Limit: limit, Unit: "count", Included: limit != 0}
	}
	used, err := s.storageUsed()
	if err != nil {
		fail(w, 503, "could not read storage usage")
		return
	}
	limit := quotaLimit(p, "storage")
	if limit >= 0 {
		limit <<= 30
	}
	out["storage"] = quotaEntry{Used: used, Limit: limit, Unit: "bytes", Included: limit != 0}
	status := "Valid"
	if e != nil {
		status = e.Error()
	}
	writeJSON(w, 200, map[string]any{"enforcement": true, "valid": e == nil, "status": status, "quotas": out, "storage_scope": "VPS disk allocation/observed usage, volume reservation/usage and backup storage; external S3 and direct guest writes are not hard-capped"})
}
