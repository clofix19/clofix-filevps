package firewall

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Status struct {
	Available     bool     `json:"nft_available"`
	Capable       bool     `json:"net_admin_likely"`
	Tables        string   `json:"tables_preview"`
	BackupPath    string   `json:"last_backup"`
	Applied       bool     `json:"clofix_table_present"`
	SkippedReason string   `json:"skipped_reason,omitempty"`
	Warnings      []string `json:"warnings"`
}

const tableName = "clofix_guard"

func StatusNow(backupDir string) Status {
	st := Status{}
	if _, err := exec.LookPath("nft"); err != nil {
		st.SkippedReason = "nftables not installed"
		return st
	}
	st.Available = true
	out, err := exec.Command("nft", "list", "tables").CombinedOutput()
	if err != nil {
		st.SkippedReason = fmt.Sprintf("nft list failed (often missing CAP_NET_ADMIN in unprivileged VPS): %v", err)
		st.Warnings = append(st.Warnings, string(out))
		return st
	}
	st.Capable = true
	st.Tables = string(out)
	st.Applied = strings.Contains(st.Tables, tableName)
	st.BackupPath = latestBackup(backupDir)
	return st
}

func Backup(backupDir string) (string, error) {
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(backupDir, fmt.Sprintf("nft-%s.rules", time.Now().UTC().Format("20060102T150405")))
	out, err := exec.Command("nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nft list ruleset: %w (%s)", err, out)
	}
	if err := os.WriteFile(path, out, 0o640); err != nil {
		return "", err
	}
	meta := map[string]string{"path": path, "time": time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(backupDir, "latest.json"), b, 0o640)
	return path, nil
}

// ApplySSHProtect creates an additive CloFix table; does not flush foreign rules.
func ApplySSHProtect(backupDir string, sshPort int, allowIPs []string) error {
	st := StatusNow(backupDir)
	if !st.Available {
		return fmt.Errorf("UNSUPPORTED: nft not available")
	}
	if !st.Capable {
		return fmt.Errorf("UNSUPPORTED in this environment: %s", st.SkippedReason)
	}
	if _, err := Backup(backupDir); err != nil {
		return fmt.Errorf("backup before apply: %w", err)
	}
	if sshPort <= 0 {
		sshPort = 22
	}
	// Delete our table only, then recreate - never `nft flush ruleset`
	_ = exec.Command("nft", "delete", "table", "inet", tableName).Run()
	script := fmt.Sprintf(`
add table inet %s
add chain inet %s input { type filter hook input priority 0; policy accept; }
add rule inet %s input ct state established,related accept
add rule inet %s input iif lo accept
`, tableName, tableName, tableName, tableName)
	for _, ip := range allowIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		script += fmt.Sprintf("add rule inet %s input ip saddr %s tcp dport %d accept\n", tableName, ip, sshPort)
	}
	script += fmt.Sprintf("add rule inet %s input tcp dport %d ct state new limit rate 20/minute accept\n", tableName, sshPort)
	script += fmt.Sprintf("add rule inet %s input tcp dport %d counter drop\n", tableName, sshPort)
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft apply: %w (%s)", err, out)
	}
	return nil
}

func Rollback(backupDir string) error {
	latest := latestBackup(backupDir)
	if latest == "" {
		return fmt.Errorf("no firewall backup found")
	}
	// Restore full ruleset from backup - operator explicit action
	raw, err := os.ReadFile(latest)
	if err != nil {
		return err
	}
	_ = exec.Command("nft", "flush", "ruleset").Run()
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rollback failed: %w (%s)", err, out)
	}
	return nil
}

func latestBackup(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m["path"]
}
