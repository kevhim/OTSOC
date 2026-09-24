package detection

import (
	"context"
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
		EventID:       "telemetry-123",
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
	if !ok || len(evidence) != 1 || evidence[0] != "telemetry-123" {
		t.Errorf("Finding must reference the original telemetry event ID")
	}
}

func TestDetectionEngine_NegativeMatch(t *testing.T) {
	store := &mockStorage{}
	engine := NewEngine(store, []Rule{&TestIOCRule{}})

	telemetryEvent := &events.CanonicalEvent{
		EventID: "telemetry-123",
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
		EventID:  "telemetry-123",
		TenantID: "tenant-1",
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
		EventID:  "telemetry-1",
		TenantID: "tenant-1",
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	ev2 := &events.CanonicalEvent{
		EventID:  "telemetry-2", // Changed EventID
		TenantID: "tenant-1",
		Metadata: map[string]interface{}{
			"test_ioc": "RF-TEST-MALICIOUS",
		},
	}

	ev3 := &events.CanonicalEvent{
		EventID:  "telemetry-1",
		TenantID: "tenant-2", // Changed TenantID
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

	ev := &events.CanonicalEvent{EventID: "test"}
	engine.Evaluate(context.Background(), ev)

	if len(traces) != 3 {
		t.Fatalf("Expected 3 rule evaluations, got %d", len(traces))
	}

	if traces[0] != "RULE-A" || traces[1] != "RULE-B" || traces[2] != "RULE-C" {
		t.Errorf("Rule evaluation order is not deterministic. Got: %v", traces)
	}
}
