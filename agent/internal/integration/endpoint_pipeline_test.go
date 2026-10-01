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
	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

type observableStorage struct {
	interfaces.Storage
	removed chan string
	dlq     chan string
	failed  chan string
}

func (s *observableStorage) RemoveEvent(ctx context.Context, eventID string) error {
	err := s.Storage.RemoveEvent(ctx, eventID)
	if err != nil {
		return err
	}
	select {
	case s.removed <- eventID:
	default:
	}
	return nil
}

func (s *observableStorage) MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error {
	err := s.Storage.MoveToDLQ(ctx, event, failureType, failureReason)
	if err != nil {
		return err
	}
	select {
	case s.dlq <- event.EventID:
	default:
	}
	return nil
}

func (s *observableStorage) MarkFailed(ctx context.Context, eventID string, retryAfter int, failErr error) error {
	err := s.Storage.MarkFailed(ctx, eventID, retryAfter, failErr)
	if err != nil {
		return err
	}
	select {
	case s.failed <- eventID:
	default:
	}
	return nil
}

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

	obsDb := &observableStorage{
		Storage: db,
		removed: make(chan string, 10),
		dlq:     make(chan string, 10),
		failed:  make(chan string, 10),
	}

	// 2. Forwarder
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, obsDb)
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

			// Replace arbitrary timeout with test context to simulate normal operation backpressure
			if err := db.Store(ctx, ev); err != nil {
				t.Errorf("Store failed: %v", err)
				continue
			}

			fwd.Wakeup()

			select {
			case storeComplete <- struct{}{}:
			default:
			}
		}
	}()

	// Register teardown defers in correct LIFO order:
	// 3. Wait for ingestion loop to finish draining
	defer ingestWg.Wait()
	// 2. Close channel to signal ingestion loop to drain and exit
	defer close(processEvents)
	// 1. Stop the process collector from emitting more events
	defer procCol.Stop()

	// Wait for the collector to establish its initial OS baseline
	readyCtx, readyCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	err := procCol.WaitReady(readyCtx)
	readyCancel()
	if err != nil {
		procCol.Reconcile(ctx, &process.Snapshot{})
	}

	// STOP the process collector to prevent background OS events from polluting
	// the integration test assertions. We will manually inject events for the test.
	procCol.Stop()

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

	select {
	case <-obsDb.removed:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for event to be removed")
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

	select {
	case <-obsDb.dlq:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for event to move to DLQ")
	}

	// ==========================================
	// Test 3: HTTP 429 -> Event remains PENDING / FAILED (Durable/retryable)
	// ==========================================
	mu.Lock()
	statusToReturn = http.StatusTooManyRequests
	mu.Unlock()

	injectProcessEvent(1236, "test_429.exe")

	select {
	case <-obsDb.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for event to be marked failed")
	}
}
