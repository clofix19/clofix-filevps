package proto

import (
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"time"
)

type ManagedTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RAMMB       int64  `json:"ram_mb"`
}

// WebDeployment is the client supplied application package for a managed web
// app. The controller validates and persists the artifact, then sends it only
// to the selected worker over the existing authenticated operation channel.
type WebDeployment struct {
	Runtime  string            `json:"runtime,omitempty"`
	Entry    string            `json:"entry,omitempty"`
	Artifact []byte            `json:"artifact,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

var ManagedTemplates = []ManagedTemplate{
	{"adminer", "Adminer", "Browser-based database management", 256},
	{"uptime-kuma", "Uptime Kuma", "Website monitoring and status dashboards", 512},
	{"web-static", "Static website", "Upload a ZIP containing HTML, CSS, JavaScript and assets", 128},
	{"web-php", "PHP website", "Upload a ZIP containing a PHP application", 256},
	{"web-nodejs", "Node.js application", "Production ZIP with package.json and node_modules", 512},
	{"web-nextjs", "Next.js application", "Production ZIP with .next and node_modules", 768},
	{"web-wordpress", "WordPress", "Upload themes/plugins or a complete WordPress content package", 512},
}
var managedID = regexp.MustCompile(`^ma-[a-f0-9]{16}$`)

type ManagedSpec struct {
	ID          string  `json:"id"`
	Template    string  `json:"template"`
	RAMMB       int64   `json:"ram_mb"`
	CPU         float64 `json:"cpu"`
	Address     string  `json:"address"`
	Port        int     `json:"port"`
	GatewayHash string  `json:"gateway_hash"`
	Deployment  WebDeployment `json:"deployment,omitempty"`
}

func ValidateManaged(s ManagedSpec) error {
	if !managedID.MatchString(s.ID) || s.RAMMB < 128 || s.RAMMB > 4096 || math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < 0.25 || s.CPU > 4 || s.Port < 25000 || s.Port > 25099 || len(s.GatewayHash) != 64 {
		return fmt.Errorf("invalid managed app specification")
	}
	if s.Address != fmt.Sprintf("10.231.128.%d", s.Port-25000+2) {
		return fmt.Errorf("invalid managed app address")
	}
	if decoded, err := hex.DecodeString(s.GatewayHash); err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid gateway credential hash")
	}
	foundTemplate := false
	for _, t := range ManagedTemplates {
		if t.ID == s.Template {
			foundTemplate = true
			break
		}
	}
	if !foundTemplate {
		return fmt.Errorf("unknown managed app template")
	}
	if s.Deployment.Runtime != "" {
		switch s.Deployment.Runtime {
		case "static", "php", "nodejs", "nextjs", "wordpress":
		default:
			return fmt.Errorf("unsupported web deployment runtime")
		}
		if len(s.Deployment.Artifact) == 0 || len(s.Deployment.Artifact) > 16<<20 {
			return fmt.Errorf("web deployment ZIP must contain 1 byte to 16 MiB")
		}
	}
	return nil
}

type ManagedStatus struct {
	OperationID string    `json:"operation_id,omitempty"`
	ID          string    `json:"id"`
	State       string    `json:"state"`
	Desired     string    `json:"desired"`
	Error       string    `json:"error,omitempty"`
	Ready       bool      `json:"ready"`
	Created     time.Time `json:"created"`
	Logs        string    `json:"logs,omitempty"`
}
