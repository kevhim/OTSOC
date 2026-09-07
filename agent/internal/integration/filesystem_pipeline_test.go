package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/collectors/filesystem"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestFilesystemPipeline_Integration(t *testing.T) {
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

	watchDir := filepath.Join(tmpDir, "watch")
	os.MkdirAll(watchDir, 0755)

	cfg := &config.Config{
		APIAddr:      ts.URL,
		TenantID:     "test-tenant",
		SiteID:       "test-site",
		MonitorPaths: []string{watchDir},
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

	// 3. Filesystem Collector
	fsEvents := make(chan *events.CanonicalEvent, 100)
	fsCol := filesystem.NewCollector(cfg)
	if err := fsCol.Start(ctx, fsEvents); err != nil {
		t.Fatalf("Filesystem collector failed to start: %v", err)
	}

	// 4. Central Ingestion Loop
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)

	storeComplete := make(chan struct{}, 10)

	go func() {
		defer ingestWg.Done()
		for ev := range fsEvents {
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

	// Wait for collector to initialize
	time.Sleep(200 * time.Millisecond)

	// ==========================================
	// Test: Filesystem Telemetry (HTTP 202) -> Event Removed
	// ==========================================
	mu.Lock()
	statusToReturn = http.StatusAccepted
	mu.Unlock()

	// Perform a filesystem action
	testFile := filepath.Join(watchDir, "telemetry_test.txt")
	os.WriteFile(testFile, []byte("hello"), 0644)

	// Wait for storage
	select {
	case <-storeComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for fs event to be stored")
	}

	// Wait for forwarder
	time.Sleep(1 * time.Second)

	mu.Lock()
	if received == 0 {
		t.Fatalf("Expected httptest server to receive event")
	}
	var fsEv events.CanonicalEvent
	if err := json.Unmarshal(lastBody, &fsEv); err != nil {
		t.Fatalf("Failed to parse request body: %v", err)
	}
	if fsEv.Category != "filesystem" || (fsEv.Action != "FILE_CREATE" && fsEv.Action != "FILE_MODIFY") {
		t.Fatalf("Expected filesystem FILE_CREATE/MODIFY event, got %s %s", fsEv.Category, fsEv.Action)
	}
	if fsEv.EventID == "" {
		t.Fatalf("Expected EventID to be populated")
	}
	mu.Unlock()

	// 5. Verify local removal
	var pendingCount int
	err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", fsEv.EventID).Scan(&pendingCount)
	if err != nil {
		t.Fatalf("Failed to query DB: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("Expected event %s to be successfully removed from the local event store after central acceptance, but found count %d", fsEv.EventID, pendingCount)
	}

	// Shutdown Sequence verification
	cancel()        // Cancel global context
	fsCol.Stop()    // 1. Stop collector
	close(fsEvents) // 2. Close channel, draining loop
	ingestWg.Wait() // 3. Wait for loop to exit
}
