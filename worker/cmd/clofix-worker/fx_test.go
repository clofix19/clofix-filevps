package main

import "testing"

func TestCreateSwitch(t *testing.T) {
	if !createSwitch(nil, "network", true) {
		t.Fatal("default network should stay on")
	}
	if createSwitch([]string{"--no-network"}, "network", true) {
		t.Fatal("--no-network should turn networking off")
	}
	if !createSwitch([]string{"--no-protection", "--protection"}, "protection", false) {
		t.Fatal("last switch should win")
	}
	got := stripCreateSwitches([]string{"--network", "--ssh-key", "k", "id"})
	if len(got) != 3 || got[0] != "--ssh-key" {
		t.Fatalf("stripCreateSwitches = %v", got)
	}
}

func TestReorderCreateArgsKeepsIDAfterFlags(t *testing.T) {
	got := reorderCreateArgs([]string{"cfxb-debug", "--ssh-key", "k", "--hostname", "h"})
	if len(got) != 5 || got[0] != "--ssh-key" || got[1] != "k" || got[4] != "cfxb-debug" {
		t.Fatalf("reorderCreateArgs = %v", got)
	}
}
