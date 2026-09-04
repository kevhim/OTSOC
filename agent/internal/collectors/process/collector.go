package process

import (
	"context"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

// ProcessCollector implements interfaces.Collector for process telemetry.
// This wraps the common LifecycleEngine and serves as the generic foundation.
// OS-specific implementations (e.g. Linux /proc or Windows ETW) will be
// attached to this struct in future phases.
type ProcessCollector struct {
	engine *LifecycleEngine
	cfg    *config.Config
	cancel context.CancelFunc
}

// NewCollector creates a new platform-neutral ProcessCollector.
func NewCollector(cfg *config.Config) *ProcessCollector {
	return &ProcessCollector{
		cfg: cfg,
	}
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

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Delegate to OS-specific collection loops.
	// This will block until ctx is canceled.
	c.startOSAdapter(ctx)

	return nil
}

// Stop halts the process collector by cancelling the internal context.
func (c *ProcessCollector) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
