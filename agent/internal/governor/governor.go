package governor

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Transition represents an observable state transition.
type Transition struct {
	PreviousState HostResourceState
	NewState      HostResourceState
	Reason        string
	ObservedAt    time.Time
	Signals       ResourceSignals
}

type NowFunc func() time.Time

type Governor struct {
	config            Config
	currentState      HostResourceState
	recoveryStartTime time.Time
	nowFunc           NowFunc

	// onTransition is an optional callback fired synchronously upon state transition.
	// It must be bounded, non-blocking, and must not synchronously re-enter Evaluate.
	onTransition func(Transition)
}

func NewGovernor(cfg Config, now NowFunc, onTransition func(Transition)) (*Governor, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Governor{
		config:       cfg,
		currentState: StateNormal,
		nowFunc:      now,
		onTransition: onTransition,
	}, nil
}

func validateConfig(c Config) error {
	if c.CPUSaverThreshold < 0 || c.CPUSaverThreshold > 100 {
		return errors.New("invalid CPUSaverThreshold")
	}
	if c.CPUEmergencyThreshold <= c.CPUSaverThreshold || c.CPUEmergencyThreshold > 100 {
		return errors.New("invalid CPUEmergencyThreshold")
	}
	if c.CPURecoveryMargin < 0 || c.CPURecoveryMargin >= c.CPUSaverThreshold {
		return errors.New("invalid CPURecoveryMargin")
	}

	if c.MemoryEmergencyBytes <= c.MemorySaverBytes {
		return errors.New("invalid Memory limits")
	}
	if c.MemoryRecoveryMarginBytes >= c.MemorySaverBytes {
		return errors.New("invalid MemoryRecoveryMarginBytes")
	}

	if c.StorageSaverRatio < 0 || c.StorageSaverRatio > 1 {
		return errors.New("invalid StorageSaverRatio")
	}
	if c.StorageEmergencyRatio <= c.StorageSaverRatio || c.StorageEmergencyRatio > 1 {
		return errors.New("invalid StorageEmergencyRatio")
	}
	if c.StorageRecoveryMarginRatio < 0 || c.StorageRecoveryMarginRatio >= c.StorageSaverRatio {
		return errors.New("invalid StorageRecoveryMarginRatio")
	}

	if c.QueueSaverRatio < 0 || c.QueueSaverRatio > 1 {
		return errors.New("invalid QueueSaverRatio")
	}
	if c.QueueEmergencyRatio <= c.QueueSaverRatio || c.QueueEmergencyRatio > 1 {
		return errors.New("invalid QueueEmergencyRatio")
	}
	if c.QueueRecoveryMarginRatio < 0 || c.QueueRecoveryMarginRatio >= c.QueueSaverRatio {
		return errors.New("invalid QueueRecoveryMarginRatio")
	}

	if c.RecoveryDuration <= 0 {
		return errors.New("invalid RecoveryDuration")
	}
	return nil
}

// CurrentState returns the current evaluated host resource state.
func (g *Governor) CurrentState() HostResourceState {
	return g.currentState
}

// Evaluate takes fresh resource signals, updates internal state, and optionally emits transitions.
func (g *Governor) Evaluate(signals ResourceSignals) {
	targetState, reason := g.computeTargetState(signals)
	now := g.nowFunc()

	if stateSeverity(targetState) > stateSeverity(g.currentState) {
		// Immediate transition upwards
		g.transition(targetState, reason, now, signals)
	} else if stateSeverity(targetState) < stateSeverity(g.currentState) {
		// Attempting to recover downwards
		if g.recoveryStartTime.IsZero() {
			g.recoveryStartTime = now
		} else if now.Sub(g.recoveryStartTime) >= g.config.RecoveryDuration {
			// Recovery condition met
			g.transition(targetState, "Recovery duration met: "+reason, now, signals)
		}
	} else {
		// State is stable
		g.recoveryStartTime = time.Time{} // Reset recovery timer if we bump back up to current
	}
}

func stateSeverity(s HostResourceState) int {
	switch s {
	case StateNormal:
		return 0
	case StateResourceSaver:
		return 1
	case StateEmergency:
		return 2
	}
	return -1
}

func (g *Governor) transition(newState HostResourceState, reason string, now time.Time, sigs ResourceSignals) {
	prev := g.currentState
	g.currentState = newState
	g.recoveryStartTime = time.Time{} // reset

	if g.onTransition != nil {
		g.onTransition(Transition{
			PreviousState: prev,
			NewState:      newState,
			Reason:        reason,
			ObservedAt:    now,
			Signals:       sigs,
		})
	}
}

func validateSignals(s ResourceSignals) error {
	if math.IsNaN(s.CPUUtilizationPercent) || s.CPUUtilizationPercent < 0 {
		return errors.New("CPU utilization must be a valid non-negative number")
	}
	if math.IsNaN(s.StoragePressure) || s.StoragePressure < 0 || s.StoragePressure > 1 {
		return errors.New("StoragePressure must be in [0, 1]")
	}
	if math.IsNaN(s.QueueSaturation) || s.QueueSaturation < 0 || s.QueueSaturation > 1 {
		return errors.New("QueueSaturation must be in [0, 1]")
	}
	return nil
}

func (g *Governor) computeTargetState(signals ResourceSignals) (HostResourceState, string) {
	if err := validateSignals(signals); err != nil {
		return StateEmergency, "Invalid signal (fail-safe): " + err.Error()
	}

	// Evaluate each dimension with hysteresis
	cpuState, cpuReason := evalFloat64(signals.CPUUtilizationPercent, g.config.CPUSaverThreshold, g.config.CPUEmergencyThreshold, g.config.CPURecoveryMargin, g.currentState, "CPU")
	memState, memReason := evalUint64(signals.MemoryRSSBytes, g.config.MemorySaverBytes, g.config.MemoryEmergencyBytes, g.config.MemoryRecoveryMarginBytes, g.currentState, "Memory")
	storageState, storageReason := evalFloat64(signals.StoragePressure, g.config.StorageSaverRatio, g.config.StorageEmergencyRatio, g.config.StorageRecoveryMarginRatio, g.currentState, "Storage")
	queueState, queueReason := evalFloat64(signals.QueueSaturation, g.config.QueueSaverRatio, g.config.QueueEmergencyRatio, g.config.QueueRecoveryMarginRatio, g.currentState, "Queue")

	// Find highest severity state among all signals
	states := []struct {
		s HostResourceState
		r string
	}{
		{cpuState, cpuReason},
		{memState, memReason},
		{storageState, storageReason},
		{queueState, queueReason},
	}

	maxState := StateNormal
	maxReason := "Healthy"

	for _, st := range states {
		if stateSeverity(st.s) > stateSeverity(maxState) {
			maxState = st.s
			maxReason = st.r
		}
	}

	// NOTE: Network status intentionally does NOT force HostResourceState to EMERGENCY.
	// We preserve P0/P1 locally and handle Store-and-Forward logically separate from host resources.

	return maxState, maxReason
}

func evalFloat64(val, saverThresh, emergThresh, margin float64, current HostResourceState, name string) (HostResourceState, string) {
	effEmerg := emergThresh
	effSaver := saverThresh

	if current == StateEmergency {
		effEmerg -= margin
	}
	if current == StateEmergency || current == StateResourceSaver {
		effSaver -= margin
	}

	if val >= effEmerg {
		return StateEmergency, fmt.Sprintf("%s exceeded emergency threshold", name)
	}
	if val >= effSaver {
		return StateResourceSaver, fmt.Sprintf("%s exceeded saver threshold", name)
	}
	return StateNormal, fmt.Sprintf("%s healthy", name)
}

func evalUint64(val, saverThresh, emergThresh, margin uint64, current HostResourceState, name string) (HostResourceState, string) {
	effEmerg := emergThresh
	effSaver := saverThresh

	if current == StateEmergency && effEmerg > margin {
		effEmerg -= margin
	}
	if (current == StateEmergency || current == StateResourceSaver) && effSaver > margin {
		effSaver -= margin
	}

	if val >= effEmerg {
		return StateEmergency, fmt.Sprintf("%s exceeded emergency threshold", name)
	}
	if val >= effSaver {
		return StateResourceSaver, fmt.Sprintf("%s exceeded saver threshold", name)
	}
	return StateNormal, fmt.Sprintf("%s healthy", name)
}
