package process

import (
	"context"
	"sort"
	"sync"
	"time"

	"redcyberfox/pkg/events"
)

const (
	EventSource        = "agent"
	EventCategory      = "process"
	ActionProcessStart = "PROCESS_START"
	ActionProcessExit  = "PROCESS_EXIT"
	SeverityInfo       = "INFO"
)

// LifecycleEngine maintains process state and resolves duplicate observations
// and PID reuse into discrete CanonicalEvents.
type LifecycleEngine struct {
	mu          sync.Mutex
	state       map[string]*Instance
	initialized bool
	ready       chan struct{}
	out         chan<- *events.CanonicalEvent
}

// NewLifecycleEngine creates a new process lifecycle state machine.
func NewLifecycleEngine(out chan<- *events.CanonicalEvent) *LifecycleEngine {
	return &LifecycleEngine{
		state:       make(map[string]*Instance),
		initialized: false,
		ready:       make(chan struct{}),
		out:         out,
	}
}

// Reconcile processes a snapshot of currently observed processes.
// On the first run (baseline), it populates the state without emitting starts.
// On subsequent runs, it emits starts for new instances and exits for vanished ones.
func (le *LifecycleEngine) Reconcile(ctx context.Context, snapshot *Snapshot) {
	le.mu.Lock()

	observed := make(map[string]bool)
	unobservablePIDs := make(map[int]bool)
	var exits []*Instance
	var starts []*Instance

	for _, inst := range snapshot.Instances {
		// StartTime is strictly required for strong instance identity.
		if inst.StartTime.IsZero() {
			unobservablePIDs[inst.PID] = true
			continue
		}

		key := inst.IdentityKey()
		observed[key] = true

		if _, exists := le.state[key]; !exists {
			le.state[key] = inst
			// Only emit starts if baseline has been established
			if le.initialized {
				starts = append(starts, inst)
			}
		}
	}

	// Check for exits if baseline has been established
	if le.initialized {
		for key, inst := range le.state {
			if !observed[key] {
				if unobservablePIDs[inst.PID] {
					continue
				}
				exits = append(exits, inst)
				delete(le.state, key)
			}
		}
	} else {
		le.initialized = true
		close(le.ready)
	}
	le.mu.Unlock()

	// Deterministic ordering: emit exits first, then starts outside of the lock
	sort.Slice(exits, func(i, j int) bool {
		if exits[i].PID != exits[j].PID {
			return exits[i].PID < exits[j].PID
		}
		return exits[i].StartTime.Before(exits[j].StartTime)
	})
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].PID != starts[j].PID {
			return starts[i].PID < starts[j].PID
		}
		return starts[i].StartTime.Before(starts[j].StartTime)
	})

	for _, inst := range exits {
		le.emit(ctx, inst, ActionProcessExit)
	}
	for _, inst := range starts {
		le.emit(ctx, inst, ActionProcessStart)
	}
}

// WaitReady blocks until the engine has established its initial baseline.
func (le *LifecycleEngine) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-le.ready:
		return nil
	}
}

// HandleEvent handles a discrete event-driven process observation (e.g., from ETW or netlink).
func (le *LifecycleEngine) HandleEvent(ctx context.Context, inst *Instance, isStart bool) {
	if inst.StartTime.IsZero() {
		return
	}
	key := inst.IdentityKey()

	var eventToEmit *Instance
	var actionToEmit string

	le.mu.Lock()
	if isStart {
		if _, exists := le.state[key]; !exists {
			le.state[key] = inst
			if le.initialized {
				eventToEmit = inst
				actionToEmit = ActionProcessStart
			}
		}
	} else {
		// Note: using knownInst instead of inst to emit the EXIT event ensures we have
		// whatever metadata we captured initially, even if the EXIT event lacks it.
		if knownInst, exists := le.state[key]; exists {
			eventToEmit = knownInst
			actionToEmit = ActionProcessExit
			delete(le.state, key)
		}
	}
	le.mu.Unlock()

	if eventToEmit != nil {
		le.emit(ctx, eventToEmit, actionToEmit)
	}
}

func (le *LifecycleEngine) emit(ctx context.Context, inst *Instance, action string) {
	metadata := map[string]interface{}{
		"pid":        inst.PID,
		"start_time": inst.StartTime.Format(time.RFC3339Nano),
	}
	if inst.Name != nil {
		metadata["process_name"] = *inst.Name
	}
	if inst.ParentPID != nil {
		metadata["parent_pid"] = *inst.ParentPID
	}
	if inst.ExecutablePath != nil {
		metadata["executable_path"] = *inst.ExecutablePath
	}
	if inst.CommandLine != nil {
		metadata["command_line"] = *inst.CommandLine
	}
	if inst.User != nil {
		metadata["user"] = *inst.User
	}

	if action == ActionProcessExit {
		metadata["exit_time"] = time.Now().UTC().Format(time.RFC3339Nano)
	}

	event := &events.CanonicalEvent{
		// SeqNo and EventID are left empty per durability requirement;
		// Storage.Store() owns these transactionally.
		OccurredAt:    time.Now().UTC(),
		Source:        EventSource,
		Category:      EventCategory,
		Action:        action,
		Severity:      SeverityInfo,
		Metadata:      metadata,
		SchemaVersion: events.CurrentSchemaVersion,
	}

	select {
	case <-ctx.Done():
		return
	case le.out <- event:
		// Emitted to the in-process handoff channel
	}
}
