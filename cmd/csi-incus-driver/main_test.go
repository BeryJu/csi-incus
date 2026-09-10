package main

import (
	"os"
	"testing"
)

func TestResolveNodeIDPrefersNodeName(t *testing.T) {
	t.Setenv("NODE_NAME", "worker-1")

	got, err := resolveNodeID()
	if err != nil {
		t.Fatalf("resolveNodeID: %v", err)
	}
	if got != "worker-1" {
		t.Errorf("resolveNodeID() = %q, want %q (a pod's hostname is its pod name, not the Node's)", got, "worker-1")
	}
}

func TestResolveNodeIDFallsBackToHostname(t *testing.T) {
	t.Setenv("NODE_NAME", "")

	want, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname unavailable: %v", err)
	}

	got, err := resolveNodeID()
	if err != nil {
		t.Fatalf("resolveNodeID: %v", err)
	}
	if got != want {
		t.Errorf("resolveNodeID() = %q, want os.Hostname() = %q", got, want)
	}
}
