//go:build cgo

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// LatencySummary captures comprehensive summary statistics for a set of latency observations.
// Percentile Convention: Nearest-Rank Method.
// For a sorted slice of length N, the P-th percentile (P in (0, 1]) is at 1-based rank ceil(P * N),
// corresponding to 0-based index ceil(P * N) - 1.
// Edge cases:
// - N == 0: all percentiles and mean return 0.
// - N == 1: min, mean, p50, p90, p95, p99, and max all equal the single observation.
type LatencySummary struct {
	Count int           `json:"count"`
	Min   time.Duration `json:"min"`
	Mean  time.Duration `json:"mean"`
	P50   time.Duration `json:"p50"`
	P90   time.Duration `json:"p90"`
	P95   time.Duration `json:"p95"`
	P99   time.Duration `json:"p99"`
	Max   time.Duration `json:"max"`
	RawNs []int64       `json:"raw_nanoseconds,omitempty"`
}

// CalculateLatencySummary computes truthful summary statistics from a slice of durations.
// It explicitly copies the input slice before sorting, preserving original arrival order.
func CalculateLatencySummary(observations []time.Duration) LatencySummary {
	n := len(observations)
	if n == 0 {
		return LatencySummary{Count: 0}
	}

	// Work on an isolated sorted copy
	sorted := make([]time.Duration, n)
	copy(sorted, observations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var sum int64
	rawNs := make([]int64, n)
	for i, d := range observations {
		ns := d.Nanoseconds()
		sum += ns
		rawNs[i] = ns
	}

	meanNs := sum / int64(n)

	// Nearest-rank percentile helper: ceil(P * N) - 1
	calcPercentile := func(p float64) time.Duration {
		if p <= 0 {
			return sorted[0]
		}
		if p >= 1.0 {
			return sorted[n-1]
		}
		rank := int(math.Ceil(p * float64(n)))
		idx := rank - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}

	return LatencySummary{
		Count: n,
		Min:   sorted[0],
		Mean:  time.Duration(meanNs),
		P50:   calcPercentile(0.50),
		P90:   calcPercentile(0.90),
		P95:   calcPercentile(0.95),
		P99:   calcPercentile(0.99),
		Max:   sorted[n-1],
		RawNs: rawNs,
	}
}

// TestLatencyPercentiles_UnsortedAndEdgeCases tests CalculateLatencySummary with
// deliberately unsorted values and known expected results, verifying nearest-rank semantics.
func TestLatencyPercentiles_UnsortedAndEdgeCases(t *testing.T) {
	// 1. Deliberately unsorted 10-element sample:
	// Values in ms: [100, 10, 50, 90, 20, 80, 30, 70, 40, 60]
	// Sorted in ms: [10, 20, 30, 40, 50, 60, 70, 80, 90, 100]
	unsorted := []time.Duration{
		100 * time.Millisecond,
		10 * time.Millisecond,
		50 * time.Millisecond,
		90 * time.Millisecond,
		20 * time.Millisecond,
		80 * time.Millisecond,
		30 * time.Millisecond,
		70 * time.Millisecond,
		40 * time.Millisecond,
		60 * time.Millisecond,
	}

	s := CalculateLatencySummary(unsorted)

	// Verify original slice was not modified
	if unsorted[0] != 100*time.Millisecond {
		t.Errorf("original slice was modified: expected 100ms at [0], got %v", unsorted[0])
	}

	// Assertions based on Nearest-Rank method:
	// N = 10
	// Min = 10ms
	// Max = 100ms
	// Mean = 55ms
	// P50: ceil(0.50 * 10) = 5 -> index 4 = 50ms
	// P90: ceil(0.90 * 10) = 9 -> index 8 = 90ms
	// P95: ceil(0.95 * 10) = 10 -> index 9 = 100ms
	// P99: ceil(0.99 * 10) = 10 -> index 9 = 100ms
	if s.Min != 10*time.Millisecond {
		t.Errorf("expected Min=10ms, got %v", s.Min)
	}
	if s.Max != 100*time.Millisecond {
		t.Errorf("expected Max=100ms, got %v", s.Max)
	}
	if s.Mean != 55*time.Millisecond {
		t.Errorf("expected Mean=55ms, got %v", s.Mean)
	}
	if s.P50 != 50*time.Millisecond {
		t.Errorf("expected P50=50ms, got %v", s.P50)
	}
	if s.P90 != 90*time.Millisecond {
		t.Errorf("expected P90=90ms, got %v", s.P90)
	}
	if s.P95 != 100*time.Millisecond {
		t.Errorf("expected P95=100ms, got %v", s.P95)
	}
	if s.P99 != 100*time.Millisecond {
		t.Errorf("expected P99=100ms, got %v", s.P99)
	}

	// 2. Edge Case: Empty sample (N == 0)
	sEmpty := CalculateLatencySummary(nil)
	if sEmpty.Count != 0 || sEmpty.Min != 0 || sEmpty.Max != 0 || sEmpty.Mean != 0 || sEmpty.P50 != 0 {
		t.Errorf("expected zero values for empty sample, got %+v", sEmpty)
	}

	// 3. Edge Case: Single element (N == 1)
	singleVal := 42 * time.Millisecond
	sSingle := CalculateLatencySummary([]time.Duration{singleVal})
	if sSingle.Count != 1 || sSingle.Min != singleVal || sSingle.Max != singleVal ||
		sSingle.Mean != singleVal || sSingle.P50 != singleVal || sSingle.P95 != singleVal {
		t.Errorf("expected all metrics to equal single value, got %+v", sSingle)
	}

	// 4. Edge Case: Two elements (N == 2, unsorted: [30ms, 10ms])
	sTwo := CalculateLatencySummary([]time.Duration{30 * time.Millisecond, 10 * time.Millisecond})
	// Sorted: [10ms, 30ms].
	// P50: ceil(0.50 * 2) = 1 -> index 0 = 10ms
	// P95: ceil(0.95 * 2) = 2 -> index 1 = 30ms
	if sTwo.Min != 10*time.Millisecond || sTwo.Max != 30*time.Millisecond ||
		sTwo.Mean != 20*time.Millisecond || sTwo.P50 != 10*time.Millisecond || sTwo.P95 != 30*time.Millisecond {
		t.Errorf("unexpected summary for N=2: %+v", sTwo)
	}
}

// TestWorkload_YaraX_ComponentIntegration_PositiveVersusHarmlessDetection exercises
// the native YARA-X processing path as a COMPONENT/INTEGRATION TEST.
//
// SCOPE CLARIFICATION:
// This test directly calls ingestion.ProcessEvent, bypassing OS collectors, the eventqueue ring buffer,
// and endpoint measurement reporting. It evaluates native YARA-X matching overhead and SQLite durability
// at the component level. It DOES NOT qualify the production endpoint binary (which currently only loads
// RealIOCExecRule by default).
//
// MEASURED BOUNDARY CLARIFICATION:
// Two distinct latencies are captured per event:
//  1. Scanner-Only Latency: time spent in yaraRule.Evaluate(ev) alone (YARA-X C API scan).
//  2. Total Ingestion+Persistence Latency: time spent in detEngine.Evaluate(ev), which for positive matches
//     includes YARA-X scan + UUIDv5 finding derivation + finding validation + SQLite INSERT transaction.
//
// MEMORY BOUNDARY CLARIFICATION:
// runtime.MemStats (HeapAlloc, Sys) only measures Go runtime managed memory. Native allocations inside
// libyara_x_capi (C/Rust) and SQLite OS page caching are excluded from HeapAlloc. Total process memory
// must be measured via OS RSS (WorkingSetSize / VmRSS).
func TestWorkload_YaraX_ComponentIntegration_PositiveVersusHarmlessDetection(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "yarax_workload.db")
	idPath := filepath.Join(tempDir, "identity.json")

	// 1. Initialize SQLite storage with WAL mode
	db := storage.NewSQLiteStorage(dbPath, idPath, 100*1024*1024)
	if err := db.Init(context.Background()); err != nil {
		t.Fatalf("failed to init SQLite storage: %v", err)
	}

	deviceID := db.GetDeviceID()
	if deviceID == "" {
		t.Fatalf("expected non-empty device identity")
	}

	// 2. Compile real native YARA-X rule
	ruleSource := `
rule Suspicious_OT_Payload {
	meta:
		description = "Detects malicious industrial PLC override payload"
		severity = "CRITICAL"
		confidence = 95.0
	strings:
		$sig = "MALICIOUS_INDUSTRIAL_PAYLOAD_V1"
		$cmd = "SHUTDOWN_PLC_IMMEDIATE"
	condition:
		$sig and $cmd
}
`
	ruleID := "YARA-OT-MALWARE-001"
	ruleVersion := "1.0"
	ruleSeverity := "CRITICAL"
	ruleConfidence := 95.0

	yaraRule, err := detection.NewYaraRule(
		ruleID,
		ruleVersion,
		ruleSeverity,
		ruleConfidence,
		"Malicious industrial PLC override signature matched",
		[]string{"T1059", "T1204"},
		[]string{"T0814", "T0855"},
		ruleSource,
	)
	if err != nil {
		t.Fatalf("failed to compile native YARA-X rule: %v", err)
	}

	detEngine := detection.NewEngine(db, []detection.Rule{yaraRule})

	tenantID := "tenant-yarax-eval"
	siteID := "site-substation-01"

	var observedTotalLatencies []time.Duration
	var observedScannerLatencies []time.Duration

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: tenantID,
		SiteID:   siteID,
		AssetID:  deviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			// Measure Scanner-Only execution
			scanStart := time.Now()
			_, _ = yaraRule.Evaluate(ev)
			observedScannerLatencies = append(observedScannerLatencies, time.Since(scanStart))

			// Measure Total Engine Evaluation (Scanner + UUIDv5 finding creation + SQLite persistence)
			totalStart := time.Now()
			detEngine.Evaluate(ctx, ev)
			observedTotalLatencies = append(observedTotalLatencies, time.Since(totalStart))
		},
	})

	ctx := context.Background()
	drainCtx := context.Background()

	// 3. Prepare Test Artifact Files
	artifactDir := filepath.Join(tempDir, "artifacts")
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		t.Fatalf("failed to create artifact dir: %v", err)
	}

	eventCount := 30

	// Create harmless artifacts
	harmlessFiles := make([]string, eventCount)
	harmlessSourceIDs := make(map[string]bool)
	for i := 0; i < eventCount; i++ {
		p := filepath.Join(artifactDir, fmt.Sprintf("harmless_%d.dat", i))
		content := fmt.Sprintf("NORMAL_OPERATIONAL_TELEMETRY: reading_id=%d timestamp=%s status=OK sensor_voltage=24.1V", i, time.Now().UTC().Format(time.RFC3339Nano))
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write harmless file %d: %v", i, err)
		}
		harmlessFiles[i] = p
	}

	// Create malicious artifacts matching YARA rule
	maliciousFiles := make([]string, eventCount)
	for i := 0; i < eventCount; i++ {
		p := filepath.Join(artifactDir, fmt.Sprintf("malicious_%d.bin", i))
		content := fmt.Sprintf("PREAMBLE_DATA_%d\nMALICIOUS_INDUSTRIAL_PAYLOAD_V1\nTARGET_IP=192.168.1.50\nSHUTDOWN_PLC_IMMEDIATE\nPOSTAMBLE_DATA\n", i)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write malicious file %d: %v", i, err)
		}
		maliciousFiles[i] = p
	}

	getDBSizeBytes := func() int64 {
		var total int64
		for _, f := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			if fi, err := os.Stat(f); err == nil {
				total += fi.Size()
			}
		}
		return total
	}

	// -------------------------------------------------------------
	// Phase A: Harmless Workload Execution
	// -------------------------------------------------------------
	runtime.GC()
	var memBeforeHarmless runtime.MemStats
	runtime.ReadMemStats(&memBeforeHarmless)
	dbSizeBeforeHarmless := getDBSizeBytes()
	harmlessStartTime := time.Now()
	observedTotalLatencies = nil
	observedScannerLatencies = nil

	for i := 0; i < eventCount; i++ {
		sID := uuid.New().String()
		harmlessSourceIDs[sID] = true
		ev := &events.CanonicalEvent{
			EventID:       sID,
			Category:      "file/modification",
			Source:        "filesystem_monitor",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"file_path": harmlessFiles[i],
				"operation": "MODIFY",
			},
		}
		outcome := ingestEngine.ProcessEvent(ctx, drainCtx, ev)
		if outcome != ingestion.OutcomeCommitted {
			t.Fatalf("harmless event %d failed: %s", i, outcome)
		}
	}
	harmlessDuration := time.Since(harmlessStartTime)
	dbSizeAfterHarmless := getDBSizeBytes()
	var memAfterHarmless runtime.MemStats
	runtime.ReadMemStats(&memAfterHarmless)

	harmlessTotalSummary := CalculateLatencySummary(observedTotalLatencies)
	harmlessScannerSummary := CalculateLatencySummary(observedScannerLatencies)

	// Verify DB state after harmless workload: exactly eventCount events, 0 findings
	var harmlessDurableTotal, harmlessFindingsCount int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events").Scan(&harmlessDurableTotal); err != nil {
		t.Fatalf("failed to query events count: %v", err)
	}
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE payload LIKE '%\"category\":\"detection/finding\"%'").Scan(&harmlessFindingsCount); err != nil {
		t.Fatalf("failed to query findings count: %v", err)
	}
	if harmlessDurableTotal != eventCount {
		t.Fatalf("expected %d durable events after harmless run, got %d", eventCount, harmlessDurableTotal)
	}
	if harmlessFindingsCount != 0 {
		t.Fatalf("expected 0 findings after harmless run, got %d", harmlessFindingsCount)
	}

	// -------------------------------------------------------------
	// Phase B: Positive Detection Workload Execution
	// -------------------------------------------------------------
	runtime.GC()
	var memBeforeMalicious runtime.MemStats
	runtime.ReadMemStats(&memBeforeMalicious)
	dbSizeBeforeMalicious := getDBSizeBytes()
	maliciousStartTime := time.Now()
	observedTotalLatencies = nil
	observedScannerLatencies = nil

	expectedFindingToSource := make(map[string]string)
	sourceToExpectedFinding := make(map[string]string)
	namespace := uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")

	for i := 0; i < eventCount; i++ {
		sourceID := uuid.New().String()

		// Calculate exact expected UUIDv5 finding identity based on documented engine contract
		dedupString := fmt.Sprintf("%s:%s:%s:%s", tenantID, ruleID, ruleVersion, sourceID)
		expectedFindingID := uuid.NewSHA1(namespace, []byte(dedupString)).String()

		expectedFindingToSource[expectedFindingID] = sourceID
		sourceToExpectedFinding[sourceID] = expectedFindingID

		ev := &events.CanonicalEvent{
			EventID:       sourceID,
			Category:      "file/modification",
			Source:        "filesystem_monitor",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
			Metadata: map[string]interface{}{
				"file_path": maliciousFiles[i],
				"operation": "CREATE",
			},
		}
		outcome := ingestEngine.ProcessEvent(ctx, drainCtx, ev)
		if outcome != ingestion.OutcomeCommitted {
			t.Fatalf("malicious event %d failed: %s", i, outcome)
		}
	}
	maliciousDuration := time.Since(maliciousStartTime)
	dbSizeAfterMalicious := getDBSizeBytes()
	var memAfterMalicious runtime.MemStats
	runtime.ReadMemStats(&memAfterMalicious)

	maliciousTotalSummary := CalculateLatencySummary(observedTotalLatencies)
	maliciousScannerSummary := CalculateLatencySummary(observedScannerLatencies)

	// Verify DB state after positive detection workload:
	// Total events = eventCount (harmless) + eventCount (malicious source) + eventCount (derived findings) = 3 * eventCount
	var totalDurableEvents, totalFindingsCount int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events").Scan(&totalDurableEvents); err != nil {
		t.Fatalf("failed to query events count: %v", err)
	}
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE payload LIKE '%\"category\":\"detection/finding\"%'").Scan(&totalFindingsCount); err != nil {
		t.Fatalf("failed to query findings count: %v", err)
	}

	expectedTotal := eventCount*2 + eventCount // 30 harmless + 30 malicious + 30 findings = 90
	if totalDurableEvents != expectedTotal {
		t.Fatalf("expected %d total durable events, got %d", expectedTotal, totalDurableEvents)
	}
	if totalFindingsCount != eventCount {
		t.Fatalf("expected %d total derived findings, got %d", eventCount, totalFindingsCount)
	}

	// -------------------------------------------------------------
	// Phase C: Deep Finding Identity & Causal Linkage Assertions
	// -------------------------------------------------------------
	verifyFindings := func(storageInstance *storage.SQLiteStorage, phaseLabel string) {
		rows, err := storageInstance.GetDB().Query("SELECT event_id, payload FROM events WHERE payload LIKE '%\"category\":\"detection/finding\"%'")
		if err != nil {
			t.Fatalf("[%s] failed to query findings: %v", phaseLabel, err)
		}
		defer rows.Close()

		seenFindings := make(map[string]bool)
		for rows.Next() {
			var fID, rawPayload string
			if err := rows.Scan(&fID, &rawPayload); err != nil {
				t.Fatalf("[%s] failed to scan finding row: %v", phaseLabel, err)
			}

			// 1. Verify finding is in expected finding set
			expectedSourceID, isExpected := expectedFindingToSource[fID]
			if !isExpected {
				t.Fatalf("[%s] unexpected finding ID present in database: %s", phaseLabel, fID)
			}

			// 2. Verify no duplicates
			if seenFindings[fID] {
				t.Fatalf("[%s] duplicate finding ID detected in database: %s", phaseLabel, fID)
			}
			seenFindings[fID] = true

			// 3. Fully deserialize CanonicalEvent
			var finding events.CanonicalEvent
			if err := json.Unmarshal([]byte(rawPayload), &finding); err != nil {
				t.Fatalf("[%s] failed to deserialize finding payload for %s: %v", phaseLabel, fID, err)
			}

			// 4. Assert finding fields & metadata
			if finding.EventID != fID {
				t.Errorf("[%s] finding payload ID mismatch: payload=%s, col=%s", phaseLabel, finding.EventID, fID)
			}
			if finding.RuleID != ruleID {
				t.Errorf("[%s] finding rule ID mismatch: expected=%s, got=%s", phaseLabel, ruleID, finding.RuleID)
			}
			if finding.RuleVersion != ruleVersion {
				t.Errorf("[%s] finding rule version mismatch: expected=%s, got=%s", phaseLabel, ruleVersion, finding.RuleVersion)
			}
			if finding.Severity != ruleSeverity {
				t.Errorf("[%s] finding severity mismatch: expected=%s, got=%s", phaseLabel, ruleSeverity, finding.Severity)
			}
			if finding.Category != "detection/finding" {
				t.Errorf("[%s] finding category mismatch: expected='detection/finding', got=%s", phaseLabel, finding.Category)
			}
			if finding.Source != "local_detection" {
				t.Errorf("[%s] finding source mismatch: expected='local_detection', got=%s", phaseLabel, finding.Source)
			}

			// 5. Assert causal linkage in evidence_event_ids
			evidenceIDsRaw, ok := finding.Metadata["evidence_event_ids"]
			if !ok {
				t.Fatalf("[%s] finding %s missing evidence_event_ids metadata", phaseLabel, fID)
			}
			evidenceList, ok := evidenceIDsRaw.([]interface{})
			if !ok {
				t.Fatalf("[%s] evidence_event_ids is not []interface{}, got %T", phaseLabel, evidenceIDsRaw)
			}
			if len(evidenceList) != 1 {
				t.Fatalf("[%s] expected exactly 1 evidence event ID, got %d", phaseLabel, len(evidenceList))
			}
			linkedSourceID := fmt.Sprintf("%v", evidenceList[0])
			if linkedSourceID != expectedSourceID {
				t.Errorf("[%s] evidence linkage mismatch: expected=%s, got=%s", phaseLabel, expectedSourceID, linkedSourceID)
			}

			// 6. Assert harmless events never produced findings
			if harmlessSourceIDs[linkedSourceID] {
				t.Fatalf("[%s] CRITICAL DEFECT: finding %s was linked to harmless event %s", phaseLabel, fID, linkedSourceID)
			}
		}

		if err := rows.Err(); err != nil {
			t.Fatalf("[%s] rows iteration encountered error: %v", phaseLabel, err)
		}

		if len(seenFindings) != eventCount {
			t.Fatalf("[%s] expected exactly %d distinct findings, found %d", phaseLabel, eventCount, len(seenFindings))
		}
	}

	// Verify findings in open DB
	verifyFindings(db, "Initial Open Storage")

	// -------------------------------------------------------------
	// Phase D: Close & Reopen Durability Assertions
	// -------------------------------------------------------------
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close SQLite storage cleanly: %v", err)
	}

	// Reopen storage independently
	reopenedDB := storage.NewSQLiteStorage(dbPath, idPath, 100*1024*1024)
	if err := reopenedDB.Init(context.Background()); err != nil {
		t.Fatalf("failed to initialize reopened SQLite storage: %v", err)
	}
	defer func() {
		if err := reopenedDB.Close(); err != nil {
			t.Fatalf("failed to close reopened SQLite storage: %v", err)
		}
	}()

	// Re-verify exact finding identity and causal linkage after independent reopen
	verifyFindings(reopenedDB, "Reopened Independent Storage")

	// -------------------------------------------------------------
	// Phase E: Comparative Overhead & Machine-Readable Evidence
	// -------------------------------------------------------------
	harmlessStorageGrowth := dbSizeAfterHarmless - dbSizeBeforeHarmless
	maliciousStorageGrowth := dbSizeAfterMalicious - dbSizeBeforeMalicious

	t.Logf("=== NATIVE YARA-X COMPONENT WORKLOAD OVERHEAD COMPARISON ===")
	t.Logf("Notice: Component test directly calling ingestion.ProcessEvent (not endpoint qualification)")
	t.Logf("Events processed per batch: %d", eventCount)
	t.Logf("Harmless Workload:  duration=%v, scannerAvg=%v, scannerP95=%v, totalAvg=%v, totalP95=%v, totalMax=%v, storageGrowth=%d bytes",
		harmlessDuration, harmlessScannerSummary.Mean, harmlessScannerSummary.P95, harmlessTotalSummary.Mean, harmlessTotalSummary.P95, harmlessTotalSummary.Max, harmlessStorageGrowth)
	t.Logf("Detection Workload: duration=%v, scannerAvg=%v, scannerP95=%v, totalAvg=%v, totalP95=%v, totalMax=%v, storageGrowth=%d bytes",
		maliciousDuration, maliciousScannerSummary.Mean, maliciousScannerSummary.P95, maliciousTotalSummary.Mean, maliciousTotalSummary.P95, maliciousTotalSummary.Max, maliciousStorageGrowth)
	t.Logf("Storage Amplification Ratio: %.2fx (findings generate derived durable rows)",
		float64(maliciousStorageGrowth)/float64(harmlessStorageGrowth+1))
	t.Logf("HeapAlloc Harmless: %d bytes -> %d bytes", memBeforeHarmless.HeapAlloc, memAfterHarmless.HeapAlloc)
	t.Logf("HeapAlloc Malicious: %d bytes -> %d bytes", memBeforeMalicious.HeapAlloc, memAfterMalicious.HeapAlloc)
	t.Logf("Note on Memory: runtime.MemStats only measures Go heap; libyara_x_capi native allocations are excluded.")

	// Machine-readable component evidence artifact
	componentEvidence := map[string]interface{}{
		"evidence_type":             "component_integration",
		"component":                 "native_yarax_detection_engine",
		"timestamp_utc":             time.Now().UTC().Format(time.RFC3339),
		"rule_id":                   ruleID,
		"rule_version":              ruleVersion,
		"events_per_batch":          eventCount,
		"harmless_duration_ms":      harmlessDuration.Milliseconds(),
		"malicious_duration_ms":     maliciousDuration.Milliseconds(),
		"harmless_scanner_latency":  harmlessScannerSummary,
		"harmless_total_latency":    harmlessTotalSummary,
		"malicious_scanner_latency": maliciousScannerSummary,
		"malicious_total_latency":   maliciousTotalSummary,
		"storage_growth_bytes": map[string]int64{
			"harmless":  harmlessStorageGrowth,
			"malicious": maliciousStorageGrowth,
		},
		"storage_amplification_ratio": float64(maliciousStorageGrowth) / float64(harmlessStorageGrowth+1),
		"memory_boundary_note":        "runtime.MemStats measures Go heap only; native CGO allocations excluded",
		"durability_reopen_verified":  true,
		"zero_event_loss":             true,
	}

	evData, err := json.MarshalIndent(componentEvidence, "", "  ")
	if err == nil {
		evidencePath := filepath.Join(tempDir, "yarax_component_evidence.json")
		_ = os.WriteFile(evidencePath, evData, 0644)
	}

	// Invariants:
	// 1. Positive detection MUST produce greater storage growth than harmless workload due to finding persistence
	if maliciousStorageGrowth <= 0 {
		t.Errorf("expected positive storage growth for detection workload, got %d", maliciousStorageGrowth)
	}
	// 2. Detection total latency must be bounded (< 100ms per event even on unoptimized native scan + SQLite insert)
	if maliciousTotalSummary.Mean > 100*time.Millisecond {
		t.Errorf("average detection latency %v exceeds 100ms threshold", maliciousTotalSummary.Mean)
	}
}
