CREATE TABLE IF NOT EXISTS events (
    event_id UUID NOT NULL,
    tenant_id VARCHAR(128) NOT NULL,
    site_id VARCHAR(128) NOT NULL,
    asset_id VARCHAR(128),
    sensor_id VARCHAR(128),
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ,
    seq_no BIGINT NOT NULL,
    source VARCHAR(255) NOT NULL,
    category VARCHAR(255) NOT NULL,
    severity VARCHAR(32) NOT NULL,
    confidence REAL,
    protocol VARCHAR(128),
    src VARCHAR(255),
    dst VARCHAR(255),
    action VARCHAR(128),
    metadata JSONB,
    rule_id VARCHAR(255),
    rule_version VARCHAR(64),
    attck_enterprise TEXT[],
    attck_ics TEXT[],
    quality_flags TEXT[],
    schema_version VARCHAR(32) NOT NULL,

    -- PRIMARY KEY is effectively (tenant_id, event_id), but we will explicitly
    -- enforce idempotency via the UNIQUE constraint requested
    CONSTRAINT pk_events PRIMARY KEY (tenant_id, event_id),
    CONSTRAINT uq_events_tenant_event UNIQUE (tenant_id, event_id)
);

-- Indexes for dashboard queries and fast lookups
CREATE INDEX IF NOT EXISTS idx_events_tenant_site_time ON events (tenant_id, site_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_occurred_at ON events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_severity ON events (severity);

CREATE TABLE IF NOT EXISTS alerts (
    alert_id UUID PRIMARY KEY,
    event_id UUID NOT NULL,
    tenant_id VARCHAR(128) NOT NULL,
    severity VARCHAR(32) NOT NULL,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Foreign key to ensure alert maps to a valid event
    CONSTRAINT fk_alerts_events FOREIGN KEY (tenant_id, event_id) REFERENCES events (tenant_id, event_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_alerts_tenant_created ON alerts (tenant_id, created_at DESC);
