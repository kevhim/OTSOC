package process

import (
	"context"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

// SemanticEvent projects a CanonicalEvent down to its core semantic contract,
// stripping away platform-specific metadata that is allowed to vary.
type SemanticEvent struct {
	Action    string
	Category  string
	Severity  string
	PID       int
	StartTime time.Time
}

func projectToSemantic(ce *events.CanonicalEvent) SemanticEvent {
	var pid int
	if p, ok := ce.Metadata["pid"].(int); ok {
		pid = p
	} else if p, ok := ce.Metadata["pid"].(float64); ok {
		pid = int(p)
	}

	var startTime time.Time
	if st, ok := ce.Metadata["start_time"].(string); ok {
		startTime, _ = time.Parse(time.RFC3339Nano, st)
	}

	return SemanticEvent{
		Action:    ce.Action,
		Category:  ce.Category,
		Severity:  ce.Severity,
		PID:       pid,
		StartTime: startTime,
	}
}

// captureEvents runs a sequence of snapshots through a fresh LifecycleEngine
// and returns the projected semantic events.
func captureEvents(snapshots []*Snapshot) []SemanticEvent {
	out := make(chan *events.CanonicalEvent, 100)
	engine := NewLifecycleEngine(out)

	ctx := context.Background()
	for _, snap := range snapshots {
		if snap == nil {
			// Simulating collection failure: do nothing (as per collector logic, it skips Reconcile)
			continue
		}
		engine.Reconcile(ctx, snap)
	}
	close(out)

	var results []SemanticEvent
	for ce := range out {
		results = append(results, projectToSemantic(ce))
	}
	return results
}

// TestEquivalence_OptionalMetadataAsymmetry explicitly tests that Linux and Windows
// can represent the same lifecycle state while having legitimate metadata differences,
// and both produce identical lifecycle semantics.
func TestEquivalence_OptionalMetadataAsymmetry(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pid := 1234

	// Mimic a Linux snapshot (has CommandLine and User)
	nameLinux := "foo"
	cmdLinux := `["foo","bar"]`
	userLinux := "alice"
	snapLinux := &Snapshot{
		Instances: []*Instance{
			{
				PID:         pid,
				StartTime:   t1,
				Name:        &nameLinux,
				CommandLine: &cmdLinux,
				User:        &userLinux,
			},
		},
	}

	// Mimic a Windows snapshot (different Name, nil CommandLine)
	nameWin := "foo.exe"
	userWin := "alice"
	snapWin := &Snapshot{
		Instances: []*Instance{
			{
				PID:       pid,
				StartTime: t1,
				Name:      &nameWin,
				User:      &userWin,
				// CommandLine unavailable
			},
		},
	}

	snap0 := &Snapshot{Instances: []*Instance{}} // baseline

	eventsLinux := captureEvents([]*Snapshot{snap0, snapLinux})
	eventsWin := captureEvents([]*Snapshot{snap0, snapWin})

	if len(eventsLinux) != 1 {
		t.Fatalf("expected 1 linux event, got %d", len(eventsLinux))
	}
	if len(eventsWin) != 1 {
		t.Fatalf("expected 1 windows event, got %d", len(eventsWin))
	}

	semL := eventsLinux[0]
	semW := eventsWin[0]

	// Verify semantic equivalence
	if semL.Action != semW.Action || semL.Action != "PROCESS_START" {
		t.Errorf("action mismatch: %s vs %s", semL.Action, semW.Action)
	}
	if semL.Category != semW.Category || semL.Category != "process" {
		t.Errorf("category mismatch: %s vs %s", semL.Category, semW.Category)
	}
	if semL.PID != semW.PID || semL.PID != pid {
		t.Errorf("PID mismatch: %d vs %d", semL.PID, semW.PID)
	}
	if !semL.StartTime.Equal(semW.StartTime) {
		t.Errorf("StartTime mismatch: %v vs %v", semL.StartTime, semW.StartTime)
	}
}

// TestEquivalence_FailedCollectionRecovery verifies that if a collection fails (nil snapshot),
// the LifecycleEngine preserves state and does not emit erroneous PROCESS_EXIT events.
func TestEquivalence_FailedCollectionRecovery(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Snapshot 1: A B C
	snap1 := &Snapshot{
		Instances: []*Instance{
			{PID: 10, StartTime: t1}, // A
			{PID: 20, StartTime: t1}, // B
			{PID: 30, StartTime: t1}, // C
		},
	}

	// Snapshot 2: A B D
	snap2 := &Snapshot{
		Instances: []*Instance{
			{PID: 10, StartTime: t1}, // A
			{PID: 20, StartTime: t1}, // B
			{PID: 40, StartTime: t1}, // D
		},
	}

	// We pass: snap1, nil (failure), snap2
	// The captureEvents wrapper handles nil by simulating collector skip.
	events := captureEvents([]*Snapshot{snap1, nil, snap2})

	// Expected semantics:
	// Snapshot 1 -> Baseline (0 events)
	// nil -> 0 events
	// Snapshot 2 -> PROCESS_EXIT(C), PROCESS_START(D)
	// Total: 2 events.

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Verify subsequent events
	hasExitC := false
	hasStartD := false

	for _, e := range events {
		if e.Action == "PROCESS_EXIT" && e.PID == 30 {
			hasExitC = true
		}
		if e.Action == "PROCESS_START" && e.PID == 40 {
			hasStartD = true
		}
	}

	if !hasExitC {
		t.Errorf("missing PROCESS_EXIT for C (PID 30)")
	}
	if !hasStartD {
		t.Errorf("missing PROCESS_START for D (PID 40)")
	}
}
