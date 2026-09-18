# Phase 2E.4 Cross-Source Regression Audit

## 1. Current Implementation Inventory

- **Active OS Collectors**: Process, Filesystem, Inventory, Network, USB (Windows & Linux).
- **Shared Pipeline**: `centralEvents` (buffered at 200) -> `main.go` ingestion loop.
- **Storage**: SQLite WAL-mode durability with quota pruning.
- **Forwarder**: Background loop reading from SQLite and POSTing to API.

## 2. Cross-Source Architecture Map

1. **Collectors** emit `events.CanonicalEvent` to the shared `centralEvents` channel.
2. **Ingestion Loop** (single goroutine in `main.go`) reads from `centralEvents`.
3. **Enrichment**: Ingestion loop injects `TenantID`, `SiteID`, and `AssetID`.
4. **Storage**: Ingestion loop persists to SQLite with a 2-second context timeout.
5. **Forwarder**: Polling SQLite for `PENDING` events and forwarding them via HTTP.
6. **Resolution**: `202 Accepted` removes the event; `400` routes to DLQ; `429`/`5xx` backs off.

## 3. Shared-Resource Analysis

- **centralEvents**: All collectors write to an unbuffered or small-buffered channel (`make(chan *events.CanonicalEvent, 200)`). If filled, collectors block on `select { case out <- ev }`.
- **Unbounded Queues**: None in memory. `internalOut` in `usb` is bounded to 100.
- **Starvation**: Goroutines compete fairly for channel sends, but a high-throughput source (e.g. filesystem) could temporarily congest the channel.
- **Deduplication**: Process collector performs domain-level deduplication. SQLite ignores duplicate `event_id` + identical payload insertions idempotently.

## 4. Lifecycle/Shutdown Analysis

- **Termination**: Collectors exit deterministically via `ctx.Done()`.
- **Channel Closures**: `main.go` properly closes `centralEvents` *after* all `Stop()` calls return, avoiding panics on send to closed channel.
- **Windows vs Linux**: Windows USB gracefully shuts down via `WM_CLOSE` to its message loop. Linux USB uses a 1s socket read timeout to ensure prompt cancellation.
- **Duplicate Cleanups**: Production code is clean. The validation harness script (`validate_linux_usb.sh`) has a harmless idempotent trap sequence.

## 5. Event Identity/Order Analysis

- **Event Identity (`event_id`)**: Mixed responsibility. Collectors like `Inventory` and `USB` assign UUIDs internally. `sqlite.go:211` also injects a UUID if it receives an event with `EventID == ""`. This violates strict identity ownership.
- **Order (`seq_no`)**: Assigned globally by SQLite. Concurrency is resolved entirely by the order events are dequeued from `centralEvents`.
- **Immutability**: `event_id` and `seq_no` are immutable once generated and are persisted reliably through the DLQ.

## 6. Failure-Mode Analysis

- **SILENT DROP (CRITICAL)**: In `main.go:139`, `db.Store(storeCtx, ev)` uses a 2-second timeout. If the SQLite database is locked (e.g., during WAL checkpoint or heavy load) and times out, the ingestion loop logs an error and executes `continue`. **The event is dropped from memory and never reaches the database or DLQ.**
- **Storage Quota**: Handles disk pressure by dropping oldest `INFO`, `DEBUG`, or `WARNING` events.
- **Forwarder API Failures**: Correctly distinguishes `400` (DLQ) vs `429` (Retry-After) vs `5xx` (Exponential backoff).

## 7. Resource Contention Analysis

- **Goroutines**: Bounded per collector (usually 1-2).
- **Memory**: Controlled by 200-event channel buffer and periodic SQLite `wal_checkpoint(PASSIVE)`.
- **SQLite**: The primary bottleneck. All events serialize through one ingestion loop calling `db.Store`.

## 8. Windows/Linux Consistency Analysis

- **USB Collector**: Both parse `USBEvent` and map to `USB_INSERT`/`USB_REMOVE`.
- Windows explicitly relies on `user32.dll` WNDPROC, Linux uses `NETLINK_KOBJECT_UEVENT`.
- Missing fields (e.g., `SerialNumber`) are safely ignored on both platforms.

## 9. Existing Test Coverage

- **Unit**: Collectors are tested for parsing and event emission.
- **Integration**: Process `lifecycle_test.go`, SQLite `sqlite_test.go`, and Forwarder `forwarder_test.go`.
- **Mocked Components**: Server API is mocked during forwarder tests.

## 10. Missing Test Coverage

- **Cross-Source Concurrent Load**: No test runs >2 collectors simultaneously to verify `centralEvents` backpressure.
- **Storage Timeout Drop**: No test validates behavior when `db.Store` blocks for >2 seconds.
- **Identity Enforcement**: No test verifies that an empty `event_id` is caught earlier in the pipeline.

## 11. Findings Classified

- **MUST FIX BEFORE 2E.4**:
  - `[BUG-01]` **Silent Event Drop**: `main.go` drops events on a 2-second SQLite timeout.
  - `[BUG-02]` **Mixed Identity Generation**: `event_id` is assigned inconsistently across collectors and storage.
- **PHASE-RELEVANT**:
  - `[RISK-01]` SQLite lock contention during simultaneous high-throughput bursts (Process + FS).
- **FUTURE**:
  - `[ENH-01]` Configurable storage quotas per-severity.
- **OUT OF SCOPE**:
  - Production detection logic (Phase 3).

## 12. Proposed Phase 2E.4 Acceptance Criteria

| ID | Behavior | Evidence | Test Type | Pass/Fail Condition |
| --- | --- | --- | --- | --- |
| AC-1 | Prevent Silent Drops | `main.go` handles SQLite timeouts safely (e.g., bounded retries or disk spill). | Integration | FAILS if any event is dropped silently from memory due to `context.DeadlineExceeded` in `db.Store`. |
| AC-2 | Unified Identity | `event_id` ownership is strictly defined (either Collector OR Storage). | Unit | FAILS if identity is randomly generated at multiple layers. |
| AC-3 | Cross-Source Stability | Agent handles 5 active collectors simultaneously without deadlocks. | Integration | PASSES if `cross_source_test.go` completes 10s of high-throughput mock collection cleanly. |

## 13. Proposed Test Matrix

1. All collectors active simultaneously.
2. Concurrent event production (stress test).
3. `centralEvents` pressure (channel filled).
4. Shutdown during active collection (verifies clean exit).
5. Shutdown during blocked send (verifies cancellation awareness).
6. SQLite contention (simulated storage lock).
7. Forwarder transient/permanent failure.
8. Duplicate/retry behavior.

## 14. Exact Implementation Tasks (Ordered)

1. **Task 1**: [COMPLETED] Resolve `main.go` silent drop on SQLite timeout.
2. **Task 2**: [COMPLETED] Standardize `event_id` assignment across the pipeline.
3. **Task 3**: Implement `agent/internal/integration/cross_source_test.go`.
4. **Task 4**: Execute cross-source test matrix and resolve any resulting data-race or deadlock findings.

## 15. Implementation Status

- **Fix Silent Storage Loss (Task 1)**: Implemented in `agent/cmd/endpoint/main.go`. Replaced the 2s timeout with a lifecycle context and bounded retries that explicitly crash the agent (`log.Fatalf`) if persistence fails, preventing silent data loss in memory.
- **Fix event_id Ownership (Task 2)**: Implemented. `sqlite.go` no longer generates `event_id` (returns `ErrStoreFailedBeforeCommit` if empty). `lifecycle.go` (and other collectors) strictly generate it upstream before queuing to `centralEvents`.
- **Focused Tests**: Added tests in `sqlite_test.go` and verified integration via `offline_test.go` to assert explicit failure semantics and identity preservation. All storage tests and pipeline integration tests pass locally.

## 16. Open Questions/Unknowns

- If `db.Store` is chronically locked, is crashing the agent preferable to dropping telemetry silently, or should we implement an in-memory DLQ before SQLite?
- Does the 5-minute SQLite `wal_checkpoint(PASSIVE)` adequately prevent long IO stalls under maximum OT telemetry load?
