//go:build windows

package process

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

func TestWindowsIntegration_Lifecycle(t *testing.T) {
	adapter := &windowsAdapter{
		api: defaultOSAPI, // Uses the real Windows API on host
	}

	out := make(chan *events.CanonicalEvent, 1000)
	engine := NewLifecycleEngine(out)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Baseline
	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture initial snapshot: %v", err)
	}
	engine.Reconcile(ctx, snap)

	// Drain baseline events
drainLoop1:
	for {
		select {
		case <-out:
		default:
			break drainLoop1
		}
	}

	// 2. Spawn a short-lived process (using timeout instead of sleep for Windows)
	cmd := exec.Command("timeout", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start timeout: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	pid := cmd.Process.Pid

	// 3. Observe PROCESS_START
	snap, err = adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}
	engine.Reconcile(ctx, snap)

	foundStart := false
drainLoop2:
	for {
		select {
		case ev := <-out:
			if ev.Action == "PROCESS_START" {
				sem := projectToSemantic(ev)
				if sem.PID == pid {
					foundStart = true
				}
			}
		default:
			break drainLoop2
		}
	}

	if !foundStart {
		t.Fatalf("Did not observe PROCESS_START for spawned PID %d", pid)
	}

	// 4. Terminate process
	cmd.Process.Kill()
	cmd.Wait()

	// 5. Observe PROCESS_EXIT
	snap, err = adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}
	engine.Reconcile(ctx, snap)

	foundExit := false
drainLoop3:
	for {
		select {
		case ev := <-out:
			if ev.Action == "PROCESS_EXIT" {
				sem := projectToSemantic(ev)
				if sem.PID == pid {
					foundExit = true
				}
			}
		default:
			break drainLoop3
		}
	}

	if !foundExit {
		t.Fatalf("Did not observe PROCESS_EXIT for killed PID %d", pid)
	}
}
