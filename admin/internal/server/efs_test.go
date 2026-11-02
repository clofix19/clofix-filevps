package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/clofix/filevps/admin/internal/objectstore"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestEFSMultiWorkerAttachPathIAMAndVolumeExclusive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	if err := s.S3.SaveConnection(objectstore.Config{
		ID: "s3-default", Name: "CloFix S3", Provider: "clofix",
		Endpoint: "http://s3pool:8333", Bucket: "clofix", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	_ = m.UpsertWorker(store.Worker{ID: "w-a", Hostname: "worker-a", Status: "active"})
	_ = m.UpsertWorker(store.Worker{ID: "w-b", Hostname: "worker-b", Status: "active"})
	_ = m.SaveInstance(store.Instance{ID: "vps-efs-a", WorkerID: "w-a", State: proto.StateRunning, Spec: proto.InstanceSpec{}})
	_ = m.SaveInstance(store.Instance{ID: "vps-efs-b", WorkerID: "w-b", State: proto.StateStopped, Spec: proto.InstanceSpec{}})
	_ = m.SaveVolume(store.Volume{ID: "vol-efs1", Name: "web-data", WorkerID: "w-a", State: "READY"})
	h := s.AdminHandler()

	if page, title, _, ok := uiPage("/efs"); !ok || page != "efs" || title != "EFS" {
		t.Fatalf("efs page: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/efs") != "storage" || permissionResource("/api/efs/efs-1/attach") != "storage" {
		t.Fatal("EFS must use storage IAM")
	}
	html := do(h, "GET", "/efs", "", tok)
	if html.Code != 200 || !strings.Contains(html.Body.String(), "EFS inventory") || !strings.Contains(html.Body.String(), `id="efs-explorer-download"`) || !strings.Contains(html.Body.String(), `id="efs-move-dialog"`) {
		t.Fatalf("page: %d %s", html.Code, html.Body.String())
	}
	if w := do(h, "GET", "/api/efs", "", ""); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}

	created := do(h, "POST", "/api/efs", `{"name":"web-root"}`, tok)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var share struct {
		ID     string `json:"id"`
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &share); err != nil || share.ID == "" || share.Prefix != "efs/"+share.ID {
		t.Fatalf("create: %s", created.Body.String())
	}

	if w := do(h, "POST", "/api/efs/"+share.ID+"/attach", `{"instance_id":"vps-efs-a","guest_path":"/var/www/../html"}`, tok); w.Code != 400 {
		t.Fatalf("dot-dot path: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/api/efs/"+share.ID+"/files?path=../secret", "", tok); w.Code != 400 {
		t.Fatalf("explorer .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/api/efs/"+share.ID+"/archive?path=../secret", "", tok); w.Code != 400 {
		t.Fatalf("archive .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/efs/"+share.ID+"/move", `{"from":"css","to":"../secret"}`, tok); w.Code != 400 {
		t.Fatalf("move .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/efs/"+share.ID+"/move", `{"from":"css","to":"css","folder":true}`, tok); w.Code != 400 {
		t.Fatalf("move same: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/efs/"+share.ID+"/move", `{"from":"css","to":"css/old","folder":true}`, tok); w.Code != 400 {
		t.Fatalf("move into self: %d %s", w.Code, w.Body.String())
	}

	first := do(h, "POST", "/api/efs/"+share.ID+"/attach", `{"instance_id":"vps-efs-a","guest_path":"/var/www/html"}`, tok)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	second := do(h, "POST", "/api/efs/"+share.ID+"/attach", `{"instance_id":"vps-efs-b","guest_path":"/var/www/html"}`, tok)
	if second.Code != 200 {
		t.Fatal(second.Body.String())
	}
	opsA, _ := m.TakeOps("w-a")
	opsB, _ := m.TakeOps("w-b")
	if len(opsA) != 1 || opsA[0].Action != proto.ActionUpdateEFS || opsA[0].EFS == nil || opsA[0].EFS.Prefix != share.Prefix || opsA[0].EFS.GuestPath != "/var/www/html" {
		t.Fatalf("worker a op: %+v", opsA)
	}
	if len(opsB) != 1 || opsB[0].Action != proto.ActionUpdateEFS || opsB[0].EFS == nil || opsB[0].EFS.Prefix != share.Prefix {
		t.Fatalf("worker b op: %+v", opsB)
	}
	if opsA[0].EFS.Prefix != opsB[0].EFS.Prefix {
		t.Fatal("both workers must receive the same prefix")
	}

	forWorkerA := s.efsSharesForWorker("w-a")
	forWorkerB := s.efsSharesForWorker("w-b")
	if len(forWorkerA) != 1 || len(forWorkerB) != 1 || forWorkerA[0].Prefix != forWorkerB[0].Prefix {
		t.Fatalf("heartbeat shares: %+v %+v", forWorkerA, forWorkerB)
	}

	if w := do(h, "DELETE", "/api/efs/"+share.ID, "", tok); w.Code != 409 {
		t.Fatalf("delete attached: %d %s", w.Code, w.Body.String())
	}

	_ = m.SaveInstance(store.Instance{ID: "vps-efs-a", WorkerID: "w-a", State: proto.StateRunning, Spec: proto.InstanceSpec{
		EFSMounts: []proto.StorageMount{{ID: share.ID, GuestPath: "/var/www/html"}},
	}})
	if w := do(h, "POST", "/api/volumes/vol-efs1/attach", `{"instance_id":"vps-efs-a","guest_path":"/var/www/html"}`, tok); w.Code != 409 {
		t.Fatalf("volume overlap EFS: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/volumes/vol-efs1/attach", `{"instance_id":"vps-efs-b","guest_path":"/mnt/data"}`, tok); w.Code != 409 {
		t.Fatalf("volume still 1:1 same worker: %d %s", w.Code, w.Body.String())
	}
	volOK := do(h, "POST", "/api/volumes/vol-efs1/attach", `{"instance_id":"vps-efs-a","guest_path":"/mnt/data"}`, tok)
	if volOK.Code != 202 && volOK.Code != 200 {
		t.Fatalf("volume attach other path: %d %s", volOK.Code, volOK.Body.String())
	}
	vol, _ := m.GetVolume("vol-efs1")
	if vol.InstanceID != "vps-efs-a" {
		t.Fatalf("volume exclusivity: %+v", vol)
	}
	if w := do(h, "POST", "/api/volumes/vol-efs1/attach", `{"instance_id":"vps-efs-a","guest_path":"/mnt/other"}`, tok); w.Code != 409 {
		t.Fatalf("volume already attached: %d %s", w.Code, w.Body.String())
	}

	_ = s.Store.SaveIAMPolicy(store.IAMPolicy{ID: "pol-efsread0000000", Name: "efs-read", Permissions: []string{"storage:read"}})
	_ = s.Store.SaveIAMUser(store.IAMUser{ID: "usr-efsread0000000", AccountID: platformAccount, Kind: "iam", Username: "efsreader", Role: "IAM User", Status: "active", PolicyIDs: []string{"pol-efsread0000000"}})
	_ = s.Store.SaveIAMAccessKey(store.IAMAccessKey{AccessKeyID: "CFXEFSREAD000001", UserID: "usr-efsread0000000", SecretHash: hashToken("secret"), Active: true})
	reader := "CFXEFSREAD000001.secret"
	if w := do(h, "GET", "/api/efs", "", reader); w.Code != 200 {
		t.Fatal("storage:read GET", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/efs", `{"name":"denied"}`, reader); w.Code != 403 {
		t.Fatal("storage:read POST", w.Code, w.Body.String())
	}

	s.dropEFSInstance("vps-efs-a")
	listed := do(h, "GET", "/api/efs", "", tok)
	if listed.Code != 200 || strings.Count(listed.Body.String(), `"instance_id":"vps-efs-a"`) != 0 {
		t.Fatalf("drop instance: %s", listed.Body.String())
	}
}

func TestEFSMoveDestAndArchiveName(t *testing.T) {
	dest, err := efsMoveDest("css/app.css", "assets/", false)
	if err != nil || dest != "assets/app.css" {
		t.Fatalf("file into folder: %s %v", dest, err)
	}
	dest, err = efsMoveDest("css", "backup/", true)
	if err != nil || dest != "backup/css" {
		t.Fatalf("folder into folder: %s %v", dest, err)
	}
	if _, err = efsMoveDest("css", "css/keep", true); err == nil {
		t.Fatal("nested folder move accepted")
	}
	if efsArchiveName("site/assets") != "assets.zip" || efsArchiveName("") != "efs.zip" {
		t.Fatal("archive names")
	}
}
