package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"redcyberfox/server/pkg/events"
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
		name           string
		payload        interface{}
		expectedStatus int
		shouldFailQueue bool
	}{
		{
			name: "valid event",
			payload: events.CanonicalEvent{
				EventID:       "123",
				TenantID:      "t1",
				SiteID:        "s1",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0",
			},
			expectedStatus: http.StatusAccepted,
		},
		{
			name: "missing required fields",
			payload: events.CanonicalEvent{
				EventID: "123",
				// Missing TenantID, SiteID, OccurredAt, SchemaVersion
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "queue failure",
			payload: events.CanonicalEvent{
				EventID:       "123",
				TenantID:      "t1",
				SiteID:        "s1",
				OccurredAt:    time.Now(),
				SchemaVersion: "1.0",
			},
			expectedStatus:  http.StatusInternalServerError,
			shouldFailQueue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProducer.ShouldFail = tt.shouldFailQueue
			
			var body bytes.Buffer
			json.NewEncoder(&body).Encode(tt.payload)

			req := httptest.NewRequest(http.MethodPost, "/v1/ingest", &body)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}
