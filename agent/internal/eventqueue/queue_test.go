package eventqueue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

func TestEventQueue_FIFOAndAge(t *testing.T) {
	q := New(10)
	defer q.Close()

	// Initial empty queue: age should be invalid
	age := q.OldestEventAge()
	if age.Valid {
		t.Fatalf("expected OldestEventAge to be invalid on empty queue, got %v", age)
	}
	if q.Depth().Value != 0 {
		t.Fatalf("expected depth 0, got %d", q.Depth().Value)
	}
	if q.Capacity().Value != 10 {
		t.Fatalf("expected capacity 10, got %d", q.Capacity().Value)
	}

	ev1 := &events.CanonicalEvent{EventID: "ev-1"}
	ev2 := &events.CanonicalEvent{EventID: "ev-2"}

	q.In() <- ev1
	time.Sleep(30 * time.Millisecond) // Allow age to progress
	q.In() <- ev2

	// Check depth and age
	time.Sleep(20 * time.Millisecond)
	if q.Depth().Value != 2 {
		t.Fatalf("expected depth 2, got %d", q.Depth().Value)
	}

	age = q.OldestEventAge()
	if !age.Valid || age.Value < 0.02 {
		t.Fatalf("expected age >= 0.02s, got %v", age)
	}

	// Dequeue first
	out1 := <-q.Out()
	if out1.EventID != "ev-1" {
		t.Fatalf("expected FIFO ev-1, got %s", out1.EventID)
	}

	// Dequeue second
	out2 := <-q.Out()
	if out2.EventID != "ev-2" {
		t.Fatalf("expected FIFO ev-2, got %s", out2.EventID)
	}

	time.Sleep(20 * time.Millisecond)
	if q.Depth().Value != 0 {
		t.Fatalf("expected depth 0 after dequeue, got %d", q.Depth().Value)
	}
	if q.OldestEventAge().Valid {
		t.Fatalf("expected age invalid after draining")
	}
}

func TestEventQueue_BackpressureAndBlockedEnqueueAttempts(t *testing.T) {
	capacity := 4
	q := New(capacity)
	defer q.Close()

	ctx := context.Background()

	// Fill queue to capacity (4 items) via instrumented Enqueue API
	for i := 1; i <= capacity; i++ {
		if err := q.Enqueue(ctx, &events.CanonicalEvent{EventID: fmt.Sprintf("ev-%d", i)}); err != nil {
			t.Fatalf("unexpected err filling queue: %v", err)
		}
	}

	if q.Depth().Value != uint64(capacity) {
		t.Fatalf("expected depth %d, got %d", capacity, q.Depth().Value)
	}
	if q.SaturationPercent().Value != 100.0 {
		t.Fatalf("expected saturation 100%%, got %v", q.SaturationPercent().Value)
	}
	if q.PressureEpisodesTotal() != 1 {
		t.Fatalf("expected 1 pressure episode, got %d", q.PressureEpisodesTotal())
	}
	if q.BlockedEnqueueAttemptsTotal() != 0 {
		t.Fatalf("expected 0 blocked enqueue attempts while filling to capacity, got %d", q.BlockedEnqueueAttemptsTotal())
	}

	// Attempt to send 5th event: must block in Enqueue
	ev5Sent := make(chan struct{})
	go func() {
		if err := q.Enqueue(ctx, &events.CanonicalEvent{EventID: "ev-5"}); err != nil {
			t.Errorf("unexpected err ev-5: %v", err)
		}
		close(ev5Sent)
	}()

	select {
	case <-ev5Sent:
		t.Fatalf("expected send of ev-5 to block on full queue, but it completed immediately")
	case <-time.After(40 * time.Millisecond):
		// Expected to block while queue is full
	}

	// Read 1 item from Out to unblock ev-5
	head := <-q.Out()
	if head.EventID != "ev-1" {
		t.Fatalf("expected ev-1, got %s", head.EventID)
	}

	select {
	case <-ev5Sent:
		// Succeeded unblocking
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for ev-5 send to complete after dequeue")
	}

	// Blocked enqueue attempts should now be exactly 1
	if q.BlockedEnqueueAttemptsTotal() != 1 {
		t.Fatalf("expected 1 blocked enqueue attempt after ev-5 was unblocked, got %d", q.BlockedEnqueueAttemptsTotal())
	}
	// Pressure episodes should be 2 (first 1..4, second after ev-5 refilled the queue)
	if q.PressureEpisodesTotal() != 2 {
		t.Fatalf("expected 2 pressure episodes, got %d", q.PressureEpisodesTotal())
	}

	if q.Depth().Value != uint64(capacity) {
		t.Fatalf("expected depth back at capacity %d, got %d", capacity, q.Depth().Value)
	}

	// Time spent full should be > 0
	if q.TimeSpentFullSeconds() <= 0 {
		t.Fatalf("expected TimeSpentFullSeconds > 0, got %v", q.TimeSpentFullSeconds())
	}

	// Drain everything
	var drained []string
	for i := 0; i < capacity; i++ {
		out := <-q.Out()
		drained = append(drained, out.EventID)
	}

	if len(drained) != 4 { // ev-2, ev-3, ev-4, ev-5
		t.Fatalf("expected 4 remaining items drained, got %d", len(drained))
	}
	if drained[0] != "ev-2" || drained[3] != "ev-5" {
		t.Fatalf("unexpected drained items: %v", drained)
	}
}

func TestEventQueue_GracefulDrainOnClose(t *testing.T) {
	q := New(50)

	// Send 25 events
	for i := 1; i <= 25; i++ {
		q.In() <- &events.CanonicalEvent{EventID: fmt.Sprintf("drain-%d", i)}
	}

	// Close queue
	q.Close()

	// Receiver drains all 25 events and then receives channel close
	var received []string
	for ev := range q.Out() {
		received = append(received, ev.EventID)
	}
	q.WaitDrain()

	if len(received) != 25 {
		t.Fatalf("expected 25 drained events, got %d", len(received))
	}
	if received[0] != "drain-1" || received[24] != "drain-25" {
		t.Fatalf("FIFO ordering corrupted during drain")
	}
}

func TestEventQueue_ConcurrentSenders(t *testing.T) {
	capacity := 20
	q := New(capacity)
	senderCount := 10
	eventsPerSender := 50
	totalExpected := senderCount * eventsPerSender

	var wg sync.WaitGroup
	for s := 0; s < senderCount; s++ {
		wg.Add(1)
		go func(senderID int) {
			defer wg.Done()
			for i := 0; i < eventsPerSender; i++ {
				q.In() <- &events.CanonicalEvent{
					EventID: fmt.Sprintf("sender-%d-ev-%d", senderID, i),
				}
			}
		}(s)
	}

	var receiveCount int
	receiveDone := make(chan struct{})
	go func() {
		for range q.Out() {
			receiveCount++
		}
		close(receiveDone)
	}()

	wg.Wait()
	q.Close()
	<-receiveDone
	q.WaitDrain()

	if receiveCount != totalExpected {
		t.Fatalf("expected %d events, got %d", totalExpected, receiveCount)
	}
	// Under high contention on capacity 20 with 500 events, blocked attempts should be observed
	if q.BlockedEnqueueAttemptsTotal() == 0 {
		t.Fatalf("expected blocked enqueue attempts > 0 under contention")
	}
}

func TestEventQueue_InstrumentedEnqueue_MultipleBlockedProducers(t *testing.T) {
	capacity := 2
	q := New(capacity)
	defer q.Close()

	ctx := context.Background()

	// Fill queue to capacity (2 items)
	if err := q.Enqueue(ctx, &events.CanonicalEvent{EventID: "ev-1"}); err != nil {
		t.Fatalf("unexpected err ev-1: %v", err)
	}
	if err := q.Enqueue(ctx, &events.CanonicalEvent{EventID: "ev-2"}); err != nil {
		t.Fatalf("unexpected err ev-2: %v", err)
	}

	if q.Depth().Value != 2 {
		t.Fatalf("expected depth 2, got %d", q.Depth().Value)
	}
	if q.BlockedEnqueueAttemptsTotal() != 0 {
		t.Fatalf("expected 0 blocked attempts before saturation, got %d", q.BlockedEnqueueAttemptsTotal())
	}
	if q.PressureEpisodesTotal() != 1 {
		t.Fatalf("expected 1 pressure episode, got %d", q.PressureEpisodesTotal())
	}

	// Launch 3 concurrent producers attempting to Enqueue on full queue
	const blockedProducers = 3
	var startedWg sync.WaitGroup
	var completedWg sync.WaitGroup
	errCh := make(chan error, blockedProducers)

	startedWg.Add(blockedProducers)
	completedWg.Add(blockedProducers)

	for i := 1; i <= blockedProducers; i++ {
		go func(id int) {
			ev := &events.CanonicalEvent{EventID: fmt.Sprintf("blocked-ev-%d", id)}
			startedWg.Done()
			err := q.Enqueue(ctx, ev)
			errCh <- err
			completedWg.Done()
		}(i)
	}

	startedWg.Wait()

	// Wait deterministically for all 3 producers to be recorded as blocked
	deadline := time.Now().Add(500 * time.Millisecond)
	for q.BlockedEnqueueAttemptsTotal() < blockedProducers && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if q.BlockedEnqueueAttemptsTotal() != blockedProducers {
		t.Fatalf("expected %d blocked enqueue attempts, got %d", blockedProducers, q.BlockedEnqueueAttemptsTotal())
	}

	// Verify all 3 producers are still blocked (no errors on errCh)
	select {
	case err := <-errCh:
		t.Fatalf("producer completed prematurely with error: %v", err)
	default:
	}

	// Dequeue 2 items from Out to allow waiting producers to be admitted
	out1 := <-q.Out()
	if out1.EventID != "ev-1" {
		t.Fatalf("expected ev-1, got %s", out1.EventID)
	}
	out2 := <-q.Out()
	if out2.EventID != "ev-2" {
		t.Fatalf("expected ev-2, got %s", out2.EventID)
	}

	// Dequeue remaining items as producers are admitted
	var admittedEvents []string
	drainDone := make(chan struct{})
	go func() {
		for i := 0; i < blockedProducers; i++ {
			admitted := <-q.Out()
			admittedEvents = append(admittedEvents, admitted.EventID)
		}
		close(drainDone)
	}()

	completedWg.Wait()
	<-drainDone

	for i := 0; i < blockedProducers; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("producer failed with error: %v", err)
		}
	}

	if len(admittedEvents) != blockedProducers {
		t.Fatalf("expected %d admitted events, got %d", blockedProducers, len(admittedEvents))
	}
}

func TestEventQueue_InstrumentedEnqueue_ContextCancellation(t *testing.T) {
	capacity := 1
	q := New(capacity)
	defer q.Close()

	if err := q.Enqueue(context.Background(), &events.CanonicalEvent{EventID: "initial-ev"}); err != nil {
		t.Fatalf("failed initial enqueue: %v", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		err := q.Enqueue(ctx, &events.CanonicalEvent{EventID: "cancelled-ev"})
		errCh <- err
	}()

	// Wait deterministically until enqueue blocks
	deadline := time.Now().Add(500 * time.Millisecond)
	for q.BlockedEnqueueAttemptsTotal() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if q.BlockedEnqueueAttemptsTotal() != 1 {
		t.Fatalf("expected 1 blocked enqueue attempt, got %d", q.BlockedEnqueueAttemptsTotal())
	}

	// Cancel context while producer is blocked
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for cancelled producer to return")
	}

	// Verify queue is healthy and can continue operating
	out := <-q.Out()
	if out.EventID != "initial-ev" {
		t.Fatalf("expected initial-ev, got: %s", out.EventID)
	}

	// Can enqueue new event with fresh context
	if err := q.Enqueue(context.Background(), &events.CanonicalEvent{EventID: "recovery-ev"}); err != nil {
		t.Fatalf("failed recovery enqueue: %v", err)
	}

	out2 := <-q.Out()
	if out2.EventID != "recovery-ev" {
		t.Fatalf("expected recovery-ev, got: %s", out2.EventID)
	}
}

func TestEventQueue_TryEnqueue(t *testing.T) {
	q := New(1)
	defer q.Close()

	ok, err := q.TryEnqueue(&events.CanonicalEvent{EventID: "try-1"})
	if !ok || err != nil {
		t.Fatalf("expected TryEnqueue ok, got ok=%v, err=%v", ok, err)
	}

	// TryEnqueue on full queue should return false, nil without blocking
	ok, err = q.TryEnqueue(&events.CanonicalEvent{EventID: "try-2"})
	if ok || err != nil {
		t.Fatalf("expected TryEnqueue false on full queue, got ok=%v, err=%v", ok, err)
	}

	<-q.Out()

	ok, err = q.TryEnqueue(&events.CanonicalEvent{EventID: "try-3"})
	if !ok || err != nil {
		t.Fatalf("expected TryEnqueue ok after freeing slot, got ok=%v, err=%v", ok, err)
	}
}
