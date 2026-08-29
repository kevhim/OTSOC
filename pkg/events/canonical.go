package events

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const CurrentSchemaVersion = "1.0.0"

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

// Validate ensures the canonical event strictly adheres to the schema.
func (e *CanonicalEvent) Validate() error {
	if _, err := uuid.Parse(e.EventID); err != nil {
		return fmt.Errorf("invalid event_id: %w", err)
	}

	tenantSiteRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if e.TenantID == "" || !tenantSiteRegex.MatchString(e.TenantID) {
		return fmt.Errorf("invalid tenant_id")
	}
	if e.SiteID == "" || !tenantSiteRegex.MatchString(e.SiteID) {
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

	if e.Confidence != nil {
		if *e.Confidence < 0 || *e.Confidence > 100 {
			return fmt.Errorf("confidence must be between 0 and 100")
		}
	}

	// Ensure SchemaVersion matches supported versions exactly
	if e.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version: %s (expected %s)", e.SchemaVersion, CurrentSchemaVersion)
	}

	return nil
}
