package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestPhase3_DetectionPipeline_PositiveAndNegative(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_test.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.TestIOCRule{},
		&detection.RealIOCExecRule{},
		&detection.PanicTestRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Positive Malicious Event
	maliciousEvent := &events.CanonicalEvent{
		EventID:       "malicious-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	outcome := ingestEngine.ProcessEvent(ctx, ctx, maliciousEvent)
	if outcome != ingestion.OutcomeCommitted {
		t.Fatalf("Failed to ingest malicious event: %s", outcome)
	}

	// 2. Negative Benign Event
	benignEvent := &events.CanonicalEvent{
		EventID:       "benign-456",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "CLEAN-FILE",
		},
	}

	outcome = ingestEngine.ProcessEvent(ctx, ctx, benignEvent)
	if outcome != ingestion.OutcomeCommitted {
		t.Fatalf("Failed to ingest benign event: %s", outcome)
	}

	// Assert SQLite State
	pending, err := db.GetPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to read pending events: %v", err)
	}

	// We expect 3 total durable events:
	// 1. malicious-123 (original telemetry)
	// 2. benign-456 (original telemetry)
	// 3. The detection finding derived from malicious-123
	if len(pending) != 3 {
		t.Fatalf("Expected 3 durable events, got %d", len(pending))
	}

	var foundFinding bool
	var foundMalicious bool
	var foundBenign bool

	for _, ev := range pending {
		if ev.EventID == "malicious-123" {
			foundMalicious = true
		} else if ev.EventID == "benign-456" {
			foundBenign = true
		} else if ev.Category == "detection/finding" {
			foundFinding = true
			if ev.RuleID != "LOCAL-IOC-TEST-001" {
				t.Errorf("Unexpected rule ID: %s", ev.RuleID)
			}
			evidenceIDs, ok := ev.Metadata["evidence_event_ids"].([]interface{})
			if !ok || len(evidenceIDs) != 1 || evidenceIDs[0] != "malicious-123" {
				t.Errorf("Finding evidence does not match expected original event ID: %v", ev.Metadata["evidence_event_ids"])
			}
		}
	}

	if !foundMalicious {
		t.Errorf("Original malicious telemetry event was lost!")
	}
	if !foundBenign {
		t.Errorf("Original benign telemetry event was lost!")
	}
	if !foundFinding {
		t.Errorf("Detection finding was not durably stored!")
	}
}

func TestPhase3_DetectionPipeline_Deduplication(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_dedup.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.TestIOCRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maliciousEvent := &events.CanonicalEvent{
		EventID:       "malicious-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	// Submit identical telemetry event twice to trigger duplicate evaluation
	ingestEngine.ProcessEvent(ctx, ctx, maliciousEvent)
	ingestEngine.ProcessEvent(ctx, ctx, maliciousEvent) // SQLite will dedup this telemetry, but OnCommitted fires again if successful.

	pending, err := db.GetPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to read pending events: %v", err)
	}

	// Should have exactly 1 original event and 1 finding, total 2.
	// True idempotency means the second evaluation identical finding payload
	// succeeds in db.Store without integrity constraint error and without duplicating rows.
	if len(pending) != 2 {
		for i, p := range pending {
			t.Logf("Row %d: %s (SeqNo: %d, Category: %s)", i, p.EventID, p.SeqNo, p.Category)
		}
		t.Fatalf("Expected 2 durable events after duplicate ingest, got %d", len(pending))
	}
}

func TestPhase3_DetectionPipeline_FailureIsolation(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_panic.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.PanicTestRule{},
		&detection.TestIOCRule{}, // Executes after panic
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	panicEvent := &events.CanonicalEvent{
		EventID:       "panic-trigger-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"trigger_panic": true,
			"test_ioc":      "RF-TEST-MALICIOUS", // Ensure the rule after panic still hits
		},
	}

	outcome := ingestEngine.ProcessEvent(ctx, ctx, panicEvent)
	if outcome != ingestion.OutcomeCommitted {
		t.Fatalf("Failed to ingest panic event: %s", outcome)
	}

	// Submit a second event to prove the engine remains usable
	subsequentEvent := &events.CanonicalEvent{
		EventID:       "subsequent-event-456",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	outcome2 := ingestEngine.ProcessEvent(ctx, ctx, subsequentEvent)
	if outcome2 != ingestion.OutcomeCommitted {
		t.Fatalf("Failed to ingest subsequent event: %s", outcome2)
	}

	// We verify that the original event is durable and the engine is still alive.
	pending, err := db.GetPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to read pending events: %v", err)
	}

	// We expect:
	// 1. Telemetry for panic event
	// 2. Finding for panic event (from TestIOCRule which executed AFTER the panic)
	// 3. Telemetry for subsequent event
	// 4. Finding for subsequent event
	if len(pending) != 4 {
		t.Fatalf("Expected 4 durable events, got %d", len(pending))
	}
}

func TestPhase3_DetectionPipeline_RealIOCExec(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_realioc.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.RealIOCExecRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maliciousEvent := &events.CanonicalEvent{
		EventID:       "realioc-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"executable_path": "/opt/malicious/bin/miner",
		},
	}

	ingestEngine.ProcessEvent(ctx, ctx, maliciousEvent)

	pending, err := db.GetPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to read pending events: %v", err)
	}

	var foundFinding bool
	for _, ev := range pending {
		if ev.Category == "detection/finding" {
			foundFinding = true
			if ev.RuleID != "LOCAL-IOC-EXEC-001" {
				t.Errorf("Unexpected rule ID: %s", ev.RuleID)
			}
			if len(ev.AttckEnterprise) != 0 {
				t.Errorf("Expected empty ATT&CK mapping for fixture")
			}
			if ev.Severity != "CRITICAL" {
				t.Errorf("Expected CRITICAL severity, got %s", ev.Severity)
			}
		}
	}

	if !foundFinding {
		t.Errorf("Real IOC Exec finding was not durably stored!")
	}
}

func TestPhase3_DetectionPipeline_RuleErrorIsolation(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_err.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	_ = db.Init(context.Background())

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.ErrorTestRule{},
		&detection.TestIOCRule{}, // Executes after error
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx := context.Background()

	ev := &events.CanonicalEvent{
		EventID:       "error-trigger-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"trigger_error": true,
			"test_ioc":      "RF-TEST-MALICIOUS", // Should still trigger next rule
		},
	}

	ingestEngine.ProcessEvent(ctx, ctx, ev)

	pending, _ := db.GetPendingEvents(ctx, 10)
	if len(pending) != 2 {
		t.Fatalf("Expected 2 durable events (telemetry + test ioc finding), got %d", len(pending))
	}
	// Telemetry survived, and next rule successfully executed and stored finding.
}

func TestPhase3_DetectionPipeline_MultipleFindings(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_multi.db", tempDir+"/id.json", 1024*1024*10)
	defer db.Close()
	_ = db.Init(context.Background())

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.TestIOCRule{},
		&detection.RealIOCExecRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx := context.Background()

	ev := &events.CanonicalEvent{
		EventID:       "multi-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc":        "RF-TEST-MALICIOUS",
			"executable_path": "/opt/malicious/bin/miner",
		},
	}

	ingestEngine.ProcessEvent(ctx, ctx, ev)

	pending, _ := db.GetPendingEvents(ctx, 10)
	// Expect 1 telemetry + 2 independent findings = 3 total
	if len(pending) != 3 {
		t.Fatalf("Expected 3 durable events (1 telemetry + 2 findings), got %d", len(pending))
	}

	findings := 0
	for _, p := range pending {
		if p.Category == "detection/finding" {
			findings++
			if p.Metadata["evidence_event_ids"].([]interface{})[0].(string) != "multi-123" {
				t.Fatalf("Finding evidence event ID mismatch")
			}
		}
	}
	if findings != 2 {
		t.Fatalf("Expected 2 findings, got %d", findings)
	}
}

func TestPhase3_DetectionPipeline_UntrustedMetadata(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_untrusted.db", tempDir+"/id.json", 1024*1024*50)
	defer db.Close()
	_ = db.Init(context.Background())

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.TestIOCRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx := context.Background()

	// Missing metadata
	evMissing := &events.CanonicalEvent{
		EventID:       "untrusted-1",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}

	// Wrong type
	evWrongType := &events.CanonicalEvent{
		EventID:       "untrusted-2",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": 12345, // int instead of string
		},
	}

	// Empty string
	evEmptyStr := &events.CanonicalEvent{
		EventID:       "untrusted-3",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "",
		},
	}

	// Embedded NUL bytes
	evNulBytes := &events.CanonicalEvent{
		EventID:       "untrusted-4",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-\x00MALICIOUS",
		},
	}

	// Very long string
	longStr := string(make([]byte, 10*1024)) // 10KB string
	evLongStr := &events.CanonicalEvent{
		EventID:       "untrusted-5",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": longStr,
		},
	}

	// Unexpected metadata structures (map instead of string)
	evUnexpectedMap := &events.CanonicalEvent{
		EventID:       "untrusted-6",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": map[string]string{"foo": "bar"},
		},
	}

	ingestEngine.ProcessEvent(ctx, ctx, evMissing)
	ingestEngine.ProcessEvent(ctx, ctx, evWrongType)
	ingestEngine.ProcessEvent(ctx, ctx, evEmptyStr)
	ingestEngine.ProcessEvent(ctx, ctx, evNulBytes)
	ingestEngine.ProcessEvent(ctx, ctx, evLongStr)
	ingestEngine.ProcessEvent(ctx, ctx, evUnexpectedMap)

	pending, _ := db.GetPendingEvents(ctx, 10)
	if len(pending) != 6 {
		t.Fatalf("Expected exactly 6 telemetry events, no crashes, no findings. Got %d", len(pending))
	}
}

// mockFailingStorage wraps an existing storage to inject failure specifically for findings
type mockFailingStorage struct {
	interfaces.Storage
}

func (m *mockFailingStorage) Store(ctx context.Context, ev *events.CanonicalEvent) error {
	if ev.Category == "detection/finding" {
		return fmt.Errorf("injected finding persistence failure")
	}
	return m.Storage.Store(ctx, ev)
}

func TestPhase3_DetectionPipeline_FindingPersistenceFailure(t *testing.T) {
	tempDir := t.TempDir()
	realDB := storage.NewSQLiteStorage(tempDir+"/detection_fail.db", tempDir+"/id.json", 1024*1024*10)
	defer realDB.Close()
	_ = realDB.Init(context.Background())

	failDB := &mockFailingStorage{Storage: realDB}

	detEngine := detection.NewEngine(failDB, []detection.Rule{
		&detection.TestIOCRule{},
	})

	ingestEngine := ingestion.NewEngine(realDB, ingestion.Config{ // Ingest engine uses real DB to store telemetry
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx := context.Background()
	ev := &events.CanonicalEvent{
		EventID:       "persistence-fail-123",
		Source:        "process",
		Category:      "process_start",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	ingestEngine.ProcessEvent(ctx, ctx, ev)

	// Telemetry should be durably stored. Finding was rejected by mockFailingStorage.
	pending, _ := realDB.GetPendingEvents(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("Expected exactly 1 durable event (the telemetry), got %d", len(pending))
	}
	if pending[0].EventID != "persistence-fail-123" {
		t.Fatalf("Telemetry was lost due to finding persistence failure!")
	}
}

func TestPhase3_DetectionPipeline_Stress(t *testing.T) {
	tempDir := t.TempDir()
	db := storage.NewSQLiteStorage(tempDir+"/detection_stress.db", tempDir+"/id.json", 1024*1024*50) // 50MB DB limit
	defer db.Close()
	_ = db.Init(context.Background())

	detEngine := detection.NewEngine(db, []detection.Rule{
		&detection.TestIOCRule{},
		&detection.RealIOCExecRule{},
	})

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID:   "tenant-det",
		SiteID:     "site-det",
		AssetID:    "asset-det",
		RetryDelay: 5 * time.Millisecond,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
		},
	})

	ctx := context.Background()

	const eventCount = 10000

	start := time.Now()

	// Create and process 10,000 deterministic events
	for i := 0; i < eventCount; i++ {
		ev := &events.CanonicalEvent{
			EventID:       uuid.New().String(), // Use valid UUIDs
			Source:        "process",
			Category:      "process_start",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"test_ioc":        "RF-TEST-MALICIOUS",
				"executable_path": "/opt/malicious/bin/miner",
			},
		}

		outcome := ingestEngine.ProcessEvent(ctx, ctx, ev)
		if outcome != ingestion.OutcomeCommitted {
			t.Fatalf("Failed to ingest event %d: %v", i, outcome)
		}
	}

	elapsed := time.Since(start)

	t.Logf("Processed %d events (creating %d findings) in %v", eventCount, eventCount*2, elapsed)
	t.Logf("Throughput: %.2f events/sec", float64(eventCount)/elapsed.Seconds())

	var totalRows int
	if err := db.GetDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&totalRows); err != nil {
		t.Fatalf("Failed to count durable rows: %v", err)
	}

	expectedRows := eventCount * 3 // 1 telemetry + 2 findings per event
	if totalRows != expectedRows {
		t.Errorf("Expected %d durable rows, got %d", expectedRows, totalRows)
	}

	var distinctEventIDs int
	if err := db.GetDB().QueryRowContext(ctx, "SELECT COUNT(DISTINCT event_id) FROM events").Scan(&distinctEventIDs); err != nil {
		t.Fatalf("Failed to count distinct event IDs: %v", err)
	}

	if distinctEventIDs != expectedRows {
		t.Errorf("Expected %d distinct event IDs, got %d", expectedRows, distinctEventIDs)
	}
}
