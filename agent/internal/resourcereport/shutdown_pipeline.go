package resourcereport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"

	"redcyberfox/agent/internal/eventqueue"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/interfaces"
)

// ShutdownParams configures the unified production shutdown sequence.
type ShutdownParams struct {
	Context              context.Context
	ShutdownCoord        *ShutdownCoordinator
	Collectors           []interfaces.Stopper
	EventQueue           *eventqueue.EventQueue
	IngestWait           *sync.WaitGroup
	Forwarder            interfaces.Stopper
	MeasureJoin          func() error
	Storage              interfaces.Storage
	RawDB                *sql.DB
	InitialPendingEvents *uint64
	IngestionAccounting  func() ingestion.IngestionOutcomeAccounting
	ForwarderAccounting  func() forwarder.ForwarderAccounting
	Reporter             *Reporter
}

// ExecuteShutdown runs the single authoritative production shutdown sequence.
//
// Shared Production Shutdown Contract:
// 1. Stop collectors: stops incoming event generation. Captures any stop errors.
// 2. Close event queue: stops accepting new items, signals waiters, drains buffer.
// 3. Wait for ingestion loop: finishes draining in-flight events and exits.
// 4. Stop forwarder: stops background forwarding loop. Captures any stop errors.
// 5. Join measurement runner: ensures measurement goroutine has fully exited. Captures runner errors.
// 6. Check drain timer: captures error if the global shutdown drain deadline expired.
// 7. Inspect durable storage: queries SQLite row counts and captures accounting snapshots before closing DB.
// 8. Close storage: closes SQLite database. Captures any close errors.
// 9. Finalize report: incorporates all errors (including DB close) into the final outcome and writes report file.
// 10. Aggregates all errors. Failures affect the final outcome and return non-nil error.
func ExecuteShutdown(params ShutdownParams) error {
	var shutdownErrors []error

	// 1. Stop collectors
	for _, col := range params.Collectors {
		if col != nil {
			if err := col.Stop(); err != nil {
				log.Printf("ERROR: [Shutdown] Collector stop failed: %v", err)
				shutdownErrors = append(shutdownErrors, fmt.Errorf("collector stop: %w", err))
			}
		}
	}

	// 2. Close event queue to drain the ingestion loop
	if params.EventQueue != nil {
		params.EventQueue.Close()
	}

	// 3. Wait for ingestion loop to finish draining and exit
	if params.IngestWait != nil {
		params.IngestWait.Wait()
	}
	if params.EventQueue != nil {
		params.EventQueue.WaitDrain()
	}

	// 4. Stop Forwarder (waits for forwarding loop to exit)
	if params.Forwarder != nil {
		if err := params.Forwarder.Stop(); err != nil {
			log.Printf("ERROR: [Shutdown] Forwarder stop failed: %v", err)
			shutdownErrors = append(shutdownErrors, fmt.Errorf("forwarder stop: %w", err))
		}
	}

	// 5. Join measurement runner (ensures measurement goroutine has fully exited)
	if params.MeasureJoin != nil {
		if err := params.MeasureJoin(); err != nil {
			log.Printf("ERROR: [Shutdown] Measurement runner error: %v", err)
			shutdownErrors = append(shutdownErrors, fmt.Errorf("measurement runner: %w", err))
		}
	}

	// 6. Check drain deadline timer
	if params.ShutdownCoord != nil && params.ShutdownCoord.TimerFired() {
		err := errors.New("shutdown drain deadline expired")
		log.Printf("ERROR: [Shutdown] %v", err)
		shutdownErrors = append(shutdownErrors, err)
	}

	// 7. Inspect durable storage & capture point-in-time accounting BEFORE closing storage
	var finalAccounting *EventAccountingSummary
	if params.Reporter != nil {
		rawDB := params.RawDB
		if rawDB == nil && params.Storage != nil {
			if dbGetter, ok := params.Storage.(interface{ GetDB() *sql.DB }); ok {
				rawDB = dbGetter.GetDB()
			}
		}
		storageSnap := InspectDurableStorage(rawDB, params.InitialPendingEvents)

		var ingestAcc IngestionOutcomeSummary
		if params.IngestionAccounting != nil {
			acc := params.IngestionAccounting()
			ingestAcc = IngestionOutcomeSummary{
				IngestedTotal:           acc.IngestedTotal,
				CommittedTotal:          acc.CommittedTotal,
				FailedBeforeCommitTotal: acc.FailedBeforeCommitTotal,
				RecoveredDLQTotal:       acc.RecoveredDLQTotal,
				SpilledDiskTotal:        acc.SpilledDiskTotal,
				DroppedTotal:            acc.DroppedTotal,
				ReconciliationValid:     acc.IngestionOutcomeReconciled(),
			}
		}

		var fwdAcc ForwarderAccountingSummary
		if params.ForwarderAccounting != nil {
			acc := params.ForwarderAccounting()
			fwdAcc = ForwarderAccountingSummary{
				AttemptsTotal:           acc.AttemptsTotal,
				HTTPAcceptedTotal:       acc.HTTPAcceptedTotal,
				DLQTotal:                acc.DLQTotal,
				RetryTotal:              acc.RetryTotal,
				LocalRemovedTotal:       acc.LocalRemovedTotal,
				LocalRemovalErrorsTotal: acc.LocalRemovalErrorsTotal,
			}
		}

		var qAcc QueueAccountingSummary
		if params.EventQueue != nil {
			qAcc = QueueAccountingSummary{
				AdapterBlockedEnqueueAttemptsTotal: params.EventQueue.AdapterBlockedEnqueueAttemptsTotal(),
				BlockedEnqueueAttemptsTotal:        params.EventQueue.BlockedEnqueueAttemptsTotal(),
				PressureEpisodesTotal:              params.EventQueue.PressureEpisodesTotal(),
				TimeSpentFullSeconds:               params.EventQueue.TimeSpentFullSeconds(),
			}
		}

		finalAccounting = &EventAccountingSummary{
			IngestionOutcome: ingestAcc,
			Forwarder:        fwdAcc,
			Queue:            qAcc,
			Storage:          storageSnap,

			// Flat compatibility fields
			IngestedTotal:           ingestAcc.IngestedTotal,
			CommittedTotal:          ingestAcc.CommittedTotal,
			ForwardedTotal:          fwdAcc.HTTPAcceptedTotal,
			FailedBeforeCommitTotal: ingestAcc.FailedBeforeCommitTotal,
			RecoveredDLQTotal:       ingestAcc.RecoveredDLQTotal,
			SpilledDiskTotal:        ingestAcc.SpilledDiskTotal,
			DroppedTotal:            ingestAcc.DroppedTotal,
			BackpressureEventsTotal: qAcc.BlockedEnqueueAttemptsTotal,
			ReconciliationValid:     ingestAcc.ReconciliationValid,
		}
	}

	// 8. Close Storage (captures close error before report finalization)
	if params.Storage != nil {
		if err := params.Storage.Close(); err != nil {
			log.Printf("ERROR: [Shutdown] Failed to close storage: %v", err)
			shutdownErrors = append(shutdownErrors, fmt.Errorf("storage close: %w", err))
		}
	}

	// 9. Finalize measurement report WITH DB-close outcome and all errors included
	if params.Reporter != nil {
		params.Reporter.SetAccounting(finalAccounting)

		outcome := "UNKNOWN"
		if params.ShutdownCoord != nil {
			outcome = params.ShutdownCoord.Outcome()
		}
		if len(shutdownErrors) > 0 {
			outcome = fmt.Sprintf("%s_WITH_ERRORS: %v", outcome, errors.Join(shutdownErrors...))
		}

		if _, err := params.Reporter.Finalize(outcome); err != nil {
			log.Printf("ERROR: [Shutdown] Failed to finalize measurement report: %v", err)
			shutdownErrors = append(shutdownErrors, fmt.Errorf("finalize report: %w", err))
		}
	}

	if len(shutdownErrors) > 0 {
		return errors.Join(shutdownErrors...)
	}
	return nil
}
