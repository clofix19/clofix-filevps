package scheduler

import (
	"errors"
	"testing"
	"time"
)

const gib = 1 << 30

func cand(id string, committed uint64) Candidate {
	return Candidate{ID: id, Active: true, LastHeartbeat: time.Now(), Capacity: Capacity{
		RAMTotal: 8 * gib, HostReserve: 1 * gib, RAMCommitted: committed, MemAvailable: 6 * gib,
		CPUCores: 4, DiskTotal: 100 * gib, DiskFree: 80 * gib,
	}}
}

func TestLeastLoadedPicksEmptiest(t *testing.T) {
	d, err := Place([]Candidate{cand("a", 4*gib), cand("b", 1*gib)}, Request{RAM: gib, Disk: gib, CPU: 1}, DefaultConfig(), time.Now())
	if err != nil || d.WorkerID != "b" {
		t.Fatalf("got %v %v", d.WorkerID, err)
	}
}

func TestBinPackingPicksFullest(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Strategy = BinPacking
	d, _ := Place([]Candidate{cand("a", 4*gib), cand("b", 1*gib)}, Request{RAM: gib, Disk: gib, CPU: 1}, cfg, time.Now())
	if d.WorkerID != "a" {
		t.Fatalf("got %v", d.WorkerID)
	}
}

func TestRejectsWhenFull(t *testing.T) {
	_, err := Place([]Candidate{cand("a", 6*gib)}, Request{RAM: 2 * gib}, DefaultConfig(), time.Now())
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("want ErrNoCapacity, got %v", err)
	}
}

func TestHighPSIExcluded(t *testing.T) {
	c := cand("a", 0)
	c.Capacity.PSIMemSome10 = 50
	if _, err := Place([]Candidate{c}, Request{RAM: gib}, DefaultConfig(), time.Now()); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestZRAMNotCountedAsRAM(t *testing.T) {
	c := cand("a", 6*gib)
	c.Capacity.ZRAMLogical = 100 * gib
	if _, err := Place([]Candidate{c}, Request{RAM: 2 * gib}, DefaultConfig(), time.Now()); err == nil {
		t.Fatal("ZRAM must not make room")
	}
}

func TestStaleAndDrainingExcluded(t *testing.T) {
	s := cand("stale", 0)
	s.LastHeartbeat = time.Now().Add(-5 * time.Minute)
	d := cand("drain", 0)
	d.Draining = true
	if _, err := Place([]Candidate{s, d}, Request{RAM: gib}, DefaultConfig(), time.Now()); err == nil {
		t.Fatal("expected rejection")
	}
}
