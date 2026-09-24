# Detection Context

Phase 3 is IN PROGRESS.
Gate 1 (Boundary Setup) is CLOSED.
Gate 2 (Foundation Hardening) is CLOSED.
Gate 3 (Detection Engine Robustness) is CLOSED.
## Local Detection Foundation
The edge agent now implements a lightweight, deterministic, synchronous local detection engine (`agent/internal/detection`).

### Pipeline Boundary
- Telemetry durability is independently guaranteed.
- CanonicalEvents flow through SQLite `Store()`.
- ONLY upon successful `Store()` does `OnCommitted` invoke detection evaluation.
- Detection failure cannot discard or rollback the original telemetry.
- Findings are emitted as derived CanonicalEvents and stored securely in SQLite.

### Finding Representation
- A finding is just a CanonicalEvent with `source="local_detection"` and `category="detection/finding"`.
- It references the original telemetry event ID via `Metadata["evidence_event_ids"] = []string{originalEventID}`.
- Findings carry metadata like `rule_id`, `rule_version`, `severity`, `confidence`, and `reason`.
- The original telemetry event remains unmodified.

### Finding Identity and Deduplication
- `finding.event_id` is deterministically generated using **UUIDv5 (SHA-1)** mapped against an OID namespace.
- Canonical input string: `tenant_id:rule_id:rule_version:original_event_id`.
- Changing any component strictly produces a distinct finding identity without collisions.
- Duplicate evaluations remain completely idempotent.

### Rule Evaluation Order
- Rule execution is deterministic by iterating through a strictly ordered sequence (`[]Rule`), never relying on arbitrary map iteration.

### Error and Panic Isolation
- If a specific rule panics or returns an explicit evaluation error, the isolation boundary (`defer recover()`) catches the panic, aborts finding generation for that specific rule, and explicitly allows remaining rules and subsequent telemetry to continue processing smoothly without endpoint termination or telemetry rollback.
- Persistence failure of a finding is isolated and never rolls back original telemetry.

### Current IOC Fixture Limitations
- **`LOCAL-IOC-EXEC-001`**: A Foundation / Deterministic IOC Fixture.
- It is NOT a real-world malicious IOC feed and does not use external reputation services.
- Any confidence used by a rule must be explicitly identified as **RULE-DEFINED CONFIDENCE** unless it is actually empirically measured. For the fixture, 100.0 is rule-defined.
- Unsupported ATT&CK mappings (such as T1059 for mere executable path matching) have been removed.
- The Canonical Event currently lacks a reliable hash field, so a hash-based rule (`LOCAL-IOC-HASH-001`) could not be implemented. This is a documented schema/input gap.

### Synchronous Execution
- Benchmark evidence (~8.35 microseconds or 8355 ns/op, 7194 B/op, 100 allocs/op on Windows/amd64 13th Gen Intel i7) supports maintaining a purely synchronous evaluation pipeline for the current deterministic rule set. No async queues or worker pools are required yet.

### Resource Model & Passive/Offline Guarantees
- Bounded memory usage. No asynchronous workers or unbounded queues yet.
- Zero network traffic or OT state alterations triggered by rule evaluations.
