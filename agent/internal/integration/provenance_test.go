package integration

import (
	"context"
	"os"
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
