package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/collectors/filesystem"
	"redcyberfox/agent/internal/collectors/inventory"
	"redcyberfox/agent/internal/collectors/network"
	"redcyberfox/agent/internal/collectors/process"
	"redcyberfox/agent/internal/collectors/usb"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestPhase2E4_BroadCrossSourceRegression(t *testing.T) {
	// Setup environment
	tempDir, err := os.MkdirTemp("", "cross-source-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "agent.db")
	idPath := filepath.Join(tempDir, "identity.json")

	// Create minimal valid config
	cfg := &config.Config{
		TenantID:          "tenant-1",
		SiteID:            "site-1",
		APIAddr:           "localhost:8080",
		ProcessInterval:   "1s",
		InventoryInterval: "1s",
		NetworkInterval:   "1s",
		MonitorPaths:      []string{tempDir},
	}

	// 1. Initialize Storage
	db := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := db.Init(ctx); err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	// 2. Initialize Central Events (Capacity exactly 200)
	centralEvents := make(chan *events.CanonicalEvent, 200)

	// 3. Initialize active collectors
	procCol := process.NewCollector(cfg)
	fsCol := filesystem.NewCollector(cfg)
	invCol := inventory.NewCollector(cfg)
	netCol := network.NewCollector(cfg)
	usbCol := usb.NewCollector(cfg)

	// Start them
	if err := procCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start proc collector: %v", err)
	}
	if err := fsCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start fs collector: %v", err)
	}
	if err := invCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start inv collector: %v", err)
	}
	if err := netCol.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start net collector: %v", err)
	}
	if err := usbCol.Start(ctx, centralEvents); err != nil {
		t.Logf("USB collector failed to start (expected on some test environments without mock): %v", err)
	}

	// 4. Consume events concurrently
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)

	var (
		processedEvents int
		eventIDs        sync.Map // track event_id for uniqueness
		duplicateCount  int
		failedEvents    int
		mu              sync.Mutex
	)

	// Explicit 10-second drain context for shutdown
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	// Shutdown trigger
	go func() {
		time.Sleep(500 * time.Millisecond) // Let collectors generate events
		cancel()

		// Establish global drain deadline
		go func() {
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				drainCancel()
			case <-drainCtx.Done():
			}
		}()
	}()

	// Endpoint ingestion loop replication
	go func() {
		defer ingestWg.Done()

		for {
			select {
			case ev, ok := <-centralEvents:
				if !ok {
					return
				}

				if ev.EventID == "" {
					t.Errorf("Fabricated identity detected: empty event_id received")
				}

				if _, loaded := eventIDs.LoadOrStore(ev.EventID, true); loaded {
					mu.Lock()
					duplicateCount++
					mu.Unlock()
				}

				storeCtx := ctx
				if ctx.Err() != nil {
					storeCtx = drainCtx
				}

				var storeErr error
				attempts := 0
				for {
					storeErr = db.Store(storeCtx, ev)
					if storeErr == nil {
						mu.Lock()
						processedEvents++
						mu.Unlock()
						break
					}

					if errors.Is(storeErr, storage.ErrStoreFailedBeforeCommit) {
						if errors.Is(storeErr, context.Canceled) && storeCtx == ctx {
							// In-flight event interrupted by lifecycle cancellation. Retry.
							storeCtx = drainCtx
							continue
						} else {
							// Non-retryable
							mu.Lock()
							failedEvents++
							mu.Unlock()
							break
						}
					}

					if errors.Is(storeErr, storage.ErrStoreUncertain) {
						attempts++
						if attempts >= 3 {
							dlqCtx, dlqCancel := context.WithTimeout(storeCtx, 2*time.Second)
							db.MoveToDLQ(dlqCtx, ev, "uncertain_commit", storeErr.Error())
							dlqCancel()
							mu.Lock()
							failedEvents++
							mu.Unlock()
							break
						}

						timer := time.NewTimer(10 * time.Millisecond) // Faster for tests
						select {
						case <-storeCtx.Done():
							timer.Stop()
							if storeCtx == ctx {
								storeCtx = drainCtx
								continue
							}
							// don't break yet, evaluate below
						case <-timer.C:
						}

						if storeCtx.Err() != nil && storeCtx != ctx {
							mu.Lock()
							failedEvents++
							mu.Unlock()
							break
						}
						continue
					}

					break
				}
			}
		}
	}()

	// 5. Wait for shutdown and draining
	<-ctx.Done()
	procCol.Stop()
	fsCol.Stop()
	invCol.Stop()
	netCol.Stop()
	usbCol.Stop()

	close(centralEvents)
	ingestWg.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Assertions
	if processedEvents == 0 && failedEvents == 0 {
		t.Errorf("Expected events to be processed or failed, got 0")
	}
	if duplicateCount > 0 {
		t.Errorf("Expected 0 duplicate event identities, got %d", duplicateCount)
	}

	t.Logf("Broad Regression Results: Processed=%d, Failed/Dropped=%d, Duplicates=%d", processedEvents, failedEvents, duplicateCount)
}
