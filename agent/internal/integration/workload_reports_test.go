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
	"os/exec"
	"path/filepath"
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

type BinaryProvenance struct {
	ExePath        string
	SourceRevision string
	Dirty          bool
	Toolchain      string
}

// buildFreshEndpointWithProvenance compiles a fresh endpoint binary directly into an isolated
// temporary test directory and validates its embedded build metadata against git provenance.
// Never reuses repository-root binaries.
func buildFreshEndpointWithProvenance(t *testing.T, repoRoot string) BinaryProvenance {
	t.Helper()
	binDir := t.TempDir()
	endpointExe := filepath.Join(binDir, "endpoint.exe")

	// 1. Discover expected git revision and dirty status
	gitRevCmd := exec.Command("git", "rev-parse", "HEAD")
	gitRevCmd.Dir = repoRoot
	revOut, err := gitRevCmd.Output()
	if err != nil {
		t.Fatalf("failed to obtain git revision from %s: %v", repoRoot, err)
	}
	expectedRevision := strings.TrimSpace(string(revOut))

	gitStatusCmd := exec.Command("git", "status", "--porcelain")
	gitStatusCmd.Dir = repoRoot
	statusOut, err := gitStatusCmd.Output()
	if err != nil {
		t.Fatalf("failed to obtain git status from %s: %v", repoRoot, err)
	}
	expectedDirty := len(strings.TrimSpace(string(statusOut))) > 0

	// 2. Build fresh binary directly into isolated temp directory
	buildCmd := exec.Command("go", "build", "-v", "-o", endpointExe, "./agent/cmd/endpoint")
	buildCmd.Dir = repoRoot
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build fresh endpoint.exe in %s: %v\nOutput: %s", binDir, err, string(buildOut))
	}

	// 3. Inspect binary metadata via 'go version -m <exe>'
	versionCmd := exec.Command("go", "version", "-m", endpointExe)
	versionCmd.Dir = repoRoot
	versionOut, err := versionCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to read build info from %s: %v\nOutput: %s", endpointExe, err, string(versionOut))
	}

	// 4. Validate binary build metadata against recorded provenance
	var actualRevision string
	var actualDirty bool
	var actualToolchain string
	lines := strings.Split(string(versionOut), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "endpoint.exe: ") {
			actualToolchain = strings.TrimPrefix(trimmed, "endpoint.exe: ")
		} else if strings.HasPrefix(trimmed, "build\tvcs.revision=") {
			actualRevision = strings.TrimPrefix(trimmed, "build\tvcs.revision=")
		} else if strings.HasPrefix(trimmed, "build\tvcs.modified=") {
			actualDirty = (strings.TrimPrefix(trimmed, "build\tvcs.modified=") == "true")
		}
	}

	if actualRevision == "" {
		t.Fatalf("binary provenance unavailable: build metadata missing vcs.revision in %s\n%s", endpointExe, string(versionOut))
	}
	if !strings.HasPrefix(expectedRevision, actualRevision) && !strings.HasPrefix(actualRevision, expectedRevision) {
		t.Fatalf("binary provenance mismatch: expected revision %s, binary has %s", expectedRevision, actualRevision)
	}
	if actualDirty != expectedDirty {
		t.Fatalf("binary provenance mismatch: expected dirty=%v, binary has dirty=%v", expectedDirty, actualDirty)
	}

	t.Logf("Fresh binary provenance verified: %s (rev: %s, dirty: %v, toolchain: %s)",
		endpointExe, actualRevision, actualDirty, actualToolchain)

	return BinaryProvenance{
		ExePath:        endpointExe,
		SourceRevision: actualRevision,
		Dirty:          actualDirty,
		Toolchain:      actualToolchain,
	}
}

func copyReportEvidence(t *testing.T, srcPath, destName, repoRoot string) {
	t.Helper()
	evidenceDir := filepath.Join(repoRoot, "docs", "evidence", "phase4")
	if err := os.MkdirAll(evidenceDir, 0755); err != nil {
		t.Fatalf("failed to create evidence directory: %v", err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read report from %s: %v", srcPath, err)
	}

	destPath := filepath.Join(evidenceDir, destName)
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		t.Fatalf("failed to write evidence report to %s: %v", destPath, err)
	}
	t.Logf("Persisted workload evidence report: %s (%d bytes)", destPath, len(data))
}

func TestWorkload_WindowsMeasurementReports(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	// Always compile a fresh endpoint into an isolated test temporary directory
	provenance := buildFreshEndpointWithProvenance(t, repoRoot)
	endpointExe := provenance.ExePath

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

		ctx, cancel := context.WithTimeout(context.Background(), duration+10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+reportPath,
			"-measure-profile=windows-smoke-idle",
			"-measure-duration="+duration.String(),
			"-measure-interval="+interval.String(),
		)
		cmd.Dir = scenarioDir

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("endpoint.exe failed in idle scenario: %v\nOutput: %s", err, string(output))
		}

		reportData, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("failed to read idle report: %v", err)
		}

		var rep resourcereport.Report
		if err := json.Unmarshal(reportData, &rep); err != nil {
			t.Fatalf("failed to parse idle report JSON: %v", err)
		}

		if rep.Manifest.OS != "windows" {
			t.Errorf("expected OS=windows, got %s", rep.Manifest.OS)
		}
		if rep.Manifest.LogicalCPUs <= 0 {
			t.Errorf("expected LogicalCPUs > 0, got %d", rep.Manifest.LogicalCPUs)
		}
		if rep.Summary.ShutdownOutcome != "MEASUREMENT_COMPLETE" {
			t.Errorf("expected MEASUREMENT_COMPLETE, got %s", rep.Summary.ShutdownOutcome)
		}
		if rep.Summary.TotalSamples < 2 {
			t.Errorf("expected at least 2 samples, got %d", rep.Summary.TotalSamples)
		}
		if rep.Summary.EventAccounting == nil || !rep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting reconciliation")
		}

		copyReportEvidence(t, reportPath, "windows_idle_report.json", repoRoot)
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

		ctx, cancel := context.WithTimeout(context.Background(), duration+10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+reportPath,
			"-measure-profile=windows-smoke-sustained",
			"-measure-duration="+duration.String(),
			"-measure-interval="+interval.String(),
		)
		cmd.Dir = scenarioDir

		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start endpoint.exe: %v", err)
		}

		// Allow endpoint & fsnotify watcher to initialize
		time.Sleep(300 * time.Millisecond)

		// Produce sustained filesystem events: 1 write every 200ms
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

		err = cmd.Wait()
		close(stopSustained)
		if err != nil {
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
		if rep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal == 0 {
			t.Errorf("expected > 0 http accepted events in sustained scenario")
		}

		copyReportEvidence(t, reportPath, "windows_sustained_report.json", repoRoot)
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

		ctx, cancel := context.WithTimeout(context.Background(), duration+10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+reportPath,
			"-measure-profile=windows-smoke-burst",
			"-measure-duration="+duration.String(),
			"-measure-interval="+interval.String(),
		)
		cmd.Dir = scenarioDir

		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start endpoint.exe: %v", err)
		}

		// Allow endpoint & fsnotify watcher to initialize
		time.Sleep(300 * time.Millisecond)

		// Create a rapid burst of 35 files
		for i := 1; i <= 35; i++ {
			filePath := filepath.Join(watchedDir, fmt.Sprintf("burst_%d.txt", i))
			_ = os.WriteFile(filePath, []byte(fmt.Sprintf("burst payload %d", i)), 0644)
		}

		err = cmd.Wait()
		if err != nil {
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

		copyReportEvidence(t, reportPath, "windows_burst_report.json", repoRoot)
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

		// Part A: Offline phase (unreachable server port)
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

		ctxOffline, cancelOffline := context.WithTimeout(context.Background(), duration+10*time.Second)
		defer cancelOffline()

		cmdOffline := exec.CommandContext(ctxOffline, endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+offlineReportPath,
			"-measure-profile=windows-smoke-offline",
			"-measure-duration="+duration.String(),
			"-measure-interval="+interval.String(),
		)
		cmdOffline.Dir = scenarioDir

		if err := cmdOffline.Start(); err != nil {
			t.Fatalf("failed to start endpoint.exe for offline phase: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		// Create 10 files while offline
		for i := 1; i <= 10; i++ {
			filePath := filepath.Join(watchedDir, fmt.Sprintf("offline_%d.txt", i))
			_ = os.WriteFile(filePath, []byte(fmt.Sprintf("offline content %d", i)), 0644)
		}

		err = cmdOffline.Wait()
		if err != nil {
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

		if offlineRep.Summary.EventAccounting == nil || !offlineRep.Summary.EventAccounting.IngestionOutcome.ReconciliationValid {
			t.Errorf("expected valid ingestion accounting in offline phase")
		}
		if offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents == nil ||
			*offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents < 10 {
			t.Errorf("expected at least 10 pending events retained in SQLite, got %v",
				offlineRep.Summary.EventAccounting.Storage.DurablePendingEvents)
		}
		if offlineRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal != 0 {
			t.Errorf("expected 0 http accepted while offline, got %d",
				offlineRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal)
		}

		copyReportEvidence(t, offlineReportPath, "windows_offline_report.json", repoRoot)

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

		// Maximum exponential backoff for attempt 1 is 10s + 9s jitter = 19s from the offline send.
		// A bounded native run of 22s ensures the 5-second forwarder ticker fires after all retry
		// deadlines have elapsed, demonstrating true wall-clock retry recovery without test clock manipulation.
		recDuration := 22 * time.Second
		recInterval := 2 * time.Second

		ctxReconnect, cancelReconnect := context.WithTimeout(context.Background(), recDuration+15*time.Second)
		defer cancelReconnect()

		cmdReconnect := exec.CommandContext(ctxReconnect, endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+reconnectReportPath,
			"-measure-profile=windows-smoke-reconnect",
			"-measure-duration="+recDuration.String(),
			"-measure-interval="+recInterval.String(),
		)
		cmdReconnect.Dir = scenarioDir

		outputRec, err := cmdReconnect.CombinedOutput()
		if err != nil {
			t.Fatalf("endpoint.exe failed in real retry recovery phase: %v\nOutput: %s", err, string(outputRec))
		}

		reconnectReportData, err := os.ReadFile(reconnectReportPath)
		if err != nil {
			t.Fatalf("failed to read reconnect report: %v", err)
		}

		var recRep resourcereport.Report
		if err := json.Unmarshal(reconnectReportData, &recRep); err != nil {
			t.Fatalf("failed to parse reconnect report JSON: %v", err)
		}

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

		if recRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal < expectedBacklogCount {
			t.Errorf("expected at least %d http accepted events on reconnect, got %d",
				expectedBacklogCount, recRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal)
		}

		// Persist report as both windows_reconnect_report.json and windows_retry_recovery_report.json
		copyReportEvidence(t, reconnectReportPath, "windows_reconnect_report.json", repoRoot)
		copyReportEvidence(t, reconnectReportPath, "windows_retry_recovery_report.json", repoRoot)
	})
}
