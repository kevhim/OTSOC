package main

import (
	"bytes"
	"encoding/json"
	"flag"
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
	countFlag := flag.Int("count", 0, "Number of events to generate (0 for continuous)")
	intervalFlag := flag.Duration("interval", 10*time.Second, "Interval between events (default 10s)")
	seqFlag := flag.Int64("seq", 1, "Starting sequence number")
	severityFlag := flag.String("severity", "MIXED", "Severity of events (INFO, CRITICAL, MIXED)")
	targetURL := flag.String("url", "http://localhost:8081/v1/ingest", "Target ingest URL")
	flag.Parse()

	log.Println("Starting RedCyberFox Synthetic Agent")
	log.Printf("Config: Count=%d, Interval=%v, Seq=%d, Severity=%s", *countFlag, *intervalFlag, *seqFlag, *severityFlag)

	var seqNo = *seqFlag
	var eventsSent = 0

	for {
		if *countFlag > 0 && eventsSent >= *countFlag {
			log.Println("Reached requested event count. Exiting.")
			break
		}

		currentSeverity := *severityFlag
		if currentSeverity == "MIXED" {
			if seqNo%5 == 0 {
				currentSeverity = "CRITICAL"
			} else {
				currentSeverity = "INFO"
			}
		}

		ev := generateEvent(currentSeverity, seqNo)
		sendEvent(*targetURL, ev)
		
		seqNo++
		eventsSent++

		if *countFlag > 0 && eventsSent >= *countFlag {
			log.Println("Reached requested event count. Exiting.")
			break
		}

		time.Sleep(*intervalFlag)
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
