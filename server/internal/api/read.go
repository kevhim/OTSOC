package api

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"redcyberfox/pkg/events"
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

	tenantID := r.URL.Query().Get("tenant_id")
	tenantRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if tenantID == "" || !tenantRegex.MatchString(tenantID) {
		http.Error(w, "Bad Request: Missing or invalid tenant_id (development-only mechanism)", http.StatusBadRequest)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT event_id, tenant_id, site_id, occurred_at, severity, source, category, seq_no
		FROM events
		WHERE tenant_id = $1
		ORDER BY occurred_at DESC
		LIMIT 50
	`, tenantID)
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
	
	if err := rows.Err(); err != nil {
		log.Printf("Error iterating event rows: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
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

	tenantID := r.URL.Query().Get("tenant_id")
	tenantRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if tenantID == "" || !tenantRegex.MatchString(tenantID) {
		http.Error(w, "Bad Request: Missing or invalid tenant_id (development-only mechanism)", http.StatusBadRequest)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT alert_id, event_id, tenant_id, severity, description, created_at
		FROM alerts
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, tenantID)
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

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating alert rows: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if result == nil {
		result = []events.Alert{}
	}

	json.NewEncoder(w).Encode(result)
}
