package lifecycle

import (
	"strings"
	"testing"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestEFSBindArgsShareOneHostDir(t *testing.T) {
	args := efsBindArgs("/var/lib/clofix-filevps", []proto.StorageMount{
		{ID: "efs-aaaa", GuestPath: "/var/www/html"},
		{ID: "efs-aaaa", GuestPath: "/mnt/shared"},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/var/lib/clofix-filevps/efs/efs-aaaa/mount /var/www/html") {
		t.Fatal(joined)
	}
	if strings.Count(joined, "/var/lib/clofix-filevps/efs/efs-aaaa/mount") != 2 {
		t.Fatal("both guests must bind the same host EFS dir", joined)
	}
}

func TestEFSMountUpsertAndDetach(t *testing.T) {
	mounts := upsertStorageMount(nil, proto.StorageMount{ID: "efs-1", GuestPath: "/var/www/html"})
	mounts = upsertStorageMount(mounts, proto.StorageMount{ID: "efs-1", GuestPath: "/srv/www"})
	if len(mounts) != 1 || mounts[0].GuestPath != "/srv/www" {
		t.Fatalf("%+v", mounts)
	}
	if got := removeStorageMount(mounts, "efs-1"); got != nil {
		t.Fatal(got)
	}
}

func TestEFSCleanupLeavesOtherGuests(t *testing.T) {
	a := []proto.StorageMount{{ID: "efs-shared", GuestPath: "/var/www/html"}}
	b := []proto.StorageMount{{ID: "efs-shared", GuestPath: "/var/www/html"}}
	if len(removeStorageMount(a, "efs-shared")) != 0 {
		t.Fatal("detach must drop the local bind")
	}
	if len(b) != 1 || b[0].ID != "efs-shared" {
		t.Fatal("other guest keep the same host dir")
	}
}
