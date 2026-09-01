package queue

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestConsumerShutdown(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis/Valkey not available, skipping test: %v", err)
	}

	consumer := NewConsumer(client, nil, "test_shutdown_stream", "test_shutdown_group", "test_shutdown_consumer")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	// We use NumGoroutine to ensure the recovery loop terminates.
	// It's a heuristic, but effectively catches leaks if the loop ignores context.
	time.Sleep(100 * time.Millisecond) // Let existing background routines settle
	numGoroutinesBefore := runtime.NumGoroutine()

	go func() {
		_ = consumer.Start(ctx)
		close(done)
	}()

	// Give it a moment to start the main loop and the recovery goroutine
	time.Sleep(500 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for Start() to return
	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not return within 2s after context cancellation")
	}

	// Give the background recovery goroutine a moment to process the cancellation and exit
	time.Sleep(200 * time.Millisecond)

	numGoroutinesAfter := runtime.NumGoroutine()
	// Allow a small delta for internal Go runtime background routines,
	// but if it's strictly > +2, we might have leaked our recovery loop.
	if numGoroutinesAfter > numGoroutinesBefore+2 {
		t.Errorf("Potential goroutine leak: had %d before, now have %d", numGoroutinesBefore, numGoroutinesAfter)
	}
}
