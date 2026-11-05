package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/eventqueue"
	"redcyberfox/agent/internal/governor"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/agent/internal/resourcereport"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// TestMeasurement_FinalizeAfterDrain_QueuedEventsReconciled exercises the shared production shutdown:
// 1. Holds ingestion behind an explicit barrier.
// 2. Establishes queued events in memory.
// 3. Triggers actual measurement expiry via RunMeasurementRunner duration timer.
// 4. Releases the ingestion barrier.
// 5. Executes the shared production ExecuteShutdown implementation (without duplicated test logic).
// 6. Verifies that all queued events are drained, committed to SQLite, and accurately accounted for.
func TestMeasurement_FinalizeAfterDrain_QueuedEventsReconciled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "drain_agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	reportPath := filepath.Join(tmpDir, "drain_report.json")

	// 1. Storage & Initial Backlog
	db := storage.NewSQLiteStorage(dbPath, idPath, 50*1024*1024)
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("failed to init SQLite storage: %v", err)
	}

	deviceID := db.GetDeviceID()
	initialBacklog := uint64(0)

	// 2. Detection Engine
	rules := []detection.Rule{
		&detection.RealIOCExecRule{},
	}
	detEngine := detection.NewEngine(db, rules)

	// 3. Ingestion Barrier
	barrier := make(chan struct{})

	// 4. Shared Ingestion Engine with explicit barrier
	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: "tenant-drain-test",
		SiteID:   "site-drain-test",
		AssetID:  deviceID,
		BeforeProcess: func(ctx context.Context, ev *events.CanonicalEvent) {
			<-barrier
		},
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	// 5. Instrumented Bounded Event Queue
	eventQ := eventqueue.New(50)

	// 6. Shutdown Coordinator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	shutdownCoord := resourcereport.NewShutdownCoordinator(cancel, drainCancel, 10*time.Second)

	// 7. Reporter and Measurement Runner
	reportCfg := resourcereport.Config{
		ReportPath: reportPath,
		Profile:    "test-queued-drain-regression",
		Duration:   50 * time.Millisecond,
		Interval:   10 * time.Millisecond,
		DBPath:     dbPath,
	}
	reportRules := []resourcereport.RuleInfo{
		{RuleID: rules[0].ID(), Version: rules[0].Version(), Severity: rules[0].Severity()},
	}
	reporter := resourcereport.NewReporter(reportCfg, reportRules)

	resourceProvider, err := governor.NewSQLiteResourceProvider(dbPath, time.Now)
	if err != nil {
		t.Fatalf("failed to create resource provider: %v", err)
	}
	govMetrics := governor.NewGovernorMetrics(time.Now)
	coord, err := governor.NewCoordinator(resourceProvider, nil, govMetrics, governor.CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, time.Now)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	// 8. Start Ingestion Loop in background
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		ingestEngine.Run(ctx, drainCtx, eventQ.Out())
	}()

	// 9. Submit batch of 20 events into event queue
	const totalQueuedEvents = 20
	for i := 1; i <= totalQueuedEvents; i++ {
		ev := &events.CanonicalEvent{
			EventID:       uuid.New().String(),
			Category:      "process/start",
			Source:        "process_collector",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"executable_path": fmt.Sprintf("C:\\Windows\\System32\\queued_proc_%d.exe", i),
				"pid":             2000 + i,
			},
		}
		if err := eventQ.Enqueue(context.Background(), ev); err != nil {
			t.Fatalf("failed to enqueue event %d: %v", i, err)
		}
	}

	// First event enters BeforeProcess and blocks on <-barrier.
	// Remaining 19 events are buffered in the queue.
	time.Sleep(20 * time.Millisecond)
	if eventQ.Depth().Value < 18 {
		t.Fatalf("expected queue depth >= 18 while ingestion is held behind barrier, got %d", eventQ.Depth().Value)
	}

	// 10. Start actual measurement runner: will run for 50ms and trigger shutdown on expiry
	var measureWg sync.WaitGroup
	var measureErr error
	measureWg.Add(1)
	go func() {
		defer measureWg.Done()
		measureErr = resourcereport.RunMeasurementRunner(
			ctx,
			reportCfg,
			reporter,
			coord,
			govMetrics,
			func() governor.RuntimeSignals {
				return governor.RuntimeSignals{
					QueueDepth:        eventQ.Depth(),
					QueueCapacity:     eventQ.Capacity(),
					QueueOldestAgeSec: eventQ.OldestEventAge(),
				}
			},
			shutdownCoord,
			func() (uint64, uint64) {
				return eventQ.BlockedEnqueueAttemptsTotal(), eventQ.PressureEpisodesTotal()
			},
		)
	}()
	measureJoin := func() error {
		measureWg.Wait()
		return measureErr
	}

	// 11. Wait for actual measurement expiry to trigger the shared shutdown path
	select {
	case <-ctx.Done():
		// Measurement timer expired!
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for actual measurement expiry")
	}

	if shutdownCoord.Outcome() != "MEASUREMENT_COMPLETE" {
		t.Fatalf("expected outcome MEASUREMENT_COMPLETE, got %s", shutdownCoord.Outcome())
	}

	// 12. Release ingestion barrier so queued events can drain to SQLite
	close(barrier)

	// 13. Execute the SHARED production shutdown sequence
	shutdownErr := resourcereport.ExecuteShutdown(resourcereport.ShutdownParams{
		Context:              ctx,
		ShutdownCoord:        shutdownCoord,
		Collectors:           nil,
		EventQueue:           eventQ,
		IngestWait:           &ingestWg,
		Forwarder:            nil,
		MeasureJoin:          measureJoin,
		Storage:              db,
		RawDB:                db.GetDB(),
		InitialPendingEvents: &initialBacklog,
		IngestionAccounting:  ingestEngine.Accounting,
		Reporter:             reporter,
	})
	if shutdownErr != nil {
		t.Fatalf("shared ExecuteShutdown returned error: %v", shutdownErr)
	}

	// 14. Assertions on Final Report and Durable Accounting
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read written report file: %v", err)
	}

	var finalReport resourcereport.Report
	if err := json.Unmarshal(data, &finalReport); err != nil {
		t.Fatalf("failed to unmarshal written report JSON: %v", err)
	}

	if finalReport.Summary.ShutdownOutcome != "MEASUREMENT_COMPLETE" {
		t.Fatalf("expected shutdown outcome MEASUREMENT_COMPLETE, got %s", finalReport.Summary.ShutdownOutcome)
	}

	acc := finalReport.Summary.EventAccounting
	if acc == nil {
		t.Fatalf("expected non-nil EventAccounting in final report summary")
	}

	// Ingestion-outcome reconciliation assertion:
	if !acc.IngestionOutcome.ReconciliationValid {
		t.Fatalf("ingestion outcome reconciliation invariant failed: %+v", acc.IngestionOutcome)
	}
	if acc.IngestionOutcome.IngestedTotal != totalQueuedEvents {
		t.Fatalf("expected all %d queued events to be ingested, got %d", totalQueuedEvents, acc.IngestionOutcome.IngestedTotal)
	}
	if acc.IngestionOutcome.CommittedTotal != totalQueuedEvents {
		t.Fatalf("expected all %d queued events to be committed, got %d", totalQueuedEvents, acc.IngestionOutcome.CommittedTotal)
	}

	// Storage inspection assertions:
	if acc.Storage.DurableTotalEvents == nil || *acc.Storage.DurableTotalEvents != totalQueuedEvents {
		t.Fatalf("expected durable total events %d, got %v", totalQueuedEvents, acc.Storage.DurableTotalEvents)
	}
	if acc.Storage.SourceTelemetryEvents == nil || *acc.Storage.SourceTelemetryEvents != totalQueuedEvents {
		t.Fatalf("expected source telemetry %d, got %v", totalQueuedEvents, acc.Storage.SourceTelemetryEvents)
	}
	if acc.Storage.DerivedFindingsEvents == nil || *acc.Storage.DerivedFindingsEvents != 0 {
		t.Fatalf("expected 0 derived findings, got %v", acc.Storage.DerivedFindingsEvents)
	}
}

type failingStopper struct {
	err error
}

func (f *failingStopper) Stop() error {
	return f.err
}

// TestMeasurement_ShutdownCapturesErrors verifies that collector and forwarder stop failures
// are captured, affect the final outcome string in the report, and cause ExecuteShutdown to fail.
func TestMeasurement_ShutdownCapturesErrors(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fail_agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	reportPath := filepath.Join(tmpDir, "fail_report.json")

	db := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("failed to init SQLite storage: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	shutdownCoord := resourcereport.NewShutdownCoordinator(cancel, drainCancel, 5*time.Second)
	shutdownCoord.Trigger("SIGNAL_SIGINT")

	reportCfg := resourcereport.Config{
		ReportPath: reportPath,
		Profile:    "test-fail-capture",
		Duration:   time.Second,
		Interval:   100 * time.Millisecond,
		DBPath:     dbPath,
	}
	reporter := resourcereport.NewReporter(reportCfg, nil)

	colErr := errors.New("usb collector hardware lock failure")
	fwdErr := errors.New("forwarder flush failure")

	collectors := []interfaces.Stopper{
		&failingStopper{err: colErr},
	}
	fwdStopper := &failingStopper{err: fwdErr}

	shutdownErr := resourcereport.ExecuteShutdown(resourcereport.ShutdownParams{
		Context:       ctx,
		ShutdownCoord: shutdownCoord,
		Collectors:    collectors,
		Forwarder:     fwdStopper,
		Storage:       db,
		Reporter:      reporter,
	})

	if shutdownErr == nil {
		t.Fatalf("expected ExecuteShutdown to return aggregated error, got nil")
	}

	// Verify report outcome reflects failure
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}

	var rep resourcereport.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("failed to unmarshal report: %v", err)
	}

	if rep.Summary.ShutdownOutcome != "SIGNAL_SIGINT_WITH_ERRORS: collector stop: usb collector hardware lock failure\nforwarder stop: forwarder flush failure" {
		t.Fatalf("unexpected shutdown outcome: %s", rep.Summary.ShutdownOutcome)
	}
}

type failingCloseStorage struct {
	*storage.SQLiteStorage
	closeErr error
}

func (f *failingCloseStorage) Close() error {
	_ = f.SQLiteStorage.Close() // close real DB to avoid resource leak
	return f.closeErr
}

// TestMeasurement_ShutdownCapturesDBCloseFailure verifies that a database close failure
// is truthfully captured into the finalized measurement report outcome and returns an error
// ensuring non-zero process exit.
func TestMeasurement_ShutdownCapturesDBCloseFailure(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fail_close.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	reportPath := filepath.Join(tmpDir, "fail_close_report.json")

	realDB := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := realDB.Init(context.Background()); err != nil {
		t.Fatalf("failed to init SQLite storage: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	shutdownCoord := resourcereport.NewShutdownCoordinator(cancel, drainCancel, 5*time.Second)
	shutdownCoord.Trigger("MEASUREMENT_COMPLETE")

	reportCfg := resourcereport.Config{
		ReportPath: reportPath,
		Profile:    "test-db-close-failure",
		Duration:   time.Second,
		Interval:   100 * time.Millisecond,
		DBPath:     dbPath,
	}
	reporter := resourcereport.NewReporter(reportCfg, nil)

	simulatedCloseErr := errors.New("simulated db close failure: disk I/O lock")
	failingDB := &failingCloseStorage{
		SQLiteStorage: realDB,
		closeErr:      simulatedCloseErr,
	}

	shutdownErr := resourcereport.ExecuteShutdown(resourcereport.ShutdownParams{
		Context:       ctx,
		ShutdownCoord: shutdownCoord,
		Storage:       failingDB,
		RawDB:         realDB.GetDB(),
		Reporter:      reporter,
	})

	// 1. Assert ExecuteShutdown returns non-nil error (ensuring os.Exit(1) in production)
	if shutdownErr == nil {
		t.Fatalf("expected ExecuteShutdown to return error on DB close failure, got nil")
	}
	if !errors.Is(shutdownErr, simulatedCloseErr) {
		t.Fatalf("expected errors.Is simulatedCloseErr, got: %v", shutdownErr)
	}

	// 2. Assert final report on disk truthfully recorded the DB close failure
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read persisted report: %v", err)
	}

	var rep resourcereport.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("failed to unmarshal persisted report JSON: %v", err)
	}

	expectedSubstring := "MEASUREMENT_COMPLETE_WITH_ERRORS: storage close: simulated db close failure: disk I/O lock"
	if rep.Summary.ShutdownOutcome != expectedSubstring {
		t.Fatalf("truthful report outcome mismatch:\nexpected: %s\ngot:      %s",
			expectedSubstring, rep.Summary.ShutdownOutcome)
	}
}
