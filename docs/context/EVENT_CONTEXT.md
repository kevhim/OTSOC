# Event Context

Events are the primary data structure for the OT SOC pipeline.

## Architectural Invariants
- **Canonical Event Contract Location**: 
  - JSON: `schemas/events.json`
  - Go: `pkg/events/canonical.go`
- **Current Schema Version**: The current schema version is `1.0.0`.
- **event_id semantics**: The `event_id` is a UUID generated at the edge, strictly immutable, and acts as the universal deduplication key across the entire pipeline.
- **seq_no semantics**: 
  - `seq_no` is strictly monotonically increasing for durably committed events within a device.
  - Gaps are allowed.
  - Gaps are observable telemetry-gap indicators.
  - `seq_no` is for ordering/gap detection, not deduplication.
- **Identity Distinctions**:
  - `tenant`: The overarching customer/organization.
  - `site`: A physical location or logical grouping within a tenant.
  - `asset`: The specific OT machinery or endpoint being monitored.
  - `device`: The hardware/software agent instance running the collection.
- **No Second CanonicalEvent Type**: There is strictly one `CanonicalEvent` type used throughout the system; we do not maintain separate internal vs. external event types.
