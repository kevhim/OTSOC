package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	// Start an httptest.Server
	var (
		mu             sync.Mutex
		received       int
		statusToReturn int = http.StatusAccepted
		lastBody       []byte
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.URL.Path != "/v1/ingest" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		tenantID := r.URL.Query().Get("tenant_id")
		if tenantID != "test-tenant" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing or invalid tenant_id"))
			return
		}

		body, _ := io.ReadAll(r.Body)
		lastBody = body
		received++

		w.WriteHeader(statusToReturn)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	cfg := &config.Config{
		APIAddr:  ts.URL, // Use the test server URL
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

	deviceID := db.GetDeviceID()

	// 3. Process Collector
	processEvents := make(chan *events.CanonicalEvent, 100)
	procCol := process.NewCollector(cfg)
	if err := procCol.Start(ctx, processEvents); err != nil {
		t.Fatalf("Process collector failed to start: %v", err)
	}

	// 4. Central Ingestion Loop
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)

	// This channel helps us wait for events to at least reach the DB
	storeComplete := make(chan struct{}, 10)

	go func() {
		defer ingestWg.Done()
		for ev := range processEvents {
			ev.TenantID = cfg.TenantID
			ev.SiteID = cfg.SiteID
			ev.AssetID = deviceID

			storeCtx, storeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := db.Store(storeCtx, ev); err != nil {
				t.Errorf("Store failed: %v", err)
				storeCancel()
				continue
			}
			storeCancel()

			fwd.Wakeup()

			select {
			case storeComplete <- struct{}{}:
			default:
			}
		}
	}()

	// Wait for the collector to establish its initial OS baseline
	readyCtx, readyCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	err := procCol.WaitReady(readyCtx)
	readyCancel()
	if err != nil {
		procCol.Reconcile(ctx, &process.Snapshot{})
	}

	// Helper to inject a process event
	injectProcessEvent := func(pid int, name string) {
		now := time.Now()
		inst := &process.Instance{
			PID:       pid,
			StartTime: now,
			Name:      &name,
		}
		procCol.HandleEvent(ctx, inst, true)

		// Wait for it to be stored
		select {
		case <-storeComplete:
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for event to be stored")
		}
	}

	// ==========================================
	// Test 1: Successful Ingestion (HTTP 202) -> Event Removed
	// ==========================================
	mu.Lock()
	statusToReturn = http.StatusAccepted
	mu.Unlock()

	injectProcessEvent(1234, "test_202.exe")

	// Wait for the forwarder to process it
	time.Sleep(1 * time.Second)

	// Verify persistence: should be 0 PENDING because it was removed
	var count int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 pending events after 202, got %d", count)
	}

	mu.Lock()
	if received == 0 {
		t.Fatalf("Expected httptest server to receive event")
	}

	var ev events.CanonicalEvent
	if err := json.Unmarshal(lastBody, &ev); err != nil {
		t.Fatalf("Failed to parse request body: %v", err)
	}
	if ev.TenantID != "test-tenant" || ev.Action != "PROCESS_START" {
		t.Fatalf("Invalid CanonicalEvent payload: %+v", ev)
	}
	mu.Unlock()

	// ==========================================
	// Test 2: HTTP 400 -> Event moved to DLQ
	// ==========================================
	mu.Lock()
	statusToReturn = http.StatusBadRequest
	mu.Unlock()

	injectProcessEvent(1235, "test_400.exe")

	// Wait for the forwarder to process it
	time.Sleep(1 * time.Second)

	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM dead_letter_queue").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 DLQ event after 400, got %d", count)
	}

	// ==========================================
	// Test 3: HTTP 429 -> Event remains PENDING / FAILED (Durable/retryable)
	// ==========================================
	mu.Lock()
	statusToReturn = http.StatusTooManyRequests
	mu.Unlock()

	injectProcessEvent(1236, "test_429.exe")

	// Wait for the forwarder to process it
	time.Sleep(1 * time.Second)

	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'FAILED' OR state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 failed/pending event after 429, got %d", count)
	}

	// Shutdown Sequence verification
	cancel()             // Cancel global context
	procCol.Stop()       // 1. Stop collector
	close(processEvents) // 2. Close channel, draining loop
	ingestWg.Wait()      // 3. Wait for loop to exit
}
