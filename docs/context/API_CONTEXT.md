# API Context

*See docs/implementation/api.md for authoritative details.*

## Current Endpoints

### POST /v1/ingest
- **Request**: Accepts canonical JSON event payloads.
- **Response**: 202 Accepted.
- **Validation**: Strict schema validation including UUID format, supported version (1.0.0), and required fields (source, category, occurred_at).
- **Tenant Context**: Uses development-only ?tenant_id=... parameter.
- **Error Behavior**: Returns 400 for malformed/invalid payload. 500 on internal enqueue failure.

### GET /v1/events
- **Request**: Fetch processed events.
- **Tenant Context**: Uses development-only ?tenant_id=... parameter.
- **Response**: Array of canonical events from PostgreSQL.

### GET /v1/alerts
- **Request**: Fetch generated alerts.
- **Tenant Context**: Uses development-only ?tenant_id=... parameter.
- **Response**: Array of alerts from PostgreSQL.
