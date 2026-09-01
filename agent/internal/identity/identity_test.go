package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrInitialize(t *testing.T) {
	tempDir := t.TempDir()
	idFile := filepath.Join(tempDir, "identity.json")

	// First load: should initialize a new identity
	id1, err := LoadOrInitialize(idFile)
	if err != nil {
		t.Fatalf("Failed to initialize identity: %v", err)
	}
	if id1.DeviceID == "" {
		t.Fatalf("DeviceID is empty")
	}

	// Second load: should load the exact same identity
	id2, err := LoadOrInitialize(idFile)
	if err != nil {
		t.Fatalf("Failed to load existing identity: %v", err)
	}
	if id2.DeviceID != id1.DeviceID {
		t.Fatalf("Expected DeviceID %s, got %s", id1.DeviceID, id2.DeviceID)
	}

	// Simulate a crash during atomic rename by creating just the temp file
	// If a temp file exists but no identity file, it should just create a new one.
	crashFile := filepath.Join(tempDir, "identity_crash.json")
	tmpPath := crashFile + ".tmp"
	_ = os.WriteFile(tmpPath, []byte(`{"device_id":"bad-partial-write"}`), 0600)

	id3, err := LoadOrInitialize(crashFile)
	if err != nil {
		t.Fatalf("Failed to initialize identity after simulated crash: %v", err)
	}
	if id3.DeviceID == "" || id3.DeviceID == "bad-partial-write" {
		t.Fatalf("DeviceID initialization was not crash-safe. Got: %s", id3.DeviceID)
	}
}
