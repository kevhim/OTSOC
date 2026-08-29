package events

import (
	"encoding/json"
	"time"
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
	Confidence      float64                `json:"confidence,omitempty"`
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
