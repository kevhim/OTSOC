package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "agent.json")

	// Test missing file
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Expected error loading missing config file, got nil")
	}

	// Test valid file
	validJSON := []byte(`{"api_addr":"http://test:8081","tenant_id":"t-1","site_id":"s-1"}`)
	if err := os.WriteFile(cfgPath, validJSON, 0600); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Failed to load valid config: %v", err)
	}
	if cfg.APIAddr != "http://test:8081" {
		t.Errorf("Expected APIAddr http://test:8081, got %s", cfg.APIAddr)
	}
	if cfg.TenantID != "t-1" {
		t.Errorf("Expected TenantID t-1, got %s", cfg.TenantID)
	}
}
