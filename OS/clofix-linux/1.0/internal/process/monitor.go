package process

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ProcInfo struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	UID     int    `json:"uid"`
	Comm    string `json:"comm"`
	Cmdline string `json:"cmdline"`
	State   string `json:"state"`
}

type Status struct {
	Count          int        `json:"count"`
	RootProcs      []ProcInfo `json:"root_processes"`
	ListenersHint  []string   `json:"listening_hint"`
	Suspicious     []string   `json:"suspicious_indicators"`
	TerminateArmed bool       `json:"terminate_armed"`
	Note           string     `json:"note"`
}

func Snapshot(allowTerminate bool) (Status, error) {
	st := Status{
		TerminateArmed: allowTerminate,
		Note:           "Automated termination requires explicit policy rule; names alone never trigger kill",
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return st, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		info, err := readProc(pid)
		if err != nil {
			continue
		}
		st.Count++
		if info.UID == 0 {
			st.RootProcs = append(st.RootProcs, info)
		}
		cl := strings.ToLower(info.Cmdline + " " + info.Comm)
		// Indicators only — never terminate from this list
		for _, needle := range []string{"nc -l", "ncat -l", "crypto_miner", "xmrig", "/dev/tcp/"} {
			if strings.Contains(cl, needle) {
				st.Suspicious = append(st.Suspicious, info.Cmdline)
			}
		}
	}
	st.ListenersHint = listeningFromProc()
	return st, nil
}

func readProc(pid int) (ProcInfo, error) {
	base := filepath.Join("/proc", strconv.Itoa(pid))
	comm, _ := os.ReadFile(filepath.Join(base, "comm"))
	cmdlineB, _ := os.ReadFile(filepath.Join(base, "cmdline"))
	cmdline := strings.ReplaceAll(string(cmdlineB), "\x00", " ")
	statusB, err := os.ReadFile(filepath.Join(base, "status"))
	if err != nil {
		return ProcInfo{}, err
	}
	info := ProcInfo{PID: pid, Comm: strings.TrimSpace(string(comm)), Cmdline: strings.TrimSpace(cmdline)}
	for _, line := range strings.Split(string(statusB), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			info.PPID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
		}
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) > 0 {
				info.UID, _ = strconv.Atoi(fields[0])
			}
		}
		if strings.HasPrefix(line, "State:") {
			info.State = strings.TrimSpace(strings.TrimPrefix(line, "State:"))
		}
	}
	return info, nil
}

func listeningFromProc() []string {
	var out []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if i == 0 || strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			// state 0A = LISTEN
			if fields[3] == "0A" {
				out = append(out, fields[1])
			}
		}
	}
	return out
}
