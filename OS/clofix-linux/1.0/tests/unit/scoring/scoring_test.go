package scoring_test

import (
	"testing"

	"github.com/clofix/clofix-linux/internal/scoring"
)

func TestUnknownNotCountedAsPass(t *testing.T) {
	r := scoring.Compute([]scoring.Check{
		{ID: "a", Category: "ssh", Status: "pass", Weight: 50},
		{ID: "b", Category: "firewall", Status: "unknown", Weight: 50},
	})
	if r.Score != 50 {
		t.Fatalf("score=%d want 50", r.Score)
	}
	if len(r.Unknown) != 1 {
		t.Fatalf("unknown=%d", len(r.Unknown))
	}
	if len(r.Passed) != 1 {
		t.Fatalf("passed=%d", len(r.Passed))
	}
}

func TestFailReducesScore(t *testing.T) {
	r := scoring.Compute([]scoring.Check{
		{ID: "a", Category: "ssh", Status: "fail", Weight: 100},
	})
	if r.Score != 0 {
		t.Fatalf("score=%d", r.Score)
	}
}
