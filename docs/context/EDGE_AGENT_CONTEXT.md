# Edge Agent Context

The agent runs locally, queues events in SQLite, and forwards to central server when online.

## Architectural Invariants
- `device_id` != `asset_id`. `device_id` = persistent RedCyberFox agent/device identity. Asset ID refers to the monitored OT asset.
- SQLite is the endpoint durability boundary. Once an event is committed here, it is considered safe locally.
- `event_id` is immutable and is the central deduplication identity throughout the pipeline.
- `seq_no` is for ordering/gap detection.
- Event and sequence number allocation are processed in one atomic SQLite transaction.
- PENDING -> HTTP POST -> 202 -> local removal (Local events are deleted only after a 202 Accepted response from the server).
- network/429/500/503 -> retry (Transient failures result in backoff and retry).
- 400 -> local DLQ (Permanent failures like malformed payloads are moved to the Dead Letter Queue, rather than retried).
- DLQ insertion and original event removal are atomic in SQLite.
- CRITICAL/FATAL events are protected by a soft-quota policy to ensure they are not dropped under load.
- DB/WAL/SHM files all contribute to the storage footprint budget and must be accounted for.
- SQLite corruption must not be silently replaced (The agent must fail fast or quarantine rather than silently zeroing out data).
- Alpha implementation uses serialized SQLite access intentionally to avoid concurrency bugs in early versions.
