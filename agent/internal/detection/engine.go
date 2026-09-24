package detection

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/pkg/events"
)

// Rule represents a deterministic local detection rule.
type Rule interface {
	ID() string
	Version() string
	Severity() string
	Confidence() float64
	Reason() string
	AttckEnterprise() []string
	AttckICS() []string
	Evaluate(ev *events.CanonicalEvent) (bool, error)
}

// Engine evaluates durable telemetry events against a set of rules.
type Engine struct {
	db    interfaces.Storage
	rules []Rule
}

// NewEngine creates a new deterministic synchronous detection engine.
func NewEngine(db interfaces.Storage, rules []Rule) *Engine {
	return &Engine{
		db:    db,
		rules: rules,
	}
}

// Evaluate runs all loaded rules against the event and persists any findings.
func (e *Engine) Evaluate(ctx context.Context, ev *events.CanonicalEvent) {
	// Panic isolation guarantee
	defer func() {
		if r := recover(); r != nil {
			// Log explicit detection failure, do not crash endpoint, do not rollback telemetry
			log.Printf("[Detection Engine] Recovered from panic during Evaluate loop: %v", r)
		}
	}()

	// Do not evaluate findings against rules to prevent loops, though the rule logic might do this too.
	if ev.Category == "detection/finding" {
		return
	}

	for _, rule := range e.rules {
		// Individual rule evaluation panic boundary
		e.evaluateRuleSafe(ctx, ev, rule)
	}
}

func (e *Engine) evaluateRuleSafe(ctx context.Context, ev *events.CanonicalEvent, rule Rule) {
	defer func() {
		if r := recover(); r != nil {
			// Rule-specific panic contained
			log.Printf("[Detection Engine] Recovered from rule-specific panic (rule: %s, event: %s): %v", rule.ID(), ev.EventID, r)
		}
	}()

	matched, err := rule.Evaluate(ev)
	if err != nil {
		// Log explicit evaluation error, continue to next rule
		log.Printf("[Detection Engine] Explicit rule evaluation error (rule: %s, event: %s): %v", rule.ID(), ev.EventID, err)
		return
	}

	if matched {
		finding, err := e.createFinding(ev, rule)
		if err != nil {
			return
		}

		if err := finding.Validate(); err != nil {
			log.Printf("[Detection Engine] Explicit finding validation error (rule: %s, event: %s): %v", rule.ID(), ev.EventID, err)
			return
		}

		err = e.db.Store(ctx, finding)
		if err != nil {
			log.Printf("[Detection Engine] Explicit persistence error (rule: %s, event: %s): failed to store finding: %v", rule.ID(), ev.EventID, err)
		}
	}
}

// createFinding generates a derived CanonicalEvent representing the detection finding.
func (e *Engine) createFinding(ev *events.CanonicalEvent, rule Rule) (*events.CanonicalEvent, error) {
	// Deduplication identity: deterministic UUID based on rule + telemetry event id.
	// UUIDv5 based on OID namespace for deterministic generation.
	namespace := uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
	dedupString := fmt.Sprintf("%s:%s:%s:%s", ev.TenantID, rule.ID(), rule.Version(), ev.EventID)
	// Phase 3 Gate 2: Deterministic UUIDv5 (SHA-1) for finding identity
	findingID := uuid.NewSHA1(namespace, []byte(dedupString)).String()

	finding := &events.CanonicalEvent{
		EventID:         findingID,
		TenantID:        ev.TenantID,
		SiteID:          ev.SiteID,
		AssetID:         ev.AssetID,
		SensorID:        ev.SensorID,
		OccurredAt:      ev.OccurredAt, // True idempotency: use deterministic source event time
		Source:          "local_detection",
		Category:        "detection/finding",
		Severity:        rule.Severity(),
		Confidence:      func() *float64 { v := rule.Confidence(); return &v }(),
		RuleID:          rule.ID(),
		RuleVersion:     rule.Version(),
		AttckEnterprise: rule.AttckEnterprise(),
		AttckICS:        rule.AttckICS(),
		Metadata: map[string]interface{}{
			"reason":             rule.Reason(),
			"evidence_event_ids": []string{ev.EventID},
		},
		SchemaVersion: events.CurrentSchemaVersion,
	}

	return finding, nil
}
