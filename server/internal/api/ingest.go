package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"redcyberfox/server/pkg/events"
)

type Publisher interface {
	Publish(ctx context.Context, ev *events.CanonicalEvent) error
}

type IngestHandler struct {
	producer Publisher
}

func NewIngestHandler(producer Publisher) *IngestHandler {
	return &IngestHandler{
		producer: producer,
	}
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var ev events.CanonicalEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "Bad Request: Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Minimal Validation based on canonical schema
	if ev.EventID == "" || ev.TenantID == "" || ev.SiteID == "" || ev.OccurredAt.IsZero() || ev.SchemaVersion == "" {
		http.Error(w, "Bad Request: Missing required canonical fields (event_id, tenant_id, site_id, occurred_at, schema_version)", http.StatusBadRequest)
		return
	}

	// Normalize
	ev.ReceivedAt = time.Now().UTC()
	if ev.Severity == "" {
		ev.Severity = "INFO"
	}

	// Queue to Valkey
	if err := h.producer.Publish(r.Context(), &ev); err != nil {
		http.Error(w, "Internal Server Error: Failed to queue event", http.StatusInternalServerError)
		return
	}

	// Successfully queued, ONLY THEN return 202 Accepted
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("Accepted\n"))
}
