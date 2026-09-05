package integration

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/collectors/process"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestEndpointPipeline_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	cfg := &config.Config{
		APIAddr:  "http://localhost:8080",
		TenantID: "test-tenant",
		SiteID:   "test-site",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Storage
	db := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := db.Init(ctx); err != nil {
		t.Fatalf("Storage init failed: %v", err)
	}
	defer db.Close()

	// 2. Forwarder
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("Forwarder start failed: %v", err)
	}
	defer fwd.Stop()

	// 3. Process Collector
	processEvents := make(chan *events.CanonicalEvent, 100)
	procCol := process.NewCollector(cfg)
	if err := procCol.Start(ctx, processEvents); err != nil {
		t.Fatalf("Process collector failed to start: %v", err)
	}

	deviceID := db.GetDeviceID()

	// 4. Central Ingestion Loop
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	ingestComplete := make(chan struct{})
	go func() {
		defer ingestWg.Done()
		for ev := range processEvents {
			ev.TenantID = cfg.TenantID
			ev.SiteID = cfg.SiteID
			ev.AssetID = deviceID

			// Validation is omitted here because Storage owns EventID assignment.

			storeCtx, storeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := db.Store(storeCtx, ev); err != nil {
				t.Errorf("Store failed: %v", err)
				storeCancel()
				continue
			}
			storeCancel()

			fwd.Wakeup()
			
			// For testing, we signal that at least one event was ingested
			select {
			case <-ingestComplete:
			default:
				close(ingestComplete)
			}
		}
	}()

	// Wait for the collector to establish its initial OS baseline
	// so that our injected event isn't dropped.
	// If the platform is mocked/unsupported, it will timeout, and we inject an empty snapshot to unblock it.
	readyCtx, readyCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	err := procCol.WaitReady(readyCtx)
	readyCancel()
	
	if err != nil {
		// Fallback for unsupported platforms where startOSAdapter is a no-op
		procCol.Reconcile(ctx, &process.Snapshot{})
	}

	// Simulate a process lifecycle event. 
	now := time.Now()
	inst := &process.Instance{
		PID:       1234,
		StartTime: now,
	}
	name := "test_proc.exe"
	inst.Name = &name
	
	procCol.HandleEvent(ctx, inst, true)

	// Wait for ingestion
	select {
	case <-ingestComplete:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for event ingestion")
	}

	// Verify persistence
	var count int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 pending event, got %d", count)
	}

	// Check payload enrichment
	var payload string
	if err := db.GetDB().QueryRow("SELECT payload FROM events WHERE state = 'PENDING'").Scan(&payload); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}

	ev, err := events.Deserialize([]byte(payload))
	if err != nil {
		t.Fatalf("Failed to deserialize event: %v", err)
	}

	if ev.TenantID != "test-tenant" {
		t.Errorf("Expected TenantID 'test-tenant', got '%s'", ev.TenantID)
	}
	if ev.SiteID != "test-site" {
		t.Errorf("Expected SiteID 'test-site', got '%s'", ev.SiteID)
	}
	if ev.AssetID != deviceID {
		t.Errorf("Expected AssetID '%s', got '%s'", deviceID, ev.AssetID)
	}
	if ev.Source != "agent" || ev.Category != "process" || ev.Action != "PROCESS_START" {
		t.Errorf("Expected process start event, got source=%s category=%s action=%s", ev.Source, ev.Category, ev.Action)
	}
	if ev.EventID == "" {
		t.Errorf("Expected EventID to be populated by storage")
	}
	if ev.SeqNo == 0 {
		t.Errorf("Expected SeqNo to be populated by storage")
	}

	// Shutdown Sequence verification
	cancel() // Cancel global context
	
	procCol.Stop()        // 1. Stop collector
	close(processEvents)  // 2. Close channel, draining loop
	ingestWg.Wait()       // 3. Wait for loop to exit
	fwd.Stop()            // 4. Stop forwarder
	db.Close()            // 5. Close DB
	
	// If we get here without a deadlock or panic, the explicit shutdown sequence works perfectly.
}
