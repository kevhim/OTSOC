package process

import (
	"context"
	"sync"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

func strPtr(s string) *string {
	return &s
}

func drainEvents(out <-chan *events.CanonicalEvent) []*events.CanonicalEvent {
	var evs []*events.CanonicalEvent
	for {
		select {
		case ev := <-out:
			evs = append(evs, ev)
		default:
			return evs
		}
	}
}

func TestLifecycleEngine_Baseline(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)

	now := time.Now()

	// Baseline snapshot with 2 existing processes
	snap := &Snapshot{
		Instances: []*Instance{
			{PID: 100, StartTime: now.Add(-10 * time.Minute), Name: strPtr("init")},
			{PID: 200, StartTime: now.Add(-5 * time.Minute), Name: strPtr("bash")},
		},
	}

	engine.Reconcile(context.Background(), snap)

	evs := drainEvents(out)
	if len(evs) != 0 {
		t.Fatalf("expected 0 events on baseline, got %d", len(evs))
	}
}

func TestLifecycleEngine_NewProcess(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)

	// Establish baseline
	engine.Reconcile(context.Background(), &Snapshot{})

	// Next snapshot introduces a new process
	now := time.Now()
	snap := &Snapshot{
		Instances: []*Instance{
			{PID: 500, StartTime: now, Name: strPtr("malware.exe")},
		},
	}

	engine.Reconcile(context.Background(), snap)

	evs := drainEvents(out)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}

	if evs[0].Action != ActionProcessStart {
		t.Errorf("expected %s, got %s", ActionProcessStart, evs[0].Action)
	}
	if evs[0].Category != EventCategory || evs[0].Source != EventSource {
		t.Errorf("expected source/category to match constants")
	}
	if evs[0].SeqNo != 0 || evs[0].EventID != "" {
		t.Errorf("expected unassigned seq_no and event_id (assigned by Storage)")
	}
}

func TestLifecycleEngine_Deduplication(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	now := time.Now()
	inst := &Instance{PID: 500, StartTime: now, Name: strPtr("app.exe")}
	snap := &Snapshot{Instances: []*Instance{inst}}

	// First observation emits START
	engine.Reconcile(context.Background(), snap)
	evs := drainEvents(out)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event on first observation")
	}

	// Repeated identical observation emits nothing
	engine.Reconcile(context.Background(), snap)
	evs = drainEvents(out)
	if len(evs) != 0 {
		t.Fatalf("expected 0 events on duplicate observation, got %d", len(evs))
	}
}

func TestLifecycleEngine_ProcessExit(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	now := time.Now()
	inst := &Instance{PID: 500, StartTime: now, Name: strPtr("app.exe")}

	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst}})
	drainEvents(out) // drain START

	// Snapshot without the process
	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{}})

	evs := drainEvents(out)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event on vanished process")
	}
	if evs[0].Action != ActionProcessExit {
		t.Errorf("expected %s, got %s", ActionProcessExit, evs[0].Action)
	}
}

func TestLifecycleEngine_PIDReuse(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	t1 := time.Now().Add(-10 * time.Minute)
	inst1 := &Instance{PID: 500, StartTime: t1, Name: strPtr("old.exe")}

	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst1}})
	drainEvents(out) // drain START for old.exe

	// PID 500 gets reused by new.exe
	t2 := time.Now()
	inst2 := &Instance{PID: 500, StartTime: t2, Name: strPtr("new.exe")}

	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst2}})

	evs := drainEvents(out)
	// We expect 2 events: EXIT for old.exe, and START for new.exe
	if len(evs) != 2 {
		t.Fatalf("expected 2 events on PID reuse, got %d", len(evs))
	}

	// Order is usually EXIT then START (based on map iteration in reconcile, though order might not be strictly deterministic in a real map, but we check if both exist).
	hasExit := false
	hasStart := false
	for _, ev := range evs {
		if ev.Action == ActionProcessExit {
			if ev.Metadata["process_name"] != "old.exe" {
				t.Errorf("expected old.exe to exit")
			}
			hasExit = true
		}
		if ev.Action == ActionProcessStart {
			if ev.Metadata["process_name"] != "new.exe" {
				t.Errorf("expected new.exe to start")
			}
			hasStart = true
		}
	}

	if !hasExit || !hasStart {
		t.Errorf("expected both EXIT and START events")
	}
}

func TestLifecycleEngine_MissingStartTime(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	// Instance with empty StartTime
	inst := &Instance{PID: 500, Name: strPtr("unknown.exe")}

	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst}})
	evs := drainEvents(out)

	// Should be ignored to prevent PID reuse vulnerabilities
	if len(evs) != 0 {
		t.Fatalf("expected 0 events for process missing StartTime, got %d", len(evs))
	}
}

func TestLifecycleEngine_HandleEvent(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	now := time.Now()
	inst := &Instance{PID: 999, StartTime: now, Name: strPtr("event.exe")}

	// Handle discrete start event
	engine.HandleEvent(context.Background(), inst, true)
	evs := drainEvents(out)
	if len(evs) != 1 || evs[0].Action != ActionProcessStart {
		t.Fatalf("expected PROCESS_START from HandleEvent")
	}

	// Handling it again shouldn't emit a duplicate
	engine.HandleEvent(context.Background(), inst, true)
	if len(drainEvents(out)) != 0 {
		t.Fatalf("expected deduplication in HandleEvent")
	}

	// Handle discrete exit event
	engine.HandleEvent(context.Background(), inst, false)
	evs = drainEvents(out)
	if len(evs) != 1 || evs[0].Action != ActionProcessExit {
		t.Fatalf("expected PROCESS_EXIT from HandleEvent")
	}
}

func TestLifecycleEngine_Concurrency(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 200)
	engine := NewLifecycleEngine(out)

	// Establish baseline
	engine.Reconcile(context.Background(), &Snapshot{})

	var wg sync.WaitGroup
	now := time.Now()

	// Launch 100 concurrent starts (50 unique, 50 duplicates)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			inst := &Instance{PID: pid % 50, StartTime: now, Name: strPtr("conc.exe")}
			engine.HandleEvent(context.Background(), inst, true)
		}(i)
	}

	wg.Wait()

	evs := drainEvents(out)
	if len(evs) != 50 {
		t.Fatalf("expected exactly 50 PROCESS_START events, got %d", len(evs))
	}

	uniquePIDs := make(map[int]bool)
	for _, ev := range evs {
		if ev.Action != ActionProcessStart {
			t.Errorf("expected ActionProcessStart, got %s", ev.Action)
		}
		pid := ev.Metadata["pid"].(int)
		if uniquePIDs[pid] {
			t.Errorf("duplicate START event for PID %v", pid)
		}
		uniquePIDs[pid] = true
	}

	// Now launch 50 concurrent exits
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			inst := &Instance{PID: pid, StartTime: now, Name: strPtr("conc.exe")}
			engine.HandleEvent(context.Background(), inst, false)
		}(i)
	}

	wg.Wait()

	evs = drainEvents(out)
	if len(evs) != 50 {
		t.Fatalf("expected exactly 50 PROCESS_EXIT events, got %d", len(evs))
	}
}

func TestLifecycleEngine_TemporaryUnobservability(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	engine.Reconcile(context.Background(), &Snapshot{})

	now := time.Now()
	// Snapshot 1: observed normally
	inst1 := &Instance{PID: 500, StartTime: now, Name: strPtr("app.exe")}
	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst1}})

	evs := drainEvents(out)
	if len(evs) != 1 || evs[0].Action != ActionProcessStart {
		t.Fatalf("expected 1 PROCESS_START")
	}

	// Snapshot 2: unobservable (empty StartTime)
	inst2 := &Instance{PID: 500}
	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst2}})

	evs = drainEvents(out)
	if len(evs) != 0 {
		t.Fatalf("expected NO events during temporary unobservability, got %d", len(evs))
	}

	// Snapshot 3: fully recovered
	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{inst1}})
	evs = drainEvents(out)
	if len(evs) != 0 {
		t.Fatalf("expected NO duplicate events upon recovery, got %d", len(evs))
	}

	// Snapshot 4: confirmed absence
	engine.Reconcile(context.Background(), &Snapshot{Instances: []*Instance{}})
	evs = drainEvents(out)
	if len(evs) != 1 || evs[0].Action != ActionProcessExit {
		t.Fatalf("expected exactly 1 PROCESS_EXIT on confirmed absence")
	}
}

func TestLifecycleEngine_DeterministicOrdering(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 100)
	now := time.Now()

	// Create a mixed snapshot of 10 processes
	var initial []*Instance
	for i := 0; i < 10; i++ {
		initial = append(initial, &Instance{PID: 1000 + i, StartTime: now.Add(time.Duration(i) * time.Second)})
	}

	// Create a second snapshot where evens exit, odds stay, and some new processes start
	var second []*Instance
	for i := 1; i < 10; i += 2 {
		second = append(second, initial[i]) // odds stay
	}
	for i := 0; i < 5; i++ {
		second = append(second, &Instance{PID: 2000 + i, StartTime: now.Add(time.Duration(i) * time.Second)})
	}

	// Run multiple times and ensure the sequence of emitted actions/PIDs is EXACTLY identical
	var firstRunSeq []string

	for run := 0; run < 10; run++ {
		engine := NewLifecycleEngine(out)
		engine.Reconcile(context.Background(), &Snapshot{})                                             // baseline
		engine.Reconcile(context.Background(), &Snapshot{Instances: append([]*Instance{}, initial...)}) // start all
		drainEvents(out)                                                                                // ignore the initial starts

		// Now trigger exits and starts simultaneously
		engine.Reconcile(context.Background(), &Snapshot{Instances: append([]*Instance{}, second...)})

		evs := drainEvents(out)
		var currentRunSeq []string
		for _, ev := range evs {
			currentRunSeq = append(currentRunSeq, ev.Action+":"+string(rune(ev.Metadata["pid"].(int))))
		}

		if run == 0 {
			firstRunSeq = currentRunSeq
		} else {
			if len(firstRunSeq) != len(currentRunSeq) {
				t.Fatalf("Run %d: expected %d events, got %d", run, len(firstRunSeq), len(currentRunSeq))
			}
			for i := range firstRunSeq {
				if firstRunSeq[i] != currentRunSeq[i] {
					t.Fatalf("Run %d: event ordering mismatch at index %d. Expected %s, got %s", run, i, firstRunSeq[i], currentRunSeq[i])
				}
			}
		}
	}
}
