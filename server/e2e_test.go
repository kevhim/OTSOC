package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/server/pkg/events"
)

func TestE2EFlow(t *testing.T) {
	// Simple check to see if API is up
	resp, err := http.Get("http://localhost:8081/v1/events")
	if err != nil {
		t.Skipf("API server not reachable, skipping E2E test: %v", err)
	}
	resp.Body.Close()

	// 1. Ingest a normal event
	eventID := uuid.New().String()
	now := time.Now().UTC()
	ev := events.CanonicalEvent{
		EventID:       eventID,
		TenantID:      "e2e-tenant",
		SiteID:        "e2e-site",
		OccurredAt:    now,
		SeqNo:         1,
		Source:        "e2e-test",
		Category:      "test",
		Severity:      "INFO",
		SchemaVersion: "1.0",
	}

	payload, _ := json.Marshal(ev)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:8081/v1/ingest", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to ingest event: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202 Accepted, got %d", resp.StatusCode)
	}

	// 2. Wait for worker to process
	time.Sleep(2 * time.Second)

	// 3. Verify event is available in GET /v1/events
	resp, err = http.Get("http://localhost:8081/v1/events")
	if err != nil {
		t.Fatalf("Failed to fetch events: %v", err)
	}
	defer resp.Body.Close()

	var fetchedEvents []events.CanonicalEvent
	if err := json.NewDecoder(resp.Body).Decode(&fetchedEvents); err != nil {
		t.Fatalf("Failed to decode events: %v", err)
	}

	found := false
	for _, fe := range fetchedEvents {
		if fe.EventID == eventID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Event %s not found in API response", eventID)
	}

	// 4. Test duplicate event idempotency (ON CONFLICT DO NOTHING)
	resp, err = http.DefaultClient.Do(req) // Re-send exact same request
	if err != nil {
		t.Fatalf("Failed to ingest duplicate event: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected duplicate ingest to return 202 Accepted, got %d", resp.StatusCode)
	}

	// Wait for processing
	time.Sleep(2 * time.Second)

	// 5. Test Alert Generation (severity CRITICAL)
	alertEventID := uuid.New().String()
	alertEv := ev
	alertEv.EventID = alertEventID
	alertEv.Severity = "CRITICAL"

	payload, _ = json.Marshal(alertEv)
	req, _ = http.NewRequest(http.MethodPost, "http://localhost:8081/v1/ingest", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to ingest alert event: %v", err)
	}
	resp.Body.Close()

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Verify alert is available in GET /v1/alerts
	resp, err = http.Get("http://localhost:8081/v1/alerts")
	if err != nil {
		t.Fatalf("Failed to fetch alerts: %v", err)
	}
	defer resp.Body.Close()

	var fetchedAlerts []events.Alert
	if err := json.NewDecoder(resp.Body).Decode(&fetchedAlerts); err != nil {
		t.Fatalf("Failed to decode alerts: %v", err)
	}

	alertFound := false
	for _, fa := range fetchedAlerts {
		if fa.EventID == alertEventID {
			alertFound = true
			break
		}
	}
	if !alertFound {
		t.Errorf("Alert for event %s not found in API response", alertEventID)
	}
}
