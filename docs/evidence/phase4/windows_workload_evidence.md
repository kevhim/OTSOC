# Phase 4 Bounded Windows Workload Measurement Evidence

## Executive Summary & Provenance

This document records the empirical measurement results collected from the compiled, native RedCyberFox Endpoint Agent binary (`endpoint.exe`) executing bounded observational workloads across four scenarios on a Windows 11 host.

> [!IMPORTANT]
> **Developer-Host Smoke Evidence Disclaimer**: The measurements in this report reflect execution on a high-spec developer workstation and are strictly intended as **developer-host smoke evidence**. They demonstrate correct pipeline operation, bounded queue behavior, durable SQLite storage management, and truthful accounting finalization. They **do NOT constitute representative low-spec hardware qualification**, and **governor resource enforcement thresholds remain uncalibrated and observational**. Phase 4 remains **OPEN**.

> [!NOTE]
> **Mock HTTP 202 Ingest Receiver Disclaimer**: In these endpoint measurement runs, the upstream ingest endpoint is provided by an in-process mock HTTP 202 receiver. This mock confirms HTTP payload reception and contract-compliant acknowledgement (`202 Accepted`). It **does NOT establish central durable ingestion** into backend databases (e.g., PostgreSQL or Valkey); backend durability and end-to-end multi-tier pipeline guarantees belong to central server testing.

> [!NOTE]
> **Production Detection Rule Scope**: All scenarios executed with the active production detection engine loaded with deterministic rule `LOCAL-IOC-EXEC-001`. Because the workload consisted of harmless temporary file writes and OS snapshots, **no detection rule conditions matched**, and **no derived detection findings were emitted**. This run demonstrates detection engine evaluation overhead during collection, but **does not claim detection-match execution cost or finding emission overhead**.

> [!WARNING]
> **Retry Deadline Preservation & Natural Recovery**: An early iteration of the reconnect test manually cleared retry deadlines via SQL (`UPDATE events SET next_attempt_at = NULL`) to force immediate redelivery. That historical approach was invalidated. The corrected scenario (`Scenario_OfflineAndRealRetryRecovery`) **strictly preserves SQLite retry timestamps across shutdown and restart without SQL modification**. The reconnect phase executes for 22 seconds, allowing the exponential backoff window ($10\text{s} + 9\text{s}\text{ jitter} = 10\text{--}19\text{s}$) to naturally elapse so the forwarder's 5s retry ticker discovers and drains all 22 offline-buffered events natively.

### Host Hardware & Environment

| Parameter | Host Specification |
| :--- | :--- |
| **Operating System** | Microsoft Windows 11 Home Single Language (Version 10.0.26300, amd64) |
| **Processor** | Intel(R) Core(TM) Ultra 7 155H |
| **Topology** | 16 physical cores, 22 logical processors |
| **Physical Memory** | 32 GB RAM (32,961,912 KB visible) |
| **Go Toolchain** | `go1.27.0` (windows/amd64) |
| **CGO / Toolchain** | MSYS2 UCRT64 GCC 14.2.0, libyara_x_capi 1.20.0 |
| **Active Run ID** | `run_20261110_054251_8240a569` |
| **Source Revision** | `8240a5692a335dd1c9d20e2a2755e95554a0b7e9` |
| **Working Tree State**| `dirty: false` (clean committed baseline) |
| **Binary SHA-256** | `d949af478cd55535bcaf69344d670edbcc31b32ab5070bf4ac87e67f37016257` |

### Production Detection Rule Workload

All scenarios executed with the active production detection engine loaded with the deterministic rule set:
- **Rule ID**: `LOCAL-IOC-EXEC-001`
- **Version**: `1.0`
- **Severity**: `CRITICAL`
- **Linkage**: Explicit evidence event ID linkage (`evidence_event_ids: [source_event_id]`), deterministic UUIDv5 identity.
- **Match Outcome**: 0 rule matches (harmless background activity; matches zero files).

---

## Subprocess Execution Bounding & Timeout Governance

In earlier test iterations, git queries, binary compilation, and buildinfo inspection were executed without explicit context deadlines, and documentation prematurely claimed all subprocess commands were bounded. In this verified release, **every single external command is strictly governed by an explicit timeout context** with captured diagnostic output and visible deadline-exceeded failure reporting:

| Command | Working Directory | Explicit Timeout | Actual Duration | Exit Code | Purpose |
| :--- | :--- | :---: | :---: | :---: | :--- |
| `git rev-parse HEAD` | Repository Root | 5.0 s | 45 ms | `0` | Expected commit discovery |
| `git status --porcelain` | Repository Root | 5.0 s | 53 ms | `0` | Expected clean checkout verification |
| `go build -v -o <temp>/endpoint.exe ...` | Repository Root | 90.0 s | 2,600 ms | `0` | Fresh binary compilation in `t.TempDir()` |
| `go version -m <temp>/endpoint.exe` | Repository Root | 10.0 s | 89 ms | `0` | Embedded VCS buildinfo & toolchain verification |
| `endpoint.exe (windows-smoke-idle)` | `Scenario_Idle` TempDir | 13.0 s | 3,273 ms | `0` | Idle baseline workload |
| `endpoint.exe (windows-smoke-sustained)` | `Scenario_Sustained` TempDir | 14.0 s | 4,149 ms | `0` | Sustained filesystem workload |
| `endpoint.exe (windows-smoke-burst)` | `Scenario_Burst` TempDir | 14.0 s | 4,138 ms | `0` | Burst concurrency workload |
| `endpoint.exe (windows-smoke-offline)` | `Scenario_Offline` TempDir | 13.0 s | 3,605 ms | `0` | Offline storage buffering workload |
| `endpoint.exe (windows-smoke-reconnect)`| `Scenario_Offline` TempDir | 37.0 s | 22,269 ms | `0` | Reconnection & real retry recovery workload |

All command invocations, arguments, timeouts, durations, and exit statuses are deterministically recorded in [`commands.log`](file:///D:/Desktop/OT-SOC/redcyberfox-ot-soc/docs/evidence/phase4/runs/run_20261110_054251_8240a569/commands.log).

---

## Historical Run Preservation & Directory Isolation

To prevent accidental file clobbering, historical and corrected runs are segregated into dedicated, immutable directories:

```
docs/evidence/phase4/
├── historical/
│   ├── run_d6fd8bd_initial/          # Historical initial run; manual SQL deadline clearing
│   │   ├── README.md
│   │   └── windows_*_report.json
│   └── run_5cbe40b_retry_recovery/   # Historical retry run; natural backoff, but dirty=true
│       ├── README.md
│       └── windows_*_report.json
├── runs/
│   └── run_20261110_054251_8240a569/ # Clean committed run (dirty=false, verified SHA256)
│       ├── provenance.json
│       ├── commands.log
│       └── windows_*_report.json
├── windows_idle_report.json          # Canonical latest reports (exclusive creation)
├── windows_sustained_report.json
├── windows_burst_report.json
├── windows_offline_report.json
├── windows_reconnect_report.json
└── windows_retry_recovery_report.json
```

---

## Truthful Degraded Measurement Accounting

In all measurement reports, samples report:
```json
"governor_state": "NORMAL",
"sampling_error": "storage budget is unconfigured or unresolved"
```
and `degraded_samples` equals `total_samples` (e.g. 12/12 in reconnect, 5/5 in sustained, 4/4 in burst, 3/3 in idle/offline).

### Sampling Count Variations at Timer Boundaries
Sampling counts can vary at timer boundaries depending on whether initial sample collection precedes or coincides with the interval ticker, and whether the final sample is recorded immediately prior to measurement shutdown (e.g., 5 samples across a 4.0s run with a 1.0s interval, or 12 samples across a 22.0s run with a 2.0s interval). All sample counts in this document reflect the exact number of discrete samples captured in the raw JSON reports.

### Distinguishing Available Signals from Unresolved Policy
1. **Available Raw Resource Signals**:
   - Process CPU Utilization (%): Sampled via Windows `GetProcessTimes` with prime step continuity.
   - Process Memory (RSS): Exact visible bytes via `K32GetProcessMemoryInfo`.
   - Durable Persistence Footprint: Exact regular-file byte sums (`agent.db` + `agent.db-wal` + `agent.db-shm`).
   - Write IO Throughput: Derived bytes/sec via Windows `GetProcessIoCounters`.
   - Event Queue Metrics: Depth, capacity, saturation %, and oldest event age.
2. **Unavailable Storage-Pressure Evaluation**:
   - The Resource Governor contract requires an authoritative host persistence budget policy (`StorageBudgetBytes`).
   - Because this policy remains pending hardware qualification and OT deployment guidelines, `StorageBudgetBytes` is **strictly configured as 0**.
   - As a result, the governor coordinator truthfully emits `ErrMissingStorageBudget` and flags `StorageBlocked = true`.
3. **No Fabricated Budgets**:
   - The test harness does **not invent an arbitrary budget**, suppress errors, fabricate values, or artificially enable enforcement merely to make samples appear green. The degraded sample count truthfully reflects the open architectural gate.

---

## Workload Scenarios & Empirical Evidence

### 1. Idle Workload Scenario (`windows-smoke-idle`)
- **Profile**: `windows-smoke-idle` | **Duration**: 3,000 ms | **Sampling Interval**: 1,000 ms (3 samples)
- **Workload**: No artificial filesystem activity. OS background collectors initialized.
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` (201 KB), `agent.db-shm` (32 KB); peak 237.20 KB.
- **Process Memory (RSS)**: Initial 12.11 MB, peak 70.70 MB during collector initialization.
- **CPU Utilization**: 0.00% to 1.25% (mean ~0.42%).
- **Write IO Throughput**: Peaked at 339.60 KB/s.
- **Queue Metrics**: Depth: 0, Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0, Time Spent Full: 0.0s.
- **Accounting Reconciliation**:
  - Ingested: 2 (1 inventory snapshot + 1 network snapshot)
  - Committed: 2 | Failed Before Commit: 0 | Recovered DLQ: 0 | Spilled Disk: 0 | Dropped: 0
  - Forwarder Attempts: 2 | HTTP Accepted: 2 | Retries: 0 | Local Removed: 2
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 2. Sustained Workload Scenario (`windows-smoke-sustained`)
- **Profile**: `windows-smoke-sustained` | **Duration**: 4,000 ms | **Sampling Interval**: 1,000 ms (5 samples: indices 0..4)
- **Workload**: Steady file creation stream (1 file written every 200 ms to monitored directory; 12 files).
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` expanded to 1,259 KB (1.26 MB), `agent.db-shm` (32 KB); peak 1,295.37 KB.
- **Process Memory (RSS)**: Initial 12.01 MB, peak 22.89 MB.
- **CPU Utilization**: 0.00% to 0.47% (mean ~0.19%).
- **Write IO Throughput**: Peaked at 803.04 KB/s.
- **Queue Metrics**: Depth: 0 (smooth real-time processing), Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0.
- **Accounting Reconciliation**:
  - Ingested: 26 (2 baseline + 24 filesystem events)
  - Committed: 26 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 26 | HTTP Accepted: 26 | Retries: 0 | Local Removed: 26
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 3. Burst Workload Scenario (`windows-smoke-burst`)
- **Profile**: `windows-smoke-burst` | **Duration**: 4,000 ms | **Sampling Interval**: 1,000 ms (4 samples)
- **Workload**: Sudden burst of 35 files created concurrently in monitored directory.
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` expanded to 3,677 KB (3.68 MB), `agent.db-shm` (32 KB); peak 3,713.45 KB.
- **Process Memory (RSS)**: Initial 11.98 MB, peak 24.72 MB.
- **CPU Utilization**: 0.00% to 0.50% (mean ~0.25%).
- **Write IO Throughput**: Peaked at 3,620.51 KB/s (3.62 MB/s).
- **Queue Metrics**: Depth: 0 after consumer drain, Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0.
- **Accounting Reconciliation**:
  - Ingested: 72 (2 baseline + 70 file create/modify events)
  - Committed: 72 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 72 | HTTP Accepted: 72 | Retries: 0 | Local Removed: 72
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 4. Offline & Real Retry Recovery Workload Scenario (`windows-smoke-offline` / `windows-smoke-reconnect`)

Executed in two consecutive phases using the **exact same SQLite database and identity**:

#### Phase 4A: Offline Ingestion & Storage Buffering
- **Profile**: `windows-smoke-offline` | **Duration**: 3,000 ms | **Sampling Interval**: 1,000 ms (3 samples)
- **Workload**: Upstream server offline (unreachable port `http://127.0.0.1:54321`), 10 files created in monitored directory.
- **Connectivity State**: Transitioned from `UNKNOWN` to `OFFLINE`.
- **Durable Storage**: `agent.db-wal` expanded to 945 KB; peak 981.54 KB.
- **Process Memory (RSS)**: Initial 12.00 MB, peak 22.81 MB.
- **CPU Utilization**: 0.00% to 0.74% (mean ~0.50%).
- **Write IO Throughput**: Peaked at 889.55 KB/s.
- **Accounting Reconciliation**:
  - Ingested: 22 (2 baseline + 20 filesystem events)
  - Committed: 22 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 22 | HTTP Accepted: 0 | Retries: 22 | Local Removed: 0
  - Durable Pending Events: **22** (all 22 events durably preserved in SQLite `state = 'PENDING'`)
  - Exponential Backoff Timestamp: Each failed event received `next_attempt_at` between 10s and 19s in the future.
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

#### Phase 4B: Reconnection & Real Retry Recovery
- **Profile**: `windows-smoke-reconnect` | **Duration**: 22,000 ms | **Sampling Interval**: 2,000 ms (12 samples: indices 0..11)
- **Workload**: Upstream mock receiver online (`http://127.0.0.1:<port>`), same SQLite database reopened without modifying any row or column.
- **Process Memory (RSS)**: Initial 12.02 MB, peak 22.61 MB.
- **CPU Utilization**: 0.00% to 0.22% (mean ~0.05%).
- **Write IO Throughput**: Peaked at 1,923.82 KB/s during WAL checkpoint / deletion transactions.
- **Durable Storage**: Shrank from 981.54 KB down to 811.90 KB.
- **Retry Mechanism**: The forwarder runs a background retry loop every 5 seconds. Within the 22-second execution window, the 10s–19s exponential backoff window naturally elapsed without any SQL clock resetting or test intervention.
- **Event Identity & Ordering Verification**:
  - The test harness recorded the exact IDs and sequence numbers of all 22 offline-buffered events before starting Phase 4B.
  - After Phase 4B shutdown, verified that **every single recorded offline event ID and sequence number** arrived at the mock receiver.
  - Asserted `count(state = 'PENDING') == 0` in SQLite post-shutdown.
- **Backlog Ingestion Accounting**:
  - Initial Backlog Pending Events: **22**
  - Newly Ingested: 2 (startup snapshot events) | Committed: 2
  - Forwarder Attempts: **24** (22 retry backlog + 2 new events)
  - HTTP Accepted: **24** (all 24 acknowledged with HTTP 202 by mock receiver)
  - Local Removed: **24** (all 24 transactionally deleted from SQLite)
  - Remaining Durable Pending Events: **0**
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

---

## Comparative Metrics Summary Table

| Metric | Idle Scenario | Sustained Scenario | Burst Scenario | Offline Scenario | Real Retry Scenario |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **Profile** | `windows-smoke-idle` | `windows-smoke-sustained` | `windows-smoke-burst` | `windows-smoke-offline` | `windows-smoke-reconnect` |
| **Duration** | 3.0 s | 4.0 s | 4.0 s | 3.0 s | 22.0 s |
| **Total Samples** | 3 | 5 | 4 | 3 | 12 |
| **Degraded Samples** | 3 (Storage Unresolved) | 5 (Storage Unresolved) | 4 (Storage Unresolved) | 3 (Storage Unresolved) | 12 (Storage Unresolved) |
| **Peak RSS Memory** | 70.70 MB | 22.89 MB | 24.72 MB | 22.81 MB | 22.61 MB |
| **Peak Storage Size** | 237 KB | 1,295 KB | 3,713 KB | 982 KB | 812 KB |
| **Peak Write IO** | 340 KB/s | 803 KB/s | 3,621 KB/s | 890 KB/s | 1,924 KB/s |
| **Mean Process CPU** | ~0.42% | ~0.19% | ~0.25% | ~0.50% | ~0.05% |
| **Ingested Events** | 2 | 26 | 72 | 22 | 2 |
| **Committed Events** | 2 | 26 | 72 | 22 | 2 |
| **HTTP Accepted** | 2 | 26 | 72 | 0 | 24 |
| **Initial Backlog** | 0 | 0 | 0 | 0 | 22 |
| **Final Pending DB** | 0 | 0 | 0 | 22 | 0 |
| **Reconciled Valid** | **TRUE** | **TRUE** | **TRUE** | **TRUE** | **TRUE** |
| **Shutdown Outcome** | `MEASUREMENT_COMPLETE` | `MEASUREMENT_COMPLETE` | `MEASUREMENT_COMPLETE` | `MEASUREMENT_COMPLETE` | `MEASUREMENT_COMPLETE` |

---

## Accounting & Contractual Semantics Verification

1. **Ingestion-Outcome Reconciliation**: Verified across all 4 scenarios (`reconciliation_valid: true`). Every event admitted to the local ingestion pipeline had a deterministic, observable outcome: `committed_total = ingested_total` with zero dropped or uncounted events.
2. **HTTP Accepted Total**: Defined as total HTTP 202 responses acknowledged by the server, including retries. In the offline scenario, `http_accepted_total` was strictly 0 and `retry_total` was 22, proving retries are counted without falsely claiming delivery. In the reconnect scenario, `http_accepted_total` accurately accounted for 24 responses.
3. **Queue Backpressure & Admission Boundedness**: In all scenarios, queue depth and saturation remained strictly bounded. In the burst scenario (72 events), the bounded queue and consumer drained events cleanly without memory growth or data loss. In addition, unit regressions verified that adapter-held events are preserved across queue `Close()` without silent drops.
4. **Storage Exponential Backoff**: When an upstream connection fails, storage marks events with exponential backoff (`next_attempt_at = now + 10s + jitter`). On reconnect, the backlog is processed once eligible and cleanly removed from SQLite.
5. **Truthful Outcome Finalization**: All reports finalized after durable SQLite row count capture and database close, correctly recording `MEASUREMENT_COMPLETE` on disk.

---

## Open Tasks & Next Steps (Phase 4 Remains Open)

1. **Linux CI Race & Workload Evidence**: Execute equivalent bounded workload scenarios under Ubuntu Linux CI environment with `-race` and YARA-X native tags.
2. **Representative Low-Spec Qualification**: Hardware qualification on constrained OT-representative nodes (e.g., 2 vCPU, 2-4 GB RAM) remains open. The developer-host numbers in this document must not be used to calibrate production governor thresholds.
3. **Governor Threshold Calibration**: Resource governor thresholds (`CPUSaverThreshold: 80%`, `MemorySaverBytes: 200MB`, etc.) remain observational and provisional until low-spec qualification evidence is collected.
4. **Authoritative Local Storage Policy**: Storage pressure evaluation remains intentionally blocked (`StorageBudgetBytes: 0`) pending an authoritative host disk budget policy. Phase 5 has not begun.
