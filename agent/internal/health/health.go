package health

import (
	"context"
	"time"

	"redcyberfox/pkg/events"
)

type Manager struct {
	deviceID string
	tenantID string
	siteID   string
	out      chan<- *events.CanonicalEvent
}

func NewManager(deviceID, tenantID, siteID string, out chan<- *events.CanonicalEvent) *Manager {
	return &Manager{
		deviceID: deviceID,
		tenantID: tenantID,
		siteID:   siteID,
		out:      out,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// In Phase 2A, we just emit a single startup heartbeat immediately,
	// then every 60s.
	m.emitHeartbeat(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.emitHeartbeat(ctx)
		}
	}
}

func (m *Manager) emitHeartbeat(ctx context.Context) {
	event := &events.CanonicalEvent{
		TenantID:   m.tenantID,
		SiteID:     m.siteID,
		Source:     "agent",
		Category:   "health",
		OccurredAt: time.Now().UTC(),
	}
	select {
	case m.out <- event:
	case <-ctx.Done():
	}
}

func (m *Manager) Stop() error {
	return nil
}
