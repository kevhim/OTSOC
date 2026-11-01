package eventqueue

import (
	"sync"
	"sync/atomic"
	"time"

	"redcyberfox/agent/internal/governor"
	"redcyberfox/pkg/events"
)

type item struct {
	event      *events.CanonicalEvent
	enqueuedAt time.Time
}

// EventQueue is an instrumented, bounded FIFO queue with a fixed-size ring buffer.
// It exposes non-destructive queue depth, capacity, oldest event age, and backpressure
// metrics to the Resource Governor and Measurement Reporter.
type EventQueue struct {
	mu                sync.RWMutex
	capacity          int
	inCh              chan *events.CanonicalEvent
	outCh             chan *events.CanonicalEvent
	ring              []item
	head              int
	tail              int
	count             int
	backpressureTotal uint64
	closeOnce         sync.Once
	done              chan struct{}
}

// New creates an initialized EventQueue with fixed capacity.
// Defaults to 200 if capacity is <= 0.
func New(capacity int) *EventQueue {
	if capacity <= 0 {
		capacity = 200
	}
	q := &EventQueue{
		capacity: capacity,
		inCh:     make(chan *events.CanonicalEvent),
		outCh:    make(chan *events.CanonicalEvent),
		ring:     make([]item, capacity),
		done:     make(chan struct{}),
	}
	go q.run()
	return q
}

// In returns the write-only channel for collectors to submit events.
func (q *EventQueue) In() chan<- *events.CanonicalEvent {
	return q.inCh
}

// Out returns the read-only channel for the ingestion engine to receive events.
func (q *EventQueue) Out() <-chan *events.CanonicalEvent {
	return q.outCh
}

// Close gracefully stops accepting new events into the queue by closing In.
// The internal runner will continue delivering buffered items to Out until empty,
// at which point Out is closed.
func (q *EventQueue) Close() {
	q.closeOnce.Do(func() {
		close(q.inCh)
	})
}

// WaitDrain blocks until the queue buffer is completely drained and Out is closed.
func (q *EventQueue) WaitDrain() {
	<-q.done
}

// Depth returns the current number of events buffered in the queue.
func (q *EventQueue) Depth() governor.ValidUint64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return governor.ValidUint64{Value: uint64(q.count), Valid: true}
}

// Capacity returns the maximum configured capacity of the queue.
func (q *EventQueue) Capacity() governor.ValidUint64 {
	return governor.ValidUint64{Value: uint64(q.capacity), Valid: true}
}

// SaturationPercent returns the fill ratio (0.0 to 100.0) of the queue.
func (q *EventQueue) SaturationPercent() governor.ValidFloat64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	sat := (float64(q.count) / float64(q.capacity)) * 100.0
	return governor.ValidFloat64{Value: sat, Valid: true}
}

// OldestEventAge returns the elapsed duration in seconds of the oldest event in the queue.
// If the queue is empty, returns Valid: false.
func (q *EventQueue) OldestEventAge() governor.ValidFloat64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.count == 0 {
		return governor.ValidFloat64{Valid: false}
	}
	age := time.Since(q.ring[q.head].enqueuedAt).Seconds()
	if age < 0 {
		age = 0
	}
	return governor.ValidFloat64{Value: age, Valid: true}
}

// BackpressureEventsTotal returns the count of times enqueueing encountered a full queue.
func (q *EventQueue) BackpressureEventsTotal() uint64 {
	return atomic.LoadUint64(&q.backpressureTotal)
}

func (q *EventQueue) run() {
	defer close(q.outCh)
	defer close(q.done)

	inClosed := false

	for {
		var headEv *events.CanonicalEvent
		var currentOut chan *events.CanonicalEvent

		q.mu.RLock()
		currentCount := q.count
		if currentCount > 0 {
			headEv = q.ring[q.head].event
			currentOut = q.outCh
		}
		q.mu.RUnlock()

		if inClosed && currentCount == 0 {
			return
		}

		var currentIn chan *events.CanonicalEvent
		if !inClosed {
			if currentCount < q.capacity {
				currentIn = q.inCh
			} else {
				// Queue is full, backpressure is active
				atomic.AddUint64(&q.backpressureTotal, 1)
			}
		}

		select {
		case ev, ok := <-currentIn:
			if !ok {
				inClosed = true
				continue
			}
			q.mu.Lock()
			if q.count < q.capacity {
				q.ring[q.tail] = item{
					event:      ev,
					enqueuedAt: time.Now(),
				}
				q.tail = (q.tail + 1) % q.capacity
				q.count++
			}
			q.mu.Unlock()

		case currentOut <- headEv:
			q.mu.Lock()
			if q.count > 0 {
				q.ring[q.head] = item{} // Zero out reference for GC
				q.head = (q.head + 1) % q.capacity
				q.count--
			}
			q.mu.Unlock()
		}
	}
}
