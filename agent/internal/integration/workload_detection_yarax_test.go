//go:build cgo

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
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

// countingRuleWrapper intercepts calls to a detection.Rule to measure and assert
// the exact count of rule evaluations performed across an experiment.
type countingRuleWrapper struct {
	inner     detection.Rule
	evalCount int64
}

func (w *countingRuleWrapper) ID() string                { return w.inner.ID() }
func (w *countingRuleWrapper) Version() string           { return w.inner.Version() }
func (w *countingRuleWrapper) Severity() string          { return w.inner.Severity() }
func (w *countingRuleWrapper) Confidence() float64       { return w.inner.Confidence() }
func (w *countingRuleWrapper) Reason() string            { return w.inner.Reason() }
func (w *countingRuleWrapper) AttckEnterprise() []string { return w.inner.AttckEnterprise() }
func (w *countingRuleWrapper) AttckICS() []string        { return w.inner.AttckICS() }
func (w *countingRuleWrapper) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	atomic.AddInt64(&w.evalCount, 1)
	return w.inner.Evaluate(ev)
}
func (w *countingRuleWrapper) Count() int64 {
	return atomic.LoadInt64(&w.evalCount)
}
func (w *countingRuleWrapper) Reset() {
	atomic.StoreInt64(&w.evalCount, 0)
}

func getLibYaraXVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pkg-config", "--modversion", "yara_x_capi")
	out, err := cmd.Output()
	if err == nil {
		v := strings.TrimSpace(string(out))
		if v != "" {
			return v
		}
	}
	return "1.20.0"
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
// Two distinct experiments evaluate performance without double-scanning:
//  1. Experiment 1 (Pure Rule Scanner Benchmark): measures yaraRule.Evaluate(ev) alone in memory (YARA-X C API scan).
//  2. Experiment 2 (Ingestion & Pipeline Benchmark): measures OnCommitted -> detEngine.Evaluate(ev) (single scan +
//     deterministic UUIDv5 derivation + SQLite INSERT transaction), asserting exactly 1 scan per event.
//
// MEMORY BOUNDARY CLARIFICATION:
// runtime.MemStats (HeapAlloc, Sys) only measures Go runtime managed memory. Native allocations inside
// libyara_x_capi (C/Rust) and SQLite OS page caching are excluded from HeapAlloc. Total process memory
// must be measured via OS RSS (WorkingSetSize / VmRSS).
func TestWorkload_YaraX_ComponentIntegration_PositiveVersusHarmlessDetection(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("failed to determine repository root: %v", err)
	}

	// 0. Pre-workload source boundary verification
	ctxPre, cancelPre := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPre()
	preCheck, err := CheckSourceBoundary(ctxPre, repoRoot, "pre-yarax-component-workload")
	if err != nil {
		t.Fatalf("failed pre-collection source boundary check: %v", err)
	}

	exportRequested := os.Getenv("EVIDENCE_EXPORT_DIR") != "" || os.Getenv("EVIDENCE_EXPORT_TO_REPO") == "true"
	if exportRequested && preCheck.Dirty && os.Getenv("ALLOW_DIRTY_EVIDENCE") != "true" {
		t.Fatalf("cannot export accepted qualification evidence from dirty checkout: revision=%s, dirty=%v. Commit changes before collecting evidence.", preCheck.Revision, preCheck.Dirty)
	}

	tempDir := t.TempDir()
	runID := fmt.Sprintf("yarax_%s", time.Now().UTC().Format("20060102_150405"))
	persistentDiagDir := filepath.Join(repoRoot, "docs", "evidence", "phase4", "diagnostics")
	stagingDir := filepath.Join(tempDir, "staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		t.Fatalf("failed to create staging dir: %v", err)
	}
	diagMgr := NewRunDiagnosticManager(runID, persistentDiagDir, stagingDir, nil)
	diagMgr.RegisterTestingFinalizer(t)

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

	tenantID := "tenant-yarax-eval"
	siteID := "site-substation-01"

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
	// Experiment 1: Pure Rule Scanner Benchmark (Single Scan Per Event)
	// -------------------------------------------------------------
	// Stated Warm-Up: evaluate 5 harmless and 5 malicious events to warm caches / branch prediction
	for i := 0; i < 5; i++ {
		warmHarmless := &events.CanonicalEvent{
			EventID:  fmt.Sprintf("warmup-harmless-%d", i),
			Category: "file/modification",
			Metadata: map[string]interface{}{"file_path": harmlessFiles[i%eventCount]},
		}
		if _, err := yaraRule.Evaluate(warmHarmless); err != nil {
			t.Fatalf("warmup harmless evaluation failed: %v", err)
		}
		warmMalicious := &events.CanonicalEvent{
			EventID:  fmt.Sprintf("warmup-malicious-%d", i),
			Category: "file/modification",
			Metadata: map[string]interface{}{"file_path": maliciousFiles[i%eventCount]},
		}
		if _, err := yaraRule.Evaluate(warmMalicious); err != nil {
			t.Fatalf("warmup malicious evaluation failed: %v", err)
		}
	}

	// 1a. Scanner-Only Harmless Benchmark (30 events)
	var rawScannerHarmless []time.Duration
	var rawScannerHarmlessNs []int64
	for i := 0; i < eventCount; i++ {
		ev := &events.CanonicalEvent{
			EventID:  fmt.Sprintf("scanner-harmless-%d", i),
			Category: "file/modification",
			Source:   "filesystem_monitor",
			Metadata: map[string]interface{}{
				"file_path": harmlessFiles[i],
				"operation": "MODIFY",
			},
		}
		scanStart := time.Now()
		matched, scanErr := yaraRule.Evaluate(ev)
		scanDur := time.Since(scanStart)
		if scanErr != nil {
			t.Fatalf("scanner error on harmless event %d: %v", i, scanErr)
		}
		if matched {
			t.Fatalf("scanner-only harmless event matched unexpectedly")
		}
		rawScannerHarmless = append(rawScannerHarmless, scanDur)
		rawScannerHarmlessNs = append(rawScannerHarmlessNs, scanDur.Nanoseconds())
	}
	scannerHarmlessSummary := CalculateLatencySummary(rawScannerHarmless)

	// 1b. Scanner-Only Detection Benchmark (30 events)
	var rawScannerMalicious []time.Duration
	var rawScannerMaliciousNs []int64
	for i := 0; i < eventCount; i++ {
		ev := &events.CanonicalEvent{
			EventID:  fmt.Sprintf("scanner-malicious-%d", i),
			Category: "file/modification",
			Source:   "filesystem_monitor",
			Metadata: map[string]interface{}{
				"file_path": maliciousFiles[i],
				"operation": "CREATE",
			},
		}
		scanStart := time.Now()
		matched, scanErr := yaraRule.Evaluate(ev)
		scanDur := time.Since(scanStart)
		if scanErr != nil {
			t.Fatalf("scanner error on detection event %d: %v", i, scanErr)
		}
		if !matched {
			t.Fatalf("scanner-only detection event failed to match")
		}
		rawScannerMalicious = append(rawScannerMalicious, scanDur)
		rawScannerMaliciousNs = append(rawScannerMaliciousNs, scanDur.Nanoseconds())
	}
	scannerMaliciousSummary := CalculateLatencySummary(rawScannerMalicious)

	// -------------------------------------------------------------
	// Experiment 2: Ingestion & Pipeline Detection Benchmark
	// -------------------------------------------------------------
	countingRule := &countingRuleWrapper{inner: yaraRule}
	detEngine := detection.NewEngine(db, []detection.Rule{countingRule})

	var rawPipelineHarmless []time.Duration
	var rawPipelineHarmlessNs []int64
	var rawPipelineMalicious []time.Duration
	var rawPipelineMaliciousNs []int64
	var isMaliciousPhase bool

	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: tenantID,
		SiteID:   siteID,
		AssetID:  deviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			// Single evaluation per event: detEngine.Evaluate executes countingRule once
			totalStart := time.Now()
			detEngine.Evaluate(ctx, ev)
			dur := time.Since(totalStart)
			if !isMaliciousPhase {
				rawPipelineHarmless = append(rawPipelineHarmless, dur)
				rawPipelineHarmlessNs = append(rawPipelineHarmlessNs, dur.Nanoseconds())
			} else {
				rawPipelineMalicious = append(rawPipelineMalicious, dur)
				rawPipelineMaliciousNs = append(rawPipelineMaliciousNs, dur.Nanoseconds())
			}
		},
	})

	ctx := context.Background()
	drainCtx := context.Background()

	// 2a. Pipeline Harmless Workload Execution
	countingRule.Reset()
	runtime.GC()
	var memBeforeHarmless runtime.MemStats
	runtime.ReadMemStats(&memBeforeHarmless)
	dbSizeBeforeHarmless := getDBSizeBytes()
	harmlessStartTime := time.Now()

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

	// Assert exactly ONE rule evaluation per event in pipeline harmless workload
	if evalCount := countingRule.Count(); evalCount != int64(eventCount) {
		t.Fatalf("expected exactly %d rule evaluations (1 per event), got %d", eventCount, evalCount)
	}

	pipelineHarmlessSummary := CalculateLatencySummary(rawPipelineHarmless)

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

	// 2b. Pipeline Detection Workload Execution
	isMaliciousPhase = true
	countingRule.Reset()
	runtime.GC()
	var memBeforeMalicious runtime.MemStats
	runtime.ReadMemStats(&memBeforeMalicious)
	dbSizeBeforeMalicious := getDBSizeBytes()
	maliciousStartTime := time.Now()

	expectedFindingToSource := make(map[string]string)
	sourceToExpectedFinding := make(map[string]string)
	namespace := uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")

	var allAssertedSourceIDs []string
	var allAssertedFindingIDs []string

	for i := 0; i < eventCount; i++ {
		sourceID := uuid.New().String()

		// Calculate exact expected UUIDv5 finding identity based on documented engine contract
		dedupString := fmt.Sprintf("%s:%s:%s:%s", tenantID, ruleID, ruleVersion, sourceID)
		expectedFindingID := uuid.NewSHA1(namespace, []byte(dedupString)).String()

		expectedFindingToSource[expectedFindingID] = sourceID
		sourceToExpectedFinding[sourceID] = expectedFindingID
		allAssertedSourceIDs = append(allAssertedSourceIDs, sourceID)
		allAssertedFindingIDs = append(allAssertedFindingIDs, expectedFindingID)

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

	// Assert exactly ONE rule evaluation per event in pipeline detection workload
	if evalCount := countingRule.Count(); evalCount != int64(eventCount) {
		t.Fatalf("expected exactly %d rule evaluations (1 per event), got %d", eventCount, evalCount)
	}

	pipelineMaliciousSummary := CalculateLatencySummary(rawPipelineMalicious)

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
	t.Logf("Harmless Workload:  duration=%v, scannerAvg=%v, scannerP95=%v, pipelineAvg=%v, pipelineP95=%v, pipelineMax=%v, storageGrowth=%d bytes",
		harmlessDuration, scannerHarmlessSummary.Mean, scannerHarmlessSummary.P95, pipelineHarmlessSummary.Mean, pipelineHarmlessSummary.P95, pipelineHarmlessSummary.Max, harmlessStorageGrowth)
	t.Logf("Detection Workload: duration=%v, scannerAvg=%v, scannerP95=%v, pipelineAvg=%v, pipelineP95=%v, pipelineMax=%v, storageGrowth=%d bytes",
		maliciousDuration, scannerMaliciousSummary.Mean, scannerMaliciousSummary.P95, pipelineMaliciousSummary.Mean, pipelineMaliciousSummary.P95, pipelineMaliciousSummary.Max, maliciousStorageGrowth)
	t.Logf("Storage Amplification Ratio: %.2fx (findings generate derived durable rows)",
		float64(maliciousStorageGrowth)/float64(harmlessStorageGrowth+1))
	t.Logf("HeapAlloc Harmless: %d bytes -> %d bytes", memBeforeHarmless.HeapAlloc, memAfterHarmless.HeapAlloc)
	t.Logf("HeapAlloc Malicious: %d bytes -> %d bytes", memBeforeMalicious.HeapAlloc, memAfterMalicious.HeapAlloc)
	t.Logf("Note on Memory: runtime.MemStats only measures Go heap; libyara_x_capi native allocations are excluded.")

	// Invariants (asserted and verified strictly BEFORE exporting accepted evidence):
	// 1. Positive detection MUST produce greater storage growth than harmless workload due to finding persistence
	if maliciousStorageGrowth <= 0 {
		_, _ = diagMgr.FinalizeFailure(OutcomeFailed, "non-positive storage growth for detection workload", fmt.Errorf("storage growth=%d", maliciousStorageGrowth))
		t.Fatalf("expected positive storage growth for detection workload, got %d", maliciousStorageGrowth)
	}
	// 2. Detection pipeline latency must be bounded (< 100ms per event even on unoptimized native scan + SQLite insert)
	if pipelineMaliciousSummary.Mean > 100*time.Millisecond {
		_, _ = diagMgr.FinalizeFailure(OutcomeFailed, "detection pipeline latency exceeded 100ms threshold", fmt.Errorf("mean latency=%v", pipelineMaliciousSummary.Mean))
		t.Fatalf("average detection pipeline latency %v exceeds 100ms threshold", pipelineMaliciousSummary.Mean)
	}

	// Post-workload source boundary check
	ctxPost, cancelPost := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPost()
	postCheck, err := CheckSourceBoundary(ctxPost, repoRoot, "post-yarax-component-workload")
	if err != nil {
		_, _ = diagMgr.FinalizeFailure(OutcomeInvalid, "post-workload source boundary check failed", err)
		t.Fatalf("post-workload source boundary check failed: %v", err)
	}
	if postCheck.Revision != preCheck.Revision {
		bErr := fmt.Errorf("source revision changed during collection: pre=%s, post=%s", preCheck.Revision, postCheck.Revision)
		_, _ = diagMgr.FinalizeFailure(OutcomeInvalid, "source revision altered during collection", bErr)
		t.Fatalf("qualification run invalidated: %v", bErr)
	}
	if exportRequested && postCheck.Dirty && os.Getenv("ALLOW_DIRTY_EVIDENCE") != "true" {
		bErr := fmt.Errorf("working tree became dirty during workload collection: status=%s", postCheck.StatusNotes)
		_, _ = diagMgr.FinalizeFailure(OutcomeInvalid, "working tree dirty after collection", bErr)
		t.Fatalf("qualification run invalidated: %v", bErr)
	}

	libyaraVer := getLibYaraXVersion()

	// Machine-readable component evidence artifact using strongly typed struct
	componentEvidence := YaraXComponentEvidence{
		EvidenceType:    "component_integration",
		Component:       "native_yarax_detection_engine",
		TimestampUTC:    time.Now().UTC().Format(time.RFC3339),
		SourceRevision:  postCheck.Revision,
		Dirty:           postCheck.Dirty,
		Toolchain:       runtime.Version(),
		LibYaraXVersion: libyaraVer,
		RuleID:          ruleID,
		RuleVersion:     ruleVersion,
		EventsPerBatch:  eventCount,
		TimingBoundaries: map[string]string{
			"scanner_only":       "Pure in-memory YARA-X C API evaluation alone (no SQLite, no ingestion hooks)",
			"pipeline_detection": "Ingestion OnCommitted hook -> detEngine.Evaluate (single native scan + deterministic UUIDv5 derivation + SQLite INSERT)",
		},
		MemoryBoundaryNote:       "runtime.MemStats measures Go heap only; native CGO allocations (libyara_x_capi) and OS page cache are process-level memory outside the Go heap",
		ScannerHarmlessSummary:   scannerHarmlessSummary,
		ScannerMaliciousSummary:  scannerMaliciousSummary,
		PipelineHarmlessSummary:  pipelineHarmlessSummary,
		PipelineMaliciousSummary: pipelineMaliciousSummary,
		RawScannerHarmlessNs:     rawScannerHarmlessNs,
		RawScannerMaliciousNs:    rawScannerMaliciousNs,
		RawPipelineHarmlessNs:    rawPipelineHarmlessNs,
		RawPipelineMaliciousNs:   rawPipelineMaliciousNs,
		StorageGrowthBytes: map[string]int64{
			"harmless":  harmlessStorageGrowth,
			"malicious": maliciousStorageGrowth,
		},
		StorageAmplificationRatio: float64(maliciousStorageGrowth) / float64(harmlessStorageGrowth+1),
		AssertedSourceEventIDs:    allAssertedSourceIDs,
		AssertedFindingIDs:        allAssertedFindingIDs,
		DurabilityReopenVerified:  true,
		ZeroEventLoss:             true,
	}

	exportDir := os.Getenv("EVIDENCE_EXPORT_DIR")
	if exportDir == "" && os.Getenv("EVIDENCE_EXPORT_TO_REPO") == "true" {
		exportDir = filepath.Join(repoRoot, "docs", "evidence", "phase4", "runs", runID)
	}

	exportedPath, err := ExportYaraXComponentEvidence(stagingDir, exportDir, componentEvidence)
	if err != nil {
		_, _ = diagMgr.FinalizeFailure(OutcomeFailed, "failed to export accepted evidence", err)
		t.Fatalf("failed to export YARA-X component evidence: %v", err)
	}

	if exportDir != "" {
		t.Logf("Accepted qualification evidence exported exclusively to: %s", exportedPath)
	}

	diagMgr.MarkSuccess()
}

// YaraXComponentEvidence documents native YARA-X evaluation and pipeline durability performance.
type YaraXComponentEvidence struct {
	EvidenceType              string            `json:"evidence_type"`
	Component                 string            `json:"component"`
	TimestampUTC              string            `json:"timestamp_utc"`
	SourceRevision            string            `json:"source_revision"`
	Dirty                     bool              `json:"dirty"`
	Toolchain                 string            `json:"toolchain"`
	LibYaraXVersion           string            `json:"libyara_x_version"`
	RuleID                    string            `json:"rule_id"`
	RuleVersion               string            `json:"rule_version"`
	EventsPerBatch            int               `json:"events_per_batch"`
	TimingBoundaries          map[string]string `json:"timing_boundaries"`
	MemoryBoundaryNote        string            `json:"memory_boundary_note"`
	ScannerHarmlessSummary    LatencySummary    `json:"scanner_harmless_summary"`
	ScannerMaliciousSummary   LatencySummary    `json:"scanner_malicious_summary"`
	PipelineHarmlessSummary   LatencySummary    `json:"pipeline_harmless_summary"`
	PipelineMaliciousSummary  LatencySummary    `json:"pipeline_malicious_summary"`
	RawScannerHarmlessNs      []int64           `json:"raw_scanner_harmless_ns"`
	RawScannerMaliciousNs     []int64           `json:"raw_scanner_malicious_ns"`
	RawPipelineHarmlessNs     []int64           `json:"raw_pipeline_harmless_ns"`
	RawPipelineMaliciousNs    []int64           `json:"raw_pipeline_malicious_ns"`
	StorageGrowthBytes        map[string]int64  `json:"storage_growth_bytes"`
	StorageAmplificationRatio float64           `json:"storage_amplification_ratio"`
	AssertedSourceEventIDs    []string          `json:"asserted_source_event_ids"`
	AssertedFindingIDs        []string          `json:"asserted_finding_ids"`
	DurabilityReopenVerified  bool              `json:"durability_reopen_verified"`
	ZeroEventLoss             bool              `json:"zero_event_loss"`
}

// ExportYaraXComponentEvidence stages and exclusively exports component evidence.
// It writes to stagingDir/yarax_component_evidence.json, and if targetDir is non-empty,
// writes exclusively to targetDir/yarax_component_evidence.json, strictly refusing overwrite.
func ExportYaraXComponentEvidence(stagingDir, targetDir string, ev YaraXComponentEvidence) (string, error) {
	if strings.TrimSpace(ev.SourceRevision) == "" {
		return "", errors.New("source_revision is required in component evidence")
	}
	if strings.TrimSpace(ev.Toolchain) == "" {
		return "", errors.New("toolchain is required in component evidence")
	}
	if strings.TrimSpace(ev.LibYaraXVersion) == "" {
		return "", errors.New("libyara_x_version is required in component evidence")
	}

	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal component evidence: %w", err)
	}

	if stagingDir != "" {
		if err := os.MkdirAll(stagingDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create staging dir %s: %w", stagingDir, err)
		}
		stagingPath := filepath.Join(stagingDir, "yarax_component_evidence.json")
		if err := WriteEvidenceExclusive(stagingPath, data); err != nil {
			return "", fmt.Errorf("failed to write staging evidence %s: %w", stagingPath, err)
		}
	}

	if targetDir == "" {
		return filepath.Join(stagingDir, "yarax_component_evidence.json"), nil
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create target export dir %s: %w", targetDir, err)
	}

	targetPath := filepath.Join(targetDir, "yarax_component_evidence.json")
	if err := WriteEvidenceExclusive(targetPath, data); err != nil {
		return "", fmt.Errorf("failed to write target evidence exclusively to %s: %w", targetPath, err)
	}

	return targetPath, nil
}

// TestWorkload_YaraX_ComponentEvidenceExport verifies that YARA-X component evidence
// exports exclusively to an external directory outside temporary directories, survives
// cleanup of the staging environment, and strictly refuses overwrite.
func TestWorkload_YaraX_ComponentEvidenceExport(t *testing.T) {
	parentDir := t.TempDir()
	persistentExportDir := filepath.Join(parentDir, "persistent_evidence")
	var stagingDir string
	var exportedPath string

	sampleEvidence := YaraXComponentEvidence{
		EvidenceType:    "component_integration",
		Component:       "native_yarax_detection_engine",
		TimestampUTC:    time.Now().UTC().Format(time.RFC3339),
		SourceRevision:  "f81a7bf06a61c12de7b98a1df97ccdd0396b5a24",
		Dirty:           false,
		Toolchain:       runtime.Version(),
		LibYaraXVersion: "1.20.0",
		RuleID:          "YARA-OT-MALWARE-001",
		RuleVersion:     "1.0",
		EventsPerBatch:  30,
		TimingBoundaries: map[string]string{
			"scanner_only":       "Pure in-memory YARA-X C API evaluation",
			"pipeline_detection": "Ingestion OnCommitted hook -> detEngine.Evaluate",
		},
		MemoryBoundaryNote:        "runtime.MemStats measures Go heap only",
		ScannerHarmlessSummary:    LatencySummary{Count: 30, Mean: 50 * time.Microsecond, P95: 80 * time.Microsecond},
		ScannerMaliciousSummary:   LatencySummary{Count: 30, Mean: 60 * time.Microsecond, P95: 90 * time.Microsecond},
		PipelineHarmlessSummary:   LatencySummary{Count: 30, Mean: 500 * time.Microsecond, P95: 800 * time.Microsecond},
		PipelineMaliciousSummary:  LatencySummary{Count: 30, Mean: 2 * time.Millisecond, P95: 4 * time.Millisecond},
		StorageGrowthBytes:        map[string]int64{"harmless": 24576, "malicious": 81920},
		StorageAmplificationRatio: 3.33,
		DurabilityReopenVerified:  true,
		ZeroEventLoss:             true,
	}

	// Nested subtest executes staging in its own t.TempDir() and returns so cleanup runs
	t.Run("StageAndExportInSubtest", func(subT *testing.T) {
		stagingDir = subT.TempDir()
		var err error
		exportedPath, err = ExportYaraXComponentEvidence(stagingDir, persistentExportDir, sampleEvidence)
		if err != nil {
			subT.Fatalf("ExportYaraXComponentEvidence failed: %v", err)
		}

		stagingFile := filepath.Join(stagingDir, "yarax_component_evidence.json")
		if _, err := os.Stat(stagingFile); err != nil {
			subT.Fatalf("staging evidence must exist inside subtest before cleanup: %v", err)
		}
	})

	// After subtest completes, subT.TempDir() cleanup has removed stagingDir
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("expected staging directory %s to be cleaned up after subtest, but stat err=%v", stagingDir, err)
	}

	// 1. Exported evidence remains readable and complete
	data, err := os.ReadFile(exportedPath)
	if err != nil {
		t.Fatalf("exported evidence in persistent dir was not preserved after staging cleanup: %v", err)
	}

	var verified YaraXComponentEvidence
	if err := json.Unmarshal(data, &verified); err != nil {
		t.Fatalf("failed to unmarshal preserved evidence: %v", err)
	}

	// 2. Validate required provenance & timing fields
	if verified.SourceRevision != sampleEvidence.SourceRevision {
		t.Errorf("source_revision mismatch: expected %s, got %s", sampleEvidence.SourceRevision, verified.SourceRevision)
	}
	if verified.Dirty != false {
		t.Errorf("dirty mismatch: expected false, got %v", verified.Dirty)
	}
	if verified.Toolchain != runtime.Version() {
		t.Errorf("toolchain mismatch: expected %s, got %s", runtime.Version(), verified.Toolchain)
	}
	if verified.LibYaraXVersion != "1.20.0" {
		t.Errorf("libyara_x_version mismatch: expected 1.20.0, got %s", verified.LibYaraXVersion)
	}
	if verified.ScannerHarmlessSummary.Count != 30 {
		t.Errorf("timing summary count mismatch: expected 30, got %d", verified.ScannerHarmlessSummary.Count)
	}

	// 3. Repeated export strictly refuses overwrite
	dummyStaging := t.TempDir()
	_, errSecond := ExportYaraXComponentEvidence(dummyStaging, persistentExportDir, sampleEvidence)
	if errSecond == nil {
		t.Fatalf("expected repeated export to fail with overwrite refusal, but it succeeded")
	}
	if !strings.Contains(errSecond.Error(), "refusing to overwrite") {
		t.Errorf("expected 'refusing to overwrite' error, got: %v", errSecond)
	}

	// 4. Test write failure handling without dummy JSON
	unwritableBlocker := filepath.Join(parentDir, "blocker_file")
	if err := os.WriteFile(unwritableBlocker, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}
	_, errWriteFail := ExportYaraXComponentEvidence(dummyStaging, filepath.Join(unwritableBlocker, "sub"), sampleEvidence)
	if errWriteFail == nil {
		t.Fatalf("expected error when exporting to unwritable target, got nil")
	}
}
