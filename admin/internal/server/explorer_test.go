package server

import (
	"strings"
	"testing"
)

func TestExplorerPagesHaveZipAndMove(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	for _, path := range []string{"/volumes", "/storage/s3", "/efs"} {
		page := do(h, "GET", path, "", tok)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "Download folder") || !strings.Contains(page.Body.String(), "-move-dialog") {
			t.Fatalf("%s missing zip/move explorer: %d", path, page.Code)
		}
	}
}

func TestVolumeAndS3ExplorerPathValidation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	if w := do(h, "GET", "/api/volumes/vol-missing01/archive?path=../secret", "", tok); w.Code != 400 {
		t.Fatalf("volume archive .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/volumes/vol-missing01/move", `{"from":"css","to":"../secret"}`, tok); w.Code != 400 {
		t.Fatalf("volume move .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/api/storage/connections/s3-missing000001/archive?prefix=../secret", "", tok); w.Code != 400 {
		t.Fatalf("s3 archive .. : %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/storage/connections/s3-missing000001/move", `{"from":"css","to":"css/old","folder":true}`, tok); w.Code != 400 {
		t.Fatalf("s3 move into self: %d %s", w.Code, w.Body.String())
	}
}
