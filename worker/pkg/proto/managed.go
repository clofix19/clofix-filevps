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

var ManagedTemplates = []ManagedTemplate{{"adminer", "Adminer", "Browser-based database management", 256}, {"code-server", "VS Code Web", "Browser IDE, terminal and persistent workspace", 1024}, {"uptime-kuma", "Uptime Kuma", "Website monitoring and status dashboards", 512}}
var managedID = regexp.MustCompile(`^ma-[a-f0-9]{16}$`)

type ManagedSpec struct {
	ID          string  `json:"id"`
	Template    string  `json:"template"`
	RAMMB       int64   `json:"ram_mb"`
	CPU         float64 `json:"cpu"`
	Address     string  `json:"address"`
	Port        int     `json:"port"`
	GatewayHash string  `json:"gateway_hash"`
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
	for _, t := range ManagedTemplates {
		if t.ID == s.Template {
			return nil
		}
	}
	return fmt.Errorf("unknown managed app template")
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
