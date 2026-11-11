package resourcereport

import (
	"context"
	"database/sql"
	"log"
	"time"
)

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
	SampleIndex                 int                `json:"sample_index"`
	Timestamp                   string             `json:"timestamp"` // RFC3339 UTC
	CPUPercent                  *float64           `json:"cpu_percent"`
	RSSBytes                    *uint64            `json:"rss_bytes"`
	PersistenceBytes            *uint64            `json:"persistence_bytes"`
	ContributingFiles           []ContributingFile `json:"contributing_files"`
	WriteIOBytesPerSec          *float64           `json:"write_io_bytes_per_sec"`
	QueueDepth                  *uint64            `json:"queue_depth"`
	QueueCapacity               *uint64            `json:"queue_capacity"`
	QueueSaturationPercent      *float64           `json:"queue_saturation_percent"`
	QueueOldestEventAgeSeconds  *float64           `json:"queue_oldest_event_age_seconds"`
	BlockedEnqueueAttemptsTotal *uint64            `json:"blocked_enqueue_attempts_total,omitempty"`
	PressureEpisodesTotal       *uint64            `json:"pressure_episodes_total,omitempty"`
	BackpressureEventsTotal     *uint64            `json:"backpressure_events_total,omitempty"` // alias for BlockedEnqueueAttemptsTotal
	Connectivity                string             `json:"connectivity"`
	GovernorState               string             `json:"governor_state"`
	SamplingError               *string            `json:"sampling_error,omitempty"`
	EventsDroppedTotal          *uint64            `json:"events_dropped_total"`    // kept null (future priority sampling)
	EventsAggregatedTotal       *uint64            `json:"events_aggregated_total"` // kept null (future priority sampling)
}

// DurableStorageSnapshot records durable database row counts inspected prior to database close.
// Distinguishes source telemetry from derived detection findings, pending events, DLQ, and backlog.
type DurableStorageSnapshot struct {
	InitialBacklogPendingEvents *uint64 `json:"initial_backlog_pending_events,omitempty"`
	DurablePendingEvents        *uint64 `json:"durable_pending_events"`
	DurableDLQEvents            *uint64 `json:"durable_dlq_events"`
	DurableTotalEvents          *uint64 `json:"durable_total_events"`
	SourceTelemetryEvents       *uint64 `json:"source_telemetry_events"`
	DerivedFindingsEvents       *uint64 `json:"derived_findings_events"`
	PrunedEventsTotal           *uint64 `json:"pruned_events_total"` // kept null (retention pruning not active in Phase 4)
}

// IngestionOutcomeSummary records the durability boundary outcomes of the local ingestion engine.
// Note: This is an Ingestion-Outcome Reconciliation, NOT an end-to-end reconciliation or proof of zero loss.
type IngestionOutcomeSummary struct {
	IngestedTotal           uint64 `json:"ingested_total"`
	CommittedTotal          uint64 `json:"committed_total"`
	FailedBeforeCommitTotal uint64 `json:"failed_before_commit_total"`
	RecoveredDLQTotal       uint64 `json:"recovered_dlq_total"`
	SpilledDiskTotal        uint64 `json:"spilled_disk_total"`
	DroppedTotal            uint64 `json:"dropped_total"`
	ReconciliationValid     bool   `json:"reconciliation_valid"`
}

// ForwarderAccountingSummary summarizes upstream forwarding and local deletion outcomes.
//
// HTTP Accepted Accounting Contract:
// HTTPAcceptedTotal records total accepted HTTP responses (HTTP 202) received from the upstream
// ingest server, including any repeats/retries. It represents accepted HTTP responses, NOT a claim
// of unique-event delivery.
type ForwarderAccountingSummary struct {
	AttemptsTotal           uint64 `json:"attempts_total"`
	HTTPAcceptedTotal       uint64 `json:"http_accepted_total"`
	DLQTotal                uint64 `json:"dlq_total"`
	RetryTotal              uint64 `json:"retry_total"`
	LocalRemovedTotal       uint64 `json:"local_removed_total"`
	LocalRemovalErrorsTotal uint64 `json:"local_removal_errors_total"`
}

// QueueAccountingSummary summarizes in-memory queue metrics.
//
// Backpressure Accounting Scoping:
// AdapterBlockedEnqueueAttemptsTotal (and its backward-compatibility alias BlockedEnqueueAttemptsTotal)
// counts discrete enqueue attempts where the input adapter (inPump) or direct caller was blocked
// waiting for downstream queue capacity. When collectors submit via the channel adapter In(),
// this metric counts the adapter's blocked waits, rather than an exact count of all concurrent OS
// collectors waiting on the Go channel rendezvous.
type QueueAccountingSummary struct {
	AdapterBlockedEnqueueAttemptsTotal uint64  `json:"adapter_blocked_enqueue_attempts_total"`
	BlockedEnqueueAttemptsTotal        uint64  `json:"blocked_enqueue_attempts_total"`
	PressureEpisodesTotal              uint64  `json:"pressure_episodes_total"`
	TimeSpentFullSeconds               float64 `json:"time_spent_full_seconds"`
}

// EventAccountingSummary aggregates all verified accounting boundaries into the final report.
// Note: Ingestion-outcome reconciliation verifies that all events accepted by the local ingestion loop
// reached an explicit, observable terminal or durable outcome. It is not an end-to-end reconciliation
// and does not prove zero loss across upstream network tiers.
type EventAccountingSummary struct {
	IngestionOutcome IngestionOutcomeSummary    `json:"ingestion_outcome"`
	Forwarder        ForwarderAccountingSummary `json:"forwarder"`
	Queue            QueueAccountingSummary     `json:"queue"`
	Storage          DurableStorageSnapshot     `json:"storage"`

	// Flat compatibility fields for existing consumers
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

// InspectDurableStorage inspects the open SQLite database to capture exact durable row counts.
func InspectDurableStorage(db *sql.DB, initialBacklog *uint64) DurableStorageSnapshot {
	snap := DurableStorageSnapshot{
		InitialBacklogPendingEvents: initialBacklog,
		PrunedEventsTotal:           nil, // explicitly null in Phase 4
	}

	if db == nil {
		return snap
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var pendingCount uint64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&pendingCount); err == nil {
		snap.DurablePendingEvents = &pendingCount
	} else {
		log.Printf("ERROR: [Storage Inspection] failed to query pending events: %v", err)
	}

	var dlqCount uint64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dead_letter_queue").Scan(&dlqCount); err == nil {
		snap.DurableDLQEvents = &dlqCount
	} else {
		log.Printf("ERROR: [Storage Inspection] failed to query DLQ events: %v", err)
	}

	var totalCount uint64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&totalCount); err == nil {
		snap.DurableTotalEvents = &totalCount
	} else {
		log.Printf("ERROR: [Storage Inspection] failed to query total events: %v", err)
	}

	var findingsCount uint64
	queryFindings := "SELECT COUNT(*) FROM events WHERE payload LIKE '%\"category\":\"detection/finding\"%'"
	if err := db.QueryRowContext(ctx, queryFindings).Scan(&findingsCount); err == nil {
		snap.DerivedFindingsEvents = &findingsCount
	} else {
		log.Printf("ERROR: [Storage Inspection] failed to query derived findings: %v", err)
	}

	var sourceCount uint64
	querySource := "SELECT COUNT(*) FROM events WHERE payload NOT LIKE '%\"category\":\"detection/finding\"%'"
	if err := db.QueryRowContext(ctx, querySource).Scan(&sourceCount); err == nil {
		snap.SourceTelemetryEvents = &sourceCount
	} else {
		log.Printf("ERROR: [Storage Inspection] failed to query source telemetry: %v", err)
	}

	return snap
}
