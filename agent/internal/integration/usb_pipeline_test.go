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

	"redcyberfox/agent/internal/collectors/usb"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestUSBPipeline_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "events.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	// 1. Mock Server
	var receivedBody []byte
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/ingest" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted) // 202
	}))
	defer srv.Close()

	// 2. Storage
	db := storage.NewSQLiteStorage(dbPath, idPath, 1024*1024*10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.Init(ctx); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	// 4. USB Collector Config
	cfg := &config.Config{
		TenantID: "tenant-usb",
		SiteID:   "site-usb",
		APIAddr:  srv.URL,
	}

	usbCol := usb.NewCollector(cfg)

	// Inject a mock watcher for the integration test
	originalWatcher := usb.GetStartOSWatcher()
	defer usb.SetStartOSWatcher(originalWatcher)

	usb.SetStartOSWatcher(func(wCtx context.Context, out chan<- usb.USBEvent) error {
		out <- usb.USBEvent{
			Action:       "USB_INSERT",
			VendorID:     "1234",
			ProductID:    "5678",
			SerialNumber: "SN123",
		}
		<-wCtx.Done()
		return nil
	})

	// 5. Forwarder
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("Failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	// 3. Central Pipeline (simulated main loop)
	centralEvents := make(chan *events.CanonicalEvent, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range centralEvents {
			if err := db.Store(ctx, ev); err != nil {
				t.Errorf("db store failed: %v", err)
			}
			fwd.Wakeup()
		}
	}()

	if err := usbCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("collector start failed: %v", err)
	}

	// Wait for event to propagate
	time.Sleep(500 * time.Millisecond)

	usbCol.Stop()
	close(centralEvents)
	wg.Wait()

	mu.Lock()
	body := receivedBody
	mu.Unlock()

	if len(body) == 0 {
		t.Fatalf("Forwarder did not send payload")
	}

	var ev events.CanonicalEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	if ev.Category != "usb" || ev.Action != "USB_INSERT" {
		t.Errorf("Expected usb/USB_INSERT, got %s/%s", ev.Category, ev.Action)
	}
	if ev.Metadata["vendor_id"] != "1234" {
		t.Errorf("Expected metadata missing")
	}

	// Prove local removal after 202
	// Ensure enough time passed for removal
	time.Sleep(200 * time.Millisecond)

	row := db.GetDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("Failed to check db: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 events in db, got %d", count)
	}
}
