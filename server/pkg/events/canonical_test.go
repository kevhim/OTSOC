package events

import (
	"testing"
	"time"
)

func TestSerializeDeserialize(t *testing.T) {
	now := time.Now().UTC()
	original := &CanonicalEvent{
		EventID:       "evt_123",
		TenantID:      "tenant_abc",
		SiteID:        "site_1",
		OccurredAt:    now,
		SeqNo:         42,
		Source:        "test-sensor",
		Category:      "network",
		Severity:      "CRITICAL",
		SchemaVersion: "1.0",
		Metadata: map[string]interface{}{
			"foo": "bar",
			"baz": 123.0, // JSON numbers parse as float64
		},
	}

	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	deserialized, err := Deserialize(data)
	if err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	if deserialized.EventID != original.EventID {
		t.Errorf("Expected EventID %s, got %s", original.EventID, deserialized.EventID)
	}
	
	if deserialized.Severity != original.Severity {
		t.Errorf("Expected Severity %s, got %s", original.Severity, deserialized.Severity)
	}

	// JSON marshalling/unmarshalling of time might lose some precision (nanoseconds to strings),
	// but OccurredAt is preserved down to the micro/nano depending on formatting.
	// We just check if it's the same time string format or just check unix time.
	if deserialized.OccurredAt.Unix() != original.OccurredAt.Unix() {
		t.Errorf("Expected OccurredAt %v, got %v", original.OccurredAt, deserialized.OccurredAt)
	}

	if val, ok := deserialized.Metadata["foo"]; !ok || val != "bar" {
		t.Errorf("Expected Metadata['foo'] == 'bar', got %v", val)
	}
}
