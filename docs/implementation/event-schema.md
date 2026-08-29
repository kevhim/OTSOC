# Canonical Event Schema

Version: `1.0.0`

The Canonical Event is the unified data model used across the entire RedCyberFox pipeline.

## Required Fields

- `event_id` (UUID): Unique identifier for the event.
- `tenant_id` (UUID or simple string): Tenant context. Must be valid UUID or alphanumeric with hyphens.
- `site_id` (UUID or simple string): Site context. Must be valid UUID or alphanumeric with hyphens.
- `occurred_at` (Timestamp): When the event occurred.
- `seq_no` (Integer): Monotonically increasing sequence number from the agent.
- `source` (String): Agent/Sensor name.
- `category` (String): Event category.
- `severity` (String): Must be one of `DEBUG`, `INFO`, `WARNING`, `CRITICAL`, `FATAL`.
- `schema_version` (String): Must be `1.0.0`.

## Optional Fields

- `asset_id` (String)
- `sensor_id` (String)
- `received_at` (Timestamp)
- `confidence` (Float): Must be between 0.0 and 100.0 if provided.
- `protocol` (String)
- `src` (String): Source IP/MAC.
- `dst` (String): Dest IP/MAC.
- `action` (String)
- `metadata` (JSONB)
- `rule_id` (String)
- `rule_version` (String)
- `attck_enterprise` (String array)
- `attck_ics` (String array)
- `quality_flags` (Integer)
