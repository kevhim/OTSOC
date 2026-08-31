# Event Context

Events are the primary data structure for the OT SOC pipeline.

## Architectural Invariants
- **Canonical Event Contract Location**: The canonical contract is located at `schemas/events.json` (or defined in `pkg/events/event.go`).
- **Current Schema Version**: The current schema version is `1.0.0`.
- **event_id semantics**: The `event_id` is a UUID generated at the edge, strictly immutable, and acts as the universal deduplication key across the entire pipeline.
- **seq_no semantics**: The `seq_no` is a strictly monotonic, gap-free integer per device, used for ordering and detecting dropped events.
- **Identity Distinctions**:
  - `tenant`: The overarching customer/organization.
  - `site`: A physical location or logical grouping within a tenant.
  - `asset`: The specific OT machinery or endpoint being monitored.
  - `device`: The hardware/software agent instance running the collection.
- **No Second CanonicalEvent Type**: There is strictly one `CanonicalEvent` type used throughout the system; we do not maintain separate internal vs. external event types.
