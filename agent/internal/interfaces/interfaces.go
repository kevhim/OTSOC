package interfaces

import (
	"context"

	"redcyberfox/pkg/events"
)

// Stopper represents any component that can be gracefully stopped and returns an error.
type Stopper interface {
	Stop() error
}

type Collector interface {
	Start(ctx context.Context, out chan<- *events.CanonicalEvent) error
	Stop() error
}

type Storage interface {
	Init(ctx context.Context) error
	Store(ctx context.Context, event *events.CanonicalEvent) error
	GetPendingEvents(ctx context.Context, limit int) ([]*events.CanonicalEvent, error)
	RemoveEvent(ctx context.Context, eventID string) error
	MarkFailed(ctx context.Context, eventID string, retryAfter int, err error) error
	MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error
	GetDeviceID() string
	Close() error
}

type Forwarder interface {
	Start(ctx context.Context) error
	Stop() error
}

type Governor interface {
	Start(ctx context.Context) error
	Stop() error
}
