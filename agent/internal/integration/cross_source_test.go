package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/collectors/filesystem"
	"redcyberfox/agent/internal/collectors/inventory"
	"redcyberfox/agent/internal/collectors/network"
	"redcyberfox/agent/internal/collectors/process"
	"redcyberfox/agent/internal/collectors/usb"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestPhase2E4_BroadCrossSourceRegression(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cross-source-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "agent.db")
	idPath := filepath.Join(tempDir, "identity.json")

	cfg := &config.Config{
		TenantID:          "tenant-phase2e4",
		SiteID:            "site-phase2e4",
		APIAddr:           "localhost:8080",
		ProcessInterval:   "100ms",
		InventoryInterval: "100ms",
		NetworkInterval:   "100ms",
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

	// 2. Production Central Events (strictly bound to 200)
	centralEvents := make(chan *events.CanonicalEvent, 200)

	// 3. Initialize all 6 active collectors (Process, Filesystem, Inventory, Network, USB Win/Linux)
	procCol := process.NewCollector(cfg)
	fsCol := filesystem.NewCollector(cfg)
	invCol := inventory.NewCollector(cfg)
	netCol := network.NewCollector(cfg)
	usbCol := usb.NewCollector(cfg)

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
		t.Logf("USB collector note (stub or platform specific): %v", err)
	}

	// 4. Shared Ingestion Engine (No duplicated branching)
	var (
		processedEvents atomic.Uint64
		duplicateCount  atomic.Uint64
		failedEvents    atomic.Uint64
		seenIDs         sync.Map
	)

	engine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   cfg.TenantID,
		SiteID:     cfg.SiteID,
		AssetID:    "asset-test-device",
		RetryDelay: 10 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			if ev.EventID == "" {
				t.Errorf("Fabricated identity: empty event_id committed")
			}
			if _, loaded := seenIDs.LoadOrStore(ev.EventID, true); loaded {
				duplicateCount.Add(1)
			}
			processedEvents.Add(1)
		},
		OnDropped: func(ev *events.CanonicalEvent, reason string, err error) {
			failedEvents.Add(1)
		},
	})

	// 5. Global Drain Boundary setup (10s max shutdown time)
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		engine.Run(ctx, drainCtx, centralEvents)
	}()

	// 6. Mutate filesystem to guarantee event generation without arbitrary sleeps
	testFile := filepath.Join(tempDir, "sample.txt")
	os.WriteFile(testFile, []byte("hello"), 0644)
	os.WriteFile(testFile, []byte("world"), 0644)

	// Wait for at least 3 events to be committed via synchronization (timeout after 5s)
	syncDeadline := time.Now().Add(5 * time.Second)
	for processedEvents.Load() < 3 && time.Now().Before(syncDeadline) {
		time.Sleep(20 * time.Millisecond)
	}

	// 7. Trigger Shutdown Sequence
	cancel() // Normal lifecycle cancellation

	// Single global 10-second drain deadline begins
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			drainCancel()
		case <-drainCtx.Done():
		}
	}()

	// Stop collectors cleanly
	procCol.Stop()
	fsCol.Stop()
	invCol.Stop()
	netCol.Stop()
	usbCol.Stop()

	// Drain remaining events in centralEvents
	close(centralEvents)
	ingestWg.Wait()

	// 8. Assertions on Durable SQLite State (verifying real database rows, not just memory counters)
	rawDB := db.GetDB()
	var totalRows, distinctEvents int
	err = rawDB.QueryRow("SELECT count(*), count(DISTINCT event_id) FROM events").Scan(&totalRows, &distinctEvents)
	if err != nil {
		t.Fatalf("Failed to query durable SQLite events: %v", err)
	}

	if totalRows == 0 {
		t.Fatalf("Expected at least 1 durable event in SQLite, got 0")
	}
	if totalRows != distinctEvents {
		t.Errorf("Duplicate event_ids stored in DB: total=%d, distinct=%d", totalRows, distinctEvents)
	}
	if duplicateCount.Load() > 0 {
		t.Errorf("Duplicate event identity generation detected in memory: %d", duplicateCount.Load())
	}

	// Verify sequence continuity
	var minSeq, maxSeq int64
	err = rawDB.QueryRow("SELECT min(seq_no), max(seq_no) FROM events").Scan(&minSeq, &maxSeq)
	if err != nil {
		t.Fatalf("Failed to query seq_no bounds: %v", err)
	}
	if minSeq != 1 || maxSeq != int64(totalRows) {
		t.Errorf("Sequence continuity violation: min=%d, max=%d, totalRows=%d", minSeq, maxSeq, totalRows)
	}

	t.Logf("Phase 2E.4 Broad Regression PASS: Durable SQLite Events=%d, MinSeq=%d, MaxSeq=%d, Failed=%d",
		totalRows, minSeq, maxSeq, failedEvents.Load())
}

func TestPhase2E4_CentralEventsPressure(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "pressure.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "id.json"), 20*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Production channel capacity strictly 200
	centralEvents := make(chan *events.CanonicalEvent, 200)

	var committedCount atomic.Uint64
	engine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "pressure-tenant",
		SiteID:     "pressure-site",
		RetryDelay: 5 * time.Millisecond,
		AssetID:    "DEVICE-1234",
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			committedCount.Add(1)
		},
	})

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		engine.Run(ctx, drainCtx, centralEvents)
	}()

	// Concurrently write 300 events to put pressure past the 200 bound
	const totalEvents = 300
	var producerWg sync.WaitGroup
	const numProducers = 6

	for p := 0; p < numProducers; p++ {
		producerWg.Add(1)
		go func(prodID int) {
			defer producerWg.Done()
			eventsPerProd := totalEvents / numProducers
			for i := 0; i < eventsPerProd; i++ {
				ev := &events.CanonicalEvent{
					EventID:       uuid.New().String(),
					Source:        "pressure_test",
					Category:      "pressure",
					Severity:      "INFO",
					OccurredAt:    time.Now().UTC(),
					SchemaVersion: events.CurrentSchemaVersion,
				}
				select {
				case centralEvents <- ev:
				case <-ctx.Done():
					return
				}
			}
		}(p)
	}

	producerWg.Wait()
	close(centralEvents)
	ingestWg.Wait()

	if committedCount.Load() != totalEvents {
		t.Fatalf("Expected all %d events to be committed under pressure, got %d", totalEvents, committedCount.Load())
	}

	var rowCount int
	db.GetDB().QueryRow("SELECT count(*) FROM events").Scan(&rowCount)
	if rowCount != totalEvents {
		t.Errorf("Durable SQLite record count mismatch: expected %d, got %d", totalEvents, rowCount)
	}
}

func TestPhase2E4_StorageContention(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "contention.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "id.json"), 20*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// High concurrency: 10 concurrent goroutines storing events simultaneously
	const workers = 10
	const eventsPerWorker = 30
	var wg sync.WaitGroup

	errCh := make(chan error, workers*eventsPerWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < eventsPerWorker; i++ {
				ev := &events.CanonicalEvent{
					EventID:       uuid.New().String(),
					Source:        "contention_worker",
					Category:      "storage_test",
					Severity:      "INFO",
					OccurredAt:    time.Now().UTC(),
					SchemaVersion: events.CurrentSchemaVersion,
				}
				if err := db.Store(ctx, ev); err != nil {
					errCh <- err
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("Storage contention error encountered: %v", err)
	}

	var count int
	db.GetDB().QueryRow("SELECT count(*) FROM events").Scan(&count)
	expectedTotal := workers * eventsPerWorker
	if count != expectedTotal {
		t.Fatalf("Contention count mismatch: expected %d, got %d", expectedTotal, count)
	}
}

func TestPhase2E4_ShutdownWhileStorageBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "blocked_shutdown.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "id.json"), 10*1024*1024)

	initCtx, cancelInit := context.WithCancel(context.Background())
	defer cancelInit()
	if err := db.Init(initCtx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 200*time.Millisecond) // Fast timeout for test
	defer drainCancel()

	centralEvents := make(chan *events.CanonicalEvent, 10)
	ev := &events.CanonicalEvent{
		EventID:       "blocked-event-1",
		Source:        "test",
		Category:      "blocked",
		Severity:      "INFO",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	centralEvents <- ev

	var droppedCount atomic.Uint64
	engine := ingestion.NewEngine(db, ingestion.Config{
		RetryDelay: 10 * time.Millisecond,
		OnDropped: func(e *events.CanonicalEvent, reason string, err error) {
			droppedCount.Add(1)
		},
		TestInjectError: func(storeCtx context.Context, e *events.CanonicalEvent) error {
			// Simulate storage blocked until context canceled
			<-storeCtx.Done()
			return sql.ErrConnDone
		},
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		engine.Run(ctx, drainCtx, centralEvents)
	}()

	// Immediately cancel lifecycle context and close channel
	cancel()
	close(centralEvents)

	// Wait for shutdown to complete (must terminate within drain deadline without hanging)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Clean exit
	case <-time.After(1 * time.Second):
		t.Fatal("Engine failed to terminate cleanly when storage was blocked during shutdown")
	}

	if droppedCount.Load() == 0 {
		t.Errorf("Expected blocked event to be explicitly dropped on deadline expiration")
	}
}
