package integration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestFocusedIngestion_InFlightCancellation_RetrySameEventID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "identity.json"), 10*1024*1024)

	initCtx, cancelInit := context.WithCancel(context.Background())
	defer cancelInit()
	if err := db.Init(initCtx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	var mu sync.Mutex
	attempts := 0
	const expectedEventID = "retry-same-id-999"

	engine := ingestion.NewEngine(db, ingestion.Config{
		RetryDelay: 10 * time.Millisecond,
		TestInjectError: func(storeCtx context.Context, ev *events.CanonicalEvent) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++

			if attempts == 1 {
				// Simulate in-flight lifecycle context cancellation
				cancel()
				return fmt.Errorf("%w: %w", storage.ErrStoreFailedBeforeCommit, context.Canceled)
			}
			// Attempt 2: retry under drain context, allow normal storage to proceed
			return db.Store(storeCtx, ev)
		},
	})

	ev := &events.CanonicalEvent{
		EventID:  expectedEventID,
		Severity: "INFO",
		Source:   "test-source",
	}

	// Process the event through the shared engine
	outcome := engine.ProcessEvent(ctx, drainCtx, ev)
	if outcome != ingestion.OutcomeCommitted {
		t.Fatalf("Expected OutcomeCommitted after in-flight retry, got %s", outcome)
	}

	mu.Lock()
	totalAttempts := attempts
	mu.Unlock()

	if totalAttempts != 2 {
		t.Errorf("Expected exactly 2 store attempts (1 failure + 1 retry), got %d", totalAttempts)
	}

	// Verify the durable record in SQLite matches the SAME event_id
	eventsList, err := db.GetPendingEvents(initCtx, 10)
	if err != nil || len(eventsList) != 1 {
		t.Fatalf("Expected 1 persisted event in DB, got %d (err: %v)", len(eventsList), err)
	}
	if eventsList[0].EventID != expectedEventID {
		t.Errorf("EventID mismatch: expected %s, got %s", expectedEventID, eventsList[0].EventID)
	}
	if eventsList[0].SeqNo == 0 {
		t.Errorf("Expected SeqNo to be populated (>0), got 0")
	}
}

func TestFocusedIngestion_ShutdownSemantics(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "identity.json"), 10*1024*1024)

	initCtx, cancelInit := context.WithCancel(context.Background())
	defer cancelInit()
	if err := db.Init(initCtx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	centralEvents := make(chan *events.CanonicalEvent, 10)
	ev := &events.CanonicalEvent{
		EventID:  "evt-123",
		Severity: "INFO",
		Source:   "test",
	}
	centralEvents <- ev

	var mu sync.Mutex
	var pass int
	var committed bool

	engine := ingestion.NewEngine(db, ingestion.Config{
		RetryDelay: 10 * time.Millisecond,
		OnCommitted: func(ctx context.Context, e *events.CanonicalEvent) {
			mu.Lock()
			committed = true
			mu.Unlock()
		},
		TestInjectError: func(storeCtx context.Context, e *events.CanonicalEvent) error {
			mu.Lock()
			defer mu.Unlock()
			pass++
			if pass == 1 {
				// First attempt, simulate lifecycle cancellation in flight
				cancel()
				return fmt.Errorf("%w: %w", storage.ErrStoreFailedBeforeCommit, context.Canceled)
			}
			// Second attempt (retry), close channel so Run will exit cleanly
			go func() {
				close(centralEvents)
			}()
			return db.Store(storeCtx, e)
		},
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		engine.Run(ctx, drainCtx, centralEvents)
	}()

	wg.Wait()

	mu.Lock()
	wasCommitted := committed
	mu.Unlock()

	if !wasCommitted {
		t.Errorf("Expected event to be committed under drainCtx retry")
	}

	// Verify persistence succeeded under drainCtx using the SAME event_id
	eventsList, err := db.GetPendingEvents(initCtx, 10)
	if err != nil || len(eventsList) != 1 {
		t.Fatalf("Expected 1 event persisted, got %d. Err: %v", len(eventsList), err)
	}
	if eventsList[0].EventID != "evt-123" || eventsList[0].SeqNo == 0 {
		t.Errorf("Failed to retrieve event correctly")
	}
}

func TestFocusedIngestion_NonRetryableError_ExplicitTerminalHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	db := storage.NewSQLiteStorage(dbPath, filepath.Join(tmpDir, "identity.json"), 10*1024*1024)

	initCtx, cancelInit := context.WithCancel(context.Background())
	defer cancelInit()
	if err := db.Init(initCtx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	var droppedReason string
	var droppedErr error
	var mu sync.Mutex

	engine := ingestion.NewEngine(db, ingestion.Config{
		OnDropped: func(ev *events.CanonicalEvent, reason string, err error) {
			mu.Lock()
			droppedReason = reason
			droppedErr = err
			mu.Unlock()
		},
		TestInjectError: func(storeCtx context.Context, ev *events.CanonicalEvent) error {
			// Non-retryable error (e.g. quota exceeded)
			return fmt.Errorf("%w: storage quota exceeded", storage.ErrStoreFailedBeforeCommit)
		},
	})

	ev := &events.CanonicalEvent{
		EventID:  "drop-test-1",
		Severity: "INFO",
		Source:   "test",
	}

	outcome := engine.ProcessEvent(ctx, drainCtx, ev)
	if outcome != ingestion.OutcomeFailedBeforeCommit {
		t.Errorf("Expected OutcomeFailedBeforeCommit, got %s", outcome)
	}

	mu.Lock()
	reason := droppedReason
	err := droppedErr
	mu.Unlock()

	if reason != "failed_before_commit" {
		t.Errorf("Expected drop reason failed_before_commit, got %s", reason)
	}
	if !errors.Is(err, storage.ErrStoreFailedBeforeCommit) {
		t.Errorf("Expected ErrStoreFailedBeforeCommit, got %v", err)
	}
}
