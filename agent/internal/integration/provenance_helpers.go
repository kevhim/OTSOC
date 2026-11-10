package integration

import (
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
	"time"
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
func WriteEvidenceExclusive(destPath string, data []byte) error {
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
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write evidence data to %s: %w", destPath, err)
	}
	return nil
}

// ExecCommandWithTimeout executes a command under a bounded timeout context and captures output.
// Clearly reports timeout failures when the deadline expires.
func ExecCommandWithTimeout(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, fmt.Errorf("command '%s %s' in %s timed out: %w\nDiagnostic Output:\n%s",
				name, strings.Join(args, " "), dir, ctx.Err(), string(out))
		}
		return out, fmt.Errorf("command '%s %s' in %s failed: %w\nDiagnostic Output:\n%s",
			name, strings.Join(args, " "), dir, err, string(out))
	}
	return out, nil
}

// WorkloadRecipe describes the execution parameters of a scenario.
type WorkloadRecipe struct {
	Name             string        `json:"name"`
	Profile          string        `json:"profile"`
	Duration         time.Duration `json:"duration"`
	SamplingInterval time.Duration `json:"sampling_interval"`
	WorkloadAction   string        `json:"workload_action"`
}

// CommandExecutionRecord logs a single subprocess invocation.
type CommandExecutionRecord struct {
	Command      string        `json:"command"`
	Args         []string      `json:"args"`
	WorkingDir   string        `json:"working_dir"`
	Timeout      time.Duration `json:"timeout"`
	DurationMs   int64         `json:"duration_ms"`
	ExitCode     int           `json:"exit_code"`
	ErrorMessage string        `json:"error_message,omitempty"`
}

// RunProvenanceManifest captures the full verifiable provenance of an evidence run.
type RunProvenanceManifest struct {
	RunID            string                   `json:"run_id"`
	TimestampUTC     string                   `json:"timestamp_utc"`
	SourceRevision   string                   `json:"source_revision"`
	Dirty            bool                     `json:"dirty"`
	BinaryProvenance BinaryProvenance         `json:"binary_provenance"`
	HostOS           string                   `json:"host_os"`
	HostArch         string                   `json:"host_arch"`
	LogicalCPUs      int                      `json:"logical_cpus"`
	GoToolchain      string                   `json:"go_toolchain"`
	ExecutedCommands []CommandExecutionRecord `json:"executed_commands"`
	WorkloadRecipes  []WorkloadRecipe         `json:"workload_recipes"`
	GeneratedReports []string                 `json:"generated_reports"`
}
