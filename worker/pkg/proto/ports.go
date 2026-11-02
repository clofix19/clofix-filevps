package proto

import "fmt"

// Cloud-scale published host ports per worker. Local Compose may publish a
// subset; production workers bind the full ranges on the host.
const (
	HostSSHBase      = 22000
	HostSSHCount     = 5000 // 22000-26999
	HostHTTPOffset   = 5000 // HTTP  = SSH + 5000 → 27000-31999
	HostHTTPSOffset  = 10000 // HTTPS = SSH + 10000 → 32000-36999
	HostCustomBase   = 37000
	HostCustomCount  = 5000 // 37000-41999
	HostManagedBase  = 42000
	HostManagedCount = 5000 // 42000-46999
	HostDatabaseBase = 47000
	HostDatabaseCount = 5000 // 47000-51999

	// Managed apps use 10.231.128.0 and upward (away from FileVPS 10.231.0+).
	ManagedNetThirdBase = 128
	ManagedNetHosts     = 253 // guest hosts 2..254 per /24
)

func HostSSHEnd() int      { return HostSSHBase + HostSSHCount - 1 }
func HostCustomEnd() int   { return HostCustomBase + HostCustomCount - 1 }
func HostManagedEnd() int  { return HostManagedBase + HostManagedCount - 1 }
func HostDatabaseEnd() int { return HostDatabaseBase + HostDatabaseCount - 1 }

func HostHTTPPort(sshPort int) int  { return sshPort + HostHTTPOffset }
func HostHTTPSPort(sshPort int) int { return sshPort + HostHTTPSOffset }

func ValidHostSSHPort(port int) bool {
	return port >= HostSSHBase && port <= HostSSHEnd()
}

func ValidHostCustomPort(port int) bool {
	return port >= HostCustomBase && port <= HostCustomEnd()
}

func ValidHostManagedPort(port int) bool {
	return port >= HostManagedBase && port <= HostManagedEnd()
}

func ValidHostDatabasePort(port int) bool {
	return port >= HostDatabaseBase && port <= HostDatabaseEnd()
}

// ManagedAddress maps a managed-app gateway port to a private bridge address.
func ManagedAddress(port int) (string, error) {
	if !ValidHostManagedPort(port) {
		return "", fmt.Errorf("managed app port out of range")
	}
	idx := port - HostManagedBase
	third := ManagedNetThirdBase + idx/ManagedNetHosts
	host := 2 + idx%ManagedNetHosts
	if third > 255 || host > 254 {
		return "", fmt.Errorf("managed app address space exhausted")
	}
	return fmt.Sprintf("10.231.%d.%d", third, host), nil
}

func ValidManagedAddress(address string) bool {
	var a, b, c, d int
	if _, err := fmt.Sscanf(address, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil {
		return false
	}
	if a != 10 || b != 231 || c < ManagedNetThirdBase || d < 2 || d > 254 {
		return false
	}
	maxThird := ManagedNetThirdBase + (HostManagedCount-1)/ManagedNetHosts
	return c <= maxThird
}
