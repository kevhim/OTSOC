package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	APIAddr  string `json:"api_addr"`
	TenantID string `json:"tenant_id"`
	SiteID   string `json:"site_id"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
