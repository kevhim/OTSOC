package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseBinaryProvenance_AbsoluteWindowsPath(t *testing.T) {
	mockOutput := `C:\Users\test\AppData\Local\Temp\TestWorkload123\001\endpoint.exe: go1.27.0
	path	redcyberfox/agent/cmd/endpoint
	mod	redcyberfox	(devel)	
	build	-buildmode=exe
	build	-compiler=gc
	build	CGO_ENABLED=1
	build	GOARCH=amd64
	build	GOOS=windows
	build	vcs=git
	build	vcs.revision=f81a7bf06a61c12de7b98a1df97ccdd0396b5a24
	build	vcs.time=2026-11-09T11:00:00Z
	build	vcs.modified=false
`
	rev, dirty, toolchain, err := ParseBinaryProvenanceOutput(mockOutput)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if rev != "f81a7bf06a61c12de7b98a1df97ccdd0396b5a24" {
		t.Errorf("expected revision f81a7bf..., got %s", rev)
	}
	if dirty != false {
		t.Errorf("expected dirty=false, got %v", dirty)
	}
	if toolchain != "go1.27.0" {
		t.Errorf("expected toolchain=go1.27.0, got %s", toolchain)
	}
}

func TestParseBinaryProvenance_POSIXPath(t *testing.T) {
	mockOutput := `/tmp/build-isolated/endpoint: go1.24.0 linux/amd64
	path	redcyberfox/agent/cmd/endpoint
	build	vcs.revision=1234567890abcdef1234567890abcdef12345678
	build	vcs.modified=true
`
	rev, dirty, toolchain, err := ParseBinaryProvenanceOutput(mockOutput)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if rev != "1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("expected revision 12345678..., got %s", rev)
	}
	if dirty != true {
		t.Errorf("expected dirty=true, got %v", dirty)
	}
	if toolchain != "go1.24.0 linux/amd64" {
		t.Errorf("expected toolchain='go1.24.0 linux/amd64', got %s", toolchain)
	}
}

func TestParseBinaryProvenance_MissingFields(t *testing.T) {
	t.Run("MissingToolchain", func(t *testing.T) {
		output := "build\tvcs.revision=abc123\nbuild\tvcs.modified=false\n"
		_, _, _, err := ParseBinaryProvenanceOutput(output)
		if err == nil || !strings.Contains(err.Error(), "toolchain metadata missing") {
			t.Fatalf("expected toolchain missing error, got %v", err)
		}
	})

	t.Run("MissingRevision", func(t *testing.T) {
		output := "C:\\bin\\endpoint.exe: go1.27.0\nbuild\tvcs.modified=false\n"
		_, _, _, err := ParseBinaryProvenanceOutput(output)
		if err == nil || !strings.Contains(err.Error(), "vcs.revision") {
			t.Fatalf("expected vcs.revision missing error, got %v", err)
		}
	})

	t.Run("MissingModified", func(t *testing.T) {
		output := "C:\\bin\\endpoint.exe: go1.27.0\nbuild\tvcs.revision=abc123\n"
		_, _, _, err := ParseBinaryProvenanceOutput(output)
		if err == nil || !strings.Contains(err.Error(), "vcs.modified") {
			t.Fatalf("expected vcs.modified missing error, got %v", err)
		}
	})
}

func TestWriteEvidenceExclusive_RefuseOverwrite(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "evidence_report.json")

	// First write should succeed
	firstData := []byte(`{"initial":"data"}`)
	if err := WriteEvidenceExclusive(targetPath, firstData); err != nil {
		t.Fatalf("expected first write to succeed, got %v", err)
	}

	// Second write to the exact same file must fail with refusal
	secondData := []byte(`{"overwritten":"data"}`)
	err := WriteEvidenceExclusive(targetPath, secondData)
	if err == nil {
		t.Fatalf("expected write to fail on existing file, but succeeded")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("expected 'refusing to overwrite' error message, got: %v", err)
	}

	// Verify original content was preserved
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target: %v", err)
	}
	if string(content) != string(firstData) {
		t.Errorf("expected original data preserved, got %s", string(content))
	}
}

func TestExecCommandWithTimeout_ReportsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 1000"}
	} else {
		name = "sleep"
		args = []string{"1"}
	}

	_, err := ExecCommandWithTimeout(ctx, ".", name, args...)
	if err == nil {
		t.Fatalf("expected command to time out, but succeeded")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message in error, got: %v", err)
	}
}

func TestSubprocess_SuccessfulOutputCapture(t *testing.T) {
	ctx := context.Background()
	rec, err := RunBoundedCommand(ctx, ".", 10*time.Second, "go", "version")
	if err != nil {
		t.Fatalf("expected successful execution, got: %v", err)
	}
	if rec.Outcome != OutcomeSuccess {
		t.Errorf("expected outcome SUCCESS, got %s", rec.Outcome)
	}
	if !rec.ProcessStarted {
		t.Errorf("expected ProcessStarted=true")
	}
	if rec.ExitCode == nil || *rec.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %v", rec.ExitCode)
	}
	if !strings.Contains(rec.CombinedOutput, "go version") {
		t.Errorf("expected combined output to contain 'go version', got:\n%s", rec.CombinedOutput)
	}
	if rec.Truncated {
		t.Errorf("expected Truncated=false")
	}
}

func TestSubprocess_KnownNonzeroExitCode_WrappedErrors(t *testing.T) {
	ctx := context.Background()
	var name string
	var args []string
	expectedCode := 42
	if runtime.GOOS == "windows" {
		name = "cmd"
		args = []string{"/c", "exit 42"}
	} else {
		name = "sh"
		args = []string{"-c", "exit 42"}
	}

	rec, err := RunBoundedCommand(ctx, ".", 5*time.Second, name, args...)
	if err == nil {
		t.Fatalf("expected non-zero exit command to return an error, but err was nil")
	}
	if rec.Outcome != OutcomeNonzeroExit {
		t.Errorf("expected outcome NONZERO_EXIT, got %s", rec.Outcome)
	}
	if !rec.ProcessStarted {
		t.Errorf("expected ProcessStarted=true")
	}
	if rec.ExitCode == nil || *rec.ExitCode != expectedCode {
		t.Errorf("expected ExitCode=%d, got %v", expectedCode, rec.ExitCode)
	}

	// Verify errors.As correctly unwraps a wrapped *exec.ExitError
	wrappedErr := fmt.Errorf("contextual outer wrapper: %w", err)
	var exitErr *exec.ExitError
	if !errors.As(wrappedErr, &exitErr) {
		t.Fatalf("errors.As failed to extract *exec.ExitError from wrapped error: %v", wrappedErr)
	}
	if exitErr.ExitCode() != expectedCode {
		t.Errorf("expected exit code %d from errors.As, got %d", expectedCode, exitErr.ExitCode())
	}
}

func TestSubprocess_LaunchFailure(t *testing.T) {
	ctx := context.Background()
	nonExistentBin := "non_existent_executable_12345_xyz.exe"
	rec, err := RunBoundedCommand(ctx, ".", 5*time.Second, nonExistentBin)
	if err == nil {
		t.Fatalf("expected launch failure error, but err was nil")
	}
	if rec.Outcome != OutcomeLaunchFailure {
		t.Errorf("expected outcome LAUNCH_FAILURE, got %s", rec.Outcome)
	}
	if rec.ProcessStarted {
		t.Errorf("expected ProcessStarted=false")
	}
	if rec.ExitCode != nil {
		t.Errorf("expected ExitCode=nil when process never started, got %v", *rec.ExitCode)
	}
	if rec.ErrorMessage == "" {
		t.Errorf("expected error message recorded in execution record")
	}
}

func TestSubprocess_Timeout(t *testing.T) {
	ctx := context.Background()
	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 1500"}
	} else {
		name = "sleep"
		args = []string{"1.5"}
	}

	rec, err := RunBoundedCommand(ctx, ".", 100*time.Millisecond, name, args...)
	if err == nil {
		t.Fatalf("expected timeout error, but command succeeded")
	}
	if rec.Outcome != OutcomeTimeout {
		t.Errorf("expected outcome TIMEOUT, got %s", rec.Outcome)
	}
	if !rec.ProcessStarted {
		t.Errorf("expected ProcessStarted=true for timed out process")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message in error, got: %v", err)
	}
}

func TestSubprocess_OutputTruncation(t *testing.T) {
	maxBytes := 64
	buf := newBoundedBuffer(maxBytes)
	largeData := strings.Repeat("A", 128)
	_, writeErr := buf.Write([]byte(largeData))
	if writeErr != nil {
		t.Fatalf("unexpected write error on buffer: %v", writeErr)
	}
	if !buf.IsTruncated() {
		t.Errorf("expected buffer to be truncated")
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("[OUTPUT TRUNCATED: exceeded limit of %d bytes]", maxBytes)) {
		t.Errorf("expected truncation marker in output, got: %s", buf.String())
	}
}

func TestExportEvidenceRun_RefusesOverwriteAndPreservesUnrelatedFiles(t *testing.T) {
	stagingDir := t.TempDir()
	targetDir := t.TempDir()

	// Write staged files
	if err := os.WriteFile(filepath.Join(stagingDir, "report_a.json"), []byte(`{"run":"staged_a"}`), 0644); err != nil {
		t.Fatalf("failed to write staged file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "report_b.json"), []byte(`{"run":"staged_b"}`), 0644); err != nil {
		t.Fatalf("failed to write staged file: %v", err)
	}

	// In targetDir, establish an unrelated file and a colliding file
	unrelatedPath := filepath.Join(targetDir, "unrelated.txt")
	unrelatedContent := "important unrelated project artifact - do not modify or delete"
	if err := os.WriteFile(unrelatedPath, []byte(unrelatedContent), 0644); err != nil {
		t.Fatalf("failed to write unrelated file: %v", err)
	}

	collidingPath := filepath.Join(targetDir, "report_a.json")
	collidingContent := `{"run":"preexisting_a"}`
	if err := os.WriteFile(collidingPath, []byte(collidingContent), 0644); err != nil {
		t.Fatalf("failed to write colliding file: %v", err)
	}

	// Attempt export
	err := ExportEvidenceRun(stagingDir, targetDir)
	if err == nil {
		t.Fatalf("expected ExportEvidenceRun to fail due to existing file, but succeeded")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("expected 'refusing to overwrite' error, got: %v", err)
	}

	// Assert unrelated file is completely untouched
	gotUnrelated, err := os.ReadFile(unrelatedPath)
	if err != nil {
		t.Fatalf("failed to read unrelated file: %v", err)
	}
	if string(gotUnrelated) != unrelatedContent {
		t.Errorf("unrelated file was corrupted: expected %q, got %q", unrelatedContent, string(gotUnrelated))
	}

	// Assert colliding file is completely untouched
	gotColliding, err := os.ReadFile(collidingPath)
	if err != nil {
		t.Fatalf("failed to read colliding file: %v", err)
	}
	if string(gotColliding) != collidingContent {
		t.Errorf("colliding file was overwritten: expected %q, got %q", collidingContent, string(gotColliding))
	}
}

func TestCheckSourceBoundary_ValidAndRecordsStatus(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	check, err := CheckSourceBoundary(context.Background(), repoRoot, "unit-test-boundary")
	if err != nil {
		t.Fatalf("unexpected boundary check error: %v", err)
	}
	if !check.Valid {
		t.Errorf("expected check to be valid")
	}
	if check.Checkpoint != "unit-test-boundary" {
		t.Errorf("expected checkpoint 'unit-test-boundary', got %s", check.Checkpoint)
	}
	if check.Revision == "" {
		t.Errorf("expected non-empty revision")
	}
	if check.TimestampUTC == "" {
		t.Errorf("expected non-empty timestamp")
	}
}

// =============================================================================
// Truthful Diagnostic Retention Regressions for Failed / Invalid Runs
// =============================================================================

type testTracker struct {
	mu   sync.Mutex
	cmds []CommandExecutionRecord
}

func (tr *testTracker) Add(rec CommandExecutionRecord) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.cmds = append(tr.cmds, rec)
}

func (tr *testTracker) Commands() []CommandExecutionRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	res := make([]CommandExecutionRecord, len(tr.cmds))
	copy(res, tr.cmds)
	return res
}

// TestDiagnosticHelperProcess is invoked as a subprocess to simulate qualification failures
// and verify that RunDiagnosticManager persists diagnostics outside the child's temporary directory.
func TestDiagnosticHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DIAGNOSTIC_HELPER") != "1" {
		return
	}

	mode := os.Getenv("DIAGNOSTIC_HELPER_MODE")
	diagDir := os.Getenv("DIAGNOSTIC_DIR")
	runID := os.Getenv("DIAGNOSTIC_RUN_ID")
	if runID == "" {
		runID = "simulated_run_123"
	}

	stagingDir := t.TempDir()
	tracker := &testTracker{}

	diagMgr := NewRunDiagnosticManager(runID, diagDir, stagingDir, tracker)

	switch mode {
	case "simulate_dirty_invalid":
		// Simulate discovering an uncommitted dirty file during a clean qualification run
		chk := SourceBoundaryCheck{
			Checkpoint:   "pre-compilation",
			TimestampUTC: time.Now().UTC().Format(time.RFC3339),
			Revision:     "mockrev123456",
			Dirty:        true,
			Valid:        true,
			StatusNotes:  "M dirty_test_file.go",
		}
		diagMgr.RecordBoundaryCheck(chk)
		_, err := diagMgr.FinalizeFailure(OutcomeInvalid, "qualification evidence requires clean checkout, but working tree is dirty", errors.New("dirty working tree"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "FinalizeFailure error: %v\n", err)
			os.Exit(2)
		}
		os.Exit(1) // Nonzero exit because qualification failed

	case "simulate_subprocess_failure":
		// Simulate partial report staged in stagingDir before a crash
		partialData := []byte(`{"scenario":"idle_partial","status":"incomplete"}`)
		if err := os.WriteFile(filepath.Join(stagingDir, "partial_idle_report.json"), partialData, 0644); err != nil {
			os.Exit(3)
		}

		exitCode := 1
		tracker.Add(CommandExecutionRecord{
			Command:    "endpoint.exe",
			Args:       []string{"-measure-profile=windows-smoke-sustained"},
			WorkingDir: stagingDir,
			Outcome:    OutcomeNonzeroExit,
			ExitCode:   &exitCode,
		})

		_, err := diagMgr.FinalizeFailure(OutcomeFailed, "endpoint.exe crashed during sustained scenario", errors.New("exit status 1"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "FinalizeFailure error: %v\n", err)
			os.Exit(2)
		}
		os.Exit(1) // Nonzero exit

	default:
		os.Exit(99)
	}
}

func TestDiagnosticRetention_FailedAndInvalidRuns(t *testing.T) {
	// Root persistent diagnostic directory outside any child t.TempDir
	persistentDiagDir := filepath.Join(t.TempDir(), "persistent_diagnostics")

	t.Run("InvalidRun_DirtySourceRetained", func(t *testing.T) {
		runID := "run_test_dirty_001"
		cmd := exec.Command(os.Args[0], "-test.run=^TestDiagnosticHelperProcess$", "-test.v")
		cmd.Env = append(os.Environ(),
			"GO_WANT_DIAGNOSTIC_HELPER=1",
			"DIAGNOSTIC_HELPER_MODE=simulate_dirty_invalid",
			"DIAGNOSTIC_DIR="+persistentDiagDir,
			"DIAGNOSTIC_RUN_ID="+runID,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected child process to exit nonzero on invalid run, got success:\n%s", string(out))
		}

		// Verify persistent diagnostics directory retained
		targetDir := filepath.Join(persistentDiagDir, "invalid_"+runID)
		if _, statErr := os.Stat(targetDir); statErr != nil {
			t.Fatalf("expected invalid run directory to exist at %s: %v", targetDir, statErr)
		}

		manifestPath := filepath.Join(targetDir, "diagnostic_manifest.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("failed to read diagnostic manifest: %v", err)
		}

		var manifest DiagnosticManifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			t.Fatalf("failed to parse diagnostic manifest: %v", err)
		}
		if manifest.Outcome != OutcomeInvalid {
			t.Errorf("expected Outcome %s, got %s", OutcomeInvalid, manifest.Outcome)
		}
		if manifest.RunID != runID {
			t.Errorf("expected RunID %s, got %s", runID, manifest.RunID)
		}
		if len(manifest.SourceBoundaryChecks) != 1 || !manifest.SourceBoundaryChecks[0].Dirty {
			t.Errorf("expected dirty source boundary check recorded, got: %+v", manifest.SourceBoundaryChecks)
		}

		// Verify commands.log exists and contains run ID
		cmdLogPath := filepath.Join(targetDir, "commands.log")
		logData, err := os.ReadFile(cmdLogPath)
		if err != nil {
			t.Fatalf("failed to read commands.log: %v", err)
		}
		if !strings.Contains(string(logData), runID) {
			t.Errorf("expected commands.log to contain run ID %s", runID)
		}
	})

	t.Run("FailedRun_SubprocessCrashAndPartialReportsRetained", func(t *testing.T) {
		runID := "run_test_crash_002"
		cmd := exec.Command(os.Args[0], "-test.run=^TestDiagnosticHelperProcess$", "-test.v")
		cmd.Env = append(os.Environ(),
			"GO_WANT_DIAGNOSTIC_HELPER=1",
			"DIAGNOSTIC_HELPER_MODE=simulate_subprocess_failure",
			"DIAGNOSTIC_DIR="+persistentDiagDir,
			"DIAGNOSTIC_RUN_ID="+runID,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected child process to exit nonzero on failed run, got success:\n%s", string(out))
		}

		// Verify persistent diagnostics directory retained
		targetDir := filepath.Join(persistentDiagDir, "failed_"+runID)
		if _, statErr := os.Stat(targetDir); statErr != nil {
			t.Fatalf("expected failed run directory to exist at %s: %v", targetDir, statErr)
		}

		manifestPath := filepath.Join(targetDir, "diagnostic_manifest.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("failed to read diagnostic manifest: %v", err)
		}

		var manifest DiagnosticManifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			t.Fatalf("failed to parse diagnostic manifest: %v", err)
		}
		if manifest.Outcome != OutcomeFailed {
			t.Errorf("expected Outcome %s, got %s", OutcomeFailed, manifest.Outcome)
		}
		if len(manifest.PartialReports) != 1 || manifest.PartialReports[0] != "partial_idle_report.json" {
			t.Errorf("expected partial report partial_idle_report.json recorded, got: %v", manifest.PartialReports)
		}

		// Verify partial report was preserved inside targetDir
		preservedReport := filepath.Join(targetDir, "partial_idle_report.json")
		repData, err := os.ReadFile(preservedReport)
		if err != nil {
			t.Fatalf("failed to read preserved partial report: %v", err)
		}
		if !strings.Contains(string(repData), "idle_partial") {
			t.Errorf("unexpected content in preserved partial report: %s", string(repData))
		}
	})
}
