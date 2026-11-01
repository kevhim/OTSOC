package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

type MockProducer struct {
	PublishedEvents []*events.CanonicalEvent
	ShouldFail      bool
}

func (m *MockProducer) Publish(ctx context.Context, ev *events.CanonicalEvent) error {
	if m.ShouldFail {
		return context.DeadlineExceeded
	}
	m.PublishedEvents = append(m.PublishedEvents, ev)
	return nil
}

func TestIngestHandler(t *testing.T) {
	mockProducer := &MockProducer{}
	handler := NewIngestHandler(mockProducer)

	tests := []struct {
		name            string
		queryTenant     string
		payload         interface{}
		expectedStatus  int
		shouldFailQueue bool
	}{
		{
			name:        "valid event",
			queryTenant: "t1",
			payload: events.CanonicalEvent{
				EventID:       "123e4567-e89b-12d3-a456-426614174000",
				TenantID:      "t1",
				SiteID:        "s1",
				Source:        "sensor-1",
				Category:      "network",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0.0",
				Severity:      "INFO",
			},
			expectedStatus: http.StatusAccepted,
		},
		{
			name:        "missing required fields",
			queryTenant: "t1",
			payload: events.CanonicalEvent{
				EventID:  "123",
				TenantID: "t1",
				// Missing SiteID, OccurredAt, SchemaVersion
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "invalid uuid",
			queryTenant: "t1",
			payload: events.CanonicalEvent{
				EventID:       "123",
				TenantID:      "t1",
				SiteID:        "s1",
				Source:        "sensor-1",
				Category:      "network",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0.0",
				Severity:      "INFO",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "queue failure",
			queryTenant: "t1",
			payload: events.CanonicalEvent{
				EventID:       "123e4567-e89b-12d3-a456-426614174000",
				TenantID:      "t1",
				SiteID:        "s1",
				Source:        "sensor-1",
				Category:      "network",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0.0",
				Severity:      "INFO",
			},
			expectedStatus:  http.StatusInternalServerError,
			shouldFailQueue: true,
		},
		{
			name:        "missing tenant_id query",
			queryTenant: "",
			payload: events.CanonicalEvent{
				EventID:       "123e4567-e89b-12d3-a456-426614174000",
				TenantID:      "t1",
				SiteID:        "s1",
				Source:        "sensor-1",
				Category:      "network",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0.0",
				Severity:      "INFO",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "tenant_id mismatch",
			queryTenant: "t2",
			payload: events.CanonicalEvent{
				EventID:       "123e4567-e89b-12d3-a456-426614174000",
				TenantID:      "t1",
				SiteID:        "s1",
				Source:        "sensor-1",
				Category:      "network",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0.0",
				Severity:      "INFO",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProducer.ShouldFail = tt.shouldFailQueue

			var body bytes.Buffer
			json.NewEncoder(&body).Encode(tt.payload)

			req := httptest.NewRequest(http.MethodPost, "/v1/ingest", &body)
			if tt.queryTenant != "" {
				q := req.URL.Query()
				q.Add("tenant_id", tt.queryTenant)
				req.URL.RawQuery = q.Encode()
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestIngestRejectsMultipleJSONEvents(t *testing.T) {
	producer := &MockProducer{}
	h := NewIngestHandler(producer)
	ev := events.CanonicalEvent{
		EventID:  "550e8400-e29b-41d4-a716-446655440000",
		TenantID: "t1", SiteID: "s1", OccurredAt: time.Now().UTC(),
		Source: "test", Category: "network", Severity: "INFO", SchemaVersion: "1.0.0",
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{string(payload) + string(payload), string(payload) + " extra"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/ingest?tenant_id=t1", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got HTTP %d, want 400", w.Code)
		}
	}
	if len(producer.PublishedEvents) != 0 {
		t.Fatal("rejected request must not be published")
	}
}

func TestIngestRejectsOversizedBody(t *testing.T) {
	producer := &MockProducer{}
	h := NewIngestHandler(producer)
	body := `{"tenant_id":"t1","metadata":"` + strings.Repeat("x", int(maxIngestBodyBytes)) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest?tenant_id=t1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got HTTP %d, want 413", w.Code)
	}
	if len(producer.PublishedEvents) != 0 {
		t.Fatal("oversized request must not be published")
	}
}
