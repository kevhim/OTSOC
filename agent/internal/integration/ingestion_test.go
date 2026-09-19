package integration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// IngestionTestHarness replicates the centralEvents ingestion loop from main.go
// to allow precise deterministic testing of shutdown semantics and retries.
func runIngestionTestHarness(
	ctx context.Context, 
	cancel context.CancelFunc,
	db *storage.SQLiteStorage, 
	centralEvents <-chan *events.CanonicalEvent,
	onDrainCtxCreated func(context.Context),
	injectError func(context.Context, *events.CanonicalEvent) error,
) {
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	// Shutdown trigger exactly matching main.go
	go func() {
		<-ctx.Done()
		if onDrainCtxCreated != nil {
			onDrainCtxCreated(drainCtx)
		}
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

	go func() {
		defer ingestWg.Done()
		for {
			select {
			case ev, ok := <-centralEvents:
				if !ok {
					return
				}
				
				storeCtx := ctx
				if ctx.Err() != nil {
					storeCtx = drainCtx
				}

				var storeErr error
				attempts := 0
				for {
					if injectError != nil {
						storeErr = injectError(storeCtx, ev)
					}
					if storeErr == nil {
						storeErr = db.Store(storeCtx, ev)
					}
					
					if storeErr == nil {
						break
					}

					if errors.Is(storeErr, storage.ErrStoreFailedBeforeCommit) {
						if errors.Is(storeErr, context.Canceled) && storeCtx == ctx {
							storeCtx = drainCtx
							continue
						}
						break
					}

					if errors.Is(storeErr, storage.ErrStoreUncertain) {
						attempts++
						if attempts >= 3 {
							dlqCtx, dlqCancel := context.WithTimeout(storeCtx, 2*time.Second)
							db.MoveToDLQ(dlqCtx, ev, "uncertain_commit", storeErr.Error())
							dlqCancel()
							break
						}
						
						timer := time.NewTimer(10 * time.Millisecond)
						select {
						case <-storeCtx.Done():
							timer.Stop()
							if storeCtx == ctx {
								storeCtx = drainCtx
								continue
							}
						case <-timer.C:
						}

						if storeCtx.Err() != nil && storeCtx != ctx {
							break
						}
						continue
					}
					break
				}
			}
		}
	}()
	ingestWg.Wait()
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
	centralEvents := make(chan *events.CanonicalEvent, 10)

	ev := &events.CanonicalEvent{EventID: "evt-123"}
	centralEvents <- ev

	var drainCtxCreated bool
	var mu sync.Mutex
	onDrainCtxCreated := func(dCtx context.Context) {
		mu.Lock()
		drainCtxCreated = true
		mu.Unlock()
	}

	var pass int
	injectHook := func(storeCtx context.Context, e *events.CanonicalEvent) error {
		mu.Lock()
		defer mu.Unlock()
		pass++
		if pass == 1 {
			// First attempt, simulate lifecycle cancellation in flight
			cancel()
			return fmt.Errorf("%w: %w", storage.ErrStoreFailedBeforeCommit, context.Canceled)
		}
		// Second attempt (retry), close channel so loop will exit after this event
		go func() {
			close(centralEvents)
		}()
		return nil
	}

	runIngestionTestHarness(ctx, cancel, db, centralEvents, onDrainCtxCreated, injectHook)

	mu.Lock()
	created := drainCtxCreated
	mu.Unlock()
	if !created {
		t.Errorf("Shutdown drain context was not created properly")
	}

	// Verify persistence succeeded under drainCtx using the SAME event_id
	events, err := db.GetPendingEvents(initCtx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("Expected 1 event persisted, got %d. Err: %v", len(events), err)
	}
	if events[0].EventID != "evt-123" || events[0].SeqNo == 0 {
		t.Errorf("Failed to retrieve event correctly")
	}
}
