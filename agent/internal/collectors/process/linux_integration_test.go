//go:build linux

package process

import (
	"os"
	"testing"
)

// TestLinuxAdapter_Integration runs against the real /proc filesystem.
// It proves the adapter can read the current test process.
func TestLinuxAdapter_Integration(t *testing.T) {
	adapter, err := newLinuxAdapter("/proc")
	if err != nil {
		t.Fatalf("Failed to initialize real /proc adapter: %v", err)
	}

	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}
	if len(snapshot.Instances) == 0 {
		t.Fatal("Expected at least one process instance in real /proc")
	}

	// Find this test process in the snapshot
	myPid := os.Getpid()
	var found *Instance
	for _, inst := range snapshot.Instances {
		if inst.PID == myPid {
			found = inst
			break
		}
	}

	if found == nil {
		t.Fatalf("Could not find test process PID %d in /proc", myPid)
	}

	if found.Name == nil || *found.Name == "" {
		t.Error("Test process Name should not be empty")
	}

	if found.StartTime.IsZero() {
		t.Error("Test process StartTime should not be zero")
	}

	if found.ParentPID == nil {
		t.Error("Test process ParentPID should not be nil")
	}

	if found.CommandLine == nil {
		t.Error("Test process CommandLine should not be nil")
	}
}
