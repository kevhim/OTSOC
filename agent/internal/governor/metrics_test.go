package governor

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestMetrics_CPU(t *testing.T) {
	m := NewGovernorMetrics(time.Now)

	// Valid
	err := m.UpdateCPU(ValidFloat64{Valid: true, Value: 50.0})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	snap := m.Snapshot()
	if !snap.AgentCPUPercent.Valid || snap.AgentCPUPercent.Value != 50.0 {
		t.Errorf("expected 50.0, got %v", snap.AgentCPUPercent)
	}

	// Invalid
	err = m.UpdateCPU(ValidFloat64{Valid: true, Value: -1.0})
	if err == nil {
		t.Error("expected error for negative CPU")
	}

	err = m.UpdateCPU(ValidFloat64{Valid: true, Value: math.NaN()})
	if err == nil {
		t.Error("expected error for NaN CPU")
	}

	err = m.UpdateCPU(ValidFloat64{Valid: true, Value: 101.0})
	if err == nil {
		t.Error("expected error for >100 CPU")
	}

	// Unavailable
	err = m.UpdateCPU(ValidFloat64{Valid: false})
	if err != nil {
		t.Errorf("expected no error for unavailable CPU, got %v", err)
	}
	snap = m.Snapshot()
	if snap.AgentCPUPercent.Valid {
		t.Error("expected CPU to be invalid/unavailable")
	}
	if snap.AgentCPUPercent.Value == 0 {
		// Value is 0 because of zero-init, but Valid is false, which is correct
	}
}

func TestMetrics_RSS(t *testing.T) {
	m := NewGovernorMetrics(time.Now)
	m.UpdateMemory(ValidUint64{Valid: true, Value: 1024})
	snap := m.Snapshot()
	if !snap.AgentRSSBytes.Valid || snap.AgentRSSBytes.Value != 1024 {
		t.Errorf("expected 1024, got %v", snap.AgentRSSBytes)
	}
}

func TestMetrics_Disk(t *testing.T) {
	m := NewGovernorMetrics(time.Now)
	err := m.UpdateDisk(ValidUint64{Valid: true, Value: 1000}, ValidFloat64{Valid: true, Value: 50.0})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	snap := m.Snapshot()
	if !snap.DiskStorageUsedBytes.Valid || snap.DiskStorageUsedBytes.Value != 1000 {
		t.Errorf("expected 1000 used, got %v", snap.DiskStorageUsedBytes)
	}
	if !snap.DiskWritePressureBytesSec.Valid || snap.DiskWritePressureBytesSec.Value != 50.0 {
		t.Errorf("expected 50 pressure, got %v", snap.DiskWritePressureBytesSec)
	}

	// Invalid pressure
	err = m.UpdateDisk(ValidUint64{Valid: true, Value: 1000}, ValidFloat64{Valid: true, Value: -1.0})
	if err == nil {
		t.Error("expected error for negative disk pressure")
	}
}

func TestMetrics_Queue(t *testing.T) {
	m := NewGovernorMetrics(time.Now)

	// Valid
	err := m.UpdateQueue(ValidUint64{Valid: true, Value: 50}, ValidUint64{Valid: true, Value: 100}, ValidFloat64{Valid: true, Value: 5.5})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	snap := m.Snapshot()
	if !snap.QueueSaturationPercent.Valid || snap.QueueSaturationPercent.Value != 0.5 {
		t.Errorf("expected 0.5 saturation, got %v", snap.QueueSaturationPercent)
	}

	// Invalid - depth > capacity
	err = m.UpdateQueue(ValidUint64{Valid: true, Value: 150}, ValidUint64{Valid: true, Value: 100}, ValidFloat64{Valid: true, Value: 5.5})
	if err == nil {
		t.Error("expected error for depth > capacity")
	}

	// Invalid - capacity = 0
	err = m.UpdateQueue(ValidUint64{Valid: true, Value: 0}, ValidUint64{Valid: true, Value: 0}, ValidFloat64{Valid: true, Value: 5.5})
	if err == nil {
		t.Error("expected error for capacity 0")
	}

	// Invalid - negative oldest age
	err = m.UpdateQueue(ValidUint64{Valid: true, Value: 0}, ValidUint64{Valid: true, Value: 100}, ValidFloat64{Valid: true, Value: -1.0})
	if err == nil {
		t.Error("expected error for negative age")
	}
}

func TestMetrics_Counters(t *testing.T) {
	m := NewGovernorMetrics(time.Now)
	cat1 := EventCategory{Priority: PriorityP3, Reason: ReasonStorageLimit, Source: SourceSystem}

	m.IncrementAggregated(cat1)
	m.IncrementAggregated(cat1)
	m.IncrementDropped(cat1)

	snap := m.Snapshot()
	if snap.EventsAggregatedTotal[cat1] != 2 {
		t.Errorf("expected 2 aggregated, got %d", snap.EventsAggregatedTotal[cat1])
	}
	if snap.EventsDroppedTotal[cat1] != 1 {
		t.Errorf("expected 1 dropped, got %d", snap.EventsDroppedTotal[cat1])
	}
}

func TestMetrics_EmergencyDuration(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	m := NewGovernorMetrics(clock)

	// Record normal -> no duration
	m.RecordTransition(Transition{NewState: StateNormal, ObservedAt: now})
	snap := m.Snapshot()
	if snap.TimeInEmergencyModeSeconds != 0 {
		t.Errorf("expected 0 duration, got %f", snap.TimeInEmergencyModeSeconds)
	}
	if snap.GovernorState != StateNormal {
		t.Errorf("expected NORMAL state")
	}

	// Enter emergency
	now = now.Add(time.Second)
	m.RecordTransition(Transition{NewState: StateEmergency, ObservedAt: now})

	// Advance clock
	now = now.Add(10 * time.Second)
	snap = m.Snapshot()
	if snap.TimeInEmergencyModeSeconds != 10.0 {
		t.Errorf("expected 10s duration, got %f", snap.TimeInEmergencyModeSeconds)
	}

	// Repeated transition to emergency should NOT reset timer
	now = now.Add(5 * time.Second)
	m.RecordTransition(Transition{NewState: StateEmergency, ObservedAt: now})

	now = now.Add(5 * time.Second)
	snap = m.Snapshot()
	if snap.TimeInEmergencyModeSeconds != 20.0 {
		t.Errorf("expected 20s duration, got %f", snap.TimeInEmergencyModeSeconds)
	}

	// Exit emergency
	now = now.Add(time.Second)
	m.RecordTransition(Transition{NewState: StateResourceSaver, ObservedAt: now})

	snap = m.Snapshot()
	if snap.TimeInEmergencyModeSeconds != 0 {
		t.Errorf("expected 0 duration after exit, got %f", snap.TimeInEmergencyModeSeconds)
	}

	// State transitions check
	if snap.TotalStateTransitions != 4 {
		t.Errorf("expected 4 transitions, got %d", snap.TotalStateTransitions)
	}
}

func TestMetrics_Concurrency(t *testing.T) {
	m := NewGovernorMetrics(time.Now)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.UpdateCPU(ValidFloat64{Valid: true, Value: float64(i % 100)})
			m.IncrementAggregated(EventCategory{Priority: PriorityP4, Reason: ReasonStorageLimit, Source: SourceSystem})
			m.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := m.Snapshot()
	if snap.EventsAggregatedTotal[EventCategory{Priority: PriorityP4, Reason: ReasonStorageLimit, Source: SourceSystem}] != 100 {
		t.Errorf("expected 100 aggregated")
	}
}

func BenchmarkMetrics_Snapshot(b *testing.B) {
	m := NewGovernorMetrics(time.Now)

	// Add some categories
	cat1 := EventCategory{Priority: PriorityP0, Reason: ReasonEmergency, Source: SourceSystem}
	cat2 := EventCategory{Priority: PriorityP3, Reason: ReasonQueueFull, Source: SourceNetworkCollector}

	m.IncrementAggregated(cat1)
	m.IncrementDropped(cat2)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = m.Snapshot()
	}
}
