package inventory

import (
	"context"
	"runtime"
	"testing"
	"time"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func TestInventoryCollector_ValidSnapshot(t *testing.T) {
	cfg := &config.Config{
		TenantID: "tenant-1",
		SiteID:   "site-1",
	}

	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		// 1. Check Event Identity
		if ev.EventID == "" {
			t.Errorf("Expected EventID to be generated, got empty")
		}

		// 2. SeqNo must remain unassigned
		if ev.SeqNo != 0 {
			t.Errorf("Expected SeqNo to be 0 (unassigned), got %d", ev.SeqNo)
		}

		// 3. Category/Action
		if ev.Category != "inventory" {
			t.Errorf("Expected category inventory, got %s", ev.Category)
		}
		if ev.Action != "INVENTORY_SNAPSHOT" {
			t.Errorf("Expected action INVENTORY_SNAPSHOT, got %s", ev.Action)
		}

		// 4. Metadata
		if osVal, ok := ev.Metadata["os"]; !ok || osVal != runtime.GOOS {
			t.Errorf("Expected os %s, got %v", runtime.GOOS, osVal)
		}
		if _, ok := ev.Metadata["num_cpu"]; !ok {
			t.Errorf("Expected num_cpu in metadata")
		}
		if _, ok := ev.Metadata["interfaces"]; !ok {
			t.Errorf("Expected interfaces in metadata")
		}

		// 5. OccurredAt semantics
		if ev.OccurredAt.IsZero() {
			t.Errorf("Expected occurred_at to be populated")
		}

	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout waiting for INVENTORY_SNAPSHOT")
	}
}

func TestInventoryCollector_IntervalDisabled(t *testing.T) {
	cfg := &config.Config{
		TenantID:          "tenant-1",
		SiteID:            "site-1",
		InventoryInterval: "disabled",
	}

	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	// Should get initial snapshot
	select {
	case <-out:
		// success
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout waiting for initial snapshot")
	}

	// Should NOT get another one in disabled state
	select {
	case <-out:
		t.Fatalf("Received unexpected second snapshot when interval is disabled")
	case <-time.After(100 * time.Millisecond):
		// success
	}
}

func TestInventoryCollector_ShutdownWhileBlocked(t *testing.T) {
	cfg := &config.Config{
		TenantID: "tenant-1",
	}

	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent) // Unbuffered channel
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}

	// Wait to ensure it blocks
	time.Sleep(100 * time.Millisecond)

	// Stop while blocked
	stopDone := make(chan struct{})
	go func() {
		col.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop hung")
	}
}
