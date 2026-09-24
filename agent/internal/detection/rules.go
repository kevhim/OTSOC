package detection

import (
	"redcyberfox/pkg/events"
)

// RealIOCExecRule is a real deterministic local IOC rule based on a stable existing event field (executable_path).
// A hash-based rule (LOCAL-IOC-HASH-001) could not be implemented because the CanonicalEvent schema and
// current collectors do not reliably populate a hash field. This is a documented schema/input gap.
type RealIOCExecRule struct{}

func (r *RealIOCExecRule) ID() string {
	return "LOCAL-IOC-EXEC-001"
}

func (r *RealIOCExecRule) Version() string {
	return "1.0"
}

func (r *RealIOCExecRule) Severity() string {
	// Note: CRITICAL is fixture metadata only, not empirically measured.
	return "CRITICAL"
}

func (r *RealIOCExecRule) Confidence() float64 {
	// Note: 100.0 is rule-defined fixture metadata only, not empirically measured.
	return 100.0
}

func (r *RealIOCExecRule) Reason() string {
	return "Deterministic IOC: Suspicious executable path detected"
}

func (r *RealIOCExecRule) AttckEnterprise() []string {
	// Removed T1059 since simply matching an executable path doesn't inherently establish Command and Scripting Interpreter usage.
	return nil
}

func (r *RealIOCExecRule) AttckICS() []string {
	return nil
}

func (r *RealIOCExecRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	if ev.Metadata == nil {
		return false, nil
	}

	val, ok := ev.Metadata["executable_path"]
	if !ok {
		return false, nil
	}

	strVal, ok := val.(string)
	if !ok {
		return false, nil
	}

	// Deterministic test IOC fixture
	return strVal == "/opt/malicious/bin/miner", nil
}
