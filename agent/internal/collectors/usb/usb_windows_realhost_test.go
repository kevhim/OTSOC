//go:build windows
// +build windows

package usb

import (
	"context"
	"testing"
	"time"
)

func TestRealWindowsWatcher_Lifecycle(t *testing.T) {
	// This is a RUNTIME INTEGRATION TEST.
	// It invokes real Win32 APIs (defaultStartOSWatcher) to ensure
	// window class registration, message pump, and clean shutdown are working.

	ch := make(chan USBEvent, 10)

	// Test 1: Start and stop cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	
	errCh := make(chan error, 1)
	go func() {
		errCh <- defaultStartOSWatcher(ctx, ch)
	}()

	// Allow some time for window creation and message loop to start.
	time.Sleep(500 * time.Millisecond)

	// Cancel the context to initiate shutdown.
	cancel()

	// defaultStartOSWatcher must return within a reasonable time.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first lifecycle test failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("first lifecycle test timed out waiting for defaultStartOSWatcher to exit (message loop hang)")
	}

	// Test 2: Start again to ensure the window class was properly unregistered.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- defaultStartOSWatcher(ctx2, ch)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel2()

	select {
	case err := <-errCh2:
		if err != nil {
			t.Fatalf("second lifecycle test failed (did unregister fail?): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("second lifecycle test timed out")
	}
}
