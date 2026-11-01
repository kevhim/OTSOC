package eventqueue

import (
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

	// Fill queue to capacity (4 items)
	for i := 1; i <= capacity; i++ {
		q.In() <- &events.CanonicalEvent{EventID: fmt.Sprintf("ev-%d", i)}
	}

	// Allow inPump to process items into the ring buffer
	time.Sleep(30 * time.Millisecond)

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

	// Attempt to send 5th event: must block
	ev5Sent := make(chan struct{})
	go func() {
		q.In() <- &events.CanonicalEvent{EventID: "ev-5"}
		close(ev5Sent)
	}()

	select {
	case <-ev5Sent:
		t.Fatalf("expected send of ev-5 to block on full queue, but it completed immediately")
	case <-time.After(40 * time.Millisecond):
		// Expected to block on unbuffered channel while queue is full
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

	// Allow pump to settle
	time.Sleep(30 * time.Millisecond)
	if q.Depth().Value != uint64(capacity) {
		t.Fatalf("expected depth back at capacity %d, got %d", capacity, q.Depth().Value)
	}

	// Time spent full should be > 0
	if q.TimeSpentFullSeconds() <= 0 {
		t.Fatalf("expected TimeSpentFullSeconds > 0, got %v", q.TimeSpentFullSeconds())
	}

	// Drain everything
	q.Close()
	var drainedCount int
	for range q.Out() {
		drainedCount++
	}
	q.WaitDrain()

	if drainedCount != 4 { // ev-2, ev-3, ev-4, ev-5
		t.Fatalf("expected 4 remaining items drained, got %d", drainedCount)
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
