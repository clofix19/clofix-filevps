package license

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func intPtr(v int) *int { return &v }

// defaultPlan is seeded once on first deploy when no plans exist.
// It is a CloFix company template for binding one customer admin/controller installation.
func defaultPlan() Plan {
	return Plan{
		ID:                      randomID("plan-"),
		Name:                    "Standard",
		MaxVPS:                  50,
		MaxWorkers:              10,
		MaxLB:                   10,
		MaxActivations:          1,
		Active:                  true,
		MaxDatabaseProjects:     intPtr(10),
		MaxDatabases:            intPtr(50),
		MaxDatabaseUsers:        intPtr(50),
		MaxDatabaseSchedules:    intPtr(20),
		MaxVolumes:              intPtr(25),
		MaxStorageGB:            intPtr(500),
		MaxS3Connections:        intPtr(10),
		MaxBackups:              intPtr(50),
		MaxApplications:         intPtr(25),
		MaxManagedApps:          intPtr(25),
		MaxDNSZones:             intPtr(5),
		MaxDNSRecords:           intPtr(500),
		MaxKubernetesClusters:   intPtr(5),
		MaxKubernetesNodes:      intPtr(25),
		MaxKubernetesNodePools:  intPtr(10),
		MaxHealthChecks:         intPtr(25),
		MaxNotificationChannels: intPtr(10),
		MaxIAMUsers:             intPtr(25),
		MaxIAMGroups:            intPtr(25),
		MaxIAMPolicies:          intPtr(50),
		MaxAccessKeys:           intPtr(50),
		MaxDockerContainers:     intPtr(100),
		MaxDockerApplications:   intPtr(50),
		MaxDockerCompose:        intPtr(25),
		MaxDockerWorkers:        intPtr(10),
		MaxDockerVolumes:        intPtr(50),
		MaxDockerBackups:        intPtr(100),
		MaxRedisClusters:        intPtr(5),
	}
}

func (s *Service) bootstrap(username, password string) error {
	changed := false
	if len(s.Admins) == 0 {
		user := username
		if user == "" {
			return fmt.Errorf("first deploy requires LICENSE_ADMIN_USER to create the company admin account")
		}
		if len(password) < 12 {
			return fmt.Errorf("first deploy requires LICENSE_ADMIN_PASSWORD (≥12 characters)")
		}
		hash, err := bcryptHash(password)
		if err != nil {
			return err
		}
		s.Admins = map[string]AdminAccount{user: {Username: user, PasswordHash: string(hash)}}
		changed = true
	}
	if len(s.Plans) == 0 {
		p := defaultPlan()
		s.Plans[p.ID] = p
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

func bcryptHash(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}
