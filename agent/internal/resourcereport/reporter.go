package resourcereport

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
)

const QualificationNotice = "Notice: Report completion does not equate to event-accounting reconciliation, zero telemetry loss, or Phase 4 qualification."

// Reporter aggregates measurement samples, manages bounds, and persists the final machine-readable report.
type Reporter struct {
	mu              sync.Mutex
	cfg             Config
	manifest        Manifest
	samples         []Sample
	degradedSamples int
	reportingErrors int
}

// NewReporter creates a new initialized Reporter.
func NewReporter(cfg Config, rules []RuleInfo) *Reporter {
	manifest := PopulateManifest(cfg, rules)
	return &Reporter{
		cfg:      cfg,
		manifest: manifest,
		samples:  make([]Sample, 0, 64),
	}
}

// AddSample appends a sample, tracking degraded status and sample bounds.
func (r *Reporter) AddSample(sample Sample) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	maxSamples := r.manifest.SampleLimit
	if len(r.samples) >= maxSamples {
		return false
	}

	if sample.GovernorState != "NORMAL" || sample.SamplingError != nil {
		r.degradedSamples++
	}

	r.samples = append(r.samples, sample)
	return len(r.samples) < maxSamples
}

// IncrementReportingError visibly logs and tracks a reporting error.
func (r *Reporter) IncrementReportingError(format string, v ...interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reportingErrors++
	log.Printf("ERROR: [Measurement Reporter] "+format, v...)
}

// Finalize produces the final Report and writes it to disk if ReportPath is set.
func (r *Reporter) Finalize(outcome string) (*Report, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rep := &Report{
		Manifest: r.manifest,
		Samples:  r.samples,
		Summary: Summary{
			TotalSamples:    len(r.samples),
			DegradedSamples: r.degradedSamples,
			ReportingErrors: r.reportingErrors,
			ShutdownOutcome: outcome,
			Notice:          QualificationNotice,
		},
	}

	if r.cfg.ReportPath == "" {
		return rep, nil
	}

	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		r.reportingErrors++
		log.Printf("ERROR: [Measurement Reporter] failed to marshal report JSON: %v", err)
		return rep, fmt.Errorf("marshal report: %w", err)
	}

	maxBytes := r.manifest.OutputByteLimit
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		r.reportingErrors++
		log.Printf("ERROR: [Measurement Reporter] generated report size (%d bytes) exceeds output limit (%d bytes)", len(data), maxBytes)
		return rep, fmt.Errorf("report size %d bytes exceeds output limit %d bytes", len(data), maxBytes)
	}

	// Exclusive create; refuse overwrite
	f, err := os.OpenFile(r.cfg.ReportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		r.reportingErrors++
		log.Printf("ERROR: [Measurement Reporter] failed to open report destination %s exclusively: %v", r.cfg.ReportPath, err)
		return rep, fmt.Errorf("open report destination exclusively: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		r.reportingErrors++
		log.Printf("ERROR: [Measurement Reporter] failed to write report to %s: %v", r.cfg.ReportPath, err)
		return rep, fmt.Errorf("write report: %w", err)
	}

	log.Printf("Successfully wrote measurement report to %s (%d samples, %d bytes)", r.cfg.ReportPath, len(r.samples), len(data))
	return rep, nil
}

// InspectContributingFiles inspects DB, WAL, SHM, and emergency spill files to record exact file status.
func InspectContributingFiles(dbPath string, spillPath string) []ContributingFile {
	paths := []string{
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
	}
	if spillPath != "" {
		paths = append(paths, spillPath)
	}

	res := make([]ContributingFile, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			res = append(res, ContributingFile{
				Path:      p,
				SizeBytes: nil,
				Exists:    false,
			})
			continue
		}
		if err != nil {
			res = append(res, ContributingFile{
				Path:      p,
				SizeBytes: nil,
				Exists:    false,
				Error:     err.Error(),
			})
			continue
		}

		size := uint64(info.Size())
		res = append(res, ContributingFile{
			Path:      p,
			SizeBytes: &size,
			Exists:    true,
		})
	}
	return res
}
