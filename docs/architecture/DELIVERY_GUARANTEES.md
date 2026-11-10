# Delivery Guarantees and Failure Boundary Specification

**Status**: Active / Phase 4 Architectural Baseline  
**Revision Date**: 2026-11-10  
**Scope**: RedCyberFox Edge Agent Durability, Forwarding, Collector Admission, and Upstream Ingestion Boundary  

---

## 1. Executive Summary and System Boundary

The RedCyberFox Edge Agent provides **at-least-once durable telemetry delivery** from constrained industrial edge nodes to central ingest gateways. The durability and delivery model is explicitly partitioned across architectural boundaries:

```
[ OS Kernel Events (ETW, ReadDirectoryChangesW, Netlink) ]
                             │
                             ▼ (Collector Ingestion & Admission Boundary)
[ Event Queue (Bounded In-Memory Ring Buffer) ]
                             │
                             ▼ (Ingestion Engine Batch / ProcessEvent)
[ Durable SQLite Storage (WAL Mode) ] ──(Commit)──► [ Disk Database / WAL ]
                             │
                             ▼ (Forwarder Draining Loop)
[ Forwarder Engine ]
                             │
═════════════════════════════ ARCHITECTURAL BOUNDARY ═════════════════════════════
                             │ (HTTP POST /v1/ingest)
                             ▼
[ Ingest API Gateway ] ──(HTTP 202 Accepted)──► [ Central Durable Cluster ]
```

### The Edge Durability Contract

1. **Collector Admission Boundary**: OS event notifications (such as Windows `ReadDirectoryChangesW` or Linux `inotify`/`eBPF`) are received asynchronously by OS kernel buffers before entering the agent's collectors. If OS kernel buffers overflow under extreme event rates before the collector can drain them (e.g. `ERROR_NOTIFY_ENUM_DIR` on Windows), notifications may be lost at the OS boundary before admission. Once admitted by collectors into `eventqueue.Queue`, events are subject to queue bounds and backpressure.
2. **Local Durability Boundary**: An event is considered ingested by the edge agent *only after* it is durably committed to the local SQLite database (`events` table) in Write-Ahead Logging (WAL) mode.
3. **Forwarding Boundary**: An event remains durably stored on disk with `state = 'PENDING'` until an HTTP 202 Accepted response is received from the upstream ingest gateway.
4. **Local Removal**: Upon receiving HTTP 202 Accepted, the forwarder executes `RemoveEvent` within a database transaction.
5. **Boundary of Responsibility**: HTTP 202 Accepted represents **edge transport acceptance** by the ingress gateway. Durable persistence across central replicas, message queues, and long-term storage is strictly the responsibility of the central platform after HTTP 202. The edge agent does NOT claim end-to-end central durability once HTTP 202 is acknowledged.

---

## 2. Failure Boundaries and Mitigations

The edge agent forwarder and storage subsystems are hardened against delivery failure boundaries, verified by deterministic integration tests (`TestDeliveryBoundary_*` and `TestCrashBoundary_*` in `agent/internal/integration/delivery_boundaries_test.go`).

### Boundary 1: Receiver Unavailable (Connection Refused / Network Down)

- **Failure Condition**: The upstream gateway cannot be reached (DNS failure, link down, host offline, TCP connection refused).
- **Edge Behavior**:
  - `f.httpClient.Do()` returns a transport network error.
  - Forwarder atomically transitions `networkState` to `OFFLINE`.
  - The attempted event remains in `events` with `state = 'PENDING'`.
  - `attempt_count` increments monotonically.
  - `next_attempt_at` is scheduled with deterministic exponential backoff and jitter.
  - **Guarantee**: Zero event loss from SQLite storage. The event backlog accumulates durably on disk up to the configured storage quota.

### Boundary 2: Receiver Reconnecting (Backlog Recovery)

- **Failure Condition**: Upstream gateway recovers after an extended outage.
- **Edge Behavior**:
  - The next forwarder attempt (triggered by `Wakeup()` or scheduled retry timer) succeeds.
  - Forwarder atomically transitions `networkState` to `ONLINE`.
  - Backlog draining resumes in monotonic FIFO order (`ORDER BY seq_no ASC`).
  - **Guarantee**: Backlog is processed sequentially; all previously unforwarded events are delivered and removed upon HTTP 202.

### Boundary 3: Timeout During Upload (Partial Write / Stalled Gateway)

- **Failure Condition**: The gateway accepts the TCP connection but stalls or fails to respond before the client deadline (`f.httpClient.Timeout`).
- **Edge Behavior**:
  - The HTTP context deadline expires (`context.DeadlineExceeded`).
  - Forwarder transitions `networkState` to `OFFLINE`.
  - The in-flight event is marked failed with exponential backoff.
  - The event is **not removed** from SQLite.
  - **Guarantee**: No premature deletion on ambiguous transport timeout.

### Boundary 4: Lost Acknowledgement and Duplicate Retry

- **Failure Condition**: The gateway receives the event and queues it, but the network connection breaks before the HTTP 202 response reaches the edge agent.
- **Edge Behavior**:
  - The edge agent experiences a transport failure and records the attempt as failed.
  - On the next retry attempt, the edge agent re-sends the exact same payload with the identical `event_id` and `seq_no`.
  - The upstream gateway MUST implement idempotent deduplication using `event_id`.
  - When the gateway returns HTTP 202 on retry, the edge agent removes the event locally.
  - **Guarantee**: At-least-once delivery. Upstream must enforce deduplication; edge accounting truthfully records multiple attempts and 1 local removal.

### Boundary 5: Agent Process Crash Recovery (Unannounced Process Termination)

- **Failure Condition**: The edge agent process terminates abruptly (simulated via `SIGKILL` or Windows `TerminateProcess` / `Process.Kill()`) while events are committed and pending in SQLite.
- **Edge Behavior**:
  - SQLite WAL mode ensures atomic frame commits and automatic recovery upon reopening.
  - Upon process restart, `Init()` runs integrity checks and recovers uncheckpointed WAL frames.
  - All pending events retain their exact `event_id`, monotonically increasing `seq_no`, and `state = 'PENDING'`.
  - Forwarder immediately resumes forwarding from the first pending event.
  - **Guarantee**: Full durability across unannounced process terminations (SIGKILL).
  - **Power-Loss Limitation Note**: Software-level process termination tests prove SQLite WAL recovery from abrupt process death. They **do not** prove durability against sudden physical power loss. Physical power-off survival additionally depends on hardware-level disk write-cache flushing (`FLUSH CACHE`), disk controller volatile memory policies, and OS filesystem barrier semantics. Physical power-cut qualification requires hardware with battery-backed write caches or non-volatile media.

### Boundary 6: Abrupt Termination During Forwarding Removal

- **Failure Condition**: The agent receives HTTP 202 and begins `RemoveEvent`, but the process terminates before the SQLite deletion transaction commits.
- **Edge Behavior**:
  - SQLite transaction atomicity ensures uncommitted `DELETE` operations are fully rolled back.
  - Upon restart, the event remains in `state = 'PENDING'`.
  - The forwarder will re-send the event on the next cycle (handled idempotently by the upstream server via `event_id`).
  - **Guarantee**: Prevention of silent event loss due to race conditions between HTTP response processing and disk persistence.

### Boundary 7: Upstream Acknowledgement Without Durable Receiver Persistence

- **Failure Condition**: Upstream gateway returns HTTP 202 Accepted, but crashes or drops the event before persisting it into central storage.
- **Edge Behavior**:
  - The edge agent has satisfied its delivery contract upon receiving HTTP 202 and deletes the event locally.
  - The edge agent cannot and does not monitor central platform internal durability.
  - **Guarantee**: Explicit demarcation of responsibility. The edge agent guarantees delivery *to the gateway*; end-to-end central durability requires gateway-level replication before HTTP 202 acknowledgement.

---

## 3. Truthful Accounting Terminology and Reconciliation Balance

To prevent misleading claims of "lossless delivery" or "end-to-end zero loss", all edge accounting adheres to an exhaustive balance equation.

### The Complete Ingestion Balance Equation

$$\text{IngestedTotal} = \text{CommittedTotal} + \text{FailedBeforeCommitTotal} + \text{RecoveredDLQTotal} + \text{SpilledDiskTotal} + \text{DroppedTotal}$$

| Term | Formal Semantic Definition |
| :--- | :--- |
| `IngestedTotal` | Total events received by the local ingestion engine from collectors across the admission boundary. |
| `CommittedTotal` | Total events successfully committed to local SQLite WAL storage in the `events` table. |
| `FailedBeforeCommitTotal` | Events rejected at ingestion boundary prior to database insertion (e.g. malformed schema, payload validation error). |
| `RecoveredDLQTotal` | Events moved to the local dead-letter queue (`dead_letter_queue` table) due to non-retryable errors (e.g. HTTP 400 Bad Request). |
| `SpilledDiskTotal` | Events routed to secondary disk spill buffers during temporary primary storage saturation. |
| `DroppedTotal` | Events deliberately dropped by the resource governor during active load shedding under critical memory/storage pressure. |

### Reconciliation Truthfulness Principle
>
> [!IMPORTANT]
> **Reconciliation proves local ingestion accounting, NOT end-to-end zero loss.**
> When `reconciliation_valid: true` is reported, it mathematically proves that 100% of events admitted across the ingestion boundary are accounted for in one of the terminal local states (`Committed`, `FailedBeforeCommit`, `DLQ`, `Spilled`, or `Dropped`).
> If `DroppedTotal > 0` (e.g. load shedding under critical governor state), reconciliation remains valid because the drops were explicitly accounted for, but delivery was **not** lossless. Similarly, if the central gateway accepts an event with HTTP 202 and subsequently crashes before writing to central storage, edge reconciliation is valid, but end-to-end delivery was interrupted centrally.

---

## 4. Test Verification Methodology: Injected Clocks vs. Native Wall Clocks

The RedCyberFox test harness deliberately distinguishes between two classes of durability and retry verification:

### 1. Injected-Clock Unit Tests

- **Purpose**: Rapid, deterministic verification of state machine transitions, retry schedule calculations, and exponential backoff formulas.
- **Mechanism**: Use mock interfaces or synthetic timestamps without blocking system threads.
- **Limitations**: Injected-clock tests do not prove that real OS sleep timers, context deadlines, SQLite disk persistence timestamps, or forwarder background goroutines behave correctly under wall-clock time.

### 2. Native Wall-Clock Workload Tests

- **Purpose**: Verifiable qualification evidence of real-world retry recovery, timer expiration, and process restarts.
- **Mechanism**: Execute against real wall-clock durations (e.g. the 22-second run in `TestWorkload_WindowsMeasurementReports`), allowing natural expiration of calculated retry intervals (10s–19s) without altering timestamps via SQL.
- **Integrity Rule**: Workload qualification tests MUST NOT alter or clear `next_attempt_at` columns via SQL queries to artificially accelerate test execution. Timestamps must be preserved exactly as computed across offline shutdowns and restarts.
