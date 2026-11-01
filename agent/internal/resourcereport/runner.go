package resourcereport

import (
	"context"
	"errors"
	"log"
	"time"

	"redcyberfox/agent/internal/governor"
)

// QueueStatsProvider returns current blocked enqueue attempts and saturation pressure episodes.
type QueueStatsProvider func() (blockedAttempts uint64, pressureEpisodes uint64)

// RunMeasurementRunner coordinates sampling ticks, snapshots live governor signals,
// and accumulates samples into the Reporter.
//
// Measurement Lifecycle Contract:
// Measurement sampling completion is strictly separated from report finalization.
// When the measurement duration expires, this runner triggers the shared shutdown path
// via shutdownCoord.Trigger("MEASUREMENT_COMPLETE") and exits.
// Report finalization occurs in the main shutdown sequence AFTER all collectors stop,
// the event queue drains, ingestion finishes, and the forwarder stops.
func RunMeasurementRunner(
	ctx context.Context,
	cfg Config,
	reporter *Reporter,
	coordinator *governor.Coordinator,
	metrics *governor.GovernorMetrics,
	signalsFn func() governor.RuntimeSignals,
	shutdownCoord *ShutdownCoordinator,
	queueStatsProviders ...QueueStatsProvider,
) error {
	if coordinator == nil || metrics == nil || signalsFn == nil || shutdownCoord == nil {
		return errors.New("measurement runner requires non-nil coordinator, metrics, signalsFn, and shutdownCoord")
	}

	var getQueueStats QueueStatsProvider
	if len(queueStatsProviders) > 0 {
		getQueueStats = queueStatsProviders[0]
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

		var blockedPtr *uint64
		var episodesPtr *uint64
		if getQueueStats != nil {
			blocked, episodes := getQueueStats()
			blockedPtr = &blocked
			episodesPtr = &episodes
		}

		var errStrPtr *string
		if stepErr != nil {
			s := stepErr.Error()
			errStrPtr = &s
		}

		contributing := InspectContributingFiles(cfg.DBPath, cfg.EmergencySpillPath)

		sample := Sample{
			SampleIndex:                 sampleIndex,
			Timestamp:                   time.Now().UTC().Format(time.RFC3339),
			CPUPercent:                  cpuPtr,
			RSSBytes:                    rssPtr,
			PersistenceBytes:            storagePtr,
			ContributingFiles:           contributing,
			WriteIOBytesPerSec:          ioPtr,
			QueueDepth:                  qDepthPtr,
			QueueCapacity:               qCapPtr,
			QueueSaturationPercent:      qSatPtr,
			QueueOldestEventAgeSeconds:  qAgePtr,
			BlockedEnqueueAttemptsTotal: blockedPtr,
			PressureEpisodesTotal:       episodesPtr,
			BackpressureEventsTotal:     blockedPtr, // alias
			Connectivity:                string(result.Signals.NetworkState),
			GovernorState:               string(snap.GovernorState),
			SamplingError:               errStrPtr,
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

	snap := metrics.Snapshot()
	log.Printf("[MEASUREMENT SAMPLING COMPLETE] Total samples: %d, Final state: %s, Transitions: %d",
		sampleIndex, snap.GovernorState, snap.TotalStateTransitions)

	// If measurement duration expired naturally (main context not canceled), trigger shared shutdown
	if ctx.Err() == nil {
		log.Printf("[MEASUREMENT RUNNER] Duration (%v) reached. Requesting shared endpoint shutdown...", cfg.Duration)
		shutdownCoord.Trigger("MEASUREMENT_COMPLETE")
	}

	// Final report finalization is intentionally performed in the main shutdown sequence
	// after all pipeline queues, ingestion, and storage writes have completely drained.
	return nil
}
