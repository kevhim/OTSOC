package process

import (
	"context"

	"redcyberfox/pkg/events"
)

// ProcessCollector implements interfaces.Collector for process telemetry.
// This wraps the common LifecycleEngine and serves as the generic foundation.
// OS-specific implementations (e.g. Linux /proc or Windows ETW) will be
// attached to this struct in future phases.
type ProcessCollector struct {
	engine *LifecycleEngine
}

// NewCollector creates a new platform-neutral ProcessCollector.
func NewCollector() *ProcessCollector {
	return &ProcessCollector{}
}

// Reconcile exposes the internal state machine's snapshot capability
// for OS-specific polling mechanisms to feed into.
func (c *ProcessCollector) Reconcile(ctx context.Context, snapshot *Snapshot) {
	if c.engine != nil {
		c.engine.Reconcile(ctx, snapshot)
	}
}

// HandleEvent exposes the internal state machine's discrete event capability
// for OS-specific event-driven mechanisms (like ETW/netlink) to feed into.
func (c *ProcessCollector) HandleEvent(ctx context.Context, inst *Instance, isStart bool) {
	if c.engine != nil {
		c.engine.HandleEvent(ctx, inst, isStart)
	}
}

// Start begins process telemetry collection. It wires the collector to the out channel
// and blocks until the context is canceled.
// NOTE: CanonicalEvents emitted to the out channel are not durable until they are
// transactionally committed by Storage.Store() later in the pipeline.
func (c *ProcessCollector) Start(ctx context.Context, out chan<- *events.CanonicalEvent) error {
	c.engine = NewLifecycleEngine(out)

	// OS-specific scheduling or event loop will be implemented in subsequent phases
	// and will call c.Reconcile() and c.HandleEvent().

	<-ctx.Done()
	return nil
}

// Stop cleanly shuts down any OS-specific collection mechanisms.
func (c *ProcessCollector) Stop() error {
	return nil
}
