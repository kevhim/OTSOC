package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"redcyberfox/pkg/events"
)

// Keep individual events bounded before parsing or forwarding to the queue.
const maxIngestBodyBytes int64 = 1 << 20

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

	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var ev events.CanonicalEvent
	if err := decoder.Decode(&ev); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Bad Request: Invalid JSON payload", http.StatusBadRequest)
		}
		return
	}
	// A single API request may contain exactly one JSON object, never trailing
	// JSON objects or arbitrary extra data.
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Bad Request: Expected a single JSON event", http.StatusBadRequest)
		}
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
