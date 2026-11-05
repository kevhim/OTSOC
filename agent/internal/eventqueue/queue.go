package eventqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"redcyberfox/agent/internal/governor"
	"redcyberfox/pkg/events"
)

var (
	// ErrQueueClosed indicates that the queue is closed and cannot accept new events.
	ErrQueueClosed = errors.New("event queue is closed")
)

type item struct {
	event      *events.CanonicalEvent
	enqueuedAt time.Time
}

type enqueueWaiter struct {
	ev    *events.CanonicalEvent
	ready chan struct{}
	err   error
}

// EventQueue is an instrumented, bounded FIFO queue with a fixed-size ring buffer.
// It exposes non-destructive queue depth, capacity, oldest event age, and precisely
// defined backpressure metrics to the Resource Governor and Measurement Reporter.
//
// Backpressure Semantics:
//  1. BlockedEnqueueAttemptsTotal: Counts each distinct event enqueue attempt that
//     was blocked waiting for downstream capacity because the queue was at maximum capacity.
//     This metric is instrumented directly via Enqueue(ctx, ev).
//  2. PressureEpisodesTotal: Counts each discrete transition into the 100% saturated
//     state (count == capacity). A continuous period of saturation counts as 1 episode.
//  3. TimeSpentFullSeconds: Measures the cumulative elapsed duration in seconds the queue
//     remained at 100% saturation.
type EventQueue struct {
	mu       sync.Mutex
	notEmpty *sync.Cond

	capacity int
	ring     []item
	head     int
	tail     int
	count    int
	closed   bool
	waiters  []*enqueueWaiter

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
	q.notEmpty = sync.NewCond(&q.mu)

	go q.inPump()
	go q.outPump()
	return q
}

// In returns the write-only channel for collectors to submit events.
// Note: Submissions via In() are bridged internally to Enqueue(context.Background(), ev).
func (q *EventQueue) In() chan<- *events.CanonicalEvent {
	return q.inCh
}

// Out returns the read-only channel for the ingestion engine to receive events.
func (q *EventQueue) Out() <-chan *events.CanonicalEvent {
	return q.outCh
}

// Enqueue provides the instrumented enqueue API. It blocks until capacity is available,
// ctx is cancelled, or the queue is closed. If the queue is at capacity, the call
// increments BlockedEnqueueAttemptsTotal and waits for a consumer to free a slot.
func (q *EventQueue) Enqueue(ctx context.Context, ev *events.CanonicalEvent) error {
	if ev == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrQueueClosed
	}

	// If capacity is available and no previous waiters are queued:
	if q.count < q.capacity && len(q.waiters) == 0 {
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
		return nil
	}

	// Queue is full or prior waiters exist: this attempt is blocked.
	q.blockedEnqueueAttempts.Add(1)

	w := &enqueueWaiter{
		ev:    ev,
		ready: make(chan struct{}),
	}
	q.waiters = append(q.waiters, w)
	q.mu.Unlock()

	select {
	case <-w.ready:
		return w.err
	case <-ctx.Done():
		q.mu.Lock()
		// Double check if w was already fulfilled just as ctx cancelled
		select {
		case <-w.ready:
			q.mu.Unlock()
			return w.err
		default:
		}

		for i, cand := range q.waiters {
			if cand == w {
				q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
				break
			}
		}
		q.mu.Unlock()
		return ctx.Err()
	}
}

// TryEnqueue attempts to enqueue an event without blocking. Returns true if enqueued,
// false if the queue is full, or ErrQueueClosed if closed.
func (q *EventQueue) TryEnqueue(ev *events.CanonicalEvent) (bool, error) {
	if ev == nil {
		return true, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return false, ErrQueueClosed
	}
	if q.count < q.capacity && len(q.waiters) == 0 {
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
		return true, nil
	}
	return false, nil
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
	for ev := range q.inCh {
		if err := q.Enqueue(context.Background(), ev); err != nil {
			break
		}
	}

	q.mu.Lock()
	q.closed = true
	for _, w := range q.waiters {
		w.err = ErrQueueClosed
		close(w.ready)
	}
	q.waiters = nil
	q.notEmpty.Broadcast()
	q.mu.Unlock()
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

		// If waiters are pending, immediately admit the oldest waiting producer
		if len(q.waiters) > 0 {
			w := q.waiters[0]
			q.waiters = q.waiters[1:]

			q.ring[q.tail] = item{
				event:      w.ev,
				enqueuedAt: time.Now(),
			}
			q.tail = (q.tail + 1) % q.capacity
			q.count++

			if q.count == q.capacity {
				q.pressureEpisodes.Add(1)
				q.fullStartTime = time.Now()
			}
			close(w.ready)
		}
		q.mu.Unlock()
	}
}
