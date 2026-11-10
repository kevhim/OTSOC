package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
