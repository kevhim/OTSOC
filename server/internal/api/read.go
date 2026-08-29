package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"redcyberfox/server/pkg/events"
)

type ReadHandler struct {
	db *pgxpool.Pool
}

func NewReadHandler(db *pgxpool.Pool) *ReadHandler {
	return &ReadHandler{
		db: db,
	}
}

func (h *ReadHandler) ServeEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	rows, err := h.db.Query(context.Background(), `
		SELECT event_id, tenant_id, site_id, occurred_at, severity, source, category, seq_no
		FROM events
		ORDER BY occurred_at DESC
		LIMIT 50
	`)
	if err != nil {
		log.Printf("Query events failed: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var ev events.CanonicalEvent
		err := rows.Scan(&ev.EventID, &ev.TenantID, &ev.SiteID, &ev.OccurredAt, &ev.Severity, &ev.Source, &ev.Category, &ev.SeqNo)
		if err == nil {
			result = append(result, map[string]interface{}{
				"event_id":    ev.EventID,
				"tenant_id":   ev.TenantID,
				"site_id":     ev.SiteID,
				"occurred_at": ev.OccurredAt,
				"severity":    ev.Severity,
				"source":      ev.Source,
				"category":    ev.Category,
				"seq_no":      ev.SeqNo,
			})
		}
	}
	
	if result == nil {
		result = []map[string]interface{}{}
	}

	json.NewEncoder(w).Encode(result)
}

func (h *ReadHandler) ServeAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	rows, err := h.db.Query(context.Background(), `
		SELECT alert_id, event_id, tenant_id, severity, description, created_at
		FROM alerts
		ORDER BY created_at DESC
		LIMIT 50
	`)
	if err != nil {
		log.Printf("Query alerts failed: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var result []events.Alert
	for rows.Next() {
		var al events.Alert
		err := rows.Scan(&al.AlertID, &al.EventID, &al.TenantID, &al.Severity, &al.Description, &al.CreatedAt)
		if err == nil {
			result = append(result, al)
		}
	}

	if result == nil {
		result = []events.Alert{}
	}

	json.NewEncoder(w).Encode(result)
}
