package usb

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

type USBEvent struct {
	Action       string // "USB_INSERT" or "USB_REMOVE"
	VendorID     string
	ProductID    string
	SerialNumber string
	DevicePath   string
	OccurredAt   time.Time
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

// osWatcher is implemented in os-specific files. It blocks until runCtx is cancelled.
// It writes USBEvents to internalOut.
var startOSWatcher = defaultStartOSWatcher

func SetStartOSWatcher(fn func(context.Context, chan<- USBEvent) error) {
	startOSWatcher = fn
}

func GetStartOSWatcher() func(context.Context, chan<- USBEvent) error {
	return startOSWatcher
}

func (c *Collector) run(ctx context.Context, out chan<- *events.CanonicalEvent) {
	defer c.wg.Done()

	internalOut := make(chan USBEvent, 100)

	var watcherWg sync.WaitGroup
	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()
		defer close(internalOut)
		if err := startOSWatcher(ctx, internalOut); err != nil {
			// Failed to start watcher, log and exit. We do not panic or loop infinitely.
			fmt.Printf("USB watcher failed: %v\n", err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Wait for OS watcher to exit
			watcherWg.Wait()
			return
		case usbEv, ok := <-internalOut:
			if !ok {
				// Internal channel closed, exit
				return
			}
			c.emitEvent(ctx, out, usbEv)
		}
	}
}

func (c *Collector) emitEvent(ctx context.Context, out chan<- *events.CanonicalEvent, usbEv USBEvent) {
	metadata := make(map[string]interface{})
	if usbEv.VendorID != "" {
		metadata["vendor_id"] = usbEv.VendorID
	}
	if usbEv.ProductID != "" {
		metadata["product_id"] = usbEv.ProductID
	}
	if usbEv.SerialNumber != "" {
		metadata["serial_number"] = usbEv.SerialNumber
	}
	if usbEv.DevicePath != "" {
		metadata["device_path"] = usbEv.DevicePath
	}

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      c.cfg.TenantID,
		SiteID:        c.cfg.SiteID,
		OccurredAt:    usbEv.OccurredAt,
		Source:        "agent",
		Category:      "usb",
		Action:        usbEv.Action,
		Severity:      "INFO",
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata:      metadata,
	}

	// Safely emit with backpressure handling (shutdown aware)
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
