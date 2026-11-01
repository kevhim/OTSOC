package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
)

type Config struct {
	APIAddr           string   `json:"api_addr"`
	TenantID          string   `json:"tenant_id"`
	SiteID            string   `json:"site_id"`
	ProcessInterval   string   `json:"process_interval,omitempty"`
	InventoryInterval string   `json:"inventory_interval,omitempty"`
	NetworkInterval   string   `json:"network_interval,omitempty"`
	MonitorPaths      []string `json:"monitor_paths,omitempty"`
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

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

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
	parsed, err := url.Parse(c.APIAddr)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("api_addr must be a valid http(s) base URL without credentials, query, or fragment")
	}
	for _, id := range []struct{ name, value string }{
		{"tenant_id", c.TenantID}, {"site_id", c.SiteID},
	} {
		if len(id.value) > 128 || !idPattern.MatchString(id.value) {
			return fmt.Errorf("%s must be 1-128 letters, digits, or hyphens", id.name)
		}
	}
	return nil
}
