package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/eventqueue"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/resourcereport"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// TestMeasurement_FinalizeAfterDrain_QueuedEventsReconciled exercises the production shutdown contract:
// When measurement sampling expires, remaining events in the event queue must not be abandoned or
// excluded from the final machine-readable measurement report.
// The report is finalized only AFTER collectors stop, the queue drains, ingestion finishes, and
// durable storage is inspected before DB close.
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

	// 3. Shared Ingestion Engine
	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: "tenant-drain-test",
		SiteID:   "site-drain-test",
		AssetID:  deviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	// 4. Instrumented Bounded Event Queue
	eventQ := eventqueue.New(50)

	// 5. Shutdown Coordinator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	shutdownCoord := resourcereport.NewShutdownCoordinator(cancel, drainCancel, 10*time.Second)

	// 6. Reporter
	reportCfg := resourcereport.Config{
		ReportPath: reportPath,
		Profile:    "test-queued-drain-regression",
		Duration:   time.Second,
		Interval:   100 * time.Millisecond,
		DBPath:     dbPath,
	}
	reportRules := []resourcereport.RuleInfo{
		{RuleID: rules[0].ID(), Version: rules[0].Version(), Severity: rules[0].Severity()},
	}
	reporter := resourcereport.NewReporter(reportCfg, reportRules)

	// 7. Start Ingestion Loop in background
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		ingestEngine.Run(ctx, drainCtx, eventQ.Out())
	}()

	// 8. Submit batch of 20 events into event queue
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
		eventQ.In() <- ev
	}

	// 9. Measurement expires while events are still queued / being ingested
	// Measurement expiry triggers the shared shutdown path
	shutdownCoord.Trigger("MEASUREMENT_COMPLETE")

	// 10. Execute the shared production shutdown sequence:
	// A) Stop accepting new events and drain the queue
	eventQ.Close()

	// B) Wait for ingestion loop to finish draining all queued events
	ingestWg.Wait()
	eventQ.WaitDrain()

	// C) Inspect durable storage state BEFORE closing database
	storageSnap := resourcereport.InspectDurableStorage(db.GetDB(), &initialBacklog)
	ingestAcc := ingestEngine.Accounting()

	// D) Construct final accounting summary
	finalAccounting := &resourcereport.EventAccountingSummary{
		IngestionOutcome: resourcereport.IngestionOutcomeSummary{
			IngestedTotal:           ingestAcc.IngestedTotal,
			CommittedTotal:          ingestAcc.CommittedTotal,
			FailedBeforeCommitTotal: ingestAcc.FailedBeforeCommitTotal,
			RecoveredDLQTotal:       ingestAcc.RecoveredDLQTotal,
			SpilledDiskTotal:        ingestAcc.SpilledDiskTotal,
			DroppedTotal:            ingestAcc.DroppedTotal,
			ReconciliationValid:     ingestAcc.IngestionOutcomeReconciled(),
		},
		Forwarder: resourcereport.ForwarderAccountingSummary{
			AttemptsTotal:           0,
			HTTPAcceptedTotal:       0,
			DLQTotal:                0,
			RetryTotal:              0,
			LocalRemovedTotal:       0,
			LocalRemovalErrorsTotal: 0,
		},
		Queue: resourcereport.QueueAccountingSummary{
			BlockedEnqueueAttemptsTotal: eventQ.BlockedEnqueueAttemptsTotal(),
			PressureEpisodesTotal:       eventQ.PressureEpisodesTotal(),
			TimeSpentFullSeconds:        eventQ.TimeSpentFullSeconds(),
		},
		Storage: storageSnap,

		// Flat compatibility fields
		IngestedTotal:           ingestAcc.IngestedTotal,
		CommittedTotal:          ingestAcc.CommittedTotal,
		ForwardedTotal:          0,
		FailedBeforeCommitTotal: ingestAcc.FailedBeforeCommitTotal,
		RecoveredDLQTotal:       ingestAcc.RecoveredDLQTotal,
		SpilledDiskTotal:        ingestAcc.SpilledDiskTotal,
		DroppedTotal:            ingestAcc.DroppedTotal,
		BackpressureEventsTotal: eventQ.BlockedEnqueueAttemptsTotal(),
		ReconciliationValid:     ingestAcc.IngestionOutcomeReconciled(),
	}

	// E) Attach accounting and finalize the report
	reporter.SetAccounting(finalAccounting)
	finalReport, err := reporter.Finalize(shutdownCoord.Outcome())
	if err != nil {
		t.Fatalf("failed to finalize report: %v", err)
	}

	// F) Close database
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close storage: %v", err)
	}

	// 11. Assertions on Final Report and Durable Accounting
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

	// Verify report file was written to disk and unmarshals cleanly
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read written report file: %v", err)
	}

	var diskReport resourcereport.Report
	if err := json.Unmarshal(data, &diskReport); err != nil {
		t.Fatalf("failed to unmarshal written report JSON: %v", err)
	}

	if diskReport.Summary.EventAccounting.IngestionOutcome.CommittedTotal != totalQueuedEvents {
		t.Fatalf("persisted report committed_total mismatch: expected %d, got %d",
			totalQueuedEvents, diskReport.Summary.EventAccounting.IngestionOutcome.CommittedTotal)
	}
}
