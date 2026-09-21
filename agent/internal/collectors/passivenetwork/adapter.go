package passivenetwork

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrAdapterStopped indicates the adapter has already been stopped.
	ErrAdapterStopped = errors.New("capture adapter already stopped")
	// ErrAdapterRunning indicates the adapter is already running.
	ErrAdapterRunning = errors.New("capture adapter already running")
)

// CaptureAdapter defines the minimal pluggable boundary for passive network observation sources.
//
// PASSIVE SAFETY INVARIANT:
// CaptureAdapter implementations MUST strictly consume traffic only.
// Implementations MUST NOT provide any packet transmission, socket crafting, probing,
// scanning, or packet injection capabilities.
type CaptureAdapter interface {
	// Start begins streaming passive observations into out.
	// Start MUST terminate when ctx is canceled or Stop() is invoked.
	// Sends to out MUST be cancellation-aware.
	Start(ctx context.Context, out chan<- RawObservation) error

	// Stop cleanly stops the capture adapter and waits for workers to terminate.
	Stop() error
}

// ReplayAdapter implements CaptureAdapter for deterministic, offline testing and CI replay.
// It requires NO network sockets, NO administrative/root privileges, and NO external dependencies.
type ReplayAdapter struct {
	fixtures []RawObservation
	mu       sync.Mutex
	started  bool
	stopped  bool
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewReplayAdapter creates a new deterministic ReplayAdapter with pre-configured fixtures.
func NewReplayAdapter(fixtures []RawObservation) *ReplayAdapter {
	return &ReplayAdapter{
		fixtures: fixtures,
		done:     make(chan struct{}),
	}
}

// Start begins replaying the configured fixtures sequentially.
// It honors context cancellation and stops immediately if ctx is cancelled or Stop is called.
func (r *ReplayAdapter) Start(ctx context.Context, out chan<- RawObservation) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return ErrAdapterStopped
	}
	if r.started {
		r.mu.Unlock()
		return ErrAdapterRunning
	}
	r.started = true
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.mu.Unlock()

	go func() {
		defer close(r.done)
		for _, obs := range r.fixtures {
			select {
			case <-runCtx.Done():
				return
			case out <- obs:
			}
		}
	}()

	return nil
}

// Stop cleanly terminates replay and waits for the worker goroutine to exit.
// It is safe to call multiple times or before Start.
func (r *ReplayAdapter) Stop() error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
	started := r.started
	if !started {
		close(r.done)
	}
	r.mu.Unlock()

	<-r.done
	return nil
}
