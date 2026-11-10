# Delivery Guarantees and Failure Boundary Specification

**Status**: Active / Phase 4 Architectural Baseline  
**Revision Date**: 2026-11-10  
**Scope**: RedCyberFox Edge Agent Durability, Forwarding, and Upstream Ingestion Boundary  

---

## 1. Executive Summary and System Boundary

The RedCyberFox Edge Agent provides **at-least-once durable telemetry delivery** from constrained industrial edge nodes to central ingest gateways. The durability and delivery model is explicitly partitioned across an architectural boundary:

```
[ Collectors / Sensors ]
           │
           ▼
[ Ingestion Engine ] ──(Atomic SQLite WAL Commit)──► [ Durable SQLite Storage ]
                                                              │
                                                              ▼
                                                     [ Forwarder Engine ]
                                                              │
═════════════════════════════ ARCHITECTURAL BOUNDARY ═════════════════════════════
                                                              │ (HTTP POST /v1/ingest)
                                                              ▼
                                                    [ Ingest API Gateway ]
                                                              │ (HTTP 202 Accepted)
                                                              ▼
                                                    [ Central Event Pipeline ]
```

### The Edge Durability Contract
1. **Local Durability Boundary**: An event is considered ingested by the edge agent *only after* it is durably committed to the local SQLite database (`events` table) in Write-Ahead Logging (WAL) mode.
2. **Forwarding Boundary**: An event remains durably stored on disk with `state = 'PENDING'` until an HTTP 202 Accepted response is received from the upstream ingest gateway.
3. **Local Removal**: Upon receiving HTTP 202 Accepted, the forwarder executes `RemoveEvent` within a database transaction.
4. **Boundary of Responsibility**: HTTP 202 Accepted represents **edge transport acceptance** by the ingress gateway. Durable persistence across central replicas, message queues, and long-term storage is strictly the responsibility of the central platform after HTTP 202. The edge agent does NOT claim end-to-end central durability once HTTP 202 is acknowledged.

---

## 2. Failure Boundaries and Mitigations

The edge agent forwarder and storage subsystems are hardened against seven primary delivery failure boundaries, verified by deterministic integration tests (`TestDeliveryBoundary_*` in `agent/internal/integration/delivery_boundaries_test.go`).

### Boundary 1: Receiver Unavailable (Connection Refused / Network Down)
- **Failure Condition**: The upstream gateway cannot be reached (DNS failure, link down, host offline, TCP connection refused).
- **Edge Behavior**:
  - `f.httpClient.Do()` returns a transport network error.
  - Forwarder atomically transitions `networkState` to `OFFLINE`.
  - The attempted event remains in `events` with `state = 'PENDING'`.
  - `attempt_count` increments monotonically.
  - `next_attempt_at` is scheduled with deterministic exponential backoff and jitter.
  - **Guarantee**: Zero event loss. The event backlog accumulates durably on disk up to the configured storage quota.

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
  - **Guarantee**: At-least-once delivery. Upstream must enforce deduplication; edge accounting truthfully records 2 attempts and 1 local removal.

### Boundary 5: Agent Restart with Pending SQLite Events (Process Crash / Power Loss)
- **Failure Condition**: The edge agent process or host system crashes (SIGKILL, power failure) while events are pending in SQLite.
- **Edge Behavior**:
  - SQLite WAL mode ensures atomic page commits and automatic recovery on reopening.
  - Upon process restart, `Init()` runs integrity checks and recovers uncheckpointed WAL frames.
  - All pending events retain their exact `event_id`, monotonically increasing `seq_no`, and `state = 'PENDING'`.
  - Forwarder immediately resumes forwarding from the first pending event.
  - **Guarantee**: Full durability across unannounced process terminations and power interruptions.

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

## 3. Truthful Accounting Terminology

To prevent misleading claims of "lossless delivery" or "end-to-end zero loss":

| Term | Formal Semantic Definition |
| :--- | :--- |
| `ingested_total` | Total events received by the local ingestion engine from collectors. |
| `committed_total` | Total events successfully committed to local SQLite WAL storage. |
| `failed_before_commit_total` | Events rejected at ingestion boundary (e.g. malformed schema, validation failure). |
| `reconciliation_valid` | `committed_total == ingested_total - failed_before_commit_total`. Strictly an **Ingestion-Outcome Reconciliation**, not an end-to-end zero-loss proof. |
| `http_accepted_total` | Total HTTP 202 Accepted responses received from upstream gateway (includes duplicates/retries). |
| `local_removed_total` | Total events removed from local SQLite after receiving HTTP 202. |
| `local_removal_errors_total` | Number of times local SQLite deletion failed after HTTP 202 was received. |
| `attempts_total` | Cumulative HTTP transport attempts initiated by the forwarder. |
| `retry_total` | Count of failed attempts rescheduled for future transmission. |
| `dlq_total` | Count of non-retryable events moved to local dead-letter queue (e.g. HTTP 400 Bad Request). |
