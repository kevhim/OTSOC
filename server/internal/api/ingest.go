package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"redcyberfox/pkg/events"
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

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		http.Error(w, "Bad Request: Missing tenant_id", http.StatusBadRequest)
		return
	}

	var ev events.CanonicalEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "Bad Request: Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if ev.TenantID != tenantID {
		http.Error(w, "Bad Request: tenant_id mismatch", http.StatusBadRequest)
		return
	}

	// Normalize
	ev.ReceivedAt = time.Now().UTC()

	if err := ev.Validate(); err != nil {
		http.Error(w, "Bad Request: Validation failed - "+err.Error(), http.StatusBadRequest)
		return
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
