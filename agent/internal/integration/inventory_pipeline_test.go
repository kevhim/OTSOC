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

	"redcyberfox/agent/internal/collectors/inventory"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestInventoryPipeline_Integration(t *testing.T) {
	var (
		mu       sync.Mutex
		received int
		lastBody []byte
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

		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Storage
	db := storage.NewSQLiteStorage(dbPath, identityPath, 1024*1024)
	if err := db.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}
	defer db.Close()

	// 2. Initialize Config
	cfg := &config.Config{
		APIAddr:  ts.URL,
		TenantID: "test-tenant",
		SiteID:   "test-site",
	}

	// 3. Central Channel
	centralEvents := make(chan *events.CanonicalEvent, 100)

	// 4. Start Forwarder
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("Failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	// 5. Ingestion Loop
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range centralEvents {
			if err := db.Store(ctx, ev); err != nil {
				t.Errorf("Store failed: %v", err)
			}
			fwd.Wakeup()
		}
	}()

	// 6. Start Inventory Collector
	invCol := inventory.NewCollector(cfg)
	if err := invCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start inventory collector: %v", err)
	}

	// Wait for event to propagate through HTTP roundtrip
	time.Sleep(500 * time.Millisecond)

	// Shutdown
	invCol.Stop()
	close(centralEvents)
	wg.Wait()

	// Verify HTTP Reception
	mu.Lock()
	defer mu.Unlock()

	if received != 1 {
		t.Fatalf("Expected 1 event, got %d", received)
	}

	var ev events.CanonicalEvent
	if err := json.Unmarshal(lastBody, &ev); err != nil {
		t.Fatalf("Failed to parse event: %v", err)
	}

	if ev.Category != "inventory" {
		t.Errorf("Expected category inventory, got %s", ev.Category)
	}
	if ev.Action != "INVENTORY_SNAPSHOT" {
		t.Errorf("Expected action INVENTORY_SNAPSHOT, got %s", ev.Action)
	}

	// SeqNo assertion (must have been populated by SQLite)
	if ev.SeqNo == 0 {
		t.Errorf("Expected SeqNo > 0 since it should be assigned by SQLite, got 0")
	}

	// Verify Local Removal
	queryCtx, qCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer qCancel()
	
	row := db.GetDB().QueryRowContext(queryCtx, "SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("Failed to check db for removed event: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 events in db, got %d", count)
	}
}
