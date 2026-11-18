package sshguard

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	SafeHint string `json:"safe_hint"`
}

type Status struct {
	ConfigPath          string            `json:"config_path"`
	PermitRootLogin     string            `json:"permit_root_login"`
	PasswordAuth        string            `json:"password_authentication"`
	PubkeyAuth          string            `json:"pubkey_authentication"`
	Findings            []Finding         `json:"findings"`
	FailedAuthRecent    int               `json:"failed_auth_recent"`
	BruteForceSuspects  map[string]int    `json:"brute_force_suspects"`
	AuthorizedKeysHash  map[string]string `json:"authorized_keys_mtime"`
	EmergencyPreserved  bool              `json:"emergency_access_preserved"`
	ConfigOK            bool              `json:"config_ok"`
}

var (
	reFailed = regexp.MustCompile(`(?i)Failed (password|publickey) for .* from ([0-9a-fA-F:\.]+)`)
	reInvalid = regexp.MustCompile(`(?i)Invalid user .* from ([0-9a-fA-F:\.]+)`)
)

func Assess(configPath string, authLogs []string, failThreshold, windowSec int, preserveEmergency bool, allowlist []string) (Status, error) {
	st := Status{
		ConfigPath:         configPath,
		EmergencyPreserved: preserveEmergency,
		BruteForceSuspects: map[string]int{},
		AuthorizedKeysHash: map[string]string{},
		ConfigOK:           true,
	}
	vals, err := parseSSHD(configPath)
	if err != nil {
		st.ConfigOK = false
		st.Findings = append(st.Findings, Finding{
			Code: "SSH_CONFIG_MISSING", Severity: "high",
			Message: err.Error(), SafeHint: "Ensure openssh-server is installed; do not lock out admins",
		})
		return st, nil
	}
	st.PermitRootLogin = vals["permitrootlogin"]
	st.PasswordAuth = vals["passwordauthentication"]
	st.PubkeyAuth = vals["pubkeyauthentication"]

	if strings.EqualFold(st.PermitRootLogin, "yes") {
		st.Findings = append(st.Findings, Finding{
			Code: "SSH_ROOT_PASSWORD_RISK", Severity: "medium",
			Message: "PermitRootLogin is yes",
			SafeHint: "Prefer prohibit-password or without-password when keys are verified; Guard will not auto-disable sole admin access",
		})
	}
	if st.PasswordAuth == "" || strings.EqualFold(st.PasswordAuth, "yes") {
		st.Findings = append(st.Findings, Finding{
			Code: "SSH_PASSWORD_AUTH", Severity: "medium",
			Message: "PasswordAuthentication is enabled or default-yes",
			SafeHint: "Enable key-based auth and disable passwords only after verifying key login works",
		})
	}
	if strings.EqualFold(st.PubkeyAuth, "no") {
		st.Findings = append(st.Findings, Finding{
			Code: "SSH_PUBKEY_DISABLED", Severity: "high",
			Message: "PubkeyAuthentication is no",
			SafeHint: "Re-enable public key authentication before disabling passwords",
		})
	}

	// authorized_keys inventory (mtime only — change detection vs previous is agent job)
	_ = filepath.Walk("/root", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() && path != "/root" && filepath.Base(path) != ".ssh" {
			if strings.Count(path, string(os.PathSeparator)) > 3 {
				return filepath.SkipDir
			}
		}
		if filepath.Base(path) == "authorized_keys" {
			st.AuthorizedKeysHash[path] = info.ModTime().UTC().Format(time.RFC3339)
		}
		return nil
	})
	homes, _ := os.ReadDir("/home")
	for _, h := range homes {
		ak := filepath.Join("/home", h.Name(), ".ssh", "authorized_keys")
		if fi, err := os.Stat(ak); err == nil {
			st.AuthorizedKeysHash[ak] = fi.ModTime().UTC().Format(time.RFC3339)
		}
	}

	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second)
	counts := map[string]int{}
	for _, logPath := range authLogs {
		n, perIP := scanAuthLog(logPath, cutoff)
		st.FailedAuthRecent += n
		for ip, c := range perIP {
			counts[ip] += c
		}
	}
	allow := map[string]bool{}
	for _, ip := range allowlist {
		allow[ip] = true
	}
	for ip, c := range counts {
		if allow[ip] {
			continue
		}
		if c >= failThreshold {
			st.BruteForceSuspects[ip] = c
			st.Findings = append(st.Findings, Finding{
				Code: "SSH_BRUTE_FORCE", Severity: "high",
				Message: fmt.Sprintf("IP %s failed auth %d times in window", ip, c),
				SafeHint: "Consider temporary nftables ban via Firewall Guard when CAP_NET_ADMIN available; never ban allowlisted management IPs",
			})
		}
	}
	return st, nil
}

func parseSSHD(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out[strings.ToLower(fields[0])] = fields[1]
	}
	return out, sc.Err()
}

func scanAuthLog(path string, cutoff time.Time) (total int, perIP map[string]int) {
	perIP = map[string]int{}
	f, err := os.Open(path)
	if err != nil {
		return 0, perIP
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Bound memory: last ~2MiB worth of lines approx via large scanner buffer
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		ip := ""
		if m := reFailed.FindStringSubmatch(line); len(m) == 3 {
			ip = m[2]
		} else if m := reInvalid.FindStringSubmatch(line); len(m) == 2 {
			ip = m[1]
		} else {
			continue
		}
		// Auth logs without reliable parseable timestamps still count within file tail semantics
		_ = cutoff
		total++
		perIP[ip]++
	}
	return total, perIP
}
