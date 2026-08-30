package identity

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Identity struct {
	DeviceID string `json:"device_id"`
}

// LoadOrInitialize atomically initializes or loads a device identity from a file.
// A crash during this initialization must never cause a different device_id
// to be generated on the next startup. We achieve this by atomic temp-file rename.
func LoadOrInitialize(path string) (*Identity, error) {
	// Try to load existing
	data, err := os.ReadFile(path)
	if err == nil {
		var id Identity
		if err := json.Unmarshal(data, &id); err == nil && id.DeviceID != "" {
			return &id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	// Generate new UUID
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	newID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])

	id := &Identity{DeviceID: newID}

	outData, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}

	// Atomic write
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, outData, 0600); err != nil {
		return nil, fmt.Errorf("failed to write tmp identity: %w", err)
	}

	// Rename is atomic
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to commit identity: %w", err)
	}

	return id, nil
}
