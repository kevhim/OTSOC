package resourcereport

import (
	"runtime"
	"runtime/debug"
)

var (
	// Injected via -ldflags during build or discovered from debug.ReadBuildInfo
	BuildCommit = "unknown"
	BuildDirty  = "false"
)

// PopulateManifest constructs a Manifest struct reflecting current binary and environment state.
func PopulateManifest(cfg Config, rules []RuleInfo) Manifest {
	commit := BuildCommit
	dirty := BuildDirty == "true"

	if cfg.SourceRevision != "" {
		commit = cfg.SourceRevision
		dirty = cfg.Dirty
	} else if commit == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && commit == "unknown" {
					commit = setting.Value
				}
				if setting.Key == "vcs.modified" && BuildDirty == "false" {
					dirty = setting.Value == "true"
				}
			}
		}
	}

	maxSamples := cfg.MaxSamples
	if maxSamples <= 0 {
		maxSamples = DefaultMaxSamples
	}

	maxOutputBytes := cfg.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = DefaultMaxOutputBytes
	}

	return Manifest{
		SourceRevision:     commit,
		Dirty:              dirty,
		OS:                 runtime.GOOS,
		Architecture:       runtime.GOARCH,
		Toolchain:          runtime.Version(),
		LogicalCPUs:        runtime.NumCPU(),
		Profile:            cfg.Profile,
		Rules:              rules,
		SamplingIntervalMs: cfg.Interval.Milliseconds(),
		DurationMs:         cfg.Duration.Milliseconds(),
		SampleLimit:        maxSamples,
		OutputByteLimit:    maxOutputBytes,
	}
}
