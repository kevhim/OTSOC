package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/resourcereport"
	"redcyberfox/pkg/events"
)

// mockIngestReceiver represents a local test HTTP receiver returning HTTP 202 Accepted.
// Contract Note: This mock verifies upstream HTTP forwarder request transmission and local event removal
// upon acknowledgement; it does NOT establish central durable ingestion or database persistence.
type mockIngestReceiver struct {
	srv            *httptest.Server
	mu             sync.Mutex
	receivedCount  atomic.Uint64
	receivedEvents []*events.CanonicalEvent
}

func newMockIngestReceiver() *mockIngestReceiver {
	m := &mockIngestReceiver{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ingest" {
			body, _ := io.ReadAll(r.Body)
			m.receivedCount.Add(1)
			if ev, err := events.Deserialize(body); err == nil {
				m.mu.Lock()
				m.receivedEvents = append(m.receivedEvents, ev)
				m.mu.Unlock()
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	return m
}

func (m *mockIngestReceiver) Close() {
	if m.srv != nil {
		m.srv.Close()
	}
}

func (m *mockIngestReceiver) ReceivedEvents() []*events.CanonicalEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]*events.CanonicalEvent, len(m.receivedEvents))
	copy(res, m.receivedEvents)
	return res
}

// buildFreshEndpointWithProvenance compiles a fresh endpoint binary directly into an isolated
// temporary test directory and validates its embedded build metadata against git provenance.
// All external commands execute with explicit timeouts and capture diagnostic output.
func buildFreshEndpointWithProvenance(t *testing.T, repoRoot string, tracker *runTracker, preCheck SourceBoundaryCheck, diagMgr *RunDiagnosticManager) BinaryProvenance {
	t.Helper()
	binDir := t.TempDir()
	endpointExe := filepath.Join(binDir, "endpoint.exe")

	// 1. Build fresh binary directly into isolated temp directory with explicit 90s timeout
	ctxBuild, cancelBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBuild()
	recBuild, err := RunBoundedCommand(ctxBuild, repoRoot, 90*time.Second, "go", "build", "-v", "-o", endpointExe, "./agent/cmd/endpoint")
	if tracker != nil {
		tracker.Record(recBuild)
	}
	if err != nil {
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "failed to compile fresh endpoint binary", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("failed to build fresh endpoint.exe in %s: %v\nOutput: %s", binDir, err, recBuild.CombinedOutput)
	}

	// 2. Inspect binary metadata via 'go version -m <exe>' with explicit 30s timeout
	ctxVersion, cancelVersion := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelVersion()
	recVersion, err := RunBoundedCommand(ctxVersion, repoRoot, 30*time.Second, "go", "version", "-m", endpointExe)
	if tracker != nil {
		tracker.Record(recVersion)
	}
	if err != nil {
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "failed to read build info from fresh binary", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("failed to read build info from %s: %v\nOutput: %s", endpointExe, err, recVersion.CombinedOutput)
	}

	// 3. Validate binary build metadata against recorded provenance
	actualRevision, actualDirty, actualToolchain, err := ParseBinaryProvenanceOutput(recVersion.CombinedOutput)
	if err != nil {
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "binary provenance metadata parsing failed", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("binary provenance parsing failed for %s: %v", endpointExe, err)
	}

	if !strings.HasPrefix(preCheck.Revision, actualRevision) && !strings.HasPrefix(actualRevision, preCheck.Revision) {
		errMismatch := fmt.Errorf("expected revision %s, binary has %s", preCheck.Revision, actualRevision)
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeInvalid, "binary revision mismatch against source checkout", errMismatch); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("binary provenance mismatch: %v", errMismatch)
	}
	if actualDirty != preCheck.Dirty {
		errMismatch := fmt.Errorf("expected dirty=%v, binary has dirty=%v", preCheck.Dirty, actualDirty)
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeInvalid, "binary dirty-state mismatch against source checkout", errMismatch); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("binary provenance mismatch: %v", errMismatch)
	}

	// 4. Compute binary SHA-256
	sha, err := ComputeFileSHA256(endpointExe)
	if err != nil {
		if diagMgr != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "failed to compute binary SHA-256", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
		}
		t.Fatalf("failed to compute binary SHA-256 for %s: %v", endpointExe, err)
	}

	prov := BinaryProvenance{
		ExePath:        endpointExe,
		SHA256:         sha,
		SourceRevision: actualRevision,
		Dirty:          actualDirty,
		Toolchain:      actualToolchain,
		OS:             runtime.GOOS,
		Architecture:   runtime.GOARCH,
		LogicalCPUs:    runtime.NumCPU(),
	}

	t.Logf("Fresh binary provenance verified: %s (rev: %s, dirty: %v, toolchain: %s, sha256: %s)",
		endpointExe, actualRevision, actualDirty, actualToolchain, sha)

	return prov
}

type runTracker struct {
	mu       sync.Mutex
	commands []CommandExecutionRecord
}

func (r *runTracker) Record(rec *CommandExecutionRecord) {
	if r == nil || rec == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, *rec)
}

func (r *runTracker) Commands() []CommandExecutionRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]CommandExecutionRecord, len(r.commands))
	copy(res, r.commands)
	return res
}

func validateReportManifest(t *testing.T, rep *resourcereport.Report, prov BinaryProvenance, profile string) {
	t.Helper()
	if rep.Manifest.Profile != profile {
		t.Errorf("manifest profile mismatch: expected %s, got %s", profile, rep.Manifest.Profile)
	}
	if rep.Manifest.OS != prov.OS {
		t.Errorf("manifest OS mismatch: expected %s, got %s", prov.OS, rep.Manifest.OS)
	}
	if rep.Manifest.Architecture != prov.Architecture {
		t.Errorf("manifest Architecture mismatch: expected %s, got %s", prov.Architecture, rep.Manifest.Architecture)
	}
	if rep.Manifest.LogicalCPUs <= 0 {
		t.Errorf("manifest LogicalCPUs must be > 0, got %d", rep.Manifest.LogicalCPUs)
	}
	if !strings.HasPrefix(prov.SourceRevision, rep.Manifest.SourceRevision) && !strings.HasPrefix(rep.Manifest.SourceRevision, prov.SourceRevision) {
		t.Errorf("manifest source_revision mismatch: expected %s, got %s", prov.SourceRevision, rep.Manifest.SourceRevision)
	}
	if rep.Manifest.Dirty != prov.Dirty {
		t.Errorf("manifest dirty mismatch: expected %v, got %v", prov.Dirty, rep.Manifest.Dirty)
	}
	if rep.Manifest.Toolchain != prov.Toolchain {
		t.Errorf("manifest toolchain mismatch: expected %s, got %s", prov.Toolchain, rep.Manifest.Toolchain)
	}
}

func TestWorkload_WindowsMeasurementReports(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	requireClean := os.Getenv("EVIDENCE_EXPORT_TO_REPO") == "true" || os.Getenv("EVIDENCE_REQUIRE_CLEAN") == "true" || os.Getenv("EVIDENCE_EXPORT_DIR") != ""
	tracker := &runTracker{}

	ctxCheck := context.Background()

	// Unique run identifier and run directory
	runID := fmt.Sprintf("run_%s", time.Now().UTC().Format("20060102_150405"))

	// Stage all run files into an isolated directory first
	stagingRunDir := filepath.Join(t.TempDir(), runID)
	if err := os.MkdirAll(stagingRunDir, 0755); err != nil {
		t.Fatalf("failed to create staging run directory: %v", err)
	}

	persistentDiagDir := os.Getenv("DIAGNOSTIC_DIR")
	if persistentDiagDir == "" {
		persistentDiagDir = filepath.Join(repoRoot, "docs", "evidence", "phase4", "diagnostics")
	}
	diagMgr := NewRunDiagnosticManager(runID, persistentDiagDir, stagingRunDir, tracker)
	diagMgr.RegisterTestingFinalizer(t)

	// Checkpoint 1: Pre-compilation source boundary check
	check1, err := CheckSourceBoundary(ctxCheck, repoRoot, "pre-compilation")
	if err != nil {
		diagMgr.RecordBoundaryCheck(check1)
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "pre-compilation boundary check error", err); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("pre-compilation source boundary check failed: %v", err)
	}
	diagMgr.RecordBoundaryCheck(check1)
	if requireClean && check1.Dirty {
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeInvalid, "qualification evidence requires a clean committed source checkout, but working tree is dirty", fmt.Errorf("%s", check1.StatusNotes)); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("qualification evidence requires a clean committed source checkout, but working tree is dirty:\n%s", check1.StatusNotes)
	}

	// Always compile a fresh endpoint into an isolated test temporary directory
	provenance := buildFreshEndpointWithProvenance(t, repoRoot, tracker, check1, diagMgr)
	endpointExe := provenance.ExePath

	// Checkpoint 2: Post-compilation source boundary check
	check2, err := CheckSourceBoundary(ctxCheck, repoRoot, "post-compilation")
	if err != nil {
		diagMgr.RecordBoundaryCheck(check2)
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "post-compilation boundary check error", err); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("post-compilation source boundary check failed: %v", err)
	}
	diagMgr.RecordBoundaryCheck(check2)
	if check2.Revision != check1.Revision || (requireClean && check2.Dirty) {
		errInst := fmt.Errorf("rev1=%s, rev2=%s, dirty1=%v, dirty2=%v: %s",
			check1.Revision, check2.Revision, check1.Dirty, check2.Dirty, check2.StatusNotes)
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeInvalid, "source instability detected post-compilation", errInst); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("source instability detected post-compilation: %v", errInst)
	}

	var generatedReports []string
	var recipes []WorkloadRecipe

	// -------------------------------------------------------------
	// 1. Idle Workload Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_Idle", func(t *testing.T) {
		scenarioDir := t.TempDir()
		watchedDir := filepath.Join(scenarioDir, "watched")
		if err := os.MkdirAll(watchedDir, 0755); err != nil {
			t.Fatalf("failed to create watched dir: %v", err)
		}

		mockSrv := newMockIngestReceiver()
		defer mockSrv.Close()

		cfg := config.Config{
			APIAddr:      mockSrv.srv.URL,
			TenantID:     "tenant-smoke-idle",
			SiteID:       "site-smoke-idle",
			MonitorPaths: []string{watchedDir},
		}
		cfgData, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(scenarioDir, "agent.json")
		if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
			t.Fatalf("failed to write agent.json: %v", err)
		}

		idPath := filepath.Join(scenarioDir, "identity.json")
		reportPath := filepath.Join(scenarioDir, "report_idle.json")

		duration := 3 * time.Second
		interval := 1 * time.Second
		timeout := duration + 10*time.Second

		recipe := WorkloadRecipe{
			Name:             "Idle",
			Profile:          "windows-smoke-idle",
			Duration:         duration,
			SamplingInterval: interval,
			WorkloadAction:   "Harmless background collection; no filesystem changes",
		}
		recipes = append(recipes, recipe)

		args := []string{
			"-config=" + cfgPath,
			"-identity=" + idPath,
			"-measure-report=" + reportPath,
			"-measure-profile=windows-smoke-idle",
			"-measure-duration=" + duration.String(),
			"-measure-interval=" + interval.String(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		rec, err := RunBoundedCommand(ctx, scenarioDir, timeout, endpointExe, args...)
		tracker.Record(rec)
		if err != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe failed in idle scenario", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
			t.Fatalf("endpoint.exe failed in idle scenario: %v", err)
		}

		reportData, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("failed to read idle report: %v", err)
		}

		var rep resourcereport.Report
		if err := json.Unmarshal(reportData, &rep); err != nil {
			t.Fatalf("failed to parse idle report JSON: %v", err)
		}

		validateReportManifest(t, &rep, provenance, "windows-smoke-idle")

		if rep.Summary.ShutdownOutcome != "MEASUREMENT_COMPLETE" {
			t.Errorf("expected MEASUREMENT_COMPLETE, got %s", rep.Summary.ShutdownOutcome)
		}
		if rep.Summary.TotalSamples < 2 {
			t.Errorf("expected at least 2 samples, got %d", rep.Summary.TotalSamples)
		}
		if rep.Summary.EventAccounting == nil || !rep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting reconciliation")
		}

		// Save exclusively to run staging directory
		destFile := "windows_idle_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFile), reportData); err != nil {
			t.Fatalf("failed to write exclusive idle report: %v", err)
		}
		generatedReports = append(generatedReports, destFile)
	})

	// -------------------------------------------------------------
	// 2. Sustained Workload Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_Sustained", func(t *testing.T) {
		scenarioDir := t.TempDir()
		watchedDir := filepath.Join(scenarioDir, "watched")
		if err := os.MkdirAll(watchedDir, 0755); err != nil {
			t.Fatalf("failed to create watched dir: %v", err)
		}

		mockSrv := newMockIngestReceiver()
		defer mockSrv.Close()

		cfg := config.Config{
			APIAddr:      mockSrv.srv.URL,
			TenantID:     "tenant-smoke-sustained",
			SiteID:       "site-smoke-sustained",
			MonitorPaths: []string{watchedDir},
		}
		cfgData, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(scenarioDir, "agent.json")
		if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
			t.Fatalf("failed to write agent.json: %v", err)
		}

		idPath := filepath.Join(scenarioDir, "identity.json")
		reportPath := filepath.Join(scenarioDir, "report_sustained.json")

		duration := 4 * time.Second
		interval := 1 * time.Second
		timeout := duration + 10*time.Second

		recipe := WorkloadRecipe{
			Name:             "Sustained",
			Profile:          "windows-smoke-sustained",
			Duration:         duration,
			SamplingInterval: interval,
			WorkloadAction:   "1 file written every 200ms into watched directory (12 files)",
		}
		recipes = append(recipes, recipe)

		args := []string{
			"-config=" + cfgPath,
			"-identity=" + idPath,
			"-measure-report=" + reportPath,
			"-measure-profile=windows-smoke-sustained",
			"-measure-duration=" + duration.String(),
			"-measure-interval=" + interval.String(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		proc, err := StartManagedSubprocess(ctx, scenarioDir, timeout, DefaultMaxOutputBytes, endpointExe, args...)
		if err != nil {
			rec, _ := proc.Wait()
			tracker.Record(rec)
			t.Fatalf("failed to start endpoint.exe in sustained scenario: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		stopSustained := make(chan struct{})
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			idx := 1
			for {
				select {
				case <-stopSustained:
					return
				case <-ticker.C:
					filePath := filepath.Join(watchedDir, fmt.Sprintf("sustained_%d.txt", idx))
					_ = os.WriteFile(filePath, []byte(fmt.Sprintf("sustained content %d", idx)), 0644)
					idx++
					if idx > 12 {
						return
					}
				}
			}
		}()

		rec, err := proc.Wait()
		close(stopSustained)
		tracker.Record(rec)
		if err != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe failed in sustained scenario", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
			t.Fatalf("endpoint.exe failed in sustained scenario: %v", err)
		}

		reportData, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("failed to read sustained report: %v", err)
		}

		var rep resourcereport.Report
		if err := json.Unmarshal(reportData, &rep); err != nil {
			t.Fatalf("failed to parse sustained report JSON: %v", err)
		}

		validateReportManifest(t, &rep, provenance, "windows-smoke-sustained")

		if rep.Summary.ShutdownOutcome != "MEASUREMENT_COMPLETE" {
			t.Errorf("expected MEASUREMENT_COMPLETE, got %s", rep.Summary.ShutdownOutcome)
		}
		if rep.Summary.EventAccounting == nil || !rep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting reconciliation")
		}
		if rep.Summary.EventAccounting.IngestionOutcome.CommittedTotal < 8 {
			t.Errorf("expected at least 8 committed events in sustained scenario, got %d",
				rep.Summary.EventAccounting.IngestionOutcome.CommittedTotal)
		}

		destFile := "windows_sustained_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFile), reportData); err != nil {
			t.Fatalf("failed to write exclusive sustained report: %v", err)
		}
		generatedReports = append(generatedReports, destFile)
	})

	// -------------------------------------------------------------
	// 3. Burst Workload Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_Burst", func(t *testing.T) {
		scenarioDir := t.TempDir()
		watchedDir := filepath.Join(scenarioDir, "watched")
		if err := os.MkdirAll(watchedDir, 0755); err != nil {
			t.Fatalf("failed to create watched dir: %v", err)
		}

		mockSrv := newMockIngestReceiver()
		defer mockSrv.Close()

		cfg := config.Config{
			APIAddr:      mockSrv.srv.URL,
			TenantID:     "tenant-smoke-burst",
			SiteID:       "site-smoke-burst",
			MonitorPaths: []string{watchedDir},
		}
		cfgData, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(scenarioDir, "agent.json")
		if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
			t.Fatalf("failed to write agent.json: %v", err)
		}

		idPath := filepath.Join(scenarioDir, "identity.json")
		reportPath := filepath.Join(scenarioDir, "report_burst.json")

		duration := 4 * time.Second
		interval := 1 * time.Second
		timeout := duration + 10*time.Second

		recipe := WorkloadRecipe{
			Name:             "Burst",
			Profile:          "windows-smoke-burst",
			Duration:         duration,
			SamplingInterval: interval,
			WorkloadAction:   "Rapid concurrent write of 35 files into watched directory",
		}
		recipes = append(recipes, recipe)

		args := []string{
			"-config=" + cfgPath,
			"-identity=" + idPath,
			"-measure-report=" + reportPath,
			"-measure-profile=windows-smoke-burst",
			"-measure-duration=" + duration.String(),
			"-measure-interval=" + interval.String(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		proc, err := StartManagedSubprocess(ctx, scenarioDir, timeout, DefaultMaxOutputBytes, endpointExe, args...)
		if err != nil {
			rec, _ := proc.Wait()
			tracker.Record(rec)
			t.Fatalf("failed to start endpoint.exe in burst scenario: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		for i := 1; i <= 35; i++ {
			filePath := filepath.Join(watchedDir, fmt.Sprintf("burst_%d.txt", i))
			_ = os.WriteFile(filePath, []byte(fmt.Sprintf("burst payload %d", i)), 0644)
		}

		rec, err := proc.Wait()
		tracker.Record(rec)
		if err != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe failed in burst scenario", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
			t.Fatalf("endpoint.exe failed in burst scenario: %v", err)
		}

		reportData, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("failed to read burst report: %v", err)
		}

		var rep resourcereport.Report
		if err := json.Unmarshal(reportData, &rep); err != nil {
			t.Fatalf("failed to parse burst report JSON: %v", err)
		}

		validateReportManifest(t, &rep, provenance, "windows-smoke-burst")

		if rep.Summary.ShutdownOutcome != "MEASUREMENT_COMPLETE" {
			t.Errorf("expected MEASUREMENT_COMPLETE, got %s", rep.Summary.ShutdownOutcome)
		}
		if rep.Summary.EventAccounting == nil || !rep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting reconciliation")
		}
		if rep.Summary.EventAccounting.IngestionOutcome.CommittedTotal < 25 {
			t.Errorf("expected at least 25 committed burst events, got %d",
				rep.Summary.EventAccounting.IngestionOutcome.CommittedTotal)
		}

		destFile := "windows_burst_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFile), reportData); err != nil {
			t.Fatalf("failed to write exclusive burst report: %v", err)
		}
		generatedReports = append(generatedReports, destFile)
	})

	// -------------------------------------------------------------
	// 4. Offline & Real Retry Recovery Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_OfflineAndRealRetryRecovery", func(t *testing.T) {
		scenarioDir := t.TempDir()
		watchedDir := filepath.Join(scenarioDir, "watched")
		if err := os.MkdirAll(watchedDir, 0755); err != nil {
			t.Fatalf("failed to create watched dir: %v", err)
		}

		offlineCfg := config.Config{
			APIAddr:      "http://127.0.0.1:54321", // Unreachable port
			TenantID:     "tenant-smoke-offline",
			SiteID:       "site-smoke-offline",
			MonitorPaths: []string{watchedDir},
		}
		cfgData, _ := json.Marshal(offlineCfg)
		cfgPath := filepath.Join(scenarioDir, "agent.json")
		if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
			t.Fatalf("failed to write agent.json: %v", err)
		}

		idPath := filepath.Join(scenarioDir, "identity.json")
		offlineReportPath := filepath.Join(scenarioDir, "report_offline.json")

		duration := 3 * time.Second
		interval := 1 * time.Second
		timeoutOffline := duration + 10*time.Second

		recipeOffline := WorkloadRecipe{
			Name:             "Offline",
			Profile:          "windows-smoke-offline",
			Duration:         duration,
			SamplingInterval: interval,
			WorkloadAction:   "Upstream offline; 10 files written; events buffered in SQLite with backoff",
		}
		recipes = append(recipes, recipeOffline)

		argsOffline := []string{
			"-config=" + cfgPath,
			"-identity=" + idPath,
			"-measure-report=" + offlineReportPath,
			"-measure-profile=windows-smoke-offline",
			"-measure-duration=" + duration.String(),
			"-measure-interval=" + interval.String(),
		}

		ctxOffline, cancelOffline := context.WithTimeout(context.Background(), timeoutOffline)
		defer cancelOffline()

		procOffline, err := StartManagedSubprocess(ctxOffline, scenarioDir, timeoutOffline, DefaultMaxOutputBytes, endpointExe, argsOffline...)
		if err != nil {
			rec, _ := procOffline.Wait()
			tracker.Record(rec)
			t.Fatalf("failed to start endpoint.exe for offline phase: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		for i := 1; i <= 10; i++ {
			filePath := filepath.Join(watchedDir, fmt.Sprintf("offline_%d.txt", i))
			_ = os.WriteFile(filePath, []byte(fmt.Sprintf("offline content %d", i)), 0644)
		}

		recOffline, err := procOffline.Wait()
		tracker.Record(recOffline)
		if err != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe failed in offline phase", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
			t.Fatalf("endpoint.exe failed in offline phase: %v", err)
		}

		offlineReportData, err := os.ReadFile(offlineReportPath)
		if err != nil {
			t.Fatalf("failed to read offline report: %v", err)
		}

		var offlineRep resourcereport.Report
		if err := json.Unmarshal(offlineReportData, &offlineRep); err != nil {
			t.Fatalf("failed to parse offline report JSON: %v", err)
		}

		validateReportManifest(t, &offlineRep, provenance, "windows-smoke-offline")

		if offlineRep.Summary.EventAccounting == nil || !offlineRep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting in offline phase")
		}
		if offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents == nil ||
			*offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents < 10 {
			t.Errorf("expected at least 10 pending events retained in SQLite, got %v",
				offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents)
		}

		destFileOffline := "windows_offline_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFileOffline), offlineReportData); err != nil {
			t.Fatalf("failed to write exclusive offline report: %v", err)
		}
		generatedReports = append(generatedReports, destFileOffline)

		// -------------------------------------------------------------
		// Inspect & Record Offline Event IDs, Sequence Numbers & Deadlines
		// NOTE: next_attempt_at is 100% PRESERVED across restart. NO SQL clears or modifies it!
		// -------------------------------------------------------------
		type offlineRecord struct {
			EventID       string
			SeqNo         int64
			NextAttemptAt string
		}
		var recordedOffline []offlineRecord
		var maxNextAttemptStr string

		rawDB, err := sql.Open("sqlite", filepath.Join(scenarioDir, "agent.db"))
		if err != nil {
			t.Fatalf("failed to open agent.db: %v", err)
		}
		rows, err := rawDB.Query("SELECT event_id, seq_no, COALESCE(next_attempt_at, '') FROM events WHERE state = 'PENDING' ORDER BY seq_no ASC")
		if err != nil {
			rawDB.Close()
			t.Fatalf("failed to query pending events from agent.db: %v", err)
		}
		for rows.Next() {
			var r offlineRecord
			if err := rows.Scan(&r.EventID, &r.SeqNo, &r.NextAttemptAt); err != nil {
				rows.Close()
				rawDB.Close()
				t.Fatalf("failed to scan pending event: %v", err)
			}
			recordedOffline = append(recordedOffline, r)
			if r.NextAttemptAt > maxNextAttemptStr {
				maxNextAttemptStr = r.NextAttemptAt
			}
		}
		rows.Close()
		_ = rawDB.Close()

		if len(recordedOffline) == 0 {
			t.Fatalf("expected offline pending events recorded in sqlite, got 0")
		}
		t.Logf("Preserved %d offline pending events in SQLite (furthest next_attempt_at: %s)",
			len(recordedOffline), maxNextAttemptStr)

		// -------------------------------------------------------------
		// Part B: Real Retry Recovery Phase
		// Upstream receiver online. Runs long enough for retry deadlines to elapse naturally.
		// -------------------------------------------------------------
		mockReceiver := newMockIngestReceiver()
		defer mockReceiver.Close()

		reconnectCfg := config.Config{
			APIAddr:      mockReceiver.srv.URL,
			TenantID:     "tenant-smoke-offline",
			SiteID:       "site-smoke-offline",
			MonitorPaths: []string{watchedDir},
		}
		recCfgData, _ := json.Marshal(reconnectCfg)
		if err := os.WriteFile(cfgPath, recCfgData, 0644); err != nil {
			t.Fatalf("failed to update agent.json for reconnect phase: %v", err)
		}

		reconnectReportPath := filepath.Join(scenarioDir, "report_reconnect.json")

		recDuration := 22 * time.Second
		recInterval := 2 * time.Second
		timeoutReconnect := recDuration + 15*time.Second

		recipeReconnect := WorkloadRecipe{
			Name:             "ReconnectAndRealRetryRecovery",
			Profile:          "windows-smoke-reconnect",
			Duration:         recDuration,
			SamplingInterval: recInterval,
			WorkloadAction:   "Mock receiver online; 22s run allows natural 10s-19s retry backoff expiration",
		}
		recipes = append(recipes, recipeReconnect)

		argsReconnect := []string{
			"-config=" + cfgPath,
			"-identity=" + idPath,
			"-measure-report=" + reconnectReportPath,
			"-measure-profile=windows-smoke-reconnect",
			"-measure-duration=" + recDuration.String(),
			"-measure-interval=" + recInterval.String(),
		}

		ctxReconnect, cancelReconnect := context.WithTimeout(context.Background(), timeoutReconnect)
		defer cancelReconnect()

		recReconnect, err := RunBoundedCommand(ctxReconnect, scenarioDir, timeoutReconnect, endpointExe, argsReconnect...)
		tracker.Record(recReconnect)
		if err != nil {
			if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe failed in real retry recovery phase", err); diagErr != nil {
				t.Errorf("diagnostic retention failed: %v", diagErr)
			}
			t.Fatalf("endpoint.exe failed in real retry recovery phase: %v", err)
		}

		reconnectReportData, err := os.ReadFile(reconnectReportPath)
		if err != nil {
			t.Fatalf("failed to read reconnect report: %v", err)
		}

		var recRep resourcereport.Report
		if err := json.Unmarshal(reconnectReportData, &recRep); err != nil {
			t.Fatalf("failed to parse reconnect report JSON: %v", err)
		}

		validateReportManifest(t, &recRep, provenance, "windows-smoke-reconnect")

		if recRep.Summary.EventAccounting == nil {
			t.Fatalf("expected event accounting in reconnect report")
		}
		expectedBacklogCount := uint64(len(recordedOffline))
		if recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents == nil ||
			*recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents != expectedBacklogCount {
			t.Errorf("expected %d initial backlog pending events, got %v",
				expectedBacklogCount, recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents)
		}

		// Verify all recorded offline events were transmitted to mock receiver
		receivedList := mockReceiver.ReceivedEvents()
		receivedMap := make(map[string]int64)
		for _, ev := range receivedList {
			receivedMap[ev.EventID] = ev.SeqNo
		}

		for _, rec := range recordedOffline {
			gotSeq, exists := receivedMap[rec.EventID]
			if !exists {
				t.Errorf("offline event %s (seq: %d) was not received by mock receiver on retry", rec.EventID, rec.SeqNo)
			} else if gotSeq != rec.SeqNo {
				t.Errorf("event %s seq mismatch: expected %d, got %d", rec.EventID, rec.SeqNo, gotSeq)
			}
		}

		// Verify all pending events were locally removed from SQLite
		dbCheck, err := sql.Open("sqlite", filepath.Join(scenarioDir, "agent.db"))
		if err != nil {
			t.Fatalf("failed to open agent.db for post-reconnect check: %v", err)
		}
		var remainingPending int
		if err := dbCheck.QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&remainingPending); err != nil {
			dbCheck.Close()
			t.Fatalf("failed to query remaining pending count: %v", err)
		}
		_ = dbCheck.Close()

		if remainingPending != 0 {
			t.Errorf("expected 0 remaining pending events in SQLite after retry recovery, got %d", remainingPending)
		}

		if recRep.Summary.EventAccounting.Storage.DurablePendingEvents == nil ||
			*recRep.Summary.EventAccounting.Storage.DurablePendingEvents != 0 {
			t.Errorf("expected report durable_pending_events=0, got %v",
				*recRep.Summary.EventAccounting.Storage.DurablePendingEvents)
		}

		destFileReconnect := "windows_reconnect_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFileReconnect), reconnectReportData); err != nil {
			t.Fatalf("failed to write exclusive reconnect report: %v", err)
		}
		generatedReports = append(generatedReports, destFileReconnect)

		destFileRetry := "windows_retry_recovery_report.json"
		if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, destFileRetry), reconnectReportData); err != nil {
			t.Fatalf("failed to write exclusive retry recovery report: %v", err)
		}
		generatedReports = append(generatedReports, destFileRetry)
	})

	// Abort if any scenario subtest failed
	if t.Failed() {
		t.Fatalf("aborting qualification evidence generation: one or more workload scenarios failed")
	}

	// -------------------------------------------------------------
	// Checkpoint 3: Post-Workload Source Boundary Stability Check
	// -------------------------------------------------------------
	check3, err := CheckSourceBoundary(ctxCheck, repoRoot, "post-workload")
	if err != nil {
		diagMgr.RecordBoundaryCheck(check3)
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeFailed, "post-workload boundary check error", err); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("post-workload source boundary check failed: %v", err)
	}
	diagMgr.RecordBoundaryCheck(check3)
	if check3.Revision != check1.Revision || (requireClean && check3.Dirty) {
		errInst := fmt.Errorf("rev1=%s, rev3=%s, dirty1=%v, dirty3=%v: %s",
			check1.Revision, check3.Revision, check1.Dirty, check3.Dirty, check3.StatusNotes)
		if _, diagErr := diagMgr.FinalizeFailure(OutcomeInvalid, "source instability detected post-workload", errInst); diagErr != nil {
			t.Errorf("diagnostic retention failed: %v", diagErr)
		}
		t.Fatalf("source instability detected post-workload: %v", errInst)
	}

	// -------------------------------------------------------------
	// Write Provenance Manifest and Command Execution Logs
	// -------------------------------------------------------------
	provManifest := RunProvenanceManifest{
		RunID:                runID,
		TimestampUTC:         time.Now().UTC().Format(time.RFC3339),
		SourceRevision:       provenance.SourceRevision,
		Dirty:                provenance.Dirty,
		SourceBoundaryChecks: []SourceBoundaryCheck{check1, check2, check3},
		BinaryProvenance:     provenance,
		HostOS:               provenance.OS,
		HostArch:             provenance.Architecture,
		LogicalCPUs:          provenance.LogicalCPUs,
		GoToolchain:          provenance.Toolchain,
		ExecutedCommands:     tracker.Commands(),
		WorkloadRecipes:      recipes,
		GeneratedReports:     generatedReports,
	}

	provJSON, err := json.MarshalIndent(provManifest, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal run provenance manifest: %v", err)
	}
	if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, "provenance.json"), provJSON); err != nil {
		t.Fatalf("failed to write provenance.json: %v", err)
	}

	// Format commands.log
	commandsLog := FormatCommandsLog(runID, provManifest.TimestampUTC, provenance.SourceRevision, provenance.Dirty, provenance.Toolchain, provenance.SHA256, provManifest.SourceBoundaryChecks, tracker.Commands())
	if err := WriteEvidenceExclusive(filepath.Join(stagingRunDir, "commands.log"), []byte(commandsLog)); err != nil {
		t.Fatalf("failed to write commands.log: %v", err)
	}

	t.Logf("All evidence staged exclusively in: %s (%d reports, provenance.json, commands.log)",
		stagingRunDir, len(generatedReports))

	// Ensure failed subtests or boundary checks cannot produce accepted evidence export
	if t.Failed() {
		t.Fatalf("aborting qualification evidence export: test has active failures")
	}

	// If requested or if running in evidence-export mode, export run exclusively to target directory
	exportTarget := os.Getenv("EVIDENCE_EXPORT_DIR")
	if exportTarget == "" && os.Getenv("EVIDENCE_EXPORT_TO_REPO") == "true" {
		exportTarget = filepath.Join(repoRoot, "docs", "evidence", "phase4", "runs", runID)
	}
	if exportTarget != "" {
		if err := ExportEvidenceRun(stagingRunDir, exportTarget); err != nil {
			t.Fatalf("failed to export evidence run exclusively to %s: %v", exportTarget, err)
		}
		t.Logf("Exported evidence run exclusively to: %s", exportTarget)
	}
}
