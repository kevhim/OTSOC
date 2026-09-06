package network

import (
	"context"
	"fmt"
	"testing"
	"time"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func TestNetworkCollector_PartialFailure(t *testing.T) {
	originalGetConns := getConnections
	defer func() { getConnections = originalGetConns }()

	getConnections = func() ([]Connection, []string, error) {
		return []Connection{
			{Protocol: "TCP", SrcIP: "127.0.0.1", SrcPort: 80, DstIP: "0.0.0.0", DstPort: 0, State: "LISTEN"},
		}, []string{"NETWORK_UDP4_FAILED", "NETWORK_UDP6_FAILED"}, nil
	}

	cfg := &config.Config{TenantID: "tenant-1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		if len(ev.QualityFlags) != 2 {
			t.Errorf("Expected 2 quality flags, got %d", len(ev.QualityFlags))
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout")
	}
}

func TestNetworkCollector_CompleteFailure(t *testing.T) {
	originalGetConns := getConnections
	defer func() { getConnections = originalGetConns }()

	getConnections = func() ([]Connection, []string, error) {
		return nil, nil, fmt.Errorf("complete OS failure")
	}

	cfg := &config.Config{TenantID: "tenant-1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	select {
	case <-out:
		t.Fatalf("Expected no snapshot")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNetworkCollector_EmptySuccess(t *testing.T) {
	originalGetConns := getConnections
	defer func() { getConnections = originalGetConns }()

	getConnections = func() ([]Connection, []string, error) {
		return []Connection{}, nil, nil
	}

	cfg := &config.Config{TenantID: "tenant-1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		conns := ev.Metadata["connections"].([]map[string]interface{})
		if len(conns) != 0 {
			t.Errorf("Expected 0 connections, got %d", len(conns))
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout")
	}
}

func TestNetworkCollector_EventIdentity(t *testing.T) {
	originalGetConns := getConnections
	defer func() { getConnections = originalGetConns }()

	getConnections = func() ([]Connection, []string, error) {
		return []Connection{{Protocol: "UDP", SrcIP: "1.1.1.1", SrcPort: 53}}, nil, nil
	}

	cfg := &config.Config{TenantID: "tenant-1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 5)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		if ev.EventID == "" {
			t.Errorf("Expected EventID")
		}
		if ev.SeqNo != 0 {
			t.Errorf("Expected SeqNo 0")
		}
		if ev.OccurredAt.IsZero() {
			t.Errorf("Expected occurred_at")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout")
	}
}

func TestNetworkCollector_ShutdownWhileBlocked(t *testing.T) {
	originalGetConns := getConnections
	defer func() { getConnections = originalGetConns }()

	getConnections = func() ([]Connection, []string, error) {
		return []Connection{{Protocol: "TCP"}}, nil, nil
	}

	cfg := &config.Config{TenantID: "tenant-1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent) // unbuffered
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		col.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop hung")
	}
}
