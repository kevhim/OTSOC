package passivenetwork

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

// DropPolicy defines the strategy when internal buffers experience congestion.
type DropPolicy string

const (
	// DropPolicyBlock applies natural backpressure to upstream producers.
	DropPolicyBlock DropPolicy = "block"
	// DropPolicyDropOldest drops the oldest queued item and records observable quality loss.
	DropPolicyDropOldest DropPolicy = "drop_oldest"
)

// DefaultBufferCapacity is the default size of the internal raw observation channel.
const DefaultBufferCapacity = 100

// Collector manages passive network capture, normalization, and CanonicalEvent emission.
//
// PASSIVE SAFETY INVARIANT:
// The collector and its underlying adapters are STRICTLY PASSIVE.
// They NEVER transmit packets, probe ports, ping hosts, scan addresses, or poll OT devices.
type Collector struct {
	cfg        *config.Config
	adapter    CaptureAdapter
	identifier ProtocolIdentifier
	bufferCap  int
	dropPolicy DropPolicy

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	dropCount    atomic.Uint64
	processCount atomic.Uint64
}

// Option configures optional parameters for Collector.
type Option func(*Collector)

// WithBufferCapacity sets the internal raw channel capacity.
func WithBufferCapacity(cap int) Option {
	return func(c *Collector) {
		if cap > 0 {
			c.bufferCap = cap
		}
	}
}

// WithDropPolicy sets the congestion policy.
func WithDropPolicy(policy DropPolicy) Option {
	return func(c *Collector) {
		c.dropPolicy = policy
	}
}

// WithProtocolIdentifier sets a custom protocol identifier.
func WithProtocolIdentifier(pi ProtocolIdentifier) Option {
	return func(c *Collector) {
		if pi != nil {
			c.identifier = pi
		}
	}
}

// NewCollector constructs a passive network telemetry collector.
func NewCollector(cfg *config.Config, adapter CaptureAdapter, opts ...Option) *Collector {
	c := &Collector{
		cfg:        cfg,
		adapter:    adapter,
		identifier: NewDefaultProtocolIdentifier(),
		bufferCap:  DefaultBufferCapacity,
		dropPolicy: DropPolicyBlock,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Start begins the passive observation pipeline.
func (c *Collector) Start(ctx context.Context, out chan<- *events.CanonicalEvent) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return errors.New("passive network collector cannot be restarted")
	}
	if c.started {
		c.mu.Unlock()
		return errors.New("passive network collector already started")
	}
	if c.adapter == nil {
		c.mu.Unlock()
		return errors.New("capture adapter is required")
	}
	c.started = true

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()

	rawCh := make(chan RawObservation, c.bufferCap)

	// Start adapter streaming into rawCh
	if err := c.adapter.Start(runCtx, rawCh); err != nil {
		cancel()
		return fmt.Errorf("failed to start capture adapter: %w", err)
	}

	c.wg.Add(1)
	go c.run(runCtx, rawCh, out)

	return nil
}

// run consumes raw observations, normalizes them, and emits CanonicalEvents.
func (c *Collector) run(ctx context.Context, rawCh <-chan RawObservation, out chan<- *events.CanonicalEvent) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-rawCh:
			if !ok {
				return
			}
			c.processRaw(ctx, raw, out)
		}
	}
}

// processRaw handles normalization and downstream emission for a single raw observation.
func (c *Collector) processRaw(ctx context.Context, raw RawObservation, out chan<- *events.CanonicalEvent) {
	c.processCount.Add(1)
	obs, payload := NormalizeRawObservation(raw)

	// Identify protocol hint using passive evidence
	hint, conf := c.identifier.Identify(obs, payload)
	obs.ProtocolHint = hint
	if conf != ConfidenceUnknown {
		obs.Confidence = conf
	}

	tenantID := "default-tenant"
	siteID := "default-site"
	if c.cfg != nil {
		if c.cfg.TenantID != "" {
			tenantID = c.cfg.TenantID
		}
		if c.cfg.SiteID != "" {
			siteID = c.cfg.SiteID
		}
	}

	// Generate event_id strictly once at the collector ownership boundary
	eventID := uuid.New().String()

	ev := &events.CanonicalEvent{
		EventID:       eventID,
		TenantID:      tenantID,
		SiteID:        siteID,
		OccurredAt:    obs.ObservedAt,
		ReceivedAt:    time.Now().UTC(),
		Source:        "passive_network",
		Category:      "network",
		Severity:      "INFO",
		Protocol:      obs.ProtocolHint,
		QualityFlags:  obs.QualityFlags,
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata:      make(map[string]interface{}),
	}

	if ev.Protocol == "unknown" && obs.TransportProtocol != "" {
		ev.Protocol = obs.TransportProtocol
	}

	if obs.SrcIP != "" {
		ev.Src = obs.SrcIP
	} else if obs.SrcMAC != "" {
		ev.Src = obs.SrcMAC
	}

	if obs.DstIP != "" {
		ev.Dst = obs.DstIP
	} else if obs.DstMAC != "" {
		ev.Dst = obs.DstMAC
	}

	if len(obs.QualityFlags) > 0 {
		ev.Severity = "WARNING"
	}

	// Populate metadata with bounded, structured attributes.
	// NOTE: Raw byte payload is intentionally NOT retained to prevent memory amplification.
	ev.Metadata["interface"] = obs.Interface
	ev.Metadata["src_mac"] = obs.SrcMAC
	ev.Metadata["dst_mac"] = obs.DstMAC
	ev.Metadata["src_port"] = obs.SrcPort
	ev.Metadata["dst_port"] = obs.DstPort
	ev.Metadata["transport_protocol"] = obs.TransportProtocol
	ev.Metadata["confidence"] = string(obs.Confidence)
	ev.Metadata["protocol_hint"] = obs.ProtocolHint
	ev.Metadata["payload_length"] = obs.PayloadLength
	if obs.VLANID != nil {
		ev.Metadata["vlan_id"] = *obs.VLANID
	}
	for k, v := range obs.Metadata {
		ev.Metadata[k] = v
	}

	// Cancellation-aware bounded delivery to downstream
	select {
	case <-ctx.Done():
		return
	case out <- ev:
	}
}

// Stop cleanly stops the collector and waits for workers to exit.
func (c *Collector) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var adapterErr error
	if c.adapter != nil {
		adapterErr = c.adapter.Stop()
	}

	c.wg.Wait()
	return adapterErr
}

// DropCount returns the total number of dropped observations.
func (c *Collector) DropCount() uint64 {
	return c.dropCount.Load()
}

// ProcessCount returns the total number of processed observations.
func (c *Collector) ProcessCount() uint64 {
	return c.processCount.Load()
}
