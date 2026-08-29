package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"redcyberfox/pkg/events"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

// PersistEvent persists the CanonicalEvent idempotently.
// Returns a boolean indicating if an alert was generated (for metrics).
func (r *Repository) PersistEvent(ctx context.Context, ev *events.CanonicalEvent) (bool, error) {
	// Start a transaction so we can atomically insert event and phase-1 alert
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Idempotent insert: if (tenant_id, event_id) exists, DO NOTHING
	res, err := tx.Exec(ctx, `
		INSERT INTO events (
			event_id, tenant_id, site_id, asset_id, sensor_id, occurred_at, received_at,
			seq_no, source, category, severity, confidence, protocol, src, dst, action,
			metadata, rule_id, rule_version, attck_enterprise, attck_ics, quality_flags, schema_version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23
		)
		ON CONFLICT (tenant_id, event_id) DO NOTHING
	`,
		ev.EventID, ev.TenantID, ev.SiteID, ev.AssetID, ev.SensorID, ev.OccurredAt, ev.ReceivedAt,
		ev.SeqNo, ev.Source, ev.Category, ev.Severity, ev.Confidence, ev.Protocol, ev.Src, ev.Dst, ev.Action,
		ev.Metadata, ev.RuleID, ev.RuleVersion, ev.AttckEnterprise, ev.AttckICS, ev.QualityFlags, ev.SchemaVersion,
	)

	if err != nil {
		return false, fmt.Errorf("failed to insert event: %w", err)
	}

	// If no rows were affected, this event was already persisted (idempotent success).
	if res.RowsAffected() == 0 {
		return false, nil
	}

	alertGenerated := false

	// Phase-1 Alert Adapter: deterministic synthetic-event condition
	if ev.Severity == "CRITICAL" || ev.Severity == "FATAL" {
		alertID := uuid.New().String()
		_, err := tx.Exec(ctx, `
			INSERT INTO alerts (alert_id, event_id, tenant_id, severity, description, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, event_id) DO NOTHING
		`,
			alertID, ev.EventID, ev.TenantID, ev.Severity,
			fmt.Sprintf("Phase-1 Alert triggered by %s event from %s", ev.Severity, ev.Source),
			time.Now().UTC(),
		)
		if err != nil {
			return false, fmt.Errorf("failed to insert phase-1 alert: %w", err)
		}
		alertGenerated = true
		log.Printf("Generated Phase-1 Alert: %s", alertID)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit tx: %w", err)
	}

	return alertGenerated, nil
}
