package detection

import (
	"fmt"
	"io"
	"os"

	yara_x "github.com/VirusTotal/yara-x/go"
	"redcyberfox/pkg/events"
)

// Provisional Limits
// - Artifact input is bounded to 50 MB. Oversized files return an explicit error and are not evaluated.
// - Artifact input is provisionally bounded to 50 MB. Total process/native memory behavior has not yet been fully characterized. Low-spec suitability remains a Phase 4 validation item.
// - Maximum artifacts evaluated per event is 1.
// - YARA rule compilation happens exactly once during rule initialization.
const MaxArtifactSizeBytes = 50 * 1024 * 1024

// YaraRule is a local detection rule that evaluates artifacts using YARA-X.
//
// Lifecycle:
// 1. Rule definition (source code) is provided.
// 2. The rule is compiled exactly once during instantiation (NewYaraRule).
// 3. The compiled yara_x.Rules representation is retained.
// 4. For each telemetry event, Evaluate creates a lightweight Scanner and evaluates the bounded artifact.
type YaraRule struct {
	id              string
	version         string
	severity        string
	confidence      float64
	reason          string
	attckEnterprise []string
	attckICS        []string

	compiledRules *yara_x.Rules
}

// NewYaraRule compiles the provided YARA source and returns a deterministic, reusable YaraRule.
func NewYaraRule(id, version, severity string, confidence float64, reason string, attckEnt, attckICS []string, yaraSource string) (*YaraRule, error) {
	compiler, err := yara_x.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create YARA-X compiler: %w", err)
	}

	err = compiler.AddSource(yaraSource)
	if err != nil {
		return nil, fmt.Errorf("failed to compile YARA rule source: %w", err)
	}

	rules := compiler.Build()

	return &YaraRule{
		id:              id,
		version:         version,
		severity:        severity,
		confidence:      confidence,
		reason:          reason,
		attckEnterprise: attckEnt,
		attckICS:        attckICS,
		compiledRules:   rules,
	}, nil
}

func (r *YaraRule) ID() string {
	return r.id
}

func (r *YaraRule) Version() string {
	return r.version
}

func (r *YaraRule) Severity() string {
	return r.severity
}

func (r *YaraRule) Confidence() float64 {
	return r.confidence
}

func (r *YaraRule) Reason() string {
	return r.reason
}

func (r *YaraRule) AttckEnterprise() []string {
	return r.attckEnterprise
}

func (r *YaraRule) AttckICS() []string {
	return r.attckICS
}

func (r *YaraRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	if ev.Metadata == nil {
		return false, nil
	}

	var artifactPath string

	// Artifact sources - Deterministic Precedence:
	// 1. file_path (primary)
	// 2. executable_path (fallback)
	// Maximum artifacts evaluated per event remains strictly bounded to 1.
	if path, ok := ev.Metadata["file_path"].(string); ok && path != "" {
		artifactPath = path
	} else if path, ok := ev.Metadata["executable_path"].(string); ok && path != "" {
		artifactPath = path
	} else {
		// No actionable artifact path
		return false, nil
	}

	return r.evaluateFile(artifactPath)
}

func (r *YaraRule) evaluateFile(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		// Missing artifact or permission failure
		return false, fmt.Errorf("failed to stat artifact at %s: %w", path, err)
	}

	if fileInfo.Size() > MaxArtifactSizeBytes {
		// Bounded input enforcement
		return false, fmt.Errorf("oversized artifact: %s (size %d bytes exceeds provisional limit of %d bytes)", path, fileInfo.Size(), MaxArtifactSizeBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		// Read/permission failure
		return false, fmt.Errorf("failed to open artifact at %s: %w", path, err)
	}
	defer f.Close()

	// Since we know size <= 50MB, read all.
	// Artifact reading currently uses io.ReadAll after a bounded reader, causing additional transient allocations due to slice growth. Preallocation based on validated file size may reduce allocation overhead and is a Phase 4 optimization candidate.
	// Bounded read using io.LimitReader for absolute safety against mid-read file growth.
	data, err := io.ReadAll(io.LimitReader(f, MaxArtifactSizeBytes+1))
	if err != nil {
		return false, fmt.Errorf("failed to read artifact at %s: %w", path, err)
	}
	if len(data) > MaxArtifactSizeBytes {
		return false, fmt.Errorf("oversized artifact detected during read: %s", path)
	}

	scanner := yara_x.NewScanner(r.compiledRules)

	matches, err := scanner.Scan(data)
	if err != nil {
		// YARA evaluation failure
		return false, fmt.Errorf("YARA-X evaluation failed on artifact %s: %w", path, err)
	}

	// Any match yields a finding for this single rule
	if matches != nil && len(matches.MatchingRules()) > 0 {
		return true, nil
	}

	return false, nil
}
