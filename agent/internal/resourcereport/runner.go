package resourcereport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"redcyberfox/agent/internal/governor"
)

// RunMeasurementRunner coordinates sampling ticks, snapshots live governor signals,
// accumulates samples, and invokes the shared shutdown path upon measurement completion.
func RunMeasurementRunner(
	ctx context.Context,
	cfg Config,
	reporter *Reporter,
	coordinator *governor.Coordinator,
	metrics *governor.GovernorMetrics,
	signalsFn func() governor.RuntimeSignals,
	shutdownCoord *ShutdownCoordinator,
	accountingFns ...func() *EventAccountingSummary,
) error {
	if coordinator == nil || metrics == nil || signalsFn == nil || shutdownCoord == nil {
		return errors.New("measurement runner requires non-nil coordinator, metrics, signalsFn, and shutdownCoord")
	}

	var getAccounting func() *EventAccountingSummary
	if len(accountingFns) > 0 {
		getAccounting = accountingFns[0]
	}

	measureCtx, measureCancel := context.WithTimeout(ctx, cfg.Duration)
	defer measureCancel()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	var sampleIndex int

	onStep := func(result governor.StepResult, stepErr error) {
		sampleIndex++
		snap := metrics.Snapshot()

		var cpuPtr *float64
		if snap.AgentCPUPercent.Valid {
			v := snap.AgentCPUPercent.Value
			cpuPtr = &v
		}

		var rssPtr *uint64
		if snap.AgentRSSBytes.Valid {
			v := snap.AgentRSSBytes.Value
			rssPtr = &v
		}

		var storagePtr *uint64
		if snap.DiskStorageUsedBytes.Valid {
			v := snap.DiskStorageUsedBytes.Value
			storagePtr = &v
		}

		var ioPtr *float64
		if snap.DiskWritePressureBytesSec.Valid {
			v := snap.DiskWritePressureBytesSec.Value
			ioPtr = &v
		}

		var qDepthPtr *uint64
		if snap.QueueDepthCount.Valid {
			v := snap.QueueDepthCount.Value
			qDepthPtr = &v
		}

		var qCapPtr *uint64
		if snap.QueueCapacityCount.Valid {
			v := snap.QueueCapacityCount.Value
			qCapPtr = &v
		}

		var qSatPtr *float64
		if snap.QueueSaturationPercent.Valid {
			v := snap.QueueSaturationPercent.Value
			qSatPtr = &v
		}

		var qAgePtr *float64
		if snap.QueueOldestEventAgeSeconds.Valid {
			v := snap.QueueOldestEventAgeSeconds.Value
			qAgePtr = &v
		}

		var bpPtr *uint64
		if getAccounting != nil {
			if acc := getAccounting(); acc != nil {
				bpVal := acc.BackpressureEventsTotal
				bpPtr = &bpVal
			}
		}

		var errStrPtr *string
		if stepErr != nil {
			s := stepErr.Error()
			errStrPtr = &s
		}

		contributing := InspectContributingFiles(cfg.DBPath, cfg.EmergencySpillPath)

		sample := Sample{
			SampleIndex:                sampleIndex,
			Timestamp:                  time.Now().UTC().Format(time.RFC3339),
			CPUPercent:                 cpuPtr,
			RSSBytes:                   rssPtr,
			PersistenceBytes:           storagePtr,
			ContributingFiles:          contributing,
			WriteIOBytesPerSec:         ioPtr,
			QueueDepth:                 qDepthPtr,
			QueueCapacity:              qCapPtr,
			QueueSaturationPercent:     qSatPtr,
			QueueOldestEventAgeSeconds: qAgePtr,
			BackpressureEventsTotal:    bpPtr,
			Connectivity:               string(result.Signals.NetworkState),
			GovernorState:              string(snap.GovernorState),
			SamplingError:              errStrPtr,
		}

		moreAllowed := reporter.AddSample(sample)
		log.Printf("[MEASUREMENT SAMPLE #%d] state=%s cpu=%v rss=%v storage=%v queue=%v oldestAge=%v network=%s",
			sampleIndex, snap.GovernorState, snap.AgentCPUPercent, snap.AgentRSSBytes,
			snap.DiskStorageUsedBytes, snap.QueueSaturationPercent, snap.QueueOldestEventAgeSeconds, result.Signals.NetworkState)

		if !moreAllowed {
			log.Printf("[MEASUREMENT RUNNER] Reached sample limit (%d), stopping runner early", reporter.manifest.SampleLimit)
			measureCancel()
		}
	}

	err := governor.RunSampling(measureCtx, ticker.C, coordinator, signalsFn, onStep)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		reporter.IncrementReportingError("sampling error: %v", err)
	}

	outcome := "MEASUREMENT_COMPLETE"
	if ctx.Err() != nil {
		outcome = shutdownCoord.Outcome()
	} else {
		// Measurement finished naturally: trigger the shared shutdown path
		shutdownCoord.Trigger("MEASUREMENT_COMPLETE")
	}

	snap := metrics.Snapshot()
	log.Printf("[MEASUREMENT COMPLETE] Total samples: %d, Final state: %s, Transitions: %d",
		sampleIndex, snap.GovernorState, snap.TotalStateTransitions)

	if getAccounting != nil {
		reporter.SetAccounting(getAccounting())
	}

	if _, finErr := reporter.Finalize(outcome); finErr != nil {
		return fmt.Errorf("finalize report: %w", finErr)
	}

	return nil
}
