package resourcereport

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestConfig_InvalidOptions(t *testing.T) {
	// Duration <= 0
	err := ValidateConfig(Config{
		ReportPath: "test.json",
		Duration:   0,
		Interval:   time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "duration must be greater than 0") {
		t.Fatalf("expected duration error, got: %v", err)
	}

	// Interval < 100ms
	err = ValidateConfig(Config{
		ReportPath: "test.json",
		Duration:   time.Second,
		Interval:   50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "at least 100ms") {
		t.Fatalf("expected interval minimum error, got: %v", err)
	}

	// Interval > Duration
	err = ValidateConfig(Config{
		ReportPath: "test.json",
		Duration:   time.Second,
		Interval:   2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed duration") {
		t.Fatalf("expected interval exceed duration error, got: %v", err)
	}

	// Expected samples exceeds MaxSamples
	err = ValidateConfig(Config{
		ReportPath: "test.json",
		Duration:   10 * time.Second,
		Interval:   100 * time.Millisecond, // 100 samples
		MaxSamples: 50,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds configured sample limit") {
		t.Fatalf("expected sample limit error, got: %v", err)
	}
}

func TestConfig_RefuseOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "report.json")
	if err := os.WriteFile(existingFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	err := ValidateConfig(Config{
		ReportPath: existingFile,
		Duration:   time.Second,
		Interval:   time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already exists error, got: %v", err)
	}
}

func TestConfig_ProtectedFileConflicts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	cfgPath := filepath.Join(tmpDir, "agent.json")
	spillPath := filepath.Join(tmpDir, "agent_emergency_spill.log")

	cases := []struct {
		name       string
		reportPath string
	}{
		{"DB Conflict", dbPath},
		{"WAL Conflict", dbPath + "-wal"},
		{"SHM Conflict", dbPath + "-shm"},
		{"Identity Conflict", idPath},
		{"Config Conflict", cfgPath},
		{"Emergency Spill Conflict", spillPath},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(Config{
				ReportPath:         tc.reportPath,
				Duration:           time.Second,
				Interval:           time.Second,
				DBPath:             dbPath,
				IdentityPath:       idPath,
				ConfigPath:         cfgPath,
				EmergencySpillPath: spillPath,
			})
			if err == nil || !strings.Contains(err.Error(), "conflicts with protected persistence file") {
				t.Fatalf("expected persistence conflict error for %s, got: %v", tc.reportPath, err)
			}
		})
	}
}

func TestConfig_WatchedDirectoryConflict(t *testing.T) {
	tmpDir := t.TempDir()
	watchedDir := filepath.Join(tmpDir, "monitored")
	if err := os.MkdirAll(watchedDir, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	insideReport := filepath.Join(watchedDir, "nested", "report.json")

	err := ValidateConfig(Config{
		ReportPath:   insideReport,
		Duration:     time.Second,
		Interval:     time.Second,
		WatchedPaths: []string{watchedDir},
	})
	if err == nil || !strings.Contains(err.Error(), "inside watched directory") {
		t.Fatalf("expected watched directory rejection error, got: %v", err)
	}
}

func TestReport_MissingMetricsRemainNull(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.json")

	cfg := Config{
		ReportPath: reportPath,
		Profile:    "test-nulls",
		Duration:   time.Second,
		Interval:   time.Second,
	}

	reporter := NewReporter(cfg, []RuleInfo{
		{RuleID: "TEST-001", Version: "1.0", Severity: "LOW"},
	})

	// Add sample with null metrics
	reporter.AddSample(Sample{
		SampleIndex:      1,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		CPUPercent:       nil, // Unavailable / not ready
		RSSBytes:         nil, // Unavailable
		PersistenceBytes: nil, // Unavailable
		GovernorState:    "NORMAL",
	})

	rep, err := reporter.Finalize("MEASUREMENT_COMPLETE")
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if rep.Summary.TotalSamples != 1 {
		t.Fatalf("expected 1 sample, got %d", rep.Summary.TotalSamples)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report file: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	samples := parsed["samples"].([]interface{})
	sample0 := samples[0].(map[string]interface{})

	// Check null fields
	if sample0["cpu_percent"] != nil {
		t.Fatalf("expected cpu_percent to be null, got: %v", sample0["cpu_percent"])
	}
	if sample0["rss_bytes"] != nil {
		t.Fatalf("expected rss_bytes to be null, got: %v", sample0["rss_bytes"])
	}
	if sample0["persistence_bytes"] != nil {
		t.Fatalf("expected persistence_bytes to be null, got: %v", sample0["persistence_bytes"])
	}
	if sample0["queue_oldest_event_age_seconds"] != nil {
		t.Fatalf("expected queue_oldest_event_age_seconds to be null, got: %v", sample0["queue_oldest_event_age_seconds"])
	}
}

func TestReport_OutputByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.json")

	cfg := Config{
		ReportPath:     reportPath,
		Duration:       time.Second,
		Interval:       time.Second,
		MaxOutputBytes: 100, // Very small byte limit
	}

	reporter := NewReporter(cfg, nil)
	reporter.AddSample(Sample{
		SampleIndex:   1,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		GovernorState: "NORMAL",
	})

	_, err := reporter.Finalize("MEASUREMENT_COMPLETE")
	if err == nil || !strings.Contains(err.Error(), "exceeds output limit") {
		t.Fatalf("expected byte limit exceeded error, got: %v", err)
	}
	if reporter.reportingErrors != 1 {
		t.Fatalf("expected 1 reporting error, got %d", reporter.reportingErrors)
	}
}

func TestShutdownCoordinator_TimerSignalOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	drainTimeout := 200 * time.Millisecond
	coord := NewShutdownCoordinator(cancel, drainCancel, drainTimeout)

	var drainStartedCount int32
	coord.onDrainStart = func() {
		atomic.AddInt32(&drainStartedCount, 1)
	}

	// Trigger from multiple concurrent sources (e.g. signal + timer expiry)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		coord.Trigger("SIGNAL_INTERRUPT")
	}()
	go func() {
		defer wg.Done()
		coord.Trigger("MEASUREMENT_COMPLETE")
	}()
	go func() {
		defer wg.Done()
		coord.Trigger("SIGNAL_INTERRUPT")
	}()
	wg.Wait()

	// Verify cancel() was invoked
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Fatalf("expected ctx to be canceled")
	}

	// Wait for drain timeout
	time.Sleep(300 * time.Millisecond)

	// Verify drainCtx was canceled
	select {
	case <-drainCtx.Done():
		// Expected
	default:
		t.Fatalf("expected drainCtx to be canceled after timeout")
	}

	// Verify drain timer was started exactly once
	if atomic.LoadInt32(&drainStartedCount) != 1 {
		t.Fatalf("expected drain deadline timer to start exactly once, started %d times", drainStartedCount)
	}

	if !coord.TimerFired() {
		t.Fatalf("expected coord.TimerFired() to be true")
	}
}

func TestShutdownCoordinator_PendingIngestionDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	drainTimeout := 200 * time.Millisecond
	coord := NewShutdownCoordinator(cancel, drainCancel, drainTimeout)

	// Ingestion channel with 5 pending items
	ch := make(chan int, 5)
	for i := 1; i <= 5; i++ {
		ch <- i
	}
	close(ch)

	coord.Trigger("MEASUREMENT_COMPLETE")

	if ctx.Err() == nil {
		t.Fatalf("expected ctx to be canceled by Trigger")
	}

	var drained []int
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case item, ok := <-ch:
				if !ok {
					return
				}
				drained = append(drained, item)
			case <-drainCtx.Done():
				return
			}
		}
	}()

	select {
	case <-done:
		if len(drained) != 5 {
			t.Fatalf("expected 5 items drained, got %d", len(drained))
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for pending ingestion drain")
	}
}

func TestReport_EventAccounting(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report_accounting.json")

	cfg := Config{
		ReportPath: reportPath,
		Profile:    "test-accounting",
		Duration:   time.Second,
		Interval:   time.Second,
	}

	reporter := NewReporter(cfg, nil)

	ageVal := 0.45
	bpVal := uint64(3)
	reporter.AddSample(Sample{
		SampleIndex:                1,
		Timestamp:                  time.Now().UTC().Format(time.RFC3339),
		GovernorState:              "NORMAL",
		QueueOldestEventAgeSeconds: &ageVal,
		BackpressureEventsTotal:    &bpVal,
	})

	accounting := &EventAccountingSummary{
		IngestedTotal:           100,
		CommittedTotal:          90,
		ForwardedTotal:          85,
		FailedBeforeCommitTotal: 5,
		RecoveredDLQTotal:       3,
		SpilledDiskTotal:        1,
		DroppedTotal:            1,
		BackpressureEventsTotal: 3,
		ReconciliationValid:     true,
	}
	reporter.SetAccounting(accounting)

	rep, err := reporter.Finalize("MEASUREMENT_COMPLETE")
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	if rep.Summary.EventAccounting == nil {
		t.Fatalf("expected EventAccounting in Summary, got nil")
	}
	if !rep.Summary.EventAccounting.ReconciliationValid {
		t.Fatalf("expected ReconciliationValid to be true")
	}
	if rep.Summary.EventAccounting.IngestedTotal != 100 {
		t.Fatalf("expected IngestedTotal 100, got %d", rep.Summary.EventAccounting.IngestedTotal)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	summary := parsed["summary"].(map[string]interface{})
	accMap := summary["event_accounting"].(map[string]interface{})
	if accMap["ingested_total"].(float64) != 100 {
		t.Fatalf("expected json ingested_total 100, got %v", accMap["ingested_total"])
	}
	if accMap["reconciliation_valid"].(bool) != true {
		t.Fatalf("expected json reconciliation_valid true")
	}

	samples := parsed["samples"].([]interface{})
	sample0 := samples[0].(map[string]interface{})
	if sample0["queue_oldest_event_age_seconds"].(float64) != 0.45 {
		t.Fatalf("expected queue_oldest_event_age_seconds 0.45, got %v", sample0["queue_oldest_event_age_seconds"])
	}
	if sample0["backpressure_events_total"].(float64) != 3 {
		t.Fatalf("expected backpressure_events_total 3, got %v", sample0["backpressure_events_total"])
	}
}

func TestInspectDurableStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_inspect.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE events (
		event_id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		seq_no INTEGER NOT NULL,
		severity TEXT NOT NULL,
		payload TEXT NOT NULL,
		state TEXT NOT NULL
	);
	CREATE TABLE dead_letter_queue (
		event_id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		seq_no INTEGER NOT NULL,
		payload TEXT NOT NULL,
		failure_type TEXT NOT NULL,
		failure_reason TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// Insert: 2 regular events (1 pending, 1 sent), 1 finding (pending), 1 DLQ event
	_, err = db.Exec(`
	INSERT INTO events (event_id, device_id, seq_no, severity, payload, state) VALUES
		('ev-1', 'dev-1', 1, 'INFO', '{"category":"process","action":"EXEC"}', 'PENDING'),
		('ev-2', 'dev-1', 2, 'INFO', '{"category":"filesystem","action":"WRITE"}', 'SENT'),
		('ev-3', 'dev-1', 3, 'CRITICAL', '{"category":"detection/finding","rule_id":"R1"}', 'PENDING');
	INSERT INTO dead_letter_queue (event_id, device_id, seq_no, payload, failure_type, failure_reason) VALUES
		('ev-dlq-1', 'dev-1', 4, '{"category":"network"}', 'http_400', 'bad request');
	`)
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	initialBacklog := uint64(5)
	snap := InspectDurableStorage(db, &initialBacklog)

	if snap.InitialBacklogPendingEvents == nil || *snap.InitialBacklogPendingEvents != 5 {
		t.Fatalf("expected InitialBacklogPendingEvents 5, got %v", snap.InitialBacklogPendingEvents)
	}
	if snap.DurablePendingEvents == nil || *snap.DurablePendingEvents != 2 { // ev-1 and ev-3
		t.Fatalf("expected DurablePendingEvents 2, got %v", snap.DurablePendingEvents)
	}
	if snap.DurableTotalEvents == nil || *snap.DurableTotalEvents != 3 {
		t.Fatalf("expected DurableTotalEvents 3, got %v", snap.DurableTotalEvents)
	}
	if snap.DurableDLQEvents == nil || *snap.DurableDLQEvents != 1 {
		t.Fatalf("expected DurableDLQEvents 1, got %v", snap.DurableDLQEvents)
	}
	if snap.DerivedFindingsEvents == nil || *snap.DerivedFindingsEvents != 1 {
		t.Fatalf("expected DerivedFindingsEvents 1, got %v", snap.DerivedFindingsEvents)
	}
	if snap.SourceTelemetryEvents == nil || *snap.SourceTelemetryEvents != 2 {
		t.Fatalf("expected SourceTelemetryEvents 2, got %v", snap.SourceTelemetryEvents)
	}
	if snap.PrunedEventsTotal != nil {
		t.Fatalf("expected PrunedEventsTotal to be nil (unsupported), got %v", snap.PrunedEventsTotal)
	}
}
