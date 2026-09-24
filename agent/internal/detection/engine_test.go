package detection

import (
	"context"
	"fmt"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

// mockStorage implements a simple in-memory store for unit testing the detection engine.
type mockStorage struct {
	stored []*events.CanonicalEvent
}

func (m *mockStorage) Store(ctx context.Context, ev *events.CanonicalEvent) error {
	m.stored = append(m.stored, ev)
	return nil
}

func (m *mockStorage) MoveToDLQ(ctx context.Context, ev *events.CanonicalEvent, reason string, errDetail string) error {
	return nil
}

func (m *mockStorage) GetPendingEvents(ctx context.Context, batchSize int) ([]*events.CanonicalEvent, error) {
	return nil, nil
}

func (m *mockStorage) DeleteEvents(ctx context.Context, eventIDs []string) error {
	return nil
}

func (m *mockStorage) Close() error {
	return nil
}

func (m *mockStorage) GetDeviceID() string {
	return "mock-device"
}

func (m *mockStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockStorage) MarkFailed(ctx context.Context, eventID string, attempt int, err error) error {
	return nil
}

func (m *mockStorage) RemoveEvent(ctx context.Context, eventID string) error {
	return nil
}

func TestDetectionEngine_PositiveMatch(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})

	telemetryEvent := &events.CanonicalEvent{
		EventID:       "33333333-3333-3333-3333-333333333333",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	engine.Evaluate(context.Background(), telemetryEvent)

	if len(store.stored) != 1 {
		t.Fatalf("Expected 1 finding, got %d", len(store.stored))
	}

	finding := store.stored[0]
	if finding.Category != "detection/finding" {
		t.Errorf("Expected category 'detection/finding', got %s", finding.Category)
	}
	if finding.Source != "local_detection" {
		t.Errorf("Expected source 'local_detection', got %s", finding.Source)
	}
	if finding.RuleID != "LOCAL-IOC-TEST-001" {
		t.Errorf("Expected rule ID 'LOCAL-IOC-TEST-001', got %s", finding.RuleID)
	}
	if finding.EventID == telemetryEvent.EventID {
		t.Errorf("Finding must have a distinct EventID")
	}

	evidence, ok := finding.Metadata["evidence_event_ids"].([]string)
	if !ok || len(evidence) != 1 || evidence[0] != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("Finding must reference the original telemetry event ID")
	}
}

func TestDetectionEngine_NegativeMatch(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})

	telemetryEvent := &events.CanonicalEvent{
		EventID: "33333333-3333-3333-3333-333333333333",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "BENIGN-STRING",
		},
	}

	engine.Evaluate(context.Background(), telemetryEvent)

	if len(store.stored) != 0 {
		t.Fatalf("Expected 0 findings for benign event, got %d", len(store.stored))
	}
}

func TestDetectionEngine_DeduplicationIdentity(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})

	telemetryEvent := &events.CanonicalEvent{
		EventID:  "33333333-3333-3333-3333-333333333333",
		TenantID: "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	engine.Evaluate(context.Background(), telemetryEvent)
	engine.Evaluate(context.Background(), telemetryEvent)

	if len(store.stored) != 2 {
		t.Fatalf("Expected 2 finding evaluations")
	}

	if store.stored[0].EventID != store.stored[1].EventID {
		t.Errorf("Expected identical deterministic EventID for repeated evaluations of the same event/rule")
	}
}

func TestDetectionEngine_FindingIdentityChange(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})

	ev1 := &events.CanonicalEvent{
		EventID:  "44444444-4444-4444-4444-444444444444",
		TenantID: "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	ev2 := &events.CanonicalEvent{
		EventID:  "55555555-5555-5555-5555-555555555555", // Changed EventID
		TenantID: "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	ev3 := &events.CanonicalEvent{
		EventID:  "44444444-4444-4444-4444-444444444444",
		TenantID: "tenant-2", // Changed TenantID
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	engine.Evaluate(context.Background(), ev1)
	engine.Evaluate(context.Background(), ev2)
	engine.Evaluate(context.Background(), ev3)

	if len(store.stored) != 3 {
		t.Fatalf("Expected 3 findings")
	}

	id1 := store.stored[0].EventID
	id2 := store.stored[1].EventID
	id3 := store.stored[2].EventID

	if id1 == id2 || id1 == id3 || id2 == id3 {
		t.Errorf("Finding identities must be distinct when input components change. Got %s, %s, %s", id1, id2, id3)
	}

	if id1 == ev1.EventID {
		t.Errorf("Finding event_id must not equal source telemetry event_id")
	}
}

// orderedMockRule allows us to trace execution order
type orderedMockRule struct {
	id     string
	traces *[]string
}

func (r *orderedMockRule) ID() string                { return r.id }
func (r *orderedMockRule) Version() string           { return "1.0" }
func (r *orderedMockRule) Severity() string          { return "INFO" }
func (r *orderedMockRule) Confidence() float64       { return 0.0 }
func (r *orderedMockRule) Reason() string            { return "" }
func (r *orderedMockRule) AttckEnterprise() []string { return nil }
func (r *orderedMockRule) AttckICS() []string        { return nil }
func (r *orderedMockRule) Evaluate(ev *events.CanonicalEvent) (bool, error) {
	*r.traces = append(*r.traces, r.id)
	return false, nil
}

func TestDetectionEngine_RuleOrder(t *testing.T) {
	var traces []string
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{
		&orderedMockRule{id: "RULE-A", traces: &traces},
		&orderedMockRule{id: "RULE-B", traces: &traces},
		&orderedMockRule{id: "RULE-C", traces: &traces},
	})

	ev := &events.CanonicalEvent{
		EventID: "test-uuid-1111-2222-3333",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
	}
	engine.Evaluate(context.Background(), ev)

	if len(traces) != 3 {
		t.Fatalf("Expected 3 rule evaluations, got %d", len(traces))
	}

	if traces[0] != "RULE-A" || traces[1] != "RULE-B" || traces[2] != "RULE-C" {
		t.Errorf("Rule evaluation order is not deterministic. Got: %v", traces)
	}
}

type invalidSeverityRule struct{}
func (r *invalidSeverityRule) ID() string { return "INV-001" }
func (r *invalidSeverityRule) Version() string { return "1.0" }
func (r *invalidSeverityRule) Severity() string { return "SUPER_HIGH" }
func (r *invalidSeverityRule) Confidence() float64 { return 100.0 }
func (r *invalidSeverityRule) Reason() string { return "Invalid severity" }
func (r *invalidSeverityRule) AttckEnterprise() []string { return nil }
func (r *invalidSeverityRule) AttckICS() []string { return nil }
func (r *invalidSeverityRule) Evaluate(ev *events.CanonicalEvent) (bool, error) { return true, nil }

func TestDetectionEngine_InvalidSeverityValidation(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&invalidSeverityRule{}})
	
	telemetryEvent := &events.CanonicalEvent{
		EventID: "33333333-3333-3333-3333-333333333333",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
	}
	
	engine.Evaluate(context.Background(), telemetryEvent)
	
	if len(store.stored) != 0 {
		t.Fatalf("Expected 0 findings due to validation failure, got %d", len(store.stored))
	}
}

type mockFailingStore struct {
	*mockStorage
	findingFailures int
}

func (m *mockFailingStore) Store(ctx context.Context, ev *events.CanonicalEvent) error {
	if ev.Category == "detection/finding" {
		m.findingFailures++
		return fmt.Errorf("simulated finding persistence failure")
	}
	return m.mockStorage.Store(ctx, ev)
}

func TestDetectionEngine_PersistenceFailureObservable(t *testing.T) {
	baseStore := &mockStorage{}
	store := &mockFailingStore{mockStorage: baseStore}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})
	
	telemetryEvent := &events.CanonicalEvent{
		EventID: "33333333-3333-3333-3333-333333333333",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		Category:      "process",
		Source:        "linux_process",
		OccurredAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}
	
	// Simulate telemetry being successfully stored first
	_ = store.Store(context.Background(), telemetryEvent)
	
	engine.Evaluate(context.Background(), telemetryEvent)
	
	if store.findingFailures != 1 {
		t.Fatalf("Expected 1 finding persistence failure, got %d", store.findingFailures)
	}
	
	// Original telemetry remains durable
	if len(store.mockStorage.stored) != 1 {
		t.Fatalf("Expected 1 durable telemetry event, got %d", len(store.mockStorage.stored))
	}
	
	if store.mockStorage.stored[0].EventID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("Telemetry was lost or modified")
	}
}
