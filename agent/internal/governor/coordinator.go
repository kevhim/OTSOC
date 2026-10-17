package governor

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"
)

var (
	ErrMissingStorageBudget = errors.New("storage budget is unconfigured or unresolved")
	ErrInvalidQueueCapacity = errors.New("queue capacity must be greater than zero")
	ErrQueueDepthExceedsCap = errors.New("queue depth cannot exceed capacity")
	ErrUnavailableRSS       = errors.New("process memory RSS measurement unavailable")
)

// CoordinatorConfig configures the resource integration coordinator.
type CoordinatorConfig struct {
	// StorageBudgetBytes specifies the authoritative total persistence budget in bytes.
	// If 0, storage pressure calculation is blocked (unresolved).
	StorageBudgetBytes uint64
}

// RuntimeSignals provides the internal in-memory and connectivity telemetry
// gathered from agent runtime components (queues, network).
type RuntimeSignals struct {
	QueueDepth        ValidUint64
	QueueCapacity     ValidUint64
	QueueOldestAgeSec ValidFloat64
	NetworkState      NetworkState
}

// StepResult represents the outcome of a deterministic coordination step.
type StepResult struct {
	Ready          bool              // false if initial baseline is still being established
	Evaluated      bool              // true if governor.Evaluate was invoked
	Signals        ResourceSignals   // constructed signals fed to governor
	GovernorState  HostResourceState // state after evaluation
	StorageBlocked bool              // true if storage pressure calculation was blocked
	Error          error             // explicit error encountered during integration
}

// Coordinator deterministically wires OS telemetry, runtime queue state, and
// connectivity signals into the Governor and GovernorMetrics on demand.
// It executes synchronously and contains no background goroutines, tickers, or hidden polling.
type Coordinator struct {
	mu          sync.Mutex
	provider    OSResourceProvider
	governor    *Governor
	metrics     *GovernorMetrics
	config      CoordinatorConfig
	nowFunc     NowFunc
	cpuInitDone bool
}

// NewCoordinator creates a new deterministic integration coordinator.
func NewCoordinator(
	provider OSResourceProvider,
	gov *Governor,
	metrics *GovernorMetrics,
	cfg CoordinatorConfig,
	now NowFunc,
) (*Coordinator, error) {
	if provider == nil {
		return nil, errors.New("provider must not be nil")
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		provider: provider,
		governor: gov,
		metrics:  metrics,
		config:   cfg,
		nowFunc:  now,
	}, nil
}

// CalculateStoragePressure computes the storage pressure ratio (0.0 to 1.0)
// from observed usedBytes against the authoritative configured budget.
// If budgetBytes is 0, it returns ErrMissingStorageBudget and an invalid measurement.
// It never invents a denominator, defaults to 50 MiB, or silently falls back to zero.
func CalculateStoragePressure(usedBytes ValidUint64, budgetBytes uint64) (ValidFloat64, error) {
	if budgetBytes == 0 {
		return ValidFloat64{Valid: false}, ErrMissingStorageBudget
	}
	if !usedBytes.Valid {
		return ValidFloat64{Valid: false}, nil
	}
	ratio := float64(usedBytes.Value) / float64(budgetBytes)
	if ratio > 1.0 {
		ratio = 1.0
	}
	return ValidFloat64{Valid: true, Value: ratio}, nil
}

// CalculateQueueSaturation computes queue saturation (0.0 to 1.0) from depth and capacity.
// If capacity is 0 or invalid, it returns ErrInvalidQueueCapacity and an invalid measurement.
// If depth exceeds capacity, it returns ErrQueueDepthExceedsCap and an invalid measurement.
func CalculateQueueSaturation(depth, capacity ValidUint64) (ValidFloat64, error) {
	if !capacity.Valid || capacity.Value == 0 {
		return ValidFloat64{Valid: false}, ErrInvalidQueueCapacity
	}
	if !depth.Valid {
		return ValidFloat64{Valid: false}, nil
	}
	if depth.Value > capacity.Value {
		return ValidFloat64{Valid: false}, ErrQueueDepthExceedsCap
	}
	return ValidFloat64{Valid: true, Value: float64(depth.Value) / float64(capacity.Value)}, nil
}

// Step performs one deterministic, on-demand integration step:
// 1. Checks context cancellation.
// 2. Samples OS telemetry from the provider.
// 3. Updates GovernorMetrics while preserving validity/presence semantics.
// 4. Manages CPU baseline establishment vs genuine subsequent failure.
// 5. Calculates storage pressure against the authoritative budget (blocked if 0).
// 6. Calculates queue saturation guarding invalid capacity.
// 7. Evaluates the Governor if baseline initialization is ready.
func (c *Coordinator) Step(ctx context.Context, runtimeSigs RuntimeSignals) (StepResult, error) {
	select {
	case <-ctx.Done():
		return StepResult{}, ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var result StepResult
	var stepErr error

	// 1. Sample OS Provider
	rawCPU, _, cpuErr := c.provider.SampleCPU(ctx)
	rawMem, _, memErr := c.provider.SampleMemory(ctx)
	rawUsedBytes, rawWriteBytesSec, _, storageErr := c.provider.SampleStorage(ctx)

	// 2. Resolve CPU measurement validity (Requirement 1 & 2)
	var cpuMetric ValidFloat64
	var cpuVal float64 = math.NaN()
	var cpuWarmup bool

	if cpuErr != nil {
		// If SampleCPU returns an error, CPU metric must be unavailable regardless of Valid flag or value.
		// Never accept a CPU value when the provider returned an error.
		// Do not mark initialization complete merely because a failed call returned an invalid measurement.
		cpuMetric = ValidFloat64{Valid: false}
		cpuVal = math.NaN()
		stepErr = errors.Join(stepErr, cpuErr)
	} else if !c.cpuInitDone {
		// Only treat the initial unavailable, error-free CPU sample as expected baseline establishment.
		if !rawCPU.Valid {
			c.cpuInitDone = true
			cpuWarmup = true
			cpuMetric = ValidFloat64{Valid: false}
			cpuVal = math.NaN()
		} else {
			// Provider was already primed with baseline error-free
			c.cpuInitDone = true
			cpuMetric = rawCPU
			cpuVal = rawCPU.Value
		}
	} else {
		// Already initialized: subsequent sample
		if rawCPU.Valid {
			cpuMetric = rawCPU
			cpuVal = rawCPU.Value
		} else {
			// Genuine subsequent failure (e.g. invalid measurement)
			cpuMetric = ValidFloat64{Valid: false}
			cpuVal = math.NaN()
		}
	}

	// 3. Resolve Memory RSS measurement validity (Requirement 1 & 2)
	var memMetric ValidUint64
	var memVal uint64
	var memInvalid bool

	if memErr != nil {
		// If SampleMemory returns an error, RSS metric must be unavailable regardless of Valid flag or value.
		memMetric = ValidUint64{Valid: false}
		memInvalid = true
		stepErr = errors.Join(stepErr, memErr)
	} else if !rawMem.Valid {
		// Measurement unavailable error-free
		memMetric = ValidUint64{Valid: false}
		memInvalid = true
		stepErr = errors.Join(stepErr, ErrUnavailableRSS)
	} else {
		memMetric = rawMem
		memVal = rawMem.Value
	}

	// 4. Resolve Storage measurement validity (Requirement 1 & 2)
	var storageMetricUsed ValidUint64
	var storageMetricWrite ValidFloat64

	if storageErr != nil {
		// If SampleStorage returns an error, both storage-used and write-I/O-rate metrics must be unavailable.
		storageMetricUsed = ValidUint64{Valid: false}
		storageMetricWrite = ValidFloat64{Valid: false}
		stepErr = errors.Join(stepErr, storageErr)
	} else {
		storageMetricUsed = rawUsedBytes
		storageMetricWrite = rawWriteBytesSec
	}

	// Calculate Storage Pressure using resolved storage metric (Requirement 3: detect unconfigured budget)
	var storageVal float64 = math.NaN()
	storagePressure, spErr := CalculateStoragePressure(storageMetricUsed, c.config.StorageBudgetBytes)
	if spErr != nil {
		result.StorageBlocked = true
		stepErr = errors.Join(stepErr, spErr)
	} else if storagePressure.Valid {
		storageVal = storagePressure.Value
	} else {
		storageVal = math.NaN()
	}

	// 5. Resolve Queue signals and calculate saturation
	queueDepthMetric := runtimeSigs.QueueDepth
	queueCapMetric := runtimeSigs.QueueCapacity
	queueAgeMetric := runtimeSigs.QueueOldestAgeSec

	var queueVal float64 = math.NaN()
	queueSat, qErr := CalculateQueueSaturation(queueDepthMetric, queueCapMetric)
	if qErr != nil {
		queueVal = math.NaN()
		stepErr = errors.Join(stepErr, qErr)
	} else if queueSat.Valid {
		queueVal = queueSat.Value
	} else {
		queueVal = math.NaN()
	}

	// 6. Feed metrics from the exact same validated measurement results (Requirement 2)
	if c.metrics != nil {
		if err := c.metrics.UpdateCPU(cpuMetric); err != nil {
			stepErr = errors.Join(stepErr, err)
		}
		c.metrics.UpdateMemory(memMetric)
		if err := c.metrics.UpdateDisk(storageMetricUsed, storageMetricWrite); err != nil {
			stepErr = errors.Join(stepErr, err)
		}
		if err := c.metrics.UpdateQueue(queueDepthMetric, queueCapMetric, queueAgeMetric); err != nil {
			stepErr = errors.Join(stepErr, err)
		}
	}

	// 7. Network State
	netState := runtimeSigs.NetworkState
	if netState == "" {
		netState = NetworkUnknown
	}

	// 8. Construct signals for governor evaluation from same validated measurements
	signals := ResourceSignals{
		CPUUtilizationPercent: cpuVal,
		MemoryRSSBytes:        memVal,
		StoragePressure:       storageVal,
		QueueSaturation:       queueVal,
		NetworkState:          netState,
	}

	result.Signals = signals
	result.Error = stepErr

	if c.governor != nil {
		result.GovernorState = c.governor.CurrentState()
	}

	// Determine readiness and evaluation:
	// - cpuWarmup: not ready, do not evaluate
	// - !c.cpuInitDone (CPU API failed during init): not ready, do not evaluate
	// - memInvalid: not ready, do not evaluate (do not evaluate with fabricated 0 RSS)
	// - result.StorageBlocked: do not evaluate while storage budget is unresolved
	if cpuWarmup || !c.cpuInitDone || memInvalid {
		result.Ready = false
		result.Evaluated = false
		return result, stepErr
	}

	result.Ready = true

	// If storage budget is unconfigured/blocked, step must be marked not evaluated
	if result.StorageBlocked {
		result.Evaluated = false
		return result, stepErr
	}

	// Evaluate governor
	if c.governor != nil {
		c.governor.Evaluate(signals)
		result.Evaluated = true
		result.GovernorState = c.governor.CurrentState()
	}

	return result, stepErr
}
