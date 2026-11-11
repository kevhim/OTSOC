package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// TestWorkloadDemonstration_ProductionPath exercises the full production pipeline:
// Collector Event Creation -> Enrichment Boundary -> SQLite Storage Durability ->
// Synchronous Detection Evaluation (RealIOCExecRule) -> Derived Finding Durability -> Clean Shutdown.
// It verifies that source event and derived finding are explicitly linked by event_id
// and are durably present in SQLite after shutdown.
func TestWorkloadDemonstration_ProductionPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "workload_agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	// 1. Durability Layer
	db := storage.NewSQLiteStorage(dbPath, idPath, 50*1024*1024)
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("failed to init SQLite storage: %v", err)
	}

	deviceID := db.GetDeviceID()
	if deviceID == "" {
		t.Fatalf("expected non-empty device identity")
	}

	// 2. Detection Engine with Real Deterministic IOC Rule (LOCAL-IOC-EXEC-001, v1.0)
	rules := []detection.Rule{
		&detection.RealIOCExecRule{},
	}
	detEngine := detection.NewEngine(db, rules)

	// 3. Shared Ingestion Engine
	tenantID := "demo-tenant-workload"
	siteID := "demo-site-workload"
	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: tenantID,
		SiteID:   siteID,
		AssetID:  deviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	// 4. Generate Bounded Harmless Workload:
	// - 5 benign process events
	// - 1 harmless IOC fixture event (executable_path = "/opt/malicious/bin/miner")
	// - 5 benign filesystem events
	totalSourceEvents := 11
	var iocSourceEventID string

	for i := 1; i <= 5; i++ {
		ev := &events.CanonicalEvent{
			EventID:       uuid.New().String(),
			Category:      "process/start",
			Source:        "process_collector",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"executable_path": fmt.Sprintf("C:\\Windows\\System32\\process_%d.exe", i),
				"pid":             1000 + i,
			},
		}
		outcome := ingestEngine.ProcessEvent(ctx, drainCtx, ev)
		if outcome != ingestion.OutcomeCommitted {
			t.Fatalf("expected event %d committed, got %s", i, outcome)
		}
	}

	// Harmless IOC fixture event
	iocSourceEventID = uuid.New().String()
	iocEvent := &events.CanonicalEvent{
		EventID:       iocSourceEventID,
		Category:      "process/start",
		Source:        "process_collector",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"executable_path": "/opt/malicious/bin/miner",
			"pid":             6666,
		},
	}
	iocOutcome := ingestEngine.ProcessEvent(ctx, drainCtx, iocEvent)
	if iocOutcome != ingestion.OutcomeCommitted {
		t.Fatalf("expected IOC event committed, got %s", iocOutcome)
	}

	for i := 1; i <= 5; i++ {
		ev := &events.CanonicalEvent{
			EventID:       uuid.New().String(),
			Category:      "file/create",
			Source:        "filesystem_collector",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"file_path": fmt.Sprintf("/tmp/workload_file_%d.dat", i),
				"size":      1024 * i,
			},
		}
		outcome := ingestEngine.ProcessEvent(ctx, drainCtx, ev)
		if outcome != ingestion.OutcomeCommitted {
			t.Fatalf("expected file event %d committed, got %s", i, outcome)
		}
	}

	// 5. Clean Shutdown
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db cleanly: %v", err)
	}

	// 6. Independent Reopen & Durability Verification
	reopenedDB := storage.NewSQLiteStorage(dbPath, idPath, 50*1024*1024)
	if err := reopenedDB.Init(context.Background()); err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	// Query pending events from SQLite storage
	pending, err := reopenedDB.GetPendingEvents(context.Background(), 100)
	if err != nil {
		t.Fatalf("failed to get pending events from reopened db: %v", err)
	}

	// Total expected: 11 source events + 1 derived detection finding = 12 events
	expectedTotal := totalSourceEvents + 1
	if len(pending) != expectedTotal {
		t.Fatalf("expected %d durable events in storage, found %d", expectedTotal, len(pending))
	}

	// Locate derived detection finding
	var findingEvent *events.CanonicalEvent
	var sourceEventFound bool

	for _, ev := range pending {
		if ev.EventID == iocSourceEventID {
			sourceEventFound = true
		}
		if ev.Category == "detection/finding" {
			findingEvent = ev
		}
	}

	if !sourceEventFound {
		t.Fatalf("source IOC event %s not found in durable storage", iocSourceEventID)
	}

	if findingEvent == nil {
		t.Fatalf("expected derived detection finding event in storage, but none found")
	}

	// Verify Finding Integrity & Rule Attribution
	if findingEvent.RuleID != "LOCAL-IOC-EXEC-001" {
		t.Fatalf("expected rule ID LOCAL-IOC-EXEC-001, got: %s", findingEvent.RuleID)
	}
	if findingEvent.RuleVersion != "1.0" {
		t.Fatalf("expected rule version 1.0, got: %s", findingEvent.RuleVersion)
	}
	if findingEvent.Severity != "CRITICAL" {
		t.Fatalf("expected severity CRITICAL, got: %s", findingEvent.Severity)
	}
	if findingEvent.Source != "local_detection" {
		t.Fatalf("expected source local_detection, got: %s", findingEvent.Source)
	}

	// Verify Evidence Event Linkage
	evidenceIDsRaw, ok := findingEvent.Metadata["evidence_event_ids"]
	if !ok {
		t.Fatalf("finding metadata missing evidence_event_ids")
	}

	// Convert evidenceIDsRaw to string slice
	evidenceBytes, _ := json.Marshal(evidenceIDsRaw)
	var evidenceIDs []string
	if err := json.Unmarshal(evidenceBytes, &evidenceIDs); err != nil {
		t.Fatalf("failed to parse evidence_event_ids: %v", err)
	}

	if len(evidenceIDs) != 1 || evidenceIDs[0] != iocSourceEventID {
		t.Fatalf("expected finding linked to source event %s, got: %v", iocSourceEventID, evidenceIDs)
	}

	t.Logf("PASS: Durably verified 11 source events and 1 derived finding (%s) linked to source (%s) in reopened SQLite storage",
		findingEvent.EventID, iocSourceEventID)
}
