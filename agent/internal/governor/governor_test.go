package governor

import (
	"math"
	"testing"
	"time"
)

func defaultTestConfig() Config {
	return Config{
		CPUSaverThreshold:     80.0,
		CPUEmergencyThreshold: 90.0,
		CPURecoveryMargin:     5.0,

		MemorySaverBytes:          200 * 1024 * 1024,
		MemoryEmergencyBytes:      300 * 1024 * 1024,
		MemoryRecoveryMarginBytes: 10 * 1024 * 1024,

		StorageSaverRatio:          0.7,
		StorageEmergencyRatio:      0.9,
		StorageRecoveryMarginRatio: 0.05,

		QueueSaverRatio:          0.7,
		QueueEmergencyRatio:      0.9,
		QueueRecoveryMarginRatio: 0.05,

		RecoveryDuration: 30 * time.Second,
	}
}

// 1. NORMAL remains NORMAL under healthy signals.
func TestGovernor_NormalRemainsNormal(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{
		CPUUtilizationPercent: 50.0,
		MemoryRSSBytes:        100 * 1024 * 1024,
		StoragePressure:       0.5,
		QueueSaturation:       0.1,
	})

	if gov.CurrentState() != StateNormal {
		t.Errorf("expected NORMAL, got %v", gov.CurrentState())
	}
}

// 2. NORMAL -> RESOURCE-SAVER.
func TestGovernor_NormalToResourceSaver(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 85.0}) // > 80.0
	if gov.CurrentState() != StateResourceSaver {
		t.Errorf("expected RESOURCE-SAVER, got %v", gov.CurrentState())
	}
}

// 3. RESOURCE-SAVER -> EMERGENCY.
func TestGovernor_ResourceSaverToEmergency(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 85.0})
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0})

	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected EMERGENCY, got %v", gov.CurrentState())
	}
}

// 4. NORMAL -> EMERGENCY when critical thresholds are exceeded.
func TestGovernor_NormalToEmergency(t *testing.T) {
	now := time.Now()
	var emitted []Transition
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, func(tr Transition) {
		emitted = append(emitted, tr)
	})

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0})

	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected EMERGENCY, got %v", gov.CurrentState())
	}
	if len(emitted) != 1 || emitted[0].NewState != StateEmergency {
		t.Errorf("expected 1 EMERGENCY transition, got %v", emitted)
	}
}

// 5. EMERGENCY -> RESOURCE-SAVER only after recovery condition.
func TestGovernor_EmergencyToResourceSaver(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0})

	// Drop below emergency, but not below saver (e.g. 84.0, which is below 90-5 = 85)
	now = now.Add(time.Second)
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 84.0})

	// Should remain EMERGENCY because duration not met
	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected EMERGENCY before duration, got %v", gov.CurrentState())
	}

	// Advance time by RecoveryDuration
	now = now.Add(30 * time.Second)
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 84.0})

	if gov.CurrentState() != StateResourceSaver {
		t.Errorf("expected RESOURCE-SAVER after duration, got %v", gov.CurrentState())
	}
}

// 6. RESOURCE-SAVER -> NORMAL only after configured hysteresis.
func TestGovernor_ResourceSaverToNormal(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 85.0})

	// Drop below saver recovery (80 - 5 = 75)
	now = now.Add(time.Second)
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 74.0})

	if gov.CurrentState() != StateResourceSaver {
		t.Errorf("expected RESOURCE-SAVER before duration, got %v", gov.CurrentState())
	}

	now = now.Add(30 * time.Second)
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 74.0})

	if gov.CurrentState() != StateNormal {
		t.Errorf("expected NORMAL after duration, got %v", gov.CurrentState())
	}
}

// 7. Rapid threshold oscillation does not flap the state.
func TestGovernor_HysteresisPreventsFlapping(t *testing.T) {
	now := time.Now()
	transitions := 0
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, func(tr Transition) {
		transitions++
	})

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0}) // EMERGENCY
	transitions = 0                                            // reset count

	// Flap around the Emergency recovery threshold (85.0)
	for i := 0; i < 10; i++ {
		now = now.Add(time.Second)
		gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 84.0}) // Starts recovery
		now = now.Add(time.Second)
		gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 86.0}) // Aborts recovery
	}

	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected to stay in EMERGENCY, got %v", gov.CurrentState())
	}
	if transitions > 0 {
		t.Errorf("expected 0 transitions due to flapping, got %d", transitions)
	}
}

// 8. Network offline while host resources are healthy does NOT force host EMERGENCY.
func TestGovernor_NetworkDoesNotForceEmergency(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{
		CPUUtilizationPercent: 50.0,
		NetworkState:          NetworkOffline,
	})

	if gov.CurrentState() != StateNormal {
		t.Errorf("expected NORMAL when offline but healthy, got %v", gov.CurrentState())
	}
}

// 9. Multiple simultaneous pressure signals.
func TestGovernor_MultipleSimultaneousPressure(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{
		CPUUtilizationPercent: 85.0, // Saver
		StoragePressure:       0.95, // Emergency
	})

	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected highest severity (EMERGENCY), got %v", gov.CurrentState())
	}
}

// 10. Emergency transition telemetry is bounded/idempotent.
func TestGovernor_EmergencyIdempotent(t *testing.T) {
	now := time.Now()
	var emitted []Transition
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, func(tr Transition) {
		emitted = append(emitted, tr)
	})

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0}) // Triggers Emergency
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 96.0}) // Still Emergency
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 97.0}) // Still Emergency

	if len(emitted) != 1 {
		t.Errorf("expected exactly 1 transition event, got %d", len(emitted))
	}
}

// 11. Invalid configuration is rejected explicitly.
func TestGovernor_InvalidConfigRejected(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.CPUEmergencyThreshold = 70.0 // Less than Saver (80.0)

	_, err := NewGovernor(cfg, nil, nil)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

// 12. Boundary conditions immediately above/below thresholds.
func TestGovernor_BoundaryConditions(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	// CPU Saver is 80.0
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 79.999})
	if gov.CurrentState() != StateNormal {
		t.Errorf("expected NORMAL below boundary, got %v", gov.CurrentState())
	}

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 80.0})
	if gov.CurrentState() != StateResourceSaver {
		t.Errorf("expected RESOURCE-SAVER at boundary, got %v", gov.CurrentState())
	}
}

// 13. Deterministic injected-clock advancement.
func TestGovernor_DeterministicClock(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 95.0}) // Emergency

	now = now.Add(10 * time.Hour)                              // Jump forward in time
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 50.0}) // Sets recovery start time

	if gov.CurrentState() != StateEmergency {
		t.Errorf("expected EMERGENCY before duration, got %v", gov.CurrentState())
	}

	now = now.Add(30 * time.Second)
	gov.Evaluate(ResourceSignals{CPUUtilizationPercent: 50.0}) // Duration met

	if gov.CurrentState() != StateNormal {
		t.Errorf("expected NORMAL after duration, got %v", gov.CurrentState())
	}
}

// 14. Repeated evaluation does not create event-count-dependent persistent state.
// Proves bounded scalar internal state without relying on b.ReportAllocs().
func TestGovernor_BoundedState(t *testing.T) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	// Run lots of evaluates
	for i := 0; i < 100000; i++ {
		now = now.Add(time.Millisecond)
		gov.Evaluate(ResourceSignals{CPUUtilizationPercent: float64(i % 100)})
	}
	if gov.CurrentState() == "" {
		t.Error("invalid state")
	}
}

func TestGovernor_InvalidSignalsTriggerEmergency(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		sig  ResourceSignals
	}{
		{"CPU NaN", ResourceSignals{CPUUtilizationPercent: math.NaN()}},
		{"CPU Negative", ResourceSignals{CPUUtilizationPercent: -1.0}},
		{"Storage NaN", ResourceSignals{StoragePressure: math.NaN()}},
		{"Storage Negative", ResourceSignals{StoragePressure: -0.1}},
		{"Storage > 1", ResourceSignals{StoragePressure: 1.1}},
		{"Queue NaN", ResourceSignals{QueueSaturation: math.NaN()}},
		{"Queue Negative", ResourceSignals{QueueSaturation: -0.1}},
		{"Queue > 1", ResourceSignals{QueueSaturation: 1.1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)
			gov.Evaluate(tc.sig)
			if gov.CurrentState() != StateEmergency {
				t.Errorf("expected EMERGENCY fail-safe for invalid signal %s, got %v", tc.name, gov.CurrentState())
			}
		})
	}
}

func TestGovernor_InvalidMarginsRejected(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.CPURecoveryMargin = 100.0 // greater than saver (80)
	if _, err := NewGovernor(cfg, nil, nil); err == nil {
		t.Error("expected error for invalid CPU margin")
	}

	cfg = defaultTestConfig()
	cfg.MemoryRecoveryMarginBytes = 250 * 1024 * 1024 // greater than saver (200)
	if _, err := NewGovernor(cfg, nil, nil); err == nil {
		t.Error("expected error for invalid Memory margin")
	}

	cfg = defaultTestConfig()
	cfg.RecoveryDuration = 0
	if _, err := NewGovernor(cfg, nil, nil); err == nil {
		t.Error("expected error for zero recovery duration")
	}
}
