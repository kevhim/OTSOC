package detection

import (
	"fmt"
	"redcyberfox/pkg/events"
)

// TestIOCRule is a deterministic proof-of-engine local IOC rule.
// It is used to prove the detection engine foundation (evaluate -> finding -> persistence)
// without claiming to be a production malicious IOC.
type TestIOCRule struct{}

func (r *TestIOCRule) ID() string {
	return "LOCAL-IOC-TEST-001"
}

func (r *TestIOCRule) Version() string {
	return "1.0"
}

func (r *TestIOCRule) Severity() string {
	return "HIGH"
}

func (r *TestIOCRule) Confidence() float64 {
	return 100.0
}

func (r *TestIOCRule) Reason() string {
	return "Test IOC matched expected fixture value"
}

func (r *TestIOCRule) AttckEnterprise() []string {
	return nil
}

func (r *TestIOCRule) AttckICS() []string {
	return nil
}

func (r *TestIOCRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	if ev.Metadata == nil {
		return false, nil
	}

	val, ok := ev.Metadata["test_ioc"]
	if !ok {
		return false, nil
	}

	strVal, ok := val.(string)
	if !ok {
		return false, nil
	}

	return strVal == "RF-TEST-MALICIOUS", nil
}

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

// PanicTestRule intentionally panics for testing failure isolation.
type PanicTestRule struct{}

func (r *PanicTestRule) ID() string {
	return "PANIC-TEST-001"
}

func (r *PanicTestRule) Version() string {
	return "1.0"
}

func (r *PanicTestRule) Severity() string {
	return "DEBUG"
}

func (r *PanicTestRule) Confidence() float64 {
	return 0.0
}

func (r *PanicTestRule) Reason() string {
	return "Panic rule"
}

func (r *PanicTestRule) AttckEnterprise() []string {
	return nil
}

func (r *PanicTestRule) AttckICS() []string {
	return nil
}

func (r *PanicTestRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	if ev.Metadata != nil && ev.Metadata["trigger_panic"] == true {
		panic("intentional panic for failure isolation test")
	}
	return false, nil
}

// ErrorTestRule intentionally returns an explicit evaluation error.
type ErrorTestRule struct{}

func (r *ErrorTestRule) ID() string {
	return "ERROR-TEST-001"
}

func (r *ErrorTestRule) Version() string {
	return "1.0"
}

func (r *ErrorTestRule) Severity() string {
	return "DEBUG"
}

func (r *ErrorTestRule) Confidence() float64 {
	return 0.0
}

func (r *ErrorTestRule) Reason() string {
	return "Error rule"
}

func (r *ErrorTestRule) AttckEnterprise() []string {
	return nil
}

func (r *ErrorTestRule) AttckICS() []string {
	return nil
}

func (r *ErrorTestRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	if ev.Metadata != nil && ev.Metadata["trigger_error"] == true {
		return false, fmt.Errorf("explicit evaluation error for test")
	}
	return false, nil
}
