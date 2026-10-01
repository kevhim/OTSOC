package detection

import (
	"os"
	"path/filepath"
	"testing"

	"redcyberfox/pkg/events"
)

func TestNewYaraRule_Success(t *testing.T) {
	ruleSource := `rule test_rule { condition: true }`
	rule, err := NewYaraRule("YARA-TEST-001", "1.0", "HIGH", 90.0, "Test reason", []string{"T1000"}, nil, ruleSource)
	if err != nil {
		t.Fatalf("expected success, got err: %v", err)
	}
	if rule.ID() != "YARA-TEST-001" {
		t.Errorf("unexpected ID: %s", rule.ID())
	}
}

func TestNewYaraRule_Malformed(t *testing.T) {
	ruleSource := `rule test_rule { condition: syntax_error }`
	_, err := NewYaraRule("YARA-TEST-002", "1.0", "HIGH", 90.0, "Test", nil, nil, ruleSource)
	if err == nil {
		t.Fatalf("expected error for malformed rule, got nil")
	}
}

func TestYaraRule_Evaluate_PositiveAndNegative(t *testing.T) {
	tempDir := t.TempDir()

	// Create a positive artifact
	posPath := filepath.Join(tempDir, "malware.bin")
	if err := os.WriteFile(posPath, []byte("evil_payload_here"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a negative artifact
	negPath := filepath.Join(tempDir, "clean.bin")
	if err := os.WriteFile(negPath, []byte("clean_data_here"), 0644); err != nil {
		t.Fatal(err)
	}

	ruleSource := `
rule detect_evil {
	strings:
		$a = "evil_payload"
	condition:
		$a
}
`
	rule, err := NewYaraRule("YARA-EVIL", "1.0", "CRITICAL", 100.0, "Detected evil", nil, nil, ruleSource)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Positive Match
	posEv := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path": posPath,
		},
	}
	matched, err := rule.Evaluate(posEv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Errorf("expected positive match, got false")
	}

	// 2. Negative Match
	negEv := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path": negPath,
		},
	}
	matched, err = rule.Evaluate(negEv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected negative match, got true")
	}

	// 3. Repeated Evaluation (Idempotency)
	matched2, err := rule.Evaluate(posEv)
	if err != nil || !matched2 {
		t.Errorf("repeated evaluation failed idempotency")
	}
}

func TestYaraRule_Evaluate_MissingFile(t *testing.T) {
	ruleSource := `rule test_rule { condition: true }`
	rule, _ := NewYaraRule("ID", "1.0", "LOW", 50.0, "Reason", nil, nil, ruleSource)

	ev := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path": "/path/does/not/exist.bin",
		},
	}

	matched, err := rule.Evaluate(ev)
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
	if matched {
		t.Errorf("expected matched=false for missing file, got true")
	}
}

func TestYaraRule_Evaluate_ArtifactSizeEdgeCases(t *testing.T) {
	tempDir := t.TempDir()

	ruleSource := `rule test_rule { condition: true }`
	rule, _ := NewYaraRule("ID", "1.0", "LOW", 50.0, "Reason", nil, nil, ruleSource)

	testCases := []struct {
		name        string
		size        int64
		expectError bool
	}{
		{"0 bytes", 0, false},
		{"1 byte", 1, false},
		{"MaxArtifactSize - 1", MaxArtifactSizeBytes - 1, false},
		{"MaxArtifactSize", MaxArtifactSizeBytes, false},
		{"MaxArtifactSize + 1", MaxArtifactSizeBytes + 1, true},
		{"Substantially larger", MaxArtifactSizeBytes + 10*1024*1024, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tempDir, tc.name+".bin")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.size > 0 {
				_, err = f.Seek(tc.size-1, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.Write([]byte("a"))
			}
			f.Close()

			ev := &events.CanonicalEvent{
				Metadata: map[string]interface{}{
					"file_path": path,
				},
			}

			matched, err := rule.Evaluate(ev)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for oversized file, got nil")
				}
				if matched {
					t.Errorf("expected matched=false for oversized file, got true")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !matched {
					t.Errorf("expected matched=true, got false")
				}
			}
		})
	}
}

func TestYaraRule_Evaluate_Determinism(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file.bin")
	os.WriteFile(filePath, []byte("file_data"), 0644)

	execPath := filepath.Join(tempDir, "exec.bin")
	os.WriteFile(execPath, []byte("exec_data"), 0644)

	ruleSourceFile := `rule test_rule { strings: $a = "file_data" condition: $a }`
	ruleFile, _ := NewYaraRule("ID_F", "1.0", "LOW", 50.0, "Reason", nil, nil, ruleSourceFile)

	ruleSourceExec := `rule test_rule { strings: $a = "exec_data" condition: $a }`
	ruleExec, _ := NewYaraRule("ID_E", "1.0", "LOW", 50.0, "Reason", nil, nil, ruleSourceExec)

	// 1. file_path only
	ev1 := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path": filePath,
		},
	}
	matched1, _ := ruleFile.Evaluate(ev1)
	if !matched1 {
		t.Errorf("expected matched1=true")
	}

	// 2. executable_path only
	ev2 := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"executable_path": execPath,
		},
	}
	matched2, _ := ruleExec.Evaluate(ev2)
	if !matched2 {
		t.Errorf("expected matched2=true")
	}

	// 3. both (file_path should take precedence)
	ev3 := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path":       filePath,
			"executable_path": execPath,
		},
	}
	matched3File, _ := ruleFile.Evaluate(ev3)
	if !matched3File {
		t.Errorf("expected file_path to take precedence")
	}
	matched3Exec, _ := ruleExec.Evaluate(ev3)
	if matched3Exec {
		t.Errorf("expected executable_path to be ignored")
	}

	// 4. neither
	ev4 := &events.CanonicalEvent{
		Metadata: map[string]interface{}{},
	}
	matched4, _ := ruleFile.Evaluate(ev4)
	if matched4 {
		t.Errorf("expected no match")
	}
}

func benchmarkYaraRuleSize(b *testing.B, size int) {
	tempDir := b.TempDir()
	posPath := filepath.Join(tempDir, "malware.bin")

	data := make([]byte, size)
	copy(data, []byte("evil_payload_here"))
	if err := os.WriteFile(posPath, data, 0644); err != nil {
		b.Fatal(err)
	}

	ruleSource := `
rule detect_evil {
	strings:
		$a = "evil_payload"
	condition:
		$a
}
`
	rule, err := NewYaraRule("YARA-EVIL", "1.0", "CRITICAL", 100.0, "Detected evil", nil, nil, ruleSource)
	if err != nil {
		b.Fatal(err)
	}

	ev := &events.CanonicalEvent{
		Metadata: map[string]interface{}{
			"file_path": posPath,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rule.Evaluate(ev)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkYaraRule_Evaluate_1KB(b *testing.B)   { benchmarkYaraRuleSize(b, 1024) }
func BenchmarkYaraRule_Evaluate_100KB(b *testing.B) { benchmarkYaraRuleSize(b, 100*1024) }
func BenchmarkYaraRule_Evaluate_1MB(b *testing.B)   { benchmarkYaraRuleSize(b, 1024*1024) }
func BenchmarkYaraRule_Evaluate_10MB(b *testing.B)  { benchmarkYaraRuleSize(b, 10*1024*1024) }
func BenchmarkYaraRule_Evaluate_50MB(b *testing.B)  { benchmarkYaraRuleSize(b, 50*1024*1024) }
