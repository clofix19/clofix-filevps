package agent

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/internal/capacity"
	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/pkg/proto"
)

const Version = "0.1.0"

// ReadSecret reads the enrollment token from a protected channel: a no-echo prompt on a terminal,
// or stdin when piped. It is never taken from argv or environment variables.
func ReadSecret(prompt string) (string, error) {
	fi, _ := os.Stdin.Stat()
	tty := fi != nil && fi.Mode()&os.ModeCharDevice != 0
	if tty {
		fmt.Fprint(os.Stderr, prompt)
		off := exec.Command("stty", "-echo")
		off.Stdin = os.Stdin
		off.Run()
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			on.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("empty token")
	}
	return line, nil
}

// Enroll exchanges a one-time token for a controller-issued worker certificate.
// The server certificate is verified against caFile (obtain it from the Admin UI / GET /api/ca.pem).
func Enroll(controllerURL, caFile, token string, cfg config.Config) (config.Config, error) {
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return cfg, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return cfg, errors.New("CA file has no certificate")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return cfg, err
	}
	host, _ := os.Hostname()
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkixName(host)}, key)
	if err != nil {
		return cfg, err
	}
	col := &capacity.Collector{StateDir: config.StateDir(), HostReserve: uint64(cfg.HostReserveMB) << 20}
	body, _ := json.Marshal(proto.EnrollRequest{
		Token: token, Hostname: host, Version: Version,
		CSRPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		Capacity: col.Collect(0, 0, 0),
	})
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}}
	resp, err := hc.Post(strings.TrimRight(controllerURL, "/")+"/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return cfg, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return cfg, fmt.Errorf("enrollment refused (%d): %s", resp.StatusCode, e.Error)
	}
	var er proto.EnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return cfg, err
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	sec := config.SecurityDir()
	if err := os.MkdirAll(sec, 0o700); err != nil {
		return cfg, err
	}
	files := map[string]struct {
		data []byte
		mode os.FileMode
	}{
		"worker.key": {pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600},
		"worker.crt": {[]byte(er.CertPEM), 0o644},
		"ca.pem":     {[]byte(er.CAPEM), 0o644},
	}
	for n, f := range files {
		if err := os.WriteFile(filepath.Join(sec, n), f.data, f.mode); err != nil {
			return cfg, err
		}
	}
	cfg.ControllerURL, cfg.WorkerID = strings.TrimRight(controllerURL, "/"), er.WorkerID
	return cfg, cfg.Save()
}
