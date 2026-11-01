package resourcereport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Default limits to bound report allocation and output size.
const (
	DefaultMaxSamples     = 10000
	DefaultMaxOutputBytes = 10 * 1024 * 1024 // 10 MiB
	MinSamplingInterval   = 100 * time.Millisecond
)

// Config holds options for bounded measurement reporting.
type Config struct {
	ReportPath         string        // Destination file path (opt-in; empty string disables report writing)
	Profile            string        // Workload or hardware profile description
	Duration           time.Duration // Total measurement run duration
	Interval           time.Duration // Sampling interval
	MaxSamples         int           // Hard bound on maximum samples
	MaxOutputBytes     int64         // Hard bound on output file bytes
	DBPath             string        // Path to SQLite database to check for conflicts
	IdentityPath       string        // Path to identity file to check for conflicts
	ConfigPath         string        // Path to configuration file to check for conflicts
	EmergencySpillPath string        // Path to emergency spill log to check for conflicts
	WatchedPaths       []string      // Paths monitored by collectors to prevent self-generated telemetry
	SourceRevision     string        // Git commit SHA
	Dirty              bool          // Dirty status
}

// ValidateConfig validates the measurement report configuration against bounding and safety constraints.
func ValidateConfig(cfg Config) error {
	// If measurement mode is not requested (duration is 0 and no report path), validation is a no-op.
	if cfg.Duration == 0 && cfg.ReportPath == "" {
		return nil
	}

	if cfg.Duration <= 0 {
		return errors.New("measurement duration must be greater than 0")
	}

	if cfg.Interval < MinSamplingInterval {
		return fmt.Errorf("measurement interval (%v) must be at least %v", cfg.Interval, MinSamplingInterval)
	}

	if cfg.Interval > cfg.Duration {
		return fmt.Errorf("measurement interval (%v) must not exceed duration (%v)", cfg.Interval, cfg.Duration)
	}

	maxSamples := cfg.MaxSamples
	if maxSamples <= 0 {
		maxSamples = DefaultMaxSamples
	}
	expectedSamples := int(cfg.Duration / cfg.Interval)
	if expectedSamples > maxSamples {
		return fmt.Errorf("expected sample count (%d) exceeds configured sample limit (%d)", expectedSamples, maxSamples)
	}

	// Validate report destination safety if opt-in report path is provided
	if cfg.ReportPath != "" {
		absReport, err := filepath.Abs(filepath.Clean(cfg.ReportPath))
		if err != nil {
			return fmt.Errorf("invalid report path: %w", err)
		}

		// 1. Refuse overwrite: must not already exist
		if _, err := os.Stat(absReport); err == nil {
			return fmt.Errorf("report file %s already exists (refusing overwrite)", cfg.ReportPath)
		}

		// 2. Reject conflicts with DB, WAL, SHM, identity, config, and emergency spill
		protectedPaths := []string{
			cfg.ConfigPath,
			cfg.IdentityPath,
			cfg.EmergencySpillPath,
			cfg.DBPath,
			cfg.DBPath + "-wal",
			cfg.DBPath + "-shm",
		}

		for _, p := range protectedPaths {
			if p == "" {
				continue
			}
			absP, err := filepath.Abs(filepath.Clean(p))
			if err != nil {
				continue
			}
			if pathsEqual(absReport, absP) {
				return fmt.Errorf("report path %s conflicts with protected persistence file (%s)", cfg.ReportPath, p)
			}
		}

		// 3. Keep output outside watched paths to prevent self-generated filesystem telemetry
		for _, wp := range cfg.WatchedPaths {
			if wp == "" {
				continue
			}
			absWP, err := filepath.Abs(filepath.Clean(wp))
			if err != nil {
				continue
			}

			// Check if report path is inside watched directory or equals it
			if isInsideOrEqual(absReport, absWP) {
				return fmt.Errorf("report path %s is inside watched directory (%s), refusing to prevent self-generated telemetry", cfg.ReportPath, wp)
			}
		}
	}

	return nil
}

func pathsEqual(p1, p2 string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(p1, p2)
	}
	return p1 == p2
}

func isInsideOrEqual(child, parent string) bool {
	p1 := child
	p2 := parent
	if runtime.GOOS == "windows" {
		p1 = strings.ToLower(p1)
		p2 = strings.ToLower(p2)
	}

	rel, err := filepath.Rel(p2, p1)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
