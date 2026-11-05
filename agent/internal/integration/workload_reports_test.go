package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/resourcereport"
)

type mockIngestServer struct {
	srv           *httptest.Server
	receivedCount atomic.Uint64
}

func newMockIngestServer() *mockIngestServer {
	m := &mockIngestServer{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ingest" {
			_, _ = io.ReadAll(r.Body)
			m.receivedCount.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	return m
}

func (m *mockIngestServer) Close() {
	if m.srv != nil {
		m.srv.Close()
	}
}

func getOrBuildEndpointExe(t *testing.T, repoRoot string) string {
	t.Helper()
	endpointExe := filepath.Join(repoRoot, "endpoint.exe")
	if _, err := os.Stat(endpointExe); os.IsNotExist(err) {
		cmd := exec.Command("go", "build", "-v", "-o", endpointExe, "./agent/cmd/endpoint")
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("failed to build endpoint.exe: %v\n%s", err, string(out))
		}
	}
	return endpointExe
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
	endpointExe := getOrBuildEndpointExe(t, repoRoot)

	// -------------------------------------------------------------
	// 1. Idle Workload Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_Idle", func(t *testing.T) {
		scenarioDir := t.TempDir()
		watchedDir := filepath.Join(scenarioDir, "watched")
		if err := os.MkdirAll(watchedDir, 0755); err != nil {
			t.Fatalf("failed to create watched dir: %v", err)
		}

		mockSrv := newMockIngestServer()
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

		cmd := exec.Command(endpointExe,
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

		mockSrv := newMockIngestServer()
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

		cmd := exec.Command(endpointExe,
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

		mockSrv := newMockIngestServer()
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

		cmd := exec.Command(endpointExe,
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
	// 4. Offline & Reconnect Workload Scenario
	// -------------------------------------------------------------
	t.Run("Scenario_OfflineAndReconnect", func(t *testing.T) {
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

		cmdOffline := exec.Command(endpointExe,
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

		// Reset exponential backoff in agent.db to simulate backoff elapsed on reconnect
		rawDB, err := sql.Open("sqlite", filepath.Join(scenarioDir, "agent.db"))
		if err != nil {
			t.Fatalf("failed to open agent.db: %v", err)
		}
		if _, err := rawDB.Exec("UPDATE events SET next_attempt_at = NULL WHERE state = 'PENDING'"); err != nil {
			rawDB.Close()
			t.Fatalf("failed to clear next_attempt_at: %v", err)
		}
		_ = rawDB.Close()

		// Part B: Reconnect phase (server online, drain SQLite backlog)
		mockSrv := newMockIngestServer()
		defer mockSrv.Close()

		reconnectCfg := config.Config{
			APIAddr:      mockSrv.srv.URL,
			TenantID:     "tenant-smoke-offline",
			SiteID:       "site-smoke-offline",
			MonitorPaths: []string{watchedDir},
		}
		recCfgData, _ := json.Marshal(reconnectCfg)
		if err := os.WriteFile(cfgPath, recCfgData, 0644); err != nil {
			t.Fatalf("failed to update agent.json for reconnect phase: %v", err)
		}

		reconnectReportPath := filepath.Join(scenarioDir, "report_reconnect.json")

		cmdReconnect := exec.Command(endpointExe,
			"-config="+cfgPath,
			"-identity="+idPath,
			"-measure-report="+reconnectReportPath,
			"-measure-profile=windows-smoke-reconnect",
			"-measure-duration="+duration.String(),
			"-measure-interval="+interval.String(),
		)
		cmdReconnect.Dir = scenarioDir

		outputRec, err := cmdReconnect.CombinedOutput()
		if err != nil {
			t.Fatalf("endpoint.exe failed in reconnect phase: %v\nOutput: %s", err, string(outputRec))
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
		if recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents == nil ||
			*recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents < 10 {
			t.Errorf("expected at least 10 initial backlog pending events, got %v",
				recRep.Summary.EventAccounting.Storage.InitialBacklogPendingEvents)
		}
		if recRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal < 10 {
			t.Errorf("expected at least 10 forwarded and accepted events on reconnect, got %d",
				recRep.Summary.EventAccounting.Forwarder.HTTPAcceptedTotal)
		}
		if recRep.Summary.EventAccounting.Storage.DurablePendingEvents == nil ||
			*recRep.Summary.EventAccounting.Storage.DurablePendingEvents != 0 {
			t.Errorf("expected 0 remaining pending events after reconnect drain, got %v",
				recRep.Summary.EventAccounting.Storage.DurablePendingEvents)
		}

		copyReportEvidence(t, reconnectReportPath, "windows_reconnect_report.json", repoRoot)
	})
}
