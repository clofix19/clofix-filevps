package docker

import "testing"

func TestContainerSpecValidateAndImageRef(t *testing.T) {
	id := "dck-" + "0123456789abcdef"
	s := ContainerSpec{ID: id, Name: "web", Image: "nginx", Tag: "alpine", RAMMB: 128, CPU: 0.5}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if ref := s.ImageRef(); ref != "nginx:alpine" {
		t.Fatalf("image ref %s", ref)
	}
	s.Digest = "sha256:deadbeef"
	if !containsAt(s.ImageRef()) {
		t.Fatalf("digest ref %s", s.ImageRef())
	}
	s.Privileged = true
	if err := s.Validate(); err == nil {
		t.Fatal("privileged")
	}
}

func containsAt(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return true
		}
	}
	return false
}

func TestHostPortRange(t *testing.T) {
	if HostPortBase != 18000 || HostPortCount != 4000 {
		t.Fatalf("unexpected host port band %d+%d", HostPortBase, HostPortCount)
	}
}
