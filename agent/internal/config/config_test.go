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

	// Test invalid config (missing APIAddr)
	invalidJSON1 := []byte(`{"tenant_id":"t-1","site_id":"s-1"}`)
	os.WriteFile(cfgPath, invalidJSON1, 0600)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Expected error for missing api_addr")
	}

	// Test invalid config (missing TenantID)
	invalidJSON2 := []byte(`{"api_addr":"http://test:8081","site_id":"s-1"}`)
	os.WriteFile(cfgPath, invalidJSON2, 0600)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Expected error for missing tenant_id")
	}

	// Test invalid config (missing SiteID)
	invalidJSON3 := []byte(`{"api_addr":"http://test:8081","tenant_id":"t-1"}`)
	os.WriteFile(cfgPath, invalidJSON3, 0600)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Expected error for missing site_id")
	}
}

func TestConfigRejectsInvalidEndpointAndIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"invalid scheme", Config{APIAddr: "localhost:8081", TenantID: "t", SiteID: "s"}},
		{"invalid URL", Config{APIAddr: "ftp://server:21", TenantID: "t", SiteID: "s"}},
		{"URL userinfo", Config{APIAddr: "http://user:secret@server:8081", TenantID: "t", SiteID: "s"}},
		{"invalid tenant", Config{APIAddr: "http://localhost:8081", TenantID: "tenant_bad", SiteID: "s"}},
		{"invalid site", Config{APIAddr: "http://localhost:8081", TenantID: "t", SiteID: "site bad"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Fatalf("expected invalid config to be rejected")
			}
		})
	}
}
