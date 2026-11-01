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
- **Enrichment Stage (Resolved in 2E.4)**: `agent/internal/ingestion/engine.go` provides the single authoritative enrichment boundary (`TenantID`, `SiteID`, `AssetID` matching SQLite `GetDeviceID()`) prior to `Storage.Store()`.

## Bounded Measurement Reporting & Workload Invariants
- **Machine-Readable Reports (`agent/internal/resourcereport`)**: Opt-in via `-measure-report`, `-measure-profile`, `-measure-duration`, `-measure-interval`.
- **Exclusive Creation & Conflict Rejection**: Rejects overwrite (`os.O_EXCL`), rejects collisions with `agent.db`, `-wal`, `-shm`, `identity.json`, `agent.json`, and `agent_emergency_spill.log`. Rejects paths inside watched directories (`cfg.MonitorPaths`) to prevent self-generated filesystem telemetry.
- **Metric Integrity**: Missing or unready measurements remain `null` in JSON (pointers), never fabricated as `0`.
- **Shared Shutdown Path**: OS signals and measurement expiry invoke `resourcereport.ShutdownCoordinator`, starting the single global 10-second drain deadline exactly once via `sync.Once`. Bypassing the deadline with `cancel()` alone is prohibited.
- **Detection Workload & Platform Scope**: Production endpoint loads `RealIOCExecRule` (`LOCAL-IOC-EXEC-001`, `v1.0`). Fixture matching `executable_path == "/opt/malicious/bin/miner"` is metadata matching, not YARA scanning. Live Windows collectors monitor real Windows executables and do not emit this Linux path during normal operation; production-path integration tests explicitly exercise this path and verify derived findings linked via `evidence_event_ids` in SQLite.

## Ingestion Accounting, Backpressure & Finalize-After-Drain Invariants
- **Finalize-After-Drain Shutdown Ordering**: Measurement expiry requests termination via `resourcereport.ShutdownCoordinator` but does NOT finalize the report immediately. Final reporting occurs only AFTER collectors stop, the event queue closes, ingestion loop drains all in-flight items, and the forwarder stops. Durable storage state (`InspectDurableStorage`) is queried while the database connection is still open immediately prior to closing SQLite. Any report generation or drain error propagates to process exit code (`os.Exit(1)`).
- **Ingestion-Outcome Reconciliation**: The accounting equation $\text{Ingested} = \text{Committed} + \text{FailedBeforeCommit} + \text{RecoveredDLQ} + \text{SpilledDisk} + \text{Dropped}$ strictly represents local ingestion-loop outcome reconciliation, guarded by `accMu sync.Mutex` for coherent point-in-time snapshots. It is NEVER labeled end-to-end reconciliation or proof of zero loss, as it does not observe upstream network hops, server-side processing, or retention pruning.
- **Forwarder Accounting Distinctions**: Forwarder metrics distinguish HTTP request attempts (including retries) from unique events receiving HTTP 202 (`HTTPAcceptedTotal`), successful SQLite local row deletions (`LocalRemovedTotal`), and deletion failures (`LocalRemovalErrorsTotal`).
- **Durable Storage Inventory**: Storage inspects SQLite directly to distinguish source telemetry from derived detection findings (`payload LIKE '%"category":"detection/finding"%'`), pending events, DLQ events, and existing startup backlog. Unsupported metrics (such as historical retention pruning) remain explicitly unavailable (`nil` / JSON `null`).
- **Backpressure Semantics**: Queue backpressure semantics strictly distinguish:
  - `BlockedEnqueueAttemptsTotal`: counts each distinct enqueue attempt where a producer was blocked waiting for downstream capacity because the queue was at 100% capacity.
  - `PressureEpisodesTotal`: counts discrete transitions into the 100% saturated state (`count == capacity`).
  - `TimeSpentFullSeconds`: cumulative duration the queue spent at 100% saturation.
  Queue-loop iterations are never counted as blocked event submissions.
