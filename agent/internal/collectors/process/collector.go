package process

import (
	"context"
	"fmt"
	"sync"

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

	mu      sync.Mutex
	wg      sync.WaitGroup
	started bool
	stopped bool
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

// Start begins process telemetry collection. It wires the collector to the out channel.
// It returns immediately, but the OS collection runs in a background goroutine.
// NOTE: CanonicalEvents emitted to the out channel are not durable until they are
// transactionally committed by Storage.Store() later in the pipeline.
func (c *ProcessCollector) Start(ctx context.Context, out chan<- *events.CanonicalEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return fmt.Errorf("collector cannot be restarted")
	}
	if c.started {
		return fmt.Errorf("collector already started")
	}
	c.started = true

	c.engine = NewLifecycleEngine(out)

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		// Delegate to OS-specific collection loops.
		c.startOSAdapter(runCtx)
	}()

	return nil
}

// WaitReady blocks until the underlying collector has established its baseline.
func (c *ProcessCollector) WaitReady(ctx context.Context) error {
	c.mu.Lock()
	engine := c.engine
	c.mu.Unlock()

	if engine != nil {
		return engine.WaitReady(ctx)
	}
	return nil
}

// Stop halts the process collector by cancelling the internal context
// and waits for the OS collection loop to terminate.
func (c *ProcessCollector) Stop() error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.stopped = true
	c.mu.Unlock()

	c.wg.Wait()
	return nil
}
