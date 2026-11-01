package resourcereport

// RuleInfo describes a detection rule loaded by the endpoint.
type RuleInfo struct {
	RuleID   string `json:"rule_id"`
	Version  string `json:"version"`
	Severity string `json:"severity"`
}

// Manifest captures endpoint runtime metadata, hardware topology, and measurement bounds.
type Manifest struct {
	SourceRevision     string     `json:"source_revision"`
	Dirty              bool       `json:"dirty"`
	OS                 string     `json:"os"`
	Architecture       string     `json:"architecture"`
	Toolchain          string     `json:"toolchain"`
	LogicalCPUs        int        `json:"logical_cpus"`
	Profile            string     `json:"profile"`
	Rules              []RuleInfo `json:"rules"`
	SamplingIntervalMs int64      `json:"sampling_interval_ms"`
	DurationMs         int64      `json:"duration_ms"`
	SampleLimit        int        `json:"sample_limit"`
	OutputByteLimit    int64      `json:"output_byte_limit"`
}

// ContributingFile details an individual persistence file and its measured size.
type ContributingFile struct {
	Path      string  `json:"path"`
	SizeBytes *uint64 `json:"size_bytes"`
	Exists    bool    `json:"exists"`
	Error     string  `json:"error,omitempty"`
}

// Sample records an individual measurement snapshot.
// Absent or invalid measurements are represented as null (pointers), never fabricated as zero.
type Sample struct {
	SampleIndex                int                `json:"sample_index"`
	Timestamp                  string             `json:"timestamp"` // RFC3339 UTC
	CPUPercent                 *float64           `json:"cpu_percent"`
	RSSBytes                   *uint64            `json:"rss_bytes"`
	PersistenceBytes           *uint64            `json:"persistence_bytes"`
	ContributingFiles          []ContributingFile `json:"contributing_files"`
	WriteIOBytesPerSec         *float64           `json:"write_io_bytes_per_sec"`
	QueueDepth                 *uint64            `json:"queue_depth"`
	QueueCapacity              *uint64            `json:"queue_capacity"`
	QueueSaturationPercent     *float64           `json:"queue_saturation_percent"`
	QueueOldestEventAgeSeconds *float64           `json:"queue_oldest_event_age_seconds"`
	BackpressureEventsTotal    *uint64            `json:"backpressure_events_total,omitempty"`
	Connectivity               string             `json:"connectivity"`
	GovernorState              string             `json:"governor_state"`
	SamplingError              *string            `json:"sampling_error,omitempty"`
	EventsDroppedTotal         *uint64            `json:"events_dropped_total"`    // kept null (future priority sampling)
	EventsAggregatedTotal      *uint64            `json:"events_aggregated_total"` // kept null (future priority sampling)
}

// EventAccountingSummary aggregates pipeline event accounting metrics into the report summary.
type EventAccountingSummary struct {
	IngestedTotal           uint64 `json:"ingested_total"`
	CommittedTotal          uint64 `json:"committed_total"`
	ForwardedTotal          uint64 `json:"forwarded_total"`
	FailedBeforeCommitTotal uint64 `json:"failed_before_commit_total"`
	RecoveredDLQTotal       uint64 `json:"recovered_dlq_total"`
	SpilledDiskTotal        uint64 `json:"spilled_disk_total"`
	DroppedTotal            uint64 `json:"dropped_total"`
	BackpressureEventsTotal uint64 `json:"backpressure_events_total"`
	ReconciliationValid     bool   `json:"reconciliation_valid"`
}

// Summary records high-level statistics and the explicit completion notice.
type Summary struct {
	TotalSamples    int                     `json:"total_samples"`
	DegradedSamples int                     `json:"degraded_samples"`
	ReportingErrors int                     `json:"reporting_errors"`
	ShutdownOutcome string                  `json:"shutdown_outcome"`
	EventAccounting *EventAccountingSummary `json:"event_accounting,omitempty"`
	Notice          string                  `json:"notice"`
}

// Report is the root document of a bounded machine-readable measurement report.
type Report struct {
	Manifest Manifest `json:"manifest"`
	Samples  []Sample `json:"samples"`
	Summary  Summary  `json:"summary"`
}
