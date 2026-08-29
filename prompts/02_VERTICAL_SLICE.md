# Phase 1 — Vertical Slice

Build:

Agent event -> HTTPS API -> Valkey queue -> worker -> PostgreSQL -> dashboard -> visible alert.

## Implement
### Agent
A minimal synthetic event producer or stub.

### Server
- POST /v1/ingest
- validation
- canonical event normalization
- development authentication appropriate for this phase
- fast acknowledgement
- Valkey queue
- worker
- PostgreSQL persistence

### Dashboard
Show:
- event count
- recent events
- at least one alert

## Canonical event fields
event_id, tenant_id, site_id, asset_id, sensor_id, occurred_at, received_at, seq_no, source, category, severity, confidence, protocol, src, dst, action, metadata, rule_id, rule_version, attck_enterprise, attck_ics, quality_flags, schema_version.

Do not invent a second event model.

## Tests
- event reaches PostgreSQL
- dashboard displays it
- duplicate behavior is deterministic
- invalid payload rejected
- worker restart is safe

## Exit criterion
One test endpoint produces a visible alert end-to-end.
