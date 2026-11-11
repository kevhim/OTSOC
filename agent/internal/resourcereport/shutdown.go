package resourcereport

import (
	"context"
	"log"
	"sync"
	"time"
)

// ShutdownCoordinator guarantees a single shared shutdown path and starts the single
// global drain deadline timer exactly once, regardless of whether shutdown was initiated
// by measurement expiry, OS signals, or both simultaneously.
type ShutdownCoordinator struct {
	mu           sync.Mutex
	once         sync.Once
	cancel       context.CancelFunc
	drainCancel  context.CancelFunc
	drainTimeout time.Duration
	outcome      string
	timerFired   bool
	onDrainStart func()
}

// NewShutdownCoordinator creates a new ShutdownCoordinator with the configured drain timeout.
func NewShutdownCoordinator(cancel, drainCancel context.CancelFunc, drainTimeout time.Duration) *ShutdownCoordinator {
	if drainTimeout <= 0 {
		drainTimeout = 10 * time.Second
	}
	return &ShutdownCoordinator{
		cancel:       cancel,
		drainCancel:  drainCancel,
		drainTimeout: drainTimeout,
		outcome:      "RUNNING",
	}
}

// Trigger initiates shutdown exactly once with the given reason.
// Returns true if this call initiated shutdown, or false if shutdown was already triggered.
func (s *ShutdownCoordinator) Trigger(reason string) bool {
	triggered := false
	s.once.Do(func() {
		triggered = true
		s.mu.Lock()
		s.outcome = reason
		s.mu.Unlock()

		log.Printf("Endpoint Agent shutdown initiated (%s)...", reason)
		s.cancel()

		go func() {
			timer := time.NewTimer(s.drainTimeout)
			defer timer.Stop()

			if s.onDrainStart != nil {
				s.onDrainStart()
			}

			select {
			case <-timer.C:
				s.mu.Lock()
				s.timerFired = true
				s.mu.Unlock()
				log.Printf("Global shutdown drain deadline expired (%v). Forcing context cancellation.", s.drainTimeout)
				s.drainCancel()
			}
		}()
	})
	return triggered
}

// Outcome returns the recorded shutdown reason or outcome.
func (s *ShutdownCoordinator) Outcome() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outcome
}

// TimerFired indicates whether the drain deadline timer expired before draining completed.
func (s *ShutdownCoordinator) TimerFired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timerFired
}
