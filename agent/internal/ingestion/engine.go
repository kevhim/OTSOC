package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// EventOutcome describes the terminal or committed outcome of an event processed by the ingestion engine.
type EventOutcome string

const (
	OutcomeCommitted          EventOutcome = "COMMITTED"
	OutcomeFailedBeforeCommit EventOutcome = "FAILED_BEFORE_COMMIT"
	OutcomeRecoveredDLQ       EventOutcome = "RECOVERED_DLQ"
	OutcomeSpilledDisk        EventOutcome = "SPILLED_DISK"
	OutcomeDropped            EventOutcome = "DROPPED"
)

// IngestionOutcomeAccounting records observable event durability outcomes across the ingestion engine.
//
// Ingestion-Outcome Reconciliation Contract:
// This equation verifies that every event accepted by the local ingestion loop reached an explicit,
// observable terminal or durability outcome (Committed, FailedBeforeCommit, RecoveredDLQ, SpilledDisk, Dropped).
// It specifically accounts for the local ingestion boundary. It is NOT an end-to-end delivery reconciliation
// and DOES NOT prove zero loss across upstream network tiers, server processing, or storage retention pruning.
type IngestionOutcomeAccounting struct {
	IngestedTotal           uint64 `json:"ingested_total"`
	CommittedTotal          uint64 `json:"committed_total"`
	FailedBeforeCommitTotal uint64 `json:"failed_before_commit_total"`
	RecoveredDLQTotal       uint64 `json:"recovered_dlq_total"`
	SpilledDiskTotal        uint64 `json:"spilled_disk_total"`
	DroppedTotal            uint64 `json:"dropped_total"`
}

// IngestionOutcomeReconciled asserts the ingestion-outcome accounting invariant:
// IngestedTotal == CommittedTotal + FailedBeforeCommitTotal + RecoveredDLQTotal + SpilledDiskTotal + DroppedTotal
func (a IngestionOutcomeAccounting) IngestionOutcomeReconciled() bool {
	return a.IngestedTotal == (a.CommittedTotal + a.FailedBeforeCommitTotal + a.RecoveredDLQTotal + a.SpilledDiskTotal + a.DroppedTotal)
}

// Reconciled is an alias for IngestionOutcomeReconciled for backward compatibility.
func (a IngestionOutcomeAccounting) Reconciled() bool {
	return a.IngestionOutcomeReconciled()
}

// Accounting is an alias for IngestionOutcomeAccounting for backward compatibility.
type Accounting = IngestionOutcomeAccounting

// Config configures the shared ingestion engine.
type Config struct {
	TenantID           string
	SiteID             string
	AssetID            string
	RetryDelay         time.Duration
	EmergencySpillPath string
	OnCommitted        func(ctx context.Context, ev *events.CanonicalEvent)
	OnDropped          func(ev *events.CanonicalEvent, reason string, err error)
	OnDLQ              func(ev *events.CanonicalEvent, reason string)
	OnFatal            func(ev *events.CanonicalEvent, err error)
	TestInjectError    func(ctx context.Context, ev *events.CanonicalEvent) error
}

// Engine implements the single authoritative ingestion loop shared between production
// endpoint runtime and integration/regression tests.
type Engine struct {
	db  interfaces.Storage
	cfg Config

	accMu                   sync.Mutex
	ingestedTotal           uint64
	committedTotal          uint64
	failedBeforeCommitTotal uint64
	recoveredDLQTotal       uint64
	spilledDiskTotal        uint64
	droppedTotal            uint64
}

// NewEngine creates a new shared ingestion engine.
func NewEngine(db interfaces.Storage, cfg Config) *Engine {
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 100 * time.Millisecond
	}
	if cfg.EmergencySpillPath == "" {
		cfg.EmergencySpillPath = "agent_emergency_spill.log"
	}
	return &Engine{
		db:  db,
		cfg: cfg,
	}
}

func (e *Engine) recordIngest() {
	e.accMu.Lock()
	defer e.accMu.Unlock()
	e.ingestedTotal++
}

func (e *Engine) recordOutcome(outcome EventOutcome) {
	e.accMu.Lock()
	defer e.accMu.Unlock()
	switch outcome {
	case OutcomeCommitted:
		e.committedTotal++
	case OutcomeFailedBeforeCommit:
		e.failedBeforeCommitTotal++
	case OutcomeRecoveredDLQ:
		e.recoveredDLQTotal++
	case OutcomeSpilledDisk:
		e.spilledDiskTotal++
	case OutcomeDropped:
		e.droppedTotal++
	}
}

// Accounting returns a synchronized, instantaneous snapshot of the ingestion pipeline accounting statistics.
func (e *Engine) Accounting() IngestionOutcomeAccounting {
	e.accMu.Lock()
	defer e.accMu.Unlock()
	return IngestionOutcomeAccounting{
		IngestedTotal:           e.ingestedTotal,
		CommittedTotal:          e.committedTotal,
		FailedBeforeCommitTotal: e.failedBeforeCommitTotal,
		RecoveredDLQTotal:       e.recoveredDLQTotal,
		SpilledDiskTotal:        e.spilledDiskTotal,
		DroppedTotal:            e.droppedTotal,
	}
}

// Run drains centralEvents until closed, applying enrichment and durability boundaries.
func (e *Engine) Run(ctx, drainCtx context.Context, centralEvents <-chan *events.CanonicalEvent) {
	for {
		select {
		case ev, ok := <-centralEvents:
			if !ok {
				// Channel closed, draining completed
				return
			}

			// Enrichment Boundary (Rule: tenant/site/asset identity injected before durability)
			e.enrich(ev)
			e.ProcessEvent(ctx, drainCtx, ev)
		}
	}
}

func (e *Engine) enrich(ev *events.CanonicalEvent) {
	if e.cfg.TenantID != "" && ev.TenantID == "" {
		ev.TenantID = e.cfg.TenantID
	}
	if e.cfg.SiteID != "" && ev.SiteID == "" {
		ev.SiteID = e.cfg.SiteID
	}
	if e.cfg.AssetID != "" && ev.AssetID == "" {
		ev.AssetID = e.cfg.AssetID
	}
}

// ProcessEvent processes a single event through the durability and recovery pipeline.
func (e *Engine) ProcessEvent(ctx, drainCtx context.Context, ev *events.CanonicalEvent) EventOutcome {
	e.recordIngest()
	e.enrich(ev)
	storeCtx := ctx
	if ctx.Err() != nil {
		storeCtx = drainCtx
	}

	var storeErr error
	attempts := 0

	for {
		if e.cfg.TestInjectError != nil {
			storeErr = e.cfg.TestInjectError(storeCtx, ev)
		} else {
			storeErr = e.db.Store(storeCtx, ev)
		}

		// CASE 1: COMMITTED (Success)
		if storeErr == nil {
			if e.cfg.OnCommitted != nil {
				e.cfg.OnCommitted(storeCtx, ev)
			}
			e.recordOutcome(OutcomeCommitted)
			return OutcomeCommitted
		}

		// CASE 2: FAILED BEFORE COMMIT
		if errors.Is(storeErr, storage.ErrStoreFailedBeforeCommit) {
			if errors.Is(storeErr, context.Canceled) && storeCtx == ctx {
				// In-flight event interrupted by lifecycle cancellation.
				// Retry under global drain context using the SAME event_id.
				storeCtx = drainCtx
				continue
			}

			// Non-retryable cause or drain deadline expired: Explicit terminal handling
			if e.cfg.OnDropped != nil {
				e.cfg.OnDropped(ev, "failed_before_commit", storeErr)
			} else {
				log.Printf("ERROR: Event %s explicitly dropped (failed before commit): %v", ev.EventID, storeErr)
			}
			e.recordOutcome(OutcomeFailedBeforeCommit)
			return OutcomeFailedBeforeCommit
		}

		// CASE 3: UNCERTAIN COMMIT
		if errors.Is(storeErr, storage.ErrStoreUncertain) {
			attempts++
			if attempts >= 3 {
				log.Printf("CRITICAL: Event %s uncertain commit after 3 attempts. Attempting recovery.", ev.EventID)

				// Recovery metadata: move to DLQ.
				// Bounded by active storeCtx so it cannot escape global shutdown deadline.
				dlqCtx, dlqCancel := context.WithTimeout(storeCtx, 2*time.Second)
				dlqErr := e.db.MoveToDLQ(dlqCtx, ev, "uncertain_commit", storeErr.Error())
				dlqCancel()

				if dlqErr == nil {
					log.Printf("INFO: Event %s successfully recovered to DLQ (metadata only).", ev.EventID)
					if e.cfg.OnDLQ != nil {
						e.cfg.OnDLQ(ev, "uncertain_commit")
					}
					e.recordOutcome(OutcomeRecoveredDLQ)
					return OutcomeRecoveredDLQ
				}

				// DLQ failed: Best-effort emergency spill to disk.
				spillBytes, marshalErr := json.Marshal(ev)
				if marshalErr != nil {
					log.Printf("ERROR: Event %s failed to serialize for emergency spill: %v", ev.EventID, marshalErr)
					if e.cfg.OnFatal != nil {
						e.cfg.OnFatal(ev, marshalErr)
						e.recordOutcome(OutcomeDropped)
						return OutcomeDropped
					}
					log.Printf("FATAL: Event %s serialization failed during emergency spill: %v. Dropping.", ev.EventID, marshalErr)
					e.recordOutcome(OutcomeDropped)
					return OutcomeDropped
				}

				spillErr := appendEmergencySpill(e.cfg.EmergencySpillPath, append(spillBytes, '\n'))
				if spillErr == nil {
					log.Printf("CRITICAL: Event %s spilled to disk (BEST-EFFORT) due to uncertain commit and DLQ failure.", ev.EventID)
					e.recordOutcome(OutcomeSpilledDisk)
					return OutcomeSpilledDisk
				}

				// Terminal behavior
				if e.cfg.OnFatal != nil {
					e.cfg.OnFatal(ev, storeErr)
					e.recordOutcome(OutcomeDropped)
					return OutcomeDropped
				}
				log.Fatalf("FATAL: Event %s uncertain commit, DLQ failed (%v), and emergency spill failed (%v). Terminating.", ev.EventID, dlqErr, spillErr)
				e.recordOutcome(OutcomeDropped)
				return OutcomeDropped
			}

			// Cancellation-aware bounded retry wait
			timer := time.NewTimer(e.cfg.RetryDelay)
			select {
			case <-storeCtx.Done():
				timer.Stop()
				if storeCtx == ctx {
					storeCtx = drainCtx
					continue
				}
				log.Printf("ERROR: Event %s uncertain retry aborted due to global drain expiration.", ev.EventID)
			case <-timer.C:
			}

			if storeCtx.Err() != nil && storeCtx != ctx {
				if e.cfg.OnDropped != nil {
					e.cfg.OnDropped(ev, "uncertain_deadline_expired", storeCtx.Err())
				}
				e.recordOutcome(OutcomeDropped)
				return OutcomeDropped
			}
			continue
		}

		// Unknown/unhandled storage error
		if e.cfg.OnDropped != nil {
			e.cfg.OnDropped(ev, "unknown_storage_error", storeErr)
		} else {
			log.Printf("CRITICAL: Unknown storage error for event %s: %v", ev.EventID, storeErr)
		}
		e.recordOutcome(OutcomeDropped)
		return OutcomeDropped
	}
}

// emergencySpillMu serializes concurrent emergency spills to avoid partial line interleaving.
var emergencySpillMu sync.Mutex

// appendEmergencySpill preserves all recovery records in a JSON-lines file.
func appendEmergencySpill(path string, record []byte) error {
	emergencySpillMu.Lock()
	defer emergencySpillMu.Unlock()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(record)
	if writeErr == nil && n != len(record) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
