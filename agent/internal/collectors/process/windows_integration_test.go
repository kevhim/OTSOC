//go:build windows

package process

import (
	"os"
	"testing"
)

func TestWindowsIntegration_RealHost(t *testing.T) {
	// 1. Instantiate the Windows process adapter
	adapter := &windowsAdapter{
		api: &realWindowsAPI{},
	}

	// 2. Capture a real process snapshot
	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("failed to capture snapshot on real host: %v", err)
	}

	if len(snap.Instances) == 0 {
		t.Fatalf("expected at least 1 process (the test process), got 0")
	}

	// 3. Confirm at least the current test process can be represented
	myPID := os.Getpid()
	var myInst *Instance

	systemProcessAccessDenied := false

	for _, inst := range snap.Instances {
		if inst.PID == myPID {
			myInst = inst
		}
		// Confirm collector survives inaccessible processes
		// We expect that we cannot read the ExecutablePath of some system processes without admin/debug privileges
		if inst.PID != myPID && inst.ExecutablePath == nil {
			systemProcessAccessDenied = true
		}
	}

	if myInst == nil {
		t.Fatalf("test process PID %d was not found in the snapshot", myPID)
	}

	// 4. Confirm PID is populated
	if myInst.PID != myPID {
		t.Errorf("expected PID %d, got %d", myPID, myInst.PID)
	}

	// 5. Confirm StartTime is populated
	if myInst.StartTime.IsZero() {
		t.Errorf("expected StartTime to be populated for test process")
	}

	// Optional check for test process executable
	if myInst.ExecutablePath != nil {
		t.Logf("Test process path: %s", *myInst.ExecutablePath)
	} else {
		t.Logf("Test process path unavailable")
	}
	
	// 6. Confirm the collector survives inaccessible processes
	// Note: in extremely isolated environments this might be false, but practically on a normal Windows machine it will be true
	if systemProcessAccessDenied {
		t.Logf("Successfully survived inaccessible processes (found processes missing optional metadata)")
	}
}
