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
	// Wait a tiny moment so age increases noticeably
	time.Sleep(20 * time.Millisecond)
	q.In() <- ev2

	// Check depth and age
	time.Sleep(10 * time.Millisecond) // Allow pump to process
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

	time.Sleep(10 * time.Millisecond) // Allow pump to update
	if q.Depth().Value != 0 {
		t.Fatalf("expected depth 0 after dequeue, got %d", q.Depth().Value)
	}
	if q.OldestEventAge().Valid {
		t.Fatalf("expected age invalid after draining")
	}
}

func TestEventQueue_BackpressureAndBoundedCapacity(t *testing.T) {
	capacity := 5
	q := New(capacity)

	// Fill queue to capacity
	for i := 1; i <= capacity; i++ {
		q.In() <- &events.CanonicalEvent{EventID: fmt.Sprintf("ev-%d", i)}
	}

	time.Sleep(20 * time.Millisecond)
	if q.Depth().Value != uint64(capacity) {
		t.Fatalf("expected depth %d, got %d", capacity, q.Depth().Value)
	}
	if q.SaturationPercent().Value != 100.0 {
		t.Fatalf("expected saturation 100%%, got %v", q.SaturationPercent().Value)
	}

	// A 6th event send should block until an item is read
	sendComplete := make(chan struct{})
	go func() {
		q.In() <- &events.CanonicalEvent{EventID: "ev-6"}
		close(sendComplete)
	}()

	select {
	case <-sendComplete:
		t.Fatalf("expected send beyond capacity to block, but it completed immediately")
	case <-time.After(50 * time.Millisecond):
		// Expected to block
	}

	// Read one item to unblock
	head := <-q.Out()
	if head.EventID != "ev-1" {
		t.Fatalf("expected ev-1, got %s", head.EventID)
	}

	select {
	case <-sendComplete:
		// Send unblocked as expected
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for blocked send to complete")
	}

	// Backpressure counter should have been incremented
	if q.BackpressureEventsTotal() == 0 {
		t.Fatalf("expected backpressure counter > 0, got %d", q.BackpressureEventsTotal())
	}

	q.Close()
	for range q.Out() {
	}
	q.WaitDrain()
}

func TestEventQueue_GracefulDrainOnClose(t *testing.T) {
	q := New(50)

	// Send 20 events
	for i := 1; i <= 20; i++ {
		q.In() <- &events.CanonicalEvent{EventID: fmt.Sprintf("drain-%d", i)}
	}

	// Close queue
	q.Close()

	// Receiver should drain all 20 events and then receive channel close
	var received []string
	for ev := range q.Out() {
		received = append(received, ev.EventID)
	}

	if len(received) != 20 {
		t.Fatalf("expected 20 drained events, got %d", len(received))
	}
	if received[0] != "drain-1" || received[19] != "drain-20" {
		t.Fatalf("FIFO ordering corrupted during drain")
	}
}

func TestEventQueue_ConcurrentSenders(t *testing.T) {
	q := New(100)
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

	if receiveCount != totalExpected {
		t.Fatalf("expected %d events, got %d", totalExpected, receiveCount)
	}
}
