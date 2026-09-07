package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

type Connection struct {
	Protocol string
	SrcIP    string
	SrcPort  uint16
	DstIP    string
	DstPort  uint16
	State    string
	PID      uint32
}

type Collector struct {
	cfg    *config.Config
	cancel context.CancelFunc

	mu      sync.Mutex
	wg      sync.WaitGroup
	started bool
	stopped bool
}

func NewCollector(cfg *config.Config) *Collector {
	return &Collector{
		cfg: cfg,
	}
}

func (c *Collector) Start(ctx context.Context, out chan<- *events.CanonicalEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return fmt.Errorf("collector cannot be restarted")
	}
	if c.started {
		return fmt.Errorf("collector already started")
	}
	c.started = true

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go c.run(runCtx, out)

	return nil
}

func (c *Collector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.stopped {
		return nil
	}
	c.stopped = true

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	c.wg.Wait()
	return nil
}

func (c *Collector) run(ctx context.Context, out chan<- *events.CanonicalEvent) {
	defer c.wg.Done()

	// Initial snapshot on startup
	c.takeSnapshot(ctx, out)

	// Configurable interval
	interval := 5 * time.Minute
	if c.cfg.NetworkInterval != "" {
		if parsed, err := time.ParseDuration(c.cfg.NetworkInterval); err == nil && parsed > 0 {
			interval = parsed
		} else if c.cfg.NetworkInterval == "0" || c.cfg.NetworkInterval == "disabled" {
			interval = 0
		}
	}

	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.takeSnapshot(ctx, out)
			}
		}
	} else {
		// Just wait for context cancellation if periodic is disabled
		<-ctx.Done()
	}
}

var getConnections = osSpecificConnections

func (c *Collector) takeSnapshot(ctx context.Context, out chan<- *events.CanonicalEvent) {
	// Implemented by OS-specific logic
	connections, qualityFlags, err := getConnections()

	if err != nil {
		// Complete failure - do not emit false empty snapshot.
		// Emit observable error logic (not yet implemented in health subsys, so just return for now)
		return
	}

	snapshotTime := time.Now().UTC()
	var rawConns []map[string]interface{}
	for _, conn := range connections {
		m := map[string]interface{}{
			"protocol": conn.Protocol,
			"src_ip":   conn.SrcIP,
			"src_port": conn.SrcPort,
			"dst_ip":   conn.DstIP,
			"dst_port": conn.DstPort,
		}
		if conn.State != "" {
			m["state"] = conn.State
		}
		if conn.PID > 0 {
			m["pid"] = conn.PID
		}
		rawConns = append(rawConns, m)
	}

	// It's perfectly legitimate for rawConns to be empty if OS API succeeded but returned 0 conns
	if rawConns == nil {
		rawConns = []map[string]interface{}{}
	}

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      c.cfg.TenantID,
		SiteID:        c.cfg.SiteID,
		OccurredAt:    snapshotTime,
		Source:        "agent",
		Category:      "network",
		Action:        "NETWORK_CONNECTION_SNAPSHOT",
		Severity:      "INFO",
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"connections": rawConns,
		},
	}

	if len(qualityFlags) > 0 {
		ev.QualityFlags = qualityFlags
	}

	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	select {
	case out <- ev:
		return
	case <-ctx.Done():
		return
	case <-timer.C:
		select {
		case out <- ev:
			return
		case <-ctx.Done():
			return
		}
	}
}
