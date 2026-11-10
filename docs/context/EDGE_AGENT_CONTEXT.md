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
- **Finalize-After-Drain Shutdown Ordering**: Measurement expiry requests termination via `resourcereport.ShutdownCoordinator` but does NOT finalize the report immediately. The shared production implementation `resourcereport.ExecuteShutdown` executes:
  1. OS collectors stop (`procCol`, `fsCol`, `invCol`, `netCol`, `usbCol`, `healthMgr`); all `Stop()` errors are captured.
  2. Event queue closes non-blockingly (`eventQ.Close()`).
  3. Ingestion loop finishes draining all remaining in-flight items (`ingestWg.Wait()`, `eventQ.WaitDrain()`).
  4. Forwarder stops (`fwd.Stop()`); any `Stop()` error is captured.
  5. Measurement runner goroutine is joined (`MeasureJoin`); any runner error is captured.
  6. Global drain deadline check records an error if the drain deadline expired.
  7. Durable storage state (`InspectDurableStorage`) and point-in-time accounting snapshots from ingestion and forwarder are recorded BEFORE closing SQLite.
  8. SQLite storage is closed (`params.Storage.Close()`); any close error is captured into shutdown errors.
  9. Report is finalized (`reporter.Finalize`). If errors occurred during collector stop, forwarder stop, measurement run, drain timeout, or storage close, outcome truthfully reflects `_WITH_ERRORS: ...`.
  10. Any error during shutdown, drain, report generation, or DB close propagates to process exit code (`os.Exit(1)`).
- **Ingestion-Outcome Reconciliation**: The accounting equation $\text{Ingested} = \text{Committed} + \text{FailedBeforeCommit} + \text{RecoveredDLQ} + \text{SpilledDisk} + \text{Dropped}$ strictly represents local ingestion-loop outcome reconciliation, guarded by `accMu sync.Mutex` for coherent point-in-time snapshots. It is NEVER labeled end-to-end reconciliation or proof of zero loss, as it does not observe upstream network hops, server-side processing, or retention pruning.
- **Forwarder Accounting Distinctions**: Forwarder metrics distinguish HTTP request attempts (including retries) from total accepted HTTP responses (HTTP 202) received (`HTTPAcceptedTotal`, including repeats/retries), successful SQLite local row deletions (`LocalRemovedTotal`), and deletion failures (`LocalRemovalErrorsTotal`). It does NOT claim unique-event delivery.
- **Durable Storage Inventory**: Storage inspects SQLite directly to distinguish source telemetry from derived detection findings (`payload LIKE '%"category":"detection/finding"%'`), pending events, DLQ events, and existing startup backlog. Unsupported metrics (such as historical retention pruning) remain explicitly unavailable (`nil` / JSON `null`).
- **Bounded Queue Memory & Backpressure Semantics**:
  - `maxWaiters`: Queue-owned memory is bounded by `capacity + maxWaiters`. Submissions beyond `maxWaiters` fail immediately with `ErrQueueAtCapacity`. No unbounded slice growth.
  - `AdapterHeldEvent`: Senders completing `q.In() <- ev` transfer ownership to the queue adapter. When `Close()` is called while the adapter is blocked, the adapter-held event is NOT discarded; it is preserved for admission and drained via `Out()` as soon as consumer capacity becomes available. Total queue-retained events across ring, waiters, and adapter remain strictly bounded by $\le \text{capacity} + \text{maxWaiters} + 1$.
  - `AdapterBlockedEnqueueAttemptsTotal` (aliased as `BlockedEnqueueAttemptsTotal`): counts each discrete event enqueue attempt where the input pump/adapter or direct caller was blocked waiting for queue capacity. It is explicitly scoped to adapter waits against the queue rather than claiming an exact count of all concurrent OS collectors waiting on the Go channel rendezvous.
  - `PressureEpisodesTotal`: counts discrete transitions into the 100% saturated state (`count == capacity`).
  - `TimeSpentFullSeconds`: cumulative duration the queue spent at 100% saturation.
  - Deterministic FIFO waiter admission, context cancellation without state loss, and immediate rejection with `ErrQueueClosed` on queue closure.
  - Testing synchronization hooks (`SetAdapterBlockedHook`, `AdapterWaiting`, `AdapterHeldEvent`) allow barrier-based verification of queue saturation without timing assumptions.

## Subprocess Execution & Provenance Invariants
- **Subprocess Execution Bounding & Diagnostics**: All external commands executed during measurement and integration runs (git queries, compilation, buildinfo inspection, workload execution) are strictly governed by explicit timeout contexts via `RunBoundedCommand` and `StartManagedSubprocess`. Stdout and stderr are captured into bounded thread-safe buffers (`DefaultMaxOutputBytes = 256 KB`) with explicit `[OUTPUT TRUNCATED]` tagging when exceeded.
- **Truthful Exit Codes & Outcomes**: Subprocess outcomes distinguish `SUCCESS`, `NONZERO_EXIT`, `TIMEOUT`, `CANCELLED`, and `LAUNCH_FAILURE`. When a process fails to launch, `ExitCode` remains `nil` (`*int`) and is never fabricated. Nonzero exit codes are unwrapped from `*exec.ExitError` via `errors.As`.
- **Multi-Boundary Source Stability**: The test harness evaluates git revision and status across three checkpoints: `pre-compilation`, `post-compilation`, and `post-workload`. If working tree status mutates, qualification runs fail visibly and refuse export into repo runs. Boundary checks establish boundary-state consistency rather than proving no transient edit occurred between checkpoints.
- **Exclusive Evidence Export**: Automatic overwriting of canonical reports is removed. Evidence is exported only into unique run directories via `ExportEvidenceRun` using exclusive file creation (`WriteEvidenceExclusive` with `os.O_EXCL | os.O_CREATE`). When `EVIDENCE_EXPORT_DIR` is set externally, zero writes are made to the repository root.
- **Toolchain Provenance Parsing**: Toolchain provenance is extracted via `ParseBinaryProvenanceOutput` handling both Windows and POSIX absolute executable paths, strictly requiring `toolchain`, `revision`, and `vcs.modified` presence without silent defaulting.
- **Degraded Measurement Truthfulness**: Degraded measurement samples (`sampling_error: storage budget is unconfigured or unresolved`) are reported truthfully while `StorageBudgetBytes` remains 0; no artificial budget or error suppression is permitted. Sampling count variations at timer boundaries are documented based on exact raw JSON samples.
