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
					return OutcomeRecoveredDLQ
				}

				// DLQ failed: Best-effort emergency spill to disk.
				spillBytes, marshalErr := json.Marshal(ev)
				if marshalErr != nil {
					log.Printf("ERROR: Event %s failed to serialize for emergency spill: %v", ev.EventID, marshalErr)
					if e.cfg.OnFatal != nil {
						e.cfg.OnFatal(ev, marshalErr)
						return OutcomeDropped
					}
					log.Printf("FATAL: Event %s serialization failed during emergency spill: %v. Dropping.", ev.EventID, marshalErr)
					return OutcomeDropped
				}

				spillErr := appendEmergencySpill(e.cfg.EmergencySpillPath, append(spillBytes, '\n'))
				if spillErr == nil {
					log.Printf("CRITICAL: Event %s spilled to disk (BEST-EFFORT) due to uncertain commit and DLQ failure.", ev.EventID)
					return OutcomeSpilledDisk
				}

				// Terminal behavior
				if e.cfg.OnFatal != nil {
					e.cfg.OnFatal(ev, storeErr)
					return OutcomeDropped
				}
				log.Fatalf("FATAL: Event %s uncertain commit, DLQ failed (%v), and emergency spill failed (%v). Terminating.", ev.EventID, dlqErr, spillErr)
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
		return OutcomeDropped
	}
}

// emergencySpillMu serializes concurrent emergency spills to avoid partial line interleaving.
var emergencySpillMu sync.Mutex

// appendEmergencySpill preserves all recovery records in a JSON-lines file.
// os.WriteFile truncated the file on every failure, silently losing any earlier
// events saved during the same outage. This path is emergency-only: sync before
// reporting a successful spill and preserve restrictive file permissions.
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
