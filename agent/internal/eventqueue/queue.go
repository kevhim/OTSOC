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
// It exposes non-destructive queue depth, capacity, oldest event age, and precisely
// defined backpressure metrics to the Resource Governor and Measurement Reporter.
//
// Backpressure Semantics:
//  1. BlockedEnqueueAttemptsTotal: Counts each distinct event enqueue attempt that
//     was blocked waiting for downstream capacity because the queue was at maximum capacity.
//  2. PressureEpisodesTotal: Counts each discrete transition into the 100% saturated
//     state (count == capacity). A continuous period of saturation counts as 1 episode.
//  3. TimeSpentFullSeconds: Measures the cumulative elapsed duration in seconds the queue
//     remained at 100% saturation.
type EventQueue struct {
	mu       sync.Mutex
	notFull  *sync.Cond
	notEmpty *sync.Cond

	capacity int
	ring     []item
	head     int
	tail     int
	count    int
	closed   bool

	inCh      chan *events.CanonicalEvent
	outCh     chan *events.CanonicalEvent
	done      chan struct{}
	closeOnce sync.Once

	blockedEnqueueAttempts atomic.Uint64
	pressureEpisodes       atomic.Uint64
	timeSpentFull          time.Duration
	fullStartTime          time.Time
}

// New creates an initialized EventQueue with fixed capacity.
// Defaults to 200 if capacity is <= 0.
func New(capacity int) *EventQueue {
	if capacity <= 0 {
		capacity = 200
	}
	q := &EventQueue{
		capacity: capacity,
		ring:     make([]item, capacity),
		inCh:     make(chan *events.CanonicalEvent),
		outCh:    make(chan *events.CanonicalEvent),
		done:     make(chan struct{}),
	}
	q.notFull = sync.NewCond(&q.mu)
	q.notEmpty = sync.NewCond(&q.mu)

	go q.inPump()
	go q.outPump()
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
// Buffered items continue delivering to Out until empty, at which point Out is closed.
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
	q.mu.Lock()
	defer q.mu.Unlock()
	return governor.ValidUint64{Value: uint64(q.count), Valid: true}
}

// Capacity returns the maximum configured capacity of the queue.
func (q *EventQueue) Capacity() governor.ValidUint64 {
	return governor.ValidUint64{Value: uint64(q.capacity), Valid: true}
}

// SaturationPercent returns the fill ratio (0.0 to 100.0) of the queue.
func (q *EventQueue) SaturationPercent() governor.ValidFloat64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	sat := (float64(q.count) / float64(q.capacity)) * 100.0
	return governor.ValidFloat64{Value: sat, Valid: true}
}

// OldestEventAge returns the elapsed duration in seconds of the oldest event in the queue.
// If the queue is empty, returns Valid: false.
func (q *EventQueue) OldestEventAge() governor.ValidFloat64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		return governor.ValidFloat64{Valid: false}
	}
	age := time.Since(q.ring[q.head].enqueuedAt).Seconds()
	if age < 0 {
		age = 0
	}
	return governor.ValidFloat64{Value: age, Valid: true}
}

// BlockedEnqueueAttemptsTotal returns the count of event enqueue attempts that
// were blocked waiting for queue capacity because the queue was at maximum capacity.
func (q *EventQueue) BlockedEnqueueAttemptsTotal() uint64 {
	return q.blockedEnqueueAttempts.Load()
}

// BackpressureEventsTotal is an alias for BlockedEnqueueAttemptsTotal for backward compatibility.
func (q *EventQueue) BackpressureEventsTotal() uint64 {
	return q.BlockedEnqueueAttemptsTotal()
}

// PressureEpisodesTotal returns the count of discrete episodes where queue saturation reached 100%.
func (q *EventQueue) PressureEpisodesTotal() uint64 {
	return q.pressureEpisodes.Load()
}

// TimeSpentFullSeconds returns the cumulative duration in seconds the queue spent at 100% capacity.
func (q *EventQueue) TimeSpentFullSeconds() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	spent := q.timeSpentFull
	if q.count == q.capacity && !q.fullStartTime.IsZero() {
		spent += time.Since(q.fullStartTime)
	}
	return spent.Seconds()
}

func (q *EventQueue) inPump() {
	for {
		q.mu.Lock()
		wasFull := false
		for q.count == q.capacity && !q.closed {
			wasFull = true
			q.notFull.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()

		var ev *events.CanonicalEvent
		var ok bool

		if wasFull {
			// If queue was full, check if a producer was already blocked waiting to send on inCh.
			select {
			case ev, ok = <-q.inCh:
				if ok {
					q.blockedEnqueueAttempts.Add(1)
				}
			default:
				ev, ok = <-q.inCh
			}
		} else {
			ev, ok = <-q.inCh
		}

		if !ok {
			q.mu.Lock()
			q.closed = true
			q.notEmpty.Signal()
			q.notFull.Broadcast()
			q.mu.Unlock()
			return
		}

		q.mu.Lock()
		wasEmpty := (q.count == 0)
		q.ring[q.tail] = item{
			event:      ev,
			enqueuedAt: time.Now(),
		}
		q.tail = (q.tail + 1) % q.capacity
		q.count++

		if q.count == q.capacity {
			q.pressureEpisodes.Add(1)
			q.fullStartTime = time.Now()
		}

		if wasEmpty {
			q.notEmpty.Signal()
		}
		q.mu.Unlock()
	}
}

func (q *EventQueue) outPump() {
	defer close(q.outCh)
	defer close(q.done)

	for {
		q.mu.Lock()
		for q.count == 0 && !q.closed {
			q.notEmpty.Wait()
		}
		if q.count == 0 && q.closed {
			q.mu.Unlock()
			return
		}

		headEv := q.ring[q.head].event
		q.mu.Unlock()

		// Deliver to outCh without holding queue lock
		q.outCh <- headEv

		q.mu.Lock()
		q.ring[q.head] = item{} // Zero out reference for GC
		q.head = (q.head + 1) % q.capacity
		wasFull := (q.count == q.capacity)
		if wasFull && !q.fullStartTime.IsZero() {
			q.timeSpentFull += time.Since(q.fullStartTime)
			q.fullStartTime = time.Time{}
		}
		q.count--

		if wasFull {
			q.notFull.Signal()
		}
		q.mu.Unlock()
	}
}
