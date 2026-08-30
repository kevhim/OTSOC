package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
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
	newID := uuid.New().String()

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
