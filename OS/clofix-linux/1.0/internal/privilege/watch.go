package privilege

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Finding struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
}

type Status struct {
	Users       int       `json:"user_count"`
	SUID        []string  `json:"suid_sample"`
	Findings    []Finding `json:"findings"`
	SudoersHash string    `json:"sudoers_hash"`
	PasswdHash  string    `json:"passwd_hash"`
	GroupHash   string    `json:"group_hash"`
}

func Assess() (Status, error) {
	st := Status{}
	if b, err := os.ReadFile("/etc/passwd"); err == nil {
		st.PasswdHash = sum(b)
		for _, line := range strings.Split(string(b), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			st.Users++
			parts := strings.Split(line, ":")
			if len(parts) > 2 && (parts[2] == "0") && parts[0] != "root" {
				st.Findings = append(st.Findings, Finding{Path: "/etc/passwd", Kind: "uid0", Detail: "non-root uid 0: " + parts[0]})
			}
		}
	}
	if b, err := os.ReadFile("/etc/group"); err == nil {
		st.GroupHash = sum(b)
	}
	if b, err := os.ReadFile("/etc/sudoers"); err == nil {
		st.SudoersHash = sum(b)
	}
	_ = filepath.Walk("/usr/bin", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSetuid != 0 || info.Mode()&os.ModeSetgid != 0 {
			if len(st.SUID) < 50 {
				st.SUID = append(st.SUID, path)
			}
		}
		return nil
	})
	_ = filepath.Walk("/usr/sbin", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSetuid != 0 {
			if len(st.SUID) < 80 {
				st.SUID = append(st.SUID, path)
			}
		}
		return nil
	})
	// PAM / sudoers.d presence
	if fi, err := os.Stat("/etc/pam.d"); err == nil && fi.IsDir() {
		// ok
	} else {
		st.Findings = append(st.Findings, Finding{Path: "/etc/pam.d", Kind: "missing", Detail: "PAM configs not present"})
	}
	return st, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
