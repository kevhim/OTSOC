package config

import (
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	APIAddr  string `json:"api_addr"`
	TenantID        string `json:"tenant_id"`
	SiteID          string `json:"site_id"`
	ProcessInterval string `json:"process_interval,omitempty"`
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.APIAddr == "" {
		return errors.New("api_addr is required")
	}
	if c.TenantID == "" {
		return errors.New("tenant_id is required")
	}
	if c.SiteID == "" {
		return errors.New("site_id is required")
	}
	return nil
}
