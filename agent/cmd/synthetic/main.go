package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// We duplicate the CanonicalEvent here so the agent doesn't need to depend on the server module
type CanonicalEvent struct {
	EventID       string    `json:"event_id"`
	TenantID      string    `json:"tenant_id"`
	SiteID        string    `json:"site_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	SeqNo         int64     `json:"seq_no"`
	Source        string    `json:"source"`
	Category      string    `json:"category"`
	Severity      string    `json:"severity"`
	SchemaVersion string    `json:"schema_version"`
}

func main() {
	log.Println("Starting RedCyberFox Synthetic Agent")
	
	targetURL := "http://localhost:8081/v1/ingest"

	var seqNo int64 = 1

	for {
		// Create normal event
		ev := generateEvent("INFO", seqNo)
		sendEvent(targetURL, ev)
		seqNo++

		// Periodically create a CRITICAL event to trigger a Phase-1 Alert
		if seqNo%5 == 0 {
			critEv := generateEvent("CRITICAL", seqNo)
			sendEvent(targetURL, critEv)
			seqNo++
		}

		time.Sleep(5 * time.Second)
	}
}

func generateEvent(severity string, seqNo int64) CanonicalEvent {
	return CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-alpha",
		SiteID:        "site-main",
		OccurredAt:    time.Now().UTC(),
		SeqNo:         seqNo,
		Source:        "synthetic-agent",
		Category:      "network_flow",
		Severity:      severity,
		SchemaVersion: "1.0.0",
	}
}

func sendEvent(url string, ev CanonicalEvent) {
	data, _ := json.Marshal(ev)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Failed to send event %s: %v", ev.EventID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		log.Printf("Successfully sent event %s (Seq: %d, Severity: %s)", ev.EventID, ev.SeqNo, ev.Severity)
	} else {
		log.Printf("Failed to send event %s. Status: %d", ev.EventID, resp.StatusCode)
	}
}
