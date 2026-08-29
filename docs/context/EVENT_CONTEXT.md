# Event Context

*The canonical event contract is shared by endpoint, server, and future sensor components.*

## Locations
- **JSON Schema**: schemas/events.json
- **Go Implementation**: pkg/events/canonical.go

## Contract Details
- **Required Fields**: event_id (UUID), schema_version (1.0.0), source, category, occurred_at.
- **Optional Fields**: severity, confidence (0-100), quality_flags (Array of Strings).
- **Severity Enum**: DEBUG, INFO, WARNING, CRITICAL, FATAL.
- **Confidence Semantics**: If absent, it represents no defined confidence. If present, it must be exactly between 0 and 100.
- **Sequence Number**: seq_no guarantees ordering for events originating from the same source.
- **ID Semantics**: event_id is a strictly formatted UUID.
