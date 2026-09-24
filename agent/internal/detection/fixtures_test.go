package detection

import (
	"fmt"
	"redcyberfox/pkg/events"
)

// TestIOCRule is a deterministic proof-of-engine local IOC rule.
type TestIOCRule struct{}

func (r *TestIOCRule) ID() string {
	return "LOCAL-IOC-TEST-001"
}

func (r *TestIOCRule) Version() string {
	return "1.0"
}

func (r *TestIOCRule) Severity() string {
	return "CRITICAL" // Changed from HIGH to a valid CanonicalEvent severity
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
