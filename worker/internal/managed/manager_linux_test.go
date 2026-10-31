package managed

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clofix/filevps/worker/internal/engine/namespaces"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "fx-init" {
		if err := namespaces.RunInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
func TestManagedNativeLifecycle(t *testing.T) {
	helper := os.Getenv("CLOFIX_MANAGED_TEST_BINARY")
	root := os.Getenv("CLOFIX_MANAGED_TEST_STATE")
	if helper == "" || root == "" {
		t.Skip("requires a provisioned Linux native test root")
	}
	manager := New(root)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.CG.Init(); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(manager.Templates, "adminer", "app")
	if err := os.MkdirAll(template, 0755); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(template, "server"), binary, 0555); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(template, "start"), []byte("#!/bin/sh\nexec /app/server\n"), 0555); err != nil {
		t.Fatal(err)
	}
	password := "managed-smoke-password"
	sum := sha256.Sum256([]byte(password))
	spec := proto.ManagedSpec{ID: fmt.Sprintf("ma-%016x", uint64(time.Now().UnixNano())), Template: "adminer", RAMMB: 128, CPU: 0.5, Address: "10.231.128.101", Port: 25099, GatewayHash: hex.EncodeToString(sum[:])}
	op := func(action string) error {
		return manager.Apply(proto.Operation{ID: "test/" + action, Action: "managed-app", Managed: &spec, ServiceAction: action})
	}
	t.Cleanup(func() {
		if _, err := os.Stat(manager.path(spec.ID)); err == nil {
			if err := op("delete"); err != nil {
				t.Log("cleanup:", err)
			}
		}
	})
	host, err := net.Listen("tcp", "0.0.0.0:25098")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err = op("deploy"); err != nil {
		t.Fatal("deploy:", err)
	}
	endpoint := "http://127.0.0.1:25099/"
	client := &http.Client{Timeout: 2 * time.Second}
	request := func(path string, auth bool) (*http.Response, error) {
		req, _ := http.NewRequest("GET", endpoint+path, nil)
		if auth {
			req.SetBasicAuth("client", password)
		}
		return client.Do(req)
	}
	var response *http.Response
	for i := 0; i < 30; i++ {
		response, err = request("", true)
		if err == nil && response.StatusCode == 200 {
			break
		}
		if response != nil {
			response.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("gateway: %d; statuses: %+v", response.StatusCode, manager.Statuses())
	}
	defer response.Body.Close()
	var first struct {
		UID           int    `json:"uid"`
		Count         int    `json:"count"`
		Authorization string `json:"authorization"`
		HostBlocked   bool   `json:"host_blocked"`
	}
	if err = json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.UID != 65534 || first.Count != 1 || first.Authorization != "" || !first.HostBlocked {
		t.Fatalf("isolation/auth: %+v", first)
	}
	unauth, err := request("", false)
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != 401 {
		t.Fatal("gateway did not enforce authentication")
	}
	// Simulate a worker daemon restart: restore a running sandbox and its gateway.
	manager.Lock()
	for _, server := range manager.gateways {
		server.Close()
	}
	manager.gateways = map[string]*http.Server{}
	for _, old := range manager.apps {
		if old.logReader != nil {
			old.logReader.Close()
		}
	}
	manager.apps = map[string]*record{}
	manager.Unlock()
	restored := New(root)
	if err = restored.Load(); err != nil {
		t.Fatal(err)
	}
	restored.Reconcile()
	manager = restored
	if err = op("stop"); err != nil {
		t.Fatal(err)
	}
	backup, err := request("__clofix/backup", true)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(backup.Body)
	backup.Body.Close()
	if backup.StatusCode != 200 {
		t.Fatalf("backup: %d %s", backup.StatusCode, b)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range z.File {
		if f.Name == "count" {
			found = true
		}
	}
	if !found {
		t.Fatal("persistent data missing from export")
	}
	if err = op("start"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		response, err = request("", true)
		if err == nil && response.StatusCode == 200 {
			break
		}
		if response != nil {
			response.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var second map[string]any
	_ = json.NewDecoder(response.Body).Decode(&second)
	if second["count"] != float64(2) {
		t.Fatalf("data did not persist across stop/start: %+v", second)
	}
	if err = op("delete"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(manager.path(spec.ID)); !os.IsNotExist(err) {
		t.Fatal("delete left persistent files behind")
	}
}
