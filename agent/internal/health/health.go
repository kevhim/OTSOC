package health

import (
	"context"
	"time"
)

// Signal represents an internal health/lifecycle event in Phase 2A.
// The canonical heartbeat event is created in the Phase 2B persistence layer.
type Signal struct {
	Type       string
	OccurredAt time.Time
}

type Manager struct {
	deviceID string
	tenantID string
	siteID   string
	out      chan<- *Signal
}

func NewManager(deviceID, tenantID, siteID string, out chan<- *Signal) *Manager {
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
	sig := &Signal{
		Type:       "heartbeat",
		OccurredAt: time.Now().UTC(),
	}
	select {
	case m.out <- sig:
	case <-ctx.Done():
	}
}

func (m *Manager) Stop() error {
	return nil
}
