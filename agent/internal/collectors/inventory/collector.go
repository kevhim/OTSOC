package inventory

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

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
	interval := 24 * time.Hour
	if c.cfg.InventoryInterval != "" {
		if parsed, err := time.ParseDuration(c.cfg.InventoryInterval); err == nil && parsed > 0 {
			interval = parsed
		} else if c.cfg.InventoryInterval == "0" || c.cfg.InventoryInterval == "disabled" {
			// Zero or disabled means no periodic polling
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

func (c *Collector) takeSnapshot(ctx context.Context, out chan<- *events.CanonicalEvent) {
	qualityFlags := []string{}
	
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
		qualityFlags = append(qualityFlags, "HOSTNAME_UNAVAILABLE")
	}

	interfacesInfo := make([]map[string]interface{}, 0)
	ifaces, errIfaces := net.Interfaces()
	if errIfaces != nil {
		qualityFlags = append(qualityFlags, "INTERFACES_UNAVAILABLE")
	} else {
		for _, i := range ifaces {
			addrs, errAddrs := i.Addrs()
			ipStrs := []string{}
			if errAddrs == nil {
				for _, addr := range addrs {
					ipStrs = append(ipStrs, addr.String())
				}
			}
			interfacesInfo = append(interfacesInfo, map[string]interface{}{
				"name": i.Name,
				"mac":  i.HardwareAddr.String(),
				"ips":  ipStrs,
			})
		}
	}

	// Capture timestamp at the exact moment of snapshot generation
	snapshotTime := time.Now().UTC()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      c.cfg.TenantID,
		SiteID:        c.cfg.SiteID,
		OccurredAt:    snapshotTime,
		Source:        "agent",
		Category:      "inventory",
		Action:        "INVENTORY_SNAPSHOT",
		Severity:      "INFO",
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"hostname":   hostname,
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"num_cpu":    runtime.NumCPU(),
			// mem basics omitted to avoid external dependencies like gopsutil for now
		},
	}
	
	if len(qualityFlags) > 0 {
		ev.QualityFlags = qualityFlags
	}
	if len(interfacesInfo) > 0 {
		ev.Metadata["interfaces"] = interfacesInfo
	}

	// If everything failed, do not emit an empty snapshot.
	if errIfaces != nil && hostname == "unknown" {
		// Log an error and skip emission. This fulfills "Failed collection must not become an empty successful snapshot"
		fmt.Fprintf(os.Stderr, "[ERROR] Inventory collection failed completely. Not emitting snapshot.\n")
		return
	}

	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	select {
	case out <- ev:
		return
	case <-ctx.Done():
		return
	case <-timer.C:
		// Blocking due to stalled pipeline.
		select {
		case out <- ev:
			return
		case <-ctx.Done():
			return
		}
	}
}
