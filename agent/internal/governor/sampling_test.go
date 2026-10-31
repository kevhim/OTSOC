package governor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunSamplingFeedsRuntimeQueueAndStopsOnCancellation(t *testing.T) {
	provider := &mockOSProvider{
		cpu:    ValidFloat64{Value: 10, Valid: true},
		mem:    ValidUint64{Value: 100 * 1024 * 1024, Valid: true},
		stUsed: ValidUint64{Value: 10 * 1024 * 1024, Valid: true},
	}
	gov, err := NewGovernor(defaultTestConfig(), time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	coord, err := NewCoordinator(provider, gov, NewGovernorMetrics(time.Now), CoordinatorConfig{StorageBudgetBytes: 50 * 1024 * 1024}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	depth := uint64(10)
	ticks := make(chan time.Time, 1)
	results := make(chan StepResult, 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunSampling(ctx, ticks, coord, func() RuntimeSignals {
			return RuntimeSignals{
				QueueDepth: ValidUint64{Value: depth, Valid: true}, QueueCapacity: ValidUint64{Value: 100, Valid: true},
			}
		}, func(result StepResult, err error) {
			if err != nil {
				t.Errorf("unexpected sampling error: %v", err)
			}
			results <- result
		})
	}()

	select {
	case result := <-results:
		if !result.Evaluated || result.GovernorState != StateNormal || result.Signals.NetworkState != NetworkUnknown {
			t.Fatalf("unexpected initial result: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial sample did not finish")
	}
	depth = 95
	ticks <- time.Now()
	select {
	case result := <-results:
		if !result.Evaluated || result.GovernorState != StateEmergency || result.Signals.QueueSaturation != 0.95 {
			t.Fatalf("queue pressure did not reach governor: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tick sample did not finish")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sampling did not stop after cancellation")
	}
}

func TestRunSamplingIntegratesSQLiteQueueAndConnectivity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	if err := os.WriteFile(dbPath, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := &mockOSProvider{
		cpu: ValidFloat64{Value: 10, Valid: true}, mem: ValidUint64{Value: 100, Valid: true},
		stUsed: ValidUint64{Value: 100, Valid: true}, stRate: ValidFloat64{Value: 42, Valid: true},
	}
	provider := &sqliteResourceProvider{OSResourceProvider: base, dbPath: dbPath}
	config := defaultTestConfig()
	config.MemorySaverBytes = 200
	config.MemoryEmergencyBytes = 300
	config.MemoryRecoveryMarginBytes = 10
	config.StorageSaverRatio = 0.7
	config.StorageEmergencyRatio = 0.9
	config.QueueSaverRatio = 0.7
	config.QueueEmergencyRatio = 0.9
	config.RecoveryDuration = time.Second
	metrics := NewGovernorMetrics(func() time.Time { return now })
	gov, err := NewGovernor(config, func() time.Time { return now }, metrics.RecordTransition)
	if err != nil {
		t.Fatal(err)
	}
	coord, err := NewCoordinator(provider, gov, metrics, CoordinatorConfig{StorageBudgetBytes: 1000}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	queue := make(chan struct{}, 10)
	queue <- struct{}{}
	network := NetworkUnknown
	ticks := make(chan time.Time)
	type sample struct {
		result StepResult
		err    error
		snap   MetricsSnapshot
	}
	samples := make(chan sample)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunSampling(ctx, ticks, coord, func() RuntimeSignals {
			return RuntimeSignals{
				QueueDepth:        ValidUint64{Value: uint64(len(queue)), Valid: true},
				QueueCapacity:     ValidUint64{Value: uint64(cap(queue)), Valid: true},
				QueueOldestAgeSec: ValidFloat64{Valid: false},
				NetworkState:      network,
			}
		}, func(result StepResult, err error) {
			samples <- sample{result: result, err: err, snap: metrics.Snapshot()}
		})
	}()
	readSample := func() sample {
		t.Helper()
		select {
		case s := <-samples:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("resource sample did not finish")
			return sample{}
		}
	}
	tick := func() sample {
		t.Helper()
		select {
		case ticks <- now:
		case <-time.After(5 * time.Second):
			t.Fatal("sampler did not accept tick")
		}
		return readSample()
	}

	initial := readSample()
	if initial.err != nil || !initial.result.Evaluated || initial.result.GovernorState != StateNormal ||
		initial.result.Signals.NetworkState != NetworkUnknown || !initial.snap.DiskStorageUsedBytes.Valid ||
		initial.snap.DiskStorageUsedBytes.Value != 100 || initial.snap.QueueDepthCount.Value != 1 ||
		initial.snap.QueueOldestEventAgeSeconds.Valid {
		t.Fatalf("unexpected initial runtime sample: %+v", initial)
	}

	if err := os.WriteFile(dbPath+"-wal", make([]byte, 850), 0600); err != nil {
		t.Fatal(err)
	}
	network = NetworkOffline
	storagePressure := tick()
	if storagePressure.err != nil || storagePressure.result.GovernorState != StateEmergency ||
		storagePressure.result.Signals.StoragePressure != 0.95 ||
		storagePressure.result.Signals.NetworkState != NetworkOffline ||
		storagePressure.snap.DiskStorageUsedBytes.Value != 950 || storagePressure.snap.TotalStateTransitions != 1 {
		t.Fatalf("SQLite WAL pressure did not reach governor and metrics: %+v", storagePressure)
	}

	if err := os.Remove(dbPath + "-wal"); err != nil {
		t.Fatal(err)
	}
	for len(queue) < cap(queue) {
		queue <- struct{}{}
	}
	queuePressure := tick()
	if queuePressure.err != nil || queuePressure.result.GovernorState != StateEmergency ||
		queuePressure.result.Signals.QueueSaturation != 1 || queuePressure.snap.QueueDepthCount.Value != 10 ||
		queuePressure.snap.DiskStorageUsedBytes.Value != 100 {
		t.Fatalf("queue pressure or WAL removal was not observed: %+v", queuePressure)
	}

	if err := os.Mkdir(dbPath+"-wal", 0700); err != nil {
		t.Fatal(err)
	}
	invalidStorage := tick()
	if invalidStorage.err == nil || invalidStorage.snap.DiskStorageUsedBytes.Valid ||
		invalidStorage.snap.DiskWritePressureBytesSec.Valid {
		t.Fatalf("invalid WAL sidecar must invalidate storage metrics: %+v", invalidStorage)
	}
	if err := os.Remove(dbPath + "-wal"); err != nil {
		t.Fatal(err)
	}
	for len(queue) > 0 {
		<-queue
	}
	network = NetworkOnline
	firstRecovery := tick()
	if firstRecovery.err != nil || firstRecovery.result.GovernorState != StateEmergency ||
		firstRecovery.result.Signals.NetworkState != NetworkOnline {
		t.Fatalf("recovery must respect dwell time and observed connectivity: %+v", firstRecovery)
	}
	now = now.Add(2 * time.Second)
	recovered := tick()
	if recovered.err != nil || recovered.result.GovernorState != StateNormal ||
		recovered.snap.TotalStateTransitions != 2 || recovered.snap.DiskStorageUsedBytes.Value != 100 ||
		recovered.snap.QueueDepthCount.Value != 0 || recovered.snap.QueueOldestEventAgeSeconds.Valid {
		t.Fatalf("healthy runtime signals did not recover governor: %+v", recovered)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sampler did not stop")
	}
}
