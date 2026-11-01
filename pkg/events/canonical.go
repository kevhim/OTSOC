package events

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// CanonicalEvent represents the normalized telemetry event model
// defined in ADR 0006. It acts as the system-wide contract for all
// data passing through the RedCyberFox pipeline.
type CanonicalEvent struct {
	EventID         string                 `json:"event_id"`
	TenantID        string                 `json:"tenant_id"`
	SiteID          string                 `json:"site_id"`
	AssetID         string                 `json:"asset_id,omitempty"`
	SensorID        string                 `json:"sensor_id,omitempty"`
	OccurredAt      time.Time              `json:"occurred_at"`
	ReceivedAt      time.Time              `json:"received_at,omitempty"`
	SeqNo           int64                  `json:"seq_no"`
	Source          string                 `json:"source"`
	Category        string                 `json:"category"`
	Severity        string                 `json:"severity"`
	Confidence      *float64               `json:"confidence,omitempty"`
	Protocol        string                 `json:"protocol,omitempty"`
	Src             string                 `json:"src,omitempty"`
	Dst             string                 `json:"dst,omitempty"`
	Action          string                 `json:"action,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	RuleID          string                 `json:"rule_id,omitempty"`
	RuleVersion     string                 `json:"rule_version,omitempty"`
	AttckEnterprise []string               `json:"attck_enterprise,omitempty"`
	AttckICS        []string               `json:"attck_ics,omitempty"`
	QualityFlags    []string               `json:"quality_flags,omitempty"`
	SchemaVersion   string                 `json:"schema_version"`
}

// Alert represents a Phase-1 Alert derived from events
type Alert struct {
	AlertID     string    `json:"alert_id"`
	EventID     string    `json:"event_id"`
	TenantID    string    `json:"tenant_id"`
	Severity    string    `json:"severity"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Serialize converts the event to JSON
func (e *CanonicalEvent) Serialize() ([]byte, error) {
	return json.Marshal(e)
}

// Deserialize loads the event from JSON
func Deserialize(data []byte) (*CanonicalEvent, error) {
	var e CanonicalEvent
	err := json.Unmarshal(data, &e)
	return &e, err
}

// CurrentSchemaVersion defines the canonical version expected by this implementation
const CurrentSchemaVersion = "1.0.0"

var tenantSiteRegex = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// Validate ensures the canonical event strictly adheres to the schema.
func (e *CanonicalEvent) Validate() error {
	if _, err := uuid.Parse(e.EventID); err != nil {
		return fmt.Errorf("invalid event_id: %w", err)
	}

	if e.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if e.Source == "" {
		return fmt.Errorf("source is required")
	}
	if e.Category == "" {
		return fmt.Errorf("category is required")
	}

	if e.TenantID == "" || !tenantSiteRegex.MatchString(e.TenantID) || len(e.TenantID) > 128 {
		return fmt.Errorf("invalid tenant_id")
	}
	if e.SiteID == "" || !tenantSiteRegex.MatchString(e.SiteID) || len(e.SiteID) > 128 {
		return fmt.Errorf("invalid site_id")
	}

	if e.SeqNo < 0 {
		return fmt.Errorf("seq_no cannot be negative")
	}

	switch e.Severity {
	case "DEBUG", "INFO", "WARNING", "CRITICAL", "FATAL":
		// valid
	default:
		return fmt.Errorf("invalid severity: %s", e.Severity)
	}

	// Mirror PostgreSQL VARCHAR limits before queuing. Otherwise the worker
	// retries an event that can never fit in its target database columns.
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"asset_id", e.AssetID, 128}, {"sensor_id", e.SensorID, 128},
		{"source", e.Source, 255}, {"category", e.Category, 255},
		{"protocol", e.Protocol, 128}, {"src", e.Src, 255},
		{"dst", e.Dst, 255}, {"action", e.Action, 128},
		{"rule_id", e.RuleID, 255}, {"rule_version", e.RuleVersion, 64},
	} {
		if utf8.RuneCountInString(field.value) > field.limit {
			return fmt.Errorf("%s exceeds maximum length of %d", field.name, field.limit)
		}
	}

	if e.Confidence != nil {
		if math.IsNaN(*e.Confidence) || math.IsInf(*e.Confidence, 0) || *e.Confidence < 0 || *e.Confidence > 100 {
			return fmt.Errorf("confidence must be between 0 and 100")
		}
	}

	// Ensure SchemaVersion matches supported versions exactly
	if e.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version: %s (expected %s)", e.SchemaVersion, CurrentSchemaVersion)
	}

	return nil
}
