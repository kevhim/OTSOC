package events

import (
	"testing"
	"time"
)

func validEvent() CanonicalEvent {
	return CanonicalEvent{
		EventID:       "550e8400-e29b-41d4-a716-446655440000",
		TenantID:      "tenant-1",
		SiteID:        "site-1",
		OccurredAt:    time.Now().UTC(),
		SeqNo:         42,
		Source:        "test-sensor",
		Category:      "network",
		Severity:      "INFO",
		SchemaVersion: "1.0.0",
	}
}

func TestCanonicalEventValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CanonicalEvent)
		wantErr bool
	}{
		{
			name:    "valid canonical event",
			mutate:  func(e *CanonicalEvent) {},
			wantErr: false,
		},
		{
			name: "invalid UUID",
			mutate: func(e *CanonicalEvent) {
				e.EventID = "invalid-uuid"
			},
			wantErr: true,
		},
		{
			name: "schema_version = 1.0",
			mutate: func(e *CanonicalEvent) {
				e.SchemaVersion = "1.0"
			},
			wantErr: true,
		},
		{
			name: "schema_version = v1.0.0",
			mutate: func(e *CanonicalEvent) {
				e.SchemaVersion = "v1.0.0"
			},
			wantErr: false,
		},
		{
			name: "missing severity",
			mutate: func(e *CanonicalEvent) {
				e.Severity = ""
			},
			wantErr: true,
		},
		{
			name: "invalid severity",
			mutate: func(e *CanonicalEvent) {
				e.Severity = "MODERATE"
			},
			wantErr: true,
		},
		{
			name: "confidence < 0",
			mutate: func(e *CanonicalEvent) {
				val := -1.0
				e.Confidence = &val
			},
			wantErr: true,
		},
		{
			name: "confidence > 100",
			mutate: func(e *CanonicalEvent) {
				val := 101.0
				e.Confidence = &val
			},
			wantErr: true,
		},
		{
			name: "confidence 0",
			mutate: func(e *CanonicalEvent) {
				val := 0.0
				e.Confidence = &val
			},
			wantErr: false,
		},
		{
			name: "confidence 100",
			mutate: func(e *CanonicalEvent) {
				val := 100.0
				e.Confidence = &val
			},
			wantErr: false,
		},
		{
			name: "negative seq_no",
			mutate: func(e *CanonicalEvent) {
				e.SeqNo = -1
			},
			wantErr: true,
		},
		{
			name: "invalid tenant",
			mutate: func(e *CanonicalEvent) {
				e.TenantID = "tenant_123" // underscore not allowed
			},
			wantErr: true,
		},
		{
			name: "empty site",
			mutate: func(e *CanonicalEvent) {
				e.SiteID = ""
			},
			wantErr: true,
		},
		{
			name: "missing source",
			mutate: func(e *CanonicalEvent) {
				e.Source = ""
			},
			wantErr: true,
		},
		{
			name: "missing category",
			mutate: func(e *CanonicalEvent) {
				e.Category = ""
			},
			wantErr: true,
		},
		{
			name: "missing occurred_at",
			mutate: func(e *CanonicalEvent) {
				e.OccurredAt = time.Time{}
			},
			wantErr: true,
		},
		{
			name: "severity DEBUG",
			mutate: func(e *CanonicalEvent) {
				e.Severity = "DEBUG"
			},
			wantErr: false,
		},
		{
			name: "severity WARNING",
			mutate: func(e *CanonicalEvent) {
				e.Severity = "WARNING"
			},
			wantErr: false,
		},
		{
			name: "severity CRITICAL",
			mutate: func(e *CanonicalEvent) {
				e.Severity = "CRITICAL"
			},
			wantErr: false,
		},
		{
			name: "severity FATAL",
			mutate: func(e *CanonicalEvent) {
				e.Severity = "FATAL"
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := validEvent()
			tc.mutate(&ev)
			err := ev.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestSerializeDeserialize(t *testing.T) {
	original := validEvent()
	original.Metadata = map[string]interface{}{
		"foo": "bar",
		"baz": 123.0,
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

	if deserialized.OccurredAt.Unix() != original.OccurredAt.Unix() {
		t.Errorf("Expected OccurredAt %v, got %v", original.OccurredAt, deserialized.OccurredAt)
	}

	if val, ok := deserialized.Metadata["foo"]; !ok || val != "bar" {
		t.Errorf("Expected Metadata['foo'] == 'bar', got %v", val)
	}
}
