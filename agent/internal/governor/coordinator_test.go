package governor

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type flexibleMockProvider struct {
	sampleCPU     func(ctx context.Context) (ValidFloat64, time.Time, error)
	sampleMem     func(ctx context.Context) (ValidUint64, time.Time, error)
	sampleStorage func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error)
}

func (f *flexibleMockProvider) SampleCPU(ctx context.Context) (ValidFloat64, time.Time, error) {
	if f.sampleCPU != nil {
		return f.sampleCPU(ctx)
	}
	return ValidFloat64{Valid: true, Value: 0}, time.Now(), nil
}

func (f *flexibleMockProvider) SampleMemory(ctx context.Context) (ValidUint64, time.Time, error) {
	if f.sampleMem != nil {
		return f.sampleMem(ctx)
	}
	return ValidUint64{Valid: true, Value: 0}, time.Now(), nil
}

func (f *flexibleMockProvider) SampleStorage(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
	if f.sampleStorage != nil {
		return f.sampleStorage(ctx)
	}
	return ValidUint64{Valid: true, Value: 0}, ValidFloat64{Valid: true, Value: 0}, time.Now(), nil
}

// 1. Provider factory selection and platform build boundaries
func TestCoordinator_ProviderFactorySelection(t *testing.T) {
	p, err := NewOSResourceProvider([]string{"agent.db"}, time.Now)
	if err != nil {
		t.Fatalf("expected NewOSResourceProvider to succeed on supported platform, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil OSResourceProvider")
	}

	// Test NewCoordinator requires non-nil provider
	_, err = NewCoordinator(nil, nil, nil, CoordinatorConfig{}, nil)
	if err == nil {
		t.Error("expected error when provider is nil")
	}
}

// 2. Valid sample propagation into ResourceSignals and metrics
func TestCoordinator_ValidSamplePropagation(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: true, Value: 25.5}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 150 * 1024 * 1024}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 20 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 1024.0}, now, nil
		},
	}

	metrics := NewGovernorMetrics(func() time.Time { return now })
	gov, err := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("failed to create governor: %v", err)
	}

	coord, err := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
		StorageBudgetBytes: 100 * 1024 * 1024, // 100 MB
	}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	runtimeSigs := RuntimeSignals{
		QueueDepth:        ValidUint64{Valid: true, Value: 40},
		QueueCapacity:     ValidUint64{Valid: true, Value: 200},
		QueueOldestAgeSec: ValidFloat64{Valid: true, Value: 1.5},
		NetworkState:      NetworkOnline,
	}

	res, err := coord.Step(context.Background(), runtimeSigs)
	if err != nil {
		t.Fatalf("unexpected step error: %v", err)
	}

	if !res.Ready || !res.Evaluated {
		t.Errorf("expected ready and evaluated, got ready=%v evaluated=%v", res.Ready, res.Evaluated)
	}
	if res.Signals.CPUUtilizationPercent != 25.5 {
		t.Errorf("expected CPU 25.5, got %v", res.Signals.CPUUtilizationPercent)
	}
	if res.Signals.MemoryRSSBytes != 150*1024*1024 {
		t.Errorf("expected Memory 150MB, got %v", res.Signals.MemoryRSSBytes)
	}
	if res.Signals.StoragePressure != 0.2 { // 20MB / 100MB
		t.Errorf("expected StoragePressure 0.2, got %v", res.Signals.StoragePressure)
	}
	if res.Signals.QueueSaturation != 0.2 { // 40 / 200
		t.Errorf("expected QueueSaturation 0.2, got %v", res.Signals.QueueSaturation)
	}
	if res.Signals.NetworkState != NetworkOnline {
		t.Errorf("expected NetworkOnline, got %v", res.Signals.NetworkState)
	}

	// Verify metrics snapshot
	snap := metrics.Snapshot()
	if !snap.AgentCPUPercent.Valid || snap.AgentCPUPercent.Value != 25.5 {
		t.Errorf("expected metrics CPU 25.5, got %v", snap.AgentCPUPercent)
	}
	if !snap.AgentRSSBytes.Valid || snap.AgentRSSBytes.Value != 150*1024*1024 {
		t.Errorf("expected metrics RSS 150MB, got %v", snap.AgentRSSBytes)
	}
	if !snap.DiskStorageUsedBytes.Valid || snap.DiskStorageUsedBytes.Value != 20*1024*1024 {
		t.Errorf("expected metrics DiskStorageUsedBytes 20MB, got %v", snap.DiskStorageUsedBytes)
	}
	if !snap.DiskWritePressureBytesSec.Valid || snap.DiskWritePressureBytesSec.Value != 1024.0 {
		t.Errorf("expected metrics DiskWritePressureBytesSec 1024, got %v", snap.DiskWritePressureBytesSec)
	}
	if !snap.QueueSaturationPercent.Valid || snap.QueueSaturationPercent.Value != 0.2 {
		t.Errorf("expected metrics QueueSaturationPercent 0.2, got %v", snap.QueueSaturationPercent)
	}
}

// 3. Unavailable first CPU sample and explicit subsequent sampling failures
func TestCoordinator_CPUBaselineInitializationVsSubsequentFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	stepCount := 0

	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			stepCount++
			if stepCount == 1 {
				// Initial baseline establishment: unavailable
				return ValidFloat64{Valid: false}, now, nil
			} else if stepCount == 2 {
				// Second sample: valid measured rate
				return ValidFloat64{Valid: true, Value: 40.0}, now, nil
			}
			// Third sample: genuine sampling failure
			return ValidFloat64{Valid: false}, now, errors.New("proc read error")
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 50 * 1024 * 1024}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, now, nil
		},
	}

	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
	metrics := NewGovernorMetrics(func() time.Time { return now })
	coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, func() time.Time { return now })

	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
		NetworkState:  NetworkOnline,
	}

	// Step 1: Initial baseline sample
	res1, err := coord.Step(context.Background(), runtimeSigs)
	if err != nil {
		t.Fatalf("step 1 unexpected error: %v", err)
	}
	if res1.Ready {
		t.Error("step 1 expected Ready to be false during baseline establishment")
	}
	if res1.Evaluated {
		t.Error("step 1 expected Evaluated to be false")
	}
	if gov.CurrentState() != StateNormal {
		t.Errorf("step 1 expected governor to remain NORMAL, got: %v", gov.CurrentState())
	}
	if metrics.Snapshot().AgentCPUPercent.Valid {
		t.Error("step 1 expected metrics CPU to be invalid/unavailable")
	}

	// Step 2: Valid second sample
	res2, err := coord.Step(context.Background(), runtimeSigs)
	if err != nil {
		t.Fatalf("step 2 unexpected error: %v", err)
	}
	if !res2.Ready || !res2.Evaluated {
		t.Errorf("step 2 expected ready and evaluated, got ready=%v evaluated=%v", res2.Ready, res2.Evaluated)
	}
	if gov.CurrentState() != StateNormal {
		t.Errorf("step 2 expected governor NORMAL, got: %v", gov.CurrentState())
	}
	if res2.Signals.CPUUtilizationPercent != 40.0 {
		t.Errorf("step 2 expected CPU 40.0, got: %v", res2.Signals.CPUUtilizationPercent)
	}

	// Step 3: Genuine sampling failure after baseline was established
	res3, err := coord.Step(context.Background(), runtimeSigs)
	if err == nil {
		t.Error("step 3 expected error from genuine failure")
	}
	if !res3.Evaluated {
		t.Error("step 3 expected evaluation of fail-safe")
	}
	if gov.CurrentState() != StateEmergency {
		t.Errorf("step 3 expected EMERGENCY fail-safe on subsequent sampling failure, got: %v", gov.CurrentState())
	}
}

// 4. Storage-pressure calculation using actual configured budget
func TestCoordinator_StoragePressureCalculation(t *testing.T) {
	budget := uint64(50 * 1024 * 1024) // 50 MiB

	// Normal calculation
	used := ValidUint64{Valid: true, Value: 25 * 1024 * 1024} // 25 MiB
	p, err := CalculateStoragePressure(used, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.Valid || p.Value != 0.5 {
		t.Errorf("expected 0.5 ratio, got %v", p)
	}

	// Zero used
	usedZero := ValidUint64{Valid: true, Value: 0}
	pZero, err := CalculateStoragePressure(usedZero, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pZero.Valid || pZero.Value != 0.0 {
		t.Errorf("expected 0.0 ratio, got %v", pZero)
	}

	// Over budget capped at 1.0
	usedOver := ValidUint64{Valid: true, Value: 60 * 1024 * 1024}
	pOver, err := CalculateStoragePressure(usedOver, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pOver.Valid || pOver.Value != 1.0 {
		t.Errorf("expected 1.0 capped ratio, got %v", pOver)
	}

	// Unavailable usedBytes retains invalid semantics
	usedUnavail := ValidUint64{Valid: false}
	pUnavail, err := CalculateStoragePressure(usedUnavail, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pUnavail.Valid {
		t.Errorf("expected invalid ratio for unavailable usedBytes, got %v", pUnavail)
	}
}

// 5. Missing storage budget: explicit error/blocker, never an invented ratio
func TestCoordinator_MissingStorageBudgetBlocked(t *testing.T) {
	used := ValidUint64{Valid: true, Value: 25 * 1024 * 1024}

	// 0 budget represents unconfigured/unresolved budget
	p, err := CalculateStoragePressure(used, 0)
	if !errors.Is(err, ErrMissingStorageBudget) {
		t.Errorf("expected ErrMissingStorageBudget, got: %v", err)
	}
	if p.Valid {
		t.Error("expected invalid ratio when budget is missing")
	}

	// Test within Coordinator.Step
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: true, Value: 10.0}, time.Now(), nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 1024}, time.Now(), nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return used, ValidFloat64{Valid: true, Value: 0}, time.Now(), nil
		},
	}

	gov, _ := NewGovernor(defaultTestConfig(), nil, nil)
	coord, _ := NewCoordinator(mock, gov, nil, CoordinatorConfig{
		StorageBudgetBytes: 0, // Unconfigured / unresolved
	}, nil)

	res, err := coord.Step(context.Background(), RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	})
	if !errors.Is(err, ErrMissingStorageBudget) {
		t.Errorf("expected ErrMissingStorageBudget from Step, got: %v", err)
	}
	if !res.StorageBlocked {
		t.Error("expected StorageBlocked to be true")
	}
	if res.Evaluated {
		t.Error("expected Evaluated to be false when storage budget contract is unresolved")
	}
	if !math.IsNaN(res.Signals.StoragePressure) {
		t.Errorf("expected NaN StoragePressure when budget missing, got %v", res.Signals.StoragePressure)
	}
	// Explicitly confirm no false EMERGENCY transition is produced by configuration blocker
	if gov.CurrentState() != StateNormal {
		t.Errorf("expected governor to remain in NORMAL state without false EMERGENCY, got: %v", gov.CurrentState())
	}
}

// 6. Queue saturation calculated from real depth/capacity, including invalid capacity
func TestCoordinator_QueueSaturationValidation(t *testing.T) {
	// Normal
	q, err := CalculateQueueSaturation(ValidUint64{Valid: true, Value: 50}, ValidUint64{Valid: true, Value: 200})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !q.Valid || q.Value != 0.25 {
		t.Errorf("expected 0.25 saturation, got %v", q)
	}

	// Invalid capacity = 0
	_, err = CalculateQueueSaturation(ValidUint64{Valid: true, Value: 10}, ValidUint64{Valid: true, Value: 0})
	if !errors.Is(err, ErrInvalidQueueCapacity) {
		t.Errorf("expected ErrInvalidQueueCapacity, got: %v", err)
	}

	// Invalid capacity unavailable
	_, err = CalculateQueueSaturation(ValidUint64{Valid: true, Value: 10}, ValidUint64{Valid: false})
	if !errors.Is(err, ErrInvalidQueueCapacity) {
		t.Errorf("expected ErrInvalidQueueCapacity for unavailable capacity, got: %v", err)
	}

	// Depth > capacity
	_, err = CalculateQueueSaturation(ValidUint64{Valid: true, Value: 250}, ValidUint64{Valid: true, Value: 200})
	if !errors.Is(err, ErrQueueDepthExceedsCap) {
		t.Errorf("expected ErrQueueDepthExceedsCap, got: %v", err)
	}
}

// 7. Resource state transitions driven by supplied signals
func TestCoordinator_ResourceStateTransitions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	cfg := defaultTestConfig()
	cfg.CPUSaverThreshold = 80.0
	cfg.CPUEmergencyThreshold = 95.0
	cfg.CPURecoveryMargin = 10.0
	cfg.RecoveryDuration = 30 * time.Second

	gov, _ := NewGovernor(cfg, clock, nil)
	metrics := NewGovernorMetrics(clock)

	var currentCPU float64 = 50.0
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: true, Value: currentCPU}, clock(), nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 50 * 1024 * 1024}, clock(), nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, clock(), nil
		},
	}

	coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
		StorageBudgetBytes: 100 * 1024 * 1024,
	}, clock)

	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
		NetworkState:  NetworkOnline,
	}

	// Step 1: Normal (50%)
	res, _ := coord.Step(context.Background(), runtimeSigs)
	if res.GovernorState != StateNormal {
		t.Fatalf("expected NORMAL, got: %v", res.GovernorState)
	}

	// Step 2: Elevate to 85% -> RESOURCE-SAVER
	currentCPU = 85.0
	res, _ = coord.Step(context.Background(), runtimeSigs)
	if res.GovernorState != StateResourceSaver {
		t.Fatalf("expected RESOURCE-SAVER at 85%% CPU, got: %v", res.GovernorState)
	}

	// Step 3: Elevate to 96% -> EMERGENCY
	currentCPU = 96.0
	res, _ = coord.Step(context.Background(), runtimeSigs)
	if res.GovernorState != StateEmergency {
		t.Fatalf("expected EMERGENCY at 96%% CPU, got: %v", res.GovernorState)
	}

	// Step 4: Drop to 50% -> stays in EMERGENCY pending recovery duration
	currentCPU = 50.0
	res, _ = coord.Step(context.Background(), runtimeSigs)
	if res.GovernorState != StateEmergency {
		t.Fatalf("expected EMERGENCY immediately after drop, got: %v", res.GovernorState)
	}

	// Step 5: Advance clock 35s (> 30s RecoveryDuration) -> recovers to NORMAL
	now = now.Add(35 * time.Second)
	res, _ = coord.Step(context.Background(), runtimeSigs)
	if res.GovernorState != StateNormal {
		t.Fatalf("expected recovery to NORMAL after duration met, got: %v", res.GovernorState)
	}
}

// 8. Metrics retain unavailable measurements without replacing them with zero
func TestCoordinator_MetricsRetainUnavailableWithoutZeroReplacement(t *testing.T) {
	now := time.Now()
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: false}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: false}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: false}, ValidFloat64{Valid: false}, now, nil
		},
	}

	metrics := NewGovernorMetrics(func() time.Time { return now })
	coord, _ := NewCoordinator(mock, nil, metrics, CoordinatorConfig{
		StorageBudgetBytes: 100 * 1024 * 1024,
	}, func() time.Time { return now })

	_, _ = coord.Step(context.Background(), RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: false},
		QueueCapacity: ValidUint64{Valid: false},
	})

	snap := metrics.Snapshot()
	if snap.AgentCPUPercent.Valid {
		t.Error("expected AgentCPUPercent.Valid == false")
	}
	if snap.AgentRSSBytes.Valid {
		t.Error("expected AgentRSSBytes.Valid == false")
	}
	if snap.DiskStorageUsedBytes.Valid {
		t.Error("expected DiskStorageUsedBytes.Valid == false")
	}
	if snap.DiskWritePressureBytesSec.Valid {
		t.Error("expected DiskWritePressureBytesSec.Valid == false")
	}
	if snap.QueueSaturationPercent.Valid {
		t.Error("expected QueueSaturationPercent.Valid == false")
	}
}

// 9. Context cancellation and bounded, deterministic execution
func TestCoordinator_ContextCancellationAndBoundedExecution(t *testing.T) {
	mock := &flexibleMockProvider{}
	coord, _ := NewCoordinator(mock, nil, nil, CoordinatorConfig{
		StorageBudgetBytes: 100 * 1024 * 1024,
	}, nil)

	// Context already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := coord.Step(ctx, RuntimeSignals{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Bounded execution across 1,000 steps without leaking state or blocking
	validCtx := context.Background()
	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	}
	for i := 0; i < 1000; i++ {
		_, _ = coord.Step(validCtx, runtimeSigs)
	}
}

// 10. CPU sampling error handled before first baseline, never accepts value on error
func TestCoordinator_CPUSamplingErrorBeforeBaseline(t *testing.T) {
	now := time.Now()
	errProc := errors.New("cannot read process CPU")
	step := 0
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			step++
			if step == 1 {
				// Step 1: API error on initial sample
				// Even if value is returned, error must prevent accepting it
				return ValidFloat64{Valid: true, Value: 99.0}, now, errProc
			} else if step == 2 {
				// Step 2: Error-free unavailable sample establishes baseline
				return ValidFloat64{Valid: false}, now, nil
			}
			// Step 3: Valid sample
			return ValidFloat64{Valid: true, Value: 35.0}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, now, nil
		},
	}

	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
	coord, _ := NewCoordinator(mock, gov, nil, CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, func() time.Time { return now })

	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	}

	// Step 1: Failed API call
	res1, err1 := coord.Step(context.Background(), runtimeSigs)
	if !errors.Is(err1, errProc) {
		t.Fatalf("step 1 expected errProc, got: %v", err1)
	}
	if res1.Ready {
		t.Error("step 1 expected Ready to be false when CPU error occurred")
	}
	if res1.Evaluated {
		t.Error("step 1 expected Evaluated to be false")
	}
	if !math.IsNaN(res1.Signals.CPUUtilizationPercent) {
		t.Errorf("step 1 expected NaN CPU (must not accept value on error), got: %v", res1.Signals.CPUUtilizationPercent)
	}
	if gov.CurrentState() != StateNormal {
		t.Errorf("step 1 expected governor to remain NORMAL, got: %v", gov.CurrentState())
	}

	// Step 2: Now cleanly establish baseline with error-free unavailable sample
	res2, err2 := coord.Step(context.Background(), runtimeSigs)
	if err2 != nil {
		t.Fatalf("step 2 unexpected error: %v", err2)
	}
	if res2.Ready || res2.Evaluated {
		t.Errorf("step 2 expected ready=false, evaluated=false during baseline warm-up, got ready=%v evaluated=%v", res2.Ready, res2.Evaluated)
	}

	// Step 3: Now ready with valid CPU
	res3, err3 := coord.Step(context.Background(), runtimeSigs)
	if err3 != nil {
		t.Fatalf("step 3 unexpected error: %v", err3)
	}
	if !res3.Ready || !res3.Evaluated {
		t.Errorf("step 3 expected ready=true, evaluated=true, got ready=%v evaluated=%v", res3.Ready, res3.Evaluated)
	}
	if res3.Signals.CPUUtilizationPercent != 35.0 {
		t.Errorf("step 3 expected CPU 35.0, got: %v", res3.Signals.CPUUtilizationPercent)
	}
}

// 11. RSS measurement validity: unavailable or failed RSS is never evaluated as healthy zero
func TestCoordinator_RSSUnavailableOrFailedNeverEvaluatedAsZero(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	// Case A: RSS unavailable (Valid: false, err: nil)
	mockA := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: true, Value: 20.0}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: false}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, now, nil
		},
	}

	coordA, _ := NewCoordinator(mockA, gov, nil, CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, func() time.Time { return now })

	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	}

	resA, errA := coordA.Step(context.Background(), runtimeSigs)
	if !errors.Is(errA, ErrUnavailableRSS) {
		t.Fatalf("expected ErrUnavailableRSS, got: %v", errA)
	}
	if resA.Ready {
		t.Error("expected Ready to be false when RSS is unavailable")
	}
	if resA.Evaluated {
		t.Error("expected Evaluated to be false (must NOT evaluate fabricated 0 RSS as healthy)")
	}
	if gov.CurrentState() != StateNormal {
		t.Errorf("expected governor to remain NORMAL, got: %v", gov.CurrentState())
	}

	// Case B: RSS sampling error
	errMem := errors.New("failed to read memory")
	mockB := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			return ValidFloat64{Valid: true, Value: 20.0}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 1000}, now, errMem
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, now, nil
		},
	}

	coordB, _ := NewCoordinator(mockB, gov, nil, CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, func() time.Time { return now })

	resB, errB := coordB.Step(context.Background(), runtimeSigs)
	if !errors.Is(errB, errMem) {
		t.Fatalf("expected errMem, got: %v", errB)
	}
	if resB.Ready {
		t.Error("expected Ready to be false when RSS sampling failed")
	}
	if resB.Evaluated {
		t.Error("expected Evaluated to be false when RSS sampling failed")
	}
}

// 12. Missing storage budget detected even during CPU warm-up step
func TestCoordinator_MissingStorageBudgetDuringCPUWarmup(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			// First unavailable CPU sample: baseline establishment
			return ValidFloat64{Valid: false}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 0}, now, nil
		},
	}

	coord, _ := NewCoordinator(mock, gov, nil, CoordinatorConfig{
		StorageBudgetBytes: 0, // Unconfigured / missing storage budget
	}, func() time.Time { return now })

	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 10},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	}

	res, err := coord.Step(context.Background(), runtimeSigs)
	// Must report ErrMissingStorageBudget even on the first step during CPU warm-up!
	if !errors.Is(err, ErrMissingStorageBudget) {
		t.Fatalf("expected ErrMissingStorageBudget during CPU warm-up, got: %v", err)
	}
	if !res.StorageBlocked {
		t.Error("expected StorageBlocked to be true on first step with missing storage budget")
	}
	if res.Ready {
		t.Error("expected Ready to be false during warm-up")
	}
	if res.Evaluated {
		t.Error("expected Evaluated to be false")
	}
	if gov.CurrentState() != StateNormal {
		t.Errorf("expected governor to remain NORMAL (no false EMERGENCY), got: %v", gov.CurrentState())
	}
}

// 13. Metrics error propagation
func TestCoordinator_MetricsErrorPropagation(t *testing.T) {
	now := time.Now()
	mock := &flexibleMockProvider{
		sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
			// CPU value > 100 which causes metrics.UpdateCPU to fail
			return ValidFloat64{Valid: true, Value: 150.0}, now, nil
		},
		sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, now, nil
		},
		sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
			return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: -5.0}, now, nil // negative write rate causes UpdateDisk to fail
		},
	}

	metrics := NewGovernorMetrics(func() time.Time { return now })
	coord, _ := NewCoordinator(mock, nil, metrics, CoordinatorConfig{
		StorageBudgetBytes: 50 * 1024 * 1024,
	}, func() time.Time { return now })

	// Queue depth > capacity causes UpdateQueue to fail
	runtimeSigs := RuntimeSignals{
		QueueDepth:    ValidUint64{Valid: true, Value: 250},
		QueueCapacity: ValidUint64{Valid: true, Value: 100},
	}

	res, err := coord.Step(context.Background(), runtimeSigs)
	if err == nil {
		t.Fatal("expected aggregated errors from metrics updates, got nil")
	}
	if res.Error == nil {
		t.Fatal("expected res.Error to capture metrics errors")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error string from aggregated metrics errors")
	}
}

// 14. Provider returning Valid: true with an error marks affected metric(s) unavailable in GovernorMetrics,
// and errors remain discoverable via errors.Is.
func TestCoordinator_ProviderErrorsMarkMetricsUnavailableAndDiscoverable(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	errCPU := errors.New("sample CPU failure")
	errRSS := errors.New("sample memory RSS failure")
	errStorage := errors.New("sample storage failure")

	t.Run("CPUErrorWithValidTrue", func(t *testing.T) {
		mock := &flexibleMockProvider{
			sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
				// Provider returns Valid: true and a value alongside an error
				return ValidFloat64{Valid: true, Value: 75.0}, now, errCPU
			},
			sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 64 * 1024 * 1024}, now, nil
			},
			sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 256.0}, now, nil
			},
		}

		metrics := NewGovernorMetrics(func() time.Time { return now })
		gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
		coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
			StorageBudgetBytes: 100 * 1024 * 1024,
		}, func() time.Time { return now })

		res, err := coord.Step(context.Background(), RuntimeSignals{
			QueueDepth:    ValidUint64{Valid: true, Value: 10},
			QueueCapacity: ValidUint64{Valid: true, Value: 100},
		})

		// Error discoverability with errors.Is
		if !errors.Is(err, errCPU) {
			t.Fatalf("expected errors.Is(err, errCPU) to be true, got: %v", err)
		}
		if !errors.Is(res.Error, errCPU) {
			t.Fatalf("expected errors.Is(res.Error, errCPU) to be true, got: %v", res.Error)
		}

		// Resulting metrics snapshot: affected metric must be unavailable
		snap := metrics.Snapshot()
		if snap.AgentCPUPercent.Valid {
			t.Errorf("expected AgentCPUPercent.Valid to be false when CPU provider returned error, got %v", snap.AgentCPUPercent)
		}
		// Unaffected metrics must be recorded accurately
		if !snap.AgentRSSBytes.Valid || snap.AgentRSSBytes.Value != 64*1024*1024 {
			t.Errorf("expected unaffected AgentRSSBytes 64MB, got: %v", snap.AgentRSSBytes)
		}
		if !snap.DiskStorageUsedBytes.Valid || snap.DiskStorageUsedBytes.Value != 10*1024*1024 {
			t.Errorf("expected unaffected DiskStorageUsedBytes 10MB, got: %v", snap.DiskStorageUsedBytes)
		}
		if !snap.DiskWritePressureBytesSec.Valid || snap.DiskWritePressureBytesSec.Value != 256.0 {
			t.Errorf("expected unaffected DiskWritePressureBytesSec 256, got: %v", snap.DiskWritePressureBytesSec)
		}
	})

	t.Run("RSSErrorWithValidTrue", func(t *testing.T) {
		mock := &flexibleMockProvider{
			sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
				return ValidFloat64{Valid: true, Value: 30.0}, now, nil
			},
			sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
				// Provider returns Valid: true and a value alongside an error
				return ValidUint64{Valid: true, Value: 128 * 1024 * 1024}, now, errRSS
			},
			sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 10 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 128.0}, now, nil
			},
		}

		metrics := NewGovernorMetrics(func() time.Time { return now })
		gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
		coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
			StorageBudgetBytes: 100 * 1024 * 1024,
		}, func() time.Time { return now })

		res, err := coord.Step(context.Background(), RuntimeSignals{
			QueueDepth:    ValidUint64{Valid: true, Value: 10},
			QueueCapacity: ValidUint64{Valid: true, Value: 100},
		})

		// Error discoverability with errors.Is
		if !errors.Is(err, errRSS) {
			t.Fatalf("expected errors.Is(err, errRSS) to be true, got: %v", err)
		}
		if !errors.Is(res.Error, errRSS) {
			t.Fatalf("expected errors.Is(res.Error, errRSS) to be true, got: %v", res.Error)
		}

		// Resulting metrics snapshot: affected metric must be unavailable
		snap := metrics.Snapshot()
		if snap.AgentRSSBytes.Valid {
			t.Errorf("expected AgentRSSBytes.Valid to be false when memory provider returned error, got %v", snap.AgentRSSBytes)
		}
		// Unaffected metrics
		if !snap.AgentCPUPercent.Valid || snap.AgentCPUPercent.Value != 30.0 {
			t.Errorf("expected unaffected AgentCPUPercent 30.0, got: %v", snap.AgentCPUPercent)
		}
		if !snap.DiskStorageUsedBytes.Valid || snap.DiskStorageUsedBytes.Value != 10*1024*1024 {
			t.Errorf("expected unaffected DiskStorageUsedBytes 10MB, got: %v", snap.DiskStorageUsedBytes)
		}
	})

	t.Run("StorageErrorWithValidTrue", func(t *testing.T) {
		mock := &flexibleMockProvider{
			sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
				return ValidFloat64{Valid: true, Value: 30.0}, now, nil
			},
			sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 64 * 1024 * 1024}, now, nil
			},
			sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
				// Provider returns Valid: true and values alongside an error
				return ValidUint64{Valid: true, Value: 20 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 1024.0}, now, errStorage
			},
		}

		metrics := NewGovernorMetrics(func() time.Time { return now })
		gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
		coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
			StorageBudgetBytes: 100 * 1024 * 1024,
		}, func() time.Time { return now })

		res, err := coord.Step(context.Background(), RuntimeSignals{
			QueueDepth:    ValidUint64{Valid: true, Value: 10},
			QueueCapacity: ValidUint64{Valid: true, Value: 100},
		})

		// Error discoverability with errors.Is
		if !errors.Is(err, errStorage) {
			t.Fatalf("expected errors.Is(err, errStorage) to be true, got: %v", err)
		}
		if !errors.Is(res.Error, errStorage) {
			t.Fatalf("expected errors.Is(res.Error, errStorage) to be true, got: %v", res.Error)
		}

		// Resulting metrics snapshot: both storage-used and write-I/O rate must be unavailable
		snap := metrics.Snapshot()
		if snap.DiskStorageUsedBytes.Valid {
			t.Errorf("expected DiskStorageUsedBytes.Valid to be false when storage provider returned error, got %v", snap.DiskStorageUsedBytes)
		}
		if snap.DiskWritePressureBytesSec.Valid {
			t.Errorf("expected DiskWritePressureBytesSec.Valid to be false when storage provider returned error, got %v", snap.DiskWritePressureBytesSec)
		}
		// Unaffected metrics
		if !snap.AgentCPUPercent.Valid || snap.AgentCPUPercent.Value != 30.0 {
			t.Errorf("expected unaffected AgentCPUPercent 30.0, got: %v", snap.AgentCPUPercent)
		}
		if !snap.AgentRSSBytes.Valid || snap.AgentRSSBytes.Value != 64*1024*1024 {
			t.Errorf("expected unaffected AgentRSSBytes 64MB, got: %v", snap.AgentRSSBytes)
		}
	})

	t.Run("SimultaneousErrorsAllMetricsUnavailable", func(t *testing.T) {
		mock := &flexibleMockProvider{
			sampleCPU: func(ctx context.Context) (ValidFloat64, time.Time, error) {
				return ValidFloat64{Valid: true, Value: 50.0}, now, errCPU
			},
			sampleMem: func(ctx context.Context) (ValidUint64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 50 * 1024 * 1024}, now, errRSS
			},
			sampleStorage: func(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
				return ValidUint64{Valid: true, Value: 20 * 1024 * 1024}, ValidFloat64{Valid: true, Value: 512.0}, now, errStorage
			},
		}

		metrics := NewGovernorMetrics(func() time.Time { return now })
		gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
		coord, _ := NewCoordinator(mock, gov, metrics, CoordinatorConfig{
			StorageBudgetBytes: 100 * 1024 * 1024,
		}, func() time.Time { return now })

		res, err := coord.Step(context.Background(), RuntimeSignals{
			QueueDepth:    ValidUint64{Valid: true, Value: 10},
			QueueCapacity: ValidUint64{Valid: true, Value: 100},
		})

		// All 3 errors discoverable via errors.Is
		if !errors.Is(err, errCPU) {
			t.Errorf("expected errors.Is(err, errCPU), got: %v", err)
		}
		if !errors.Is(err, errRSS) {
			t.Errorf("expected errors.Is(err, errRSS), got: %v", err)
		}
		if !errors.Is(err, errStorage) {
			t.Errorf("expected errors.Is(err, errStorage), got: %v", err)
		}
		if !errors.Is(res.Error, errCPU) || !errors.Is(res.Error, errRSS) || !errors.Is(res.Error, errStorage) {
			t.Errorf("expected all 3 errors discoverable in res.Error, got: %v", res.Error)
		}

		// Resulting metrics snapshot: all affected metrics must be unavailable
		snap := metrics.Snapshot()
		if snap.AgentCPUPercent.Valid {
			t.Errorf("expected AgentCPUPercent.Valid to be false")
		}
		if snap.AgentRSSBytes.Valid {
			t.Errorf("expected AgentRSSBytes.Valid to be false")
		}
		if snap.DiskStorageUsedBytes.Valid {
			t.Errorf("expected DiskStorageUsedBytes.Valid to be false")
		}
		if snap.DiskWritePressureBytesSec.Valid {
			t.Errorf("expected DiskWritePressureBytesSec.Valid to be false")
		}
	})
}
