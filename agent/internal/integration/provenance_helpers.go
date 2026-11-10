package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultMaxOutputBytes is the maximum captured subprocess output (256 KB) before truncation.
const DefaultMaxOutputBytes = 256 * 1024

// SubprocessOutcome defines the terminal outcome of a command execution.
type SubprocessOutcome string

const (
	OutcomeSuccess       SubprocessOutcome = "SUCCESS"
	OutcomeNonzeroExit   SubprocessOutcome = "NONZERO_EXIT"
	OutcomeTimeout       SubprocessOutcome = "TIMEOUT"
	OutcomeCancelled     SubprocessOutcome = "CANCELLED"
	OutcomeLaunchFailure SubprocessOutcome = "LAUNCH_FAILURE"
)

// BinaryProvenance records verified metadata of a freshly compiled test binary.
type BinaryProvenance struct {
	ExePath        string `json:"exe_path"`
	SHA256         string `json:"sha256"`
	SourceRevision string `json:"source_revision"`
	Dirty          bool   `json:"dirty"`
	Toolchain      string `json:"toolchain"`
	OS             string `json:"os"`
	Architecture   string `json:"architecture"`
	LogicalCPUs    int    `json:"logical_cpus"`
}

// SourceBoundaryCheck records a source tree stability check performed at a key workflow boundary.
// Boundary checks establish boundary-state consistency; they cannot prove no transient edit
// occurred between checkpoints. For qualification evidence runs, an isolated committed checkout should be used.
type SourceBoundaryCheck struct {
	Checkpoint   string `json:"checkpoint"` // "pre-compilation", "post-compilation", "post-workload"
	TimestampUTC string `json:"timestamp_utc"`
	Revision     string `json:"revision"`
	Dirty        bool   `json:"dirty"`
	Valid        bool   `json:"valid"`
	StatusNotes  string `json:"status_notes,omitempty"`
}

// WorkloadRecipe describes the execution parameters of a scenario.
type WorkloadRecipe struct {
	Name             string        `json:"name"`
	Profile          string        `json:"profile"`
	Duration         time.Duration `json:"duration"`
	SamplingInterval time.Duration `json:"sampling_interval"`
	WorkloadAction   string        `json:"workload_action"`
}

// CommandExecutionRecord logs a single subprocess invocation with truthful exit code semantics.
// If the process never started, ExitCode is nil and ProcessStarted is false (exit code is never invented).
type CommandExecutionRecord struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	WorkingDir     string            `json:"working_dir"`
	Timeout        time.Duration     `json:"timeout"`
	DurationMs     int64             `json:"duration_ms"`
	Outcome        SubprocessOutcome `json:"outcome"`
	ProcessStarted bool              `json:"process_started"`
	ExitCode       *int              `json:"exit_code,omitempty"`
	Stdout         string            `json:"stdout,omitempty"`
	Stderr         string            `json:"stderr,omitempty"`
	CombinedOutput string            `json:"combined_output,omitempty"`
	Truncated      bool              `json:"truncated"`
	ErrorMessage   string            `json:"error_message,omitempty"`
}

// RunProvenanceManifest captures the full verifiable provenance of an evidence run.
type RunProvenanceManifest struct {
	RunID                string                   `json:"run_id"`
	TimestampUTC         string                   `json:"timestamp_utc"`
	SourceRevision       string                   `json:"source_revision"`
	Dirty                bool                     `json:"dirty"`
	SourceBoundaryChecks []SourceBoundaryCheck    `json:"source_boundary_checks"`
	BinaryProvenance     BinaryProvenance         `json:"binary_provenance"`
	HostOS               string                   `json:"host_os"`
	HostArch             string                   `json:"host_arch"`
	LogicalCPUs          int                      `json:"logical_cpus"`
	GoToolchain          string                   `json:"go_toolchain"`
	ExecutedCommands     []CommandExecutionRecord `json:"executed_commands"`
	WorkloadRecipes      []WorkloadRecipe         `json:"workload_recipes"`
	GeneratedReports     []string                 `json:"generated_reports"`
}

// ParseBinaryProvenanceOutput parses the output of `go version -m <path>` and extracts
// the toolchain, VCS revision, and VCS modified (dirty) state.
// Handles absolute Windows (C:\...) and POSIX (/...) binary paths.
// Strictly requires revision, dirty-state, and toolchain to be present; never silently defaults them.
func ParseBinaryProvenanceOutput(versionOutput string) (revision string, dirty bool, toolchain string, err error) {
	lines := strings.Split(versionOutput, "\n")
	var dirtyFound bool

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}

		// Toolchain is emitted on the header line: "<path>: <toolchain>"
		// E.g.: "C:\Users\...\endpoint.exe: go1.27.0" or "/tmp/endpoint: go1.24.0"
		if toolchain == "" && !strings.HasPrefix(trimmed, "build\t") &&
			!strings.HasPrefix(trimmed, "path\t") &&
			!strings.HasPrefix(trimmed, "mod\t") &&
			!strings.HasPrefix(trimmed, "dep\t") {
			if idx := strings.Index(trimmed, ": "); idx != -1 {
				val := strings.TrimSpace(trimmed[idx+2:])
				if strings.HasPrefix(val, "go") {
					toolchain = val
				}
			}
		}

		if strings.HasPrefix(trimmed, "build\tvcs.revision=") {
			revision = strings.TrimPrefix(trimmed, "build\tvcs.revision=")
		} else if strings.HasPrefix(trimmed, "build\tvcs.modified=") {
			dirtyFound = true
			dirty = (strings.TrimPrefix(trimmed, "build\tvcs.modified=") == "true")
		}
	}

	if toolchain == "" {
		return "", false, "", fmt.Errorf("toolchain metadata missing (expected '<path>: goX.Y.Z') in output:\n%s", versionOutput)
	}
	if revision == "" {
		return "", false, "", fmt.Errorf("binary metadata missing vcs.revision in output:\n%s", versionOutput)
	}
	if !dirtyFound {
		return "", false, "", fmt.Errorf("binary metadata missing vcs.modified in output:\n%s", versionOutput)
	}

	return revision, dirty, toolchain, nil
}

// ComputeFileSHA256 returns the hex-encoded SHA-256 hash of a file.
func ComputeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for hashing %s: %w", filePath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file %s: %w", filePath, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteEvidenceExclusive writes evidence data to destPath exclusively, refusing overwrite.
// Correctly checks and propagates directory creation, open, write, and close errors.
func WriteEvidenceExclusive(destPath string, data []byte) (err error) {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for evidence %s: %w", dir, err)
	}

	f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing evidence report at %s: %w", destPath, err)
		}
		return fmt.Errorf("failed to create evidence file %s: %w", destPath, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close evidence file %s: %w", destPath, closeErr)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write evidence data to %s: %w", destPath, err)
	}
	return nil
}

// ExportEvidenceRun copies all staged evidence files from stagingDir to targetDir exclusively.
// If any file already exists in targetDir, export fails immediately and leaves unrelated files untouched.
func ExportEvidenceRun(stagingDir string, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create export target directory %s: %w", targetDir, err)
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return fmt.Errorf("failed to read staging directory %s: %w", stagingDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(stagingDir, entry.Name())
		destPath := filepath.Join(targetDir, entry.Name())

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read staged evidence file %s: %w", srcPath, err)
		}

		if err := WriteEvidenceExclusive(destPath, data); err != nil {
			return fmt.Errorf("failed to exclusively write evidence file %s: %w", destPath, err)
		}
	}
	return nil
}

// boundedBuffer captures up to maxBytes in memory, appending a truncation marker if exceeded.
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func newBoundedBuffer(maxBytes int) *boundedBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxOutputBytes
	}
	return &boundedBuffer{maxBytes: maxBytes}
}

func (b *boundedBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n = len(p)
	if b.truncated {
		return n, nil
	}

	remaining := b.maxBytes - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		b.buf.WriteString(fmt.Sprintf("\n[OUTPUT TRUNCATED: exceeded limit of %d bytes]", b.maxBytes))
		return n, nil
	}

	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		b.buf.WriteString(fmt.Sprintf("\n[OUTPUT TRUNCATED: exceeded limit of %d bytes]", b.maxBytes))
		return n, nil
	}

	b.buf.Write(p)
	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *boundedBuffer) IsTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// ManagedSubprocess provides consistent, bounded subprocess lifecycle management,
// output capture, and truthful exit code observation.
type ManagedSubprocess struct {
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
	dir       string
	name      string
	args      []string
	timeout   time.Duration
	startTime time.Time
	stdoutBuf *boundedBuffer
	stderrBuf *boundedBuffer
	combBuf   *boundedBuffer
	started   bool
	launchErr error
}

// StartManagedSubprocess initializes and launches a subprocess with explicit timeouts and bounded buffers.
func StartManagedSubprocess(ctx context.Context, dir string, timeout time.Duration, maxOutputBytes int, name string, args ...string) (*ManagedSubprocess, error) {
	subCtx, cancel := context.WithTimeout(ctx, timeout)

	stdoutBuf := newBoundedBuffer(maxOutputBytes)
	stderrBuf := newBoundedBuffer(maxOutputBytes)
	combBuf := newBoundedBuffer(maxOutputBytes)

	cmd := exec.CommandContext(subCtx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = io.MultiWriter(stdoutBuf, combBuf)
	cmd.Stderr = io.MultiWriter(stderrBuf, combBuf)

	sub := &ManagedSubprocess{
		cmd:       cmd,
		ctx:       subCtx,
		cancel:    cancel,
		dir:       dir,
		name:      name,
		args:      args,
		timeout:   timeout,
		stdoutBuf: stdoutBuf,
		stderrBuf: stderrBuf,
		combBuf:   combBuf,
		startTime: time.Now(),
	}

	if err := cmd.Start(); err != nil {
		sub.started = false
		sub.launchErr = err
		cancel()
		return sub, err
	}

	sub.started = true
	return sub, nil
}

// Wait blocks until the process finishes or the context expires, returning a truthful CommandExecutionRecord.
func (sub *ManagedSubprocess) Wait() (*CommandExecutionRecord, error) {
	defer func() {
		if sub.cancel != nil {
			sub.cancel()
		}
	}()

	var waitErr error
	if sub.started {
		waitErr = sub.cmd.Wait()
	} else {
		waitErr = sub.launchErr
	}

	dur := time.Since(sub.startTime)

	rec := &CommandExecutionRecord{
		Command:        sub.name,
		Args:           sub.args,
		WorkingDir:     sub.dir,
		Timeout:        sub.timeout,
		DurationMs:     dur.Milliseconds(),
		ProcessStarted: sub.started,
		Stdout:         sub.stdoutBuf.String(),
		Stderr:         sub.stderrBuf.String(),
		CombinedOutput: sub.combBuf.String(),
		Truncated:      sub.stdoutBuf.IsTruncated() || sub.stderrBuf.IsTruncated() || sub.combBuf.IsTruncated(),
	}

	// 1. Process failed to launch
	if !sub.started {
		rec.Outcome = OutcomeLaunchFailure
		rec.ExitCode = nil // Do not invent an exit code
		rec.ErrorMessage = sub.launchErr.Error()
		return rec, fmt.Errorf("failed to start command '%s %s' in %s: %w", sub.name, strings.Join(sub.args, " "), sub.dir, sub.launchErr)
	}

	// 2. Command timed out
	if errors.Is(sub.ctx.Err(), context.DeadlineExceeded) {
		rec.Outcome = OutcomeTimeout
		rec.ErrorMessage = fmt.Sprintf("command timed out after %v", sub.timeout)
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code := exitErr.ExitCode()
			rec.ExitCode = &code
		}
		return rec, fmt.Errorf("command '%s %s' in %s timed out after %v: %w\nOutput:\n%s",
			sub.name, strings.Join(sub.args, " "), sub.dir, sub.timeout, sub.ctx.Err(), sub.combBuf.String())
	}

	// 3. Command was cancelled (by caller context)
	if errors.Is(sub.ctx.Err(), context.Canceled) {
		rec.Outcome = OutcomeCancelled
		rec.ErrorMessage = "command cancelled"
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code := exitErr.ExitCode()
			rec.ExitCode = &code
		}
		return rec, sub.ctx.Err()
	}

	// 4. Command exited with error (preserves wrapped *exec.ExitError via errors.As)
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code := exitErr.ExitCode()
			rec.ExitCode = &code
			rec.Outcome = OutcomeNonzeroExit
		} else {
			rec.Outcome = OutcomeLaunchFailure
		}
		rec.ErrorMessage = waitErr.Error()
		return rec, fmt.Errorf("command '%s %s' in %s failed: %w\nOutput:\n%s",
			sub.name, strings.Join(sub.args, " "), sub.dir, waitErr, sub.combBuf.String())
	}

	// 5. Successful execution
	exit0 := 0
	rec.ExitCode = &exit0
	rec.Outcome = OutcomeSuccess
	return rec, nil
}

// RunBoundedCommand executes a command synchronously under bounded timeout and output buffers.
func RunBoundedCommand(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) (*CommandExecutionRecord, error) {
	proc, err := StartManagedSubprocess(ctx, dir, timeout, DefaultMaxOutputBytes, name, args...)
	if err != nil {
		rec, _ := proc.Wait()
		return rec, err
	}
	return proc.Wait()
}

// ExecCommandWithTimeout executes a command under a bounded timeout context and captures output.
// Kept for backward compatibility with legacy callers; wraps RunBoundedCommand.
func ExecCommandWithTimeout(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	timeout := 60 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	rec, err := RunBoundedCommand(ctx, dir, timeout, name, args...)
	if rec != nil {
		return []byte(rec.CombinedOutput), err
	}
	return nil, err
}

// CheckSourceBoundary queries git rev-parse HEAD and git status --porcelain under bounded timeouts.
// Boundary checks prove consistency at discrete checkpoints (pre-compilation, post-compilation, post-workload);
// they do not prove that no transient edit occurred between checkpoints. For qualification evidence,
// an isolated committed checkout should be used.
func CheckSourceBoundary(ctx context.Context, repoRoot string, checkpoint string) (SourceBoundaryCheck, error) {
	check := SourceBoundaryCheck{
		Checkpoint:   checkpoint,
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
	}

	ctxRev, cancelRev := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRev()
	recRev, err := RunBoundedCommand(ctxRev, repoRoot, 5*time.Second, "git", "rev-parse", "HEAD")
	if err != nil {
		check.Valid = false
		check.StatusNotes = fmt.Sprintf("git rev-parse HEAD failed: %v", err)
		return check, err
	}
	check.Revision = strings.TrimSpace(recRev.CombinedOutput)

	ctxStatus, cancelStatus := context.WithTimeout(ctx, 5*time.Second)
	defer cancelStatus()
	recStatus, err := RunBoundedCommand(ctxStatus, repoRoot, 5*time.Second, "git", "status", "--porcelain")
	if err != nil {
		check.Valid = false
		check.StatusNotes = fmt.Sprintf("git status failed: %v", err)
		return check, err
	}
	statusStr := strings.TrimSpace(recStatus.CombinedOutput)
	check.Dirty = len(statusStr) > 0
	check.Valid = true
	if check.Dirty {
		check.StatusNotes = fmt.Sprintf("uncommitted changes detected:\n%s", statusStr)
	}
	return check, nil
}
