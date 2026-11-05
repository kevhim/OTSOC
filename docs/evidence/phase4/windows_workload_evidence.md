# Phase 4 Bounded Windows Workload Measurement Evidence

## Executive Summary & Provenance

This document records the empirical measurement results collected from the compiled, native RedCyberFox Endpoint Agent binary (`endpoint.exe`) executing bounded observational workloads across four scenarios on a Windows 11 host.

> [!IMPORTANT]
> **Developer-Host Smoke Evidence Disclaimer**: The measurements in this report reflect execution on a high-spec developer workstation and are strictly intended as **developer-host smoke evidence**. They demonstrate correct pipeline operation, bounded queue behavior, durable SQLite storage management, and truthful accounting finalization. They **do NOT constitute representative low-spec hardware qualification**, and **governor resource enforcement thresholds remain uncalibrated and observational**. Phase 4 remains **OPEN**.

### Host Hardware & Environment

| Parameter | Host Specification |
| :--- | :--- |
| **Operating System** | Microsoft Windows 11 Home Single Language (Version 10.0.26300, amd64) |
| **Processor** | Intel(R) Core(TM) Ultra 7 155H |
| **Topology** | 16 physical cores, 22 logical processors |
| **Physical Memory** | 32 GB RAM (32,961,912 KB visible) |
| **Go Toolchain** | go1.27.0 (windows/amd64) |
| **CGO / Toolchain** | MSYS2 UCRT64 GCC 14.2.0, libyara_x_capi 1.20.0 |
| **Source Revision** | `459dce1bfe48be0d92886e495bc3321c40cf65a1` (commit `459dce1`) |
| **Working Tree State**| Clean (rebuilt with `vcs.modified=false`) |

### Production Detection Rule Workload

All scenarios executed with the active production detection engine loaded with the deterministic rule set:
- **Rule ID**: `LOCAL-IOC-EXEC-001`
- **Version**: `1.0`
- **Severity**: `CRITICAL`
- **Linkage**: Explicit evidence event ID linkage (`evidence_event_ids: [source_event_id]`), deterministic UUIDv5 identity.

---

## Workload Scenarios & Empirical Evidence

Four distinct scenarios were executed using the compiled native `endpoint.exe` binary in isolated directories with dedicated persistence files (`agent.db`, `identity.json`, `agent.json`) and opt-in machine-readable JSON reports:

```
docs/evidence/phase4/
├── windows_idle_report.json
├── windows_sustained_report.json
├── windows_burst_report.json
├── windows_offline_report.json
└── windows_reconnect_report.json
```

### 1. Idle Workload Scenario (`windows-smoke-idle`)
- **Profile**: `windows-smoke-idle`
- **Duration**: 3,000 ms | **Sampling Interval**: 1,000 ms (3 samples)
- **Workload**: No artificial filesystem activity. OS background collectors (`inventory`, `network`, `process`, `usb`, `filesystem`) initialized.
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` (185 KB), `agent.db-shm` (32 KB).
- **Process Memory (RSS)**: Initial 12.11 MB, steady-state 21.91 MB.
- **CPU Utilization**: 0.00% to 0.50% (mean ~0.19%).
- **Queue Metrics**: Depth: 0, Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0, Time Spent Full: 0.0s.
- **Accounting Reconciliation**:
  - Ingested: 2 (1 inventory snapshot + 1 network snapshot)
  - Committed: 2 | Failed Before Commit: 0 | Recovered DLQ: 0 | Spilled Disk: 0 | Dropped: 0
  - Forwarder Attempts: 2 | HTTP Accepted: 2 | Retries: 0 | Local Removed: 2
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 2. Sustained Workload Scenario (`windows-smoke-sustained`)
- **Profile**: `windows-smoke-sustained`
- **Duration**: 4,000 ms | **Sampling Interval**: 1,000 ms (4 samples)
- **Workload**: Steady file creation stream (1 file written every 200 ms to monitored directory).
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` expanded to 1,273 KB (1.27 MB), `agent.db-shm` (32 KB).
- **Process Memory (RSS)**: Initial 12.01 MB, peak 23.42 MB.
- **CPU Utilization**: 0.07% to 0.64% (excluding baseline prime sample).
- **Write IO Throughput**: 298.1 KB/s to 453.4 KB/s.
- **Queue Metrics**: Depth: 0 (smooth real-time processing), Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0.
- **Accounting Reconciliation**:
  - Ingested: 26 (2 baseline + 24 filesystem events)
  - Committed: 26 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 26 | HTTP Accepted: 26 | Retries: 0 | Local Removed: 26
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 3. Burst Workload Scenario (`windows-smoke-burst`)
- **Profile**: `windows-smoke-burst`
- **Duration**: 4,000 ms | **Sampling Interval**: 1,000 ms (4 samples)
- **Workload**: Sudden burst of 35 files created concurrently in monitored directory.
- **Durable Storage**: `agent.db` (4 KB), `agent.db-wal` expanded to 3,736 KB (3.73 MB), `agent.db-shm` (32 KB).
- **Process Memory (RSS)**: Initial 11.98 MB, peak 25.78 MB.
- **CPU Utilization**: 0.00% to 0.78%.
- **Write IO Throughput**: Peaked at 3,679.3 KB/s (3.68 MB/s).
- **Queue Metrics**: Depth: 0 after consumer drain, Saturation: 0.0%, Blocked Attempts: 0, Pressure Episodes: 0.
- **Accounting Reconciliation**:
  - Ingested: 72 (2 baseline + 70 file create/modify events)
  - Committed: 72 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 72 | HTTP Accepted: 72 | Retries: 0 | Local Removed: 72
  - Retained Pending Events: 0
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

### 4. Offline & Reconnect Workload Scenario (`windows-smoke-offline` / `windows-smoke-reconnect`)

This scenario was executed in two consecutive phases using the **exact same SQLite database and identity**, verifying both offline buffering and subsequent reconnect draining:

#### Phase 4A: Offline Ingestion & Storage Buffering
- **Profile**: `windows-smoke-offline`
- **Duration**: 3,000 ms | **Sampling Interval**: 1,000 ms (3 samples)
- **Workload**: Upstream server offline (unreachable port `http://127.0.0.1:54321`), 10 files created in monitored directory.
- **Connectivity State**: Transitioned from `UNKNOWN` to `OFFLINE`.
- **Durable Storage**: `agent.db-wal` expanded to 918 KB (0.92 MB).
- **Accounting Reconciliation**:
  - Ingested: 22 (2 baseline + 20 filesystem events)
  - Committed: 22 | Failed Before Commit: 0 | Dropped: 0
  - Forwarder Attempts: 22 | HTTP Accepted: 0 | Retries: 22 | Local Removed: 0
  - Durable Pending Events: **22** (all 22 events durably preserved in SQLite `state = 'PENDING'`)
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

#### Phase 4B: Reconnection & Backlog Drain
- **Profile**: `windows-smoke-reconnect`
- **Duration**: 3,000 ms | **Sampling Interval**: 1,000 ms (3 samples)
- **Workload**: Upstream server online (`http://127.0.0.1:<port>`), same database reopened.
- **Connectivity State**: Transitioned from `UNKNOWN` to `ONLINE`.
- **Backlog Ingestion Accounting**:
  - Initial Backlog Pending Events: **22**
  - Newly Ingested: 2 (startup snapshot events) | Committed: 2
  - Forwarder Attempts: **24** (22 backlog + 2 new events)
  - HTTP Accepted: **24** (all 24 acknowledged by upstream server)
  - Local Removed: **24** (all 24 transactionally deleted from SQLite)
  - Remaining Durable Pending Events: **0**
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

---

## Comparative Metrics Summary Table

| Metric | Idle Scenario | Sustained Scenario | Burst Scenario | Offline Scenario | Reconnect Scenario |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **Duration** | 3.0 s | 4.0 s | 4.0 s | 3.0 s | 3.0 s |
| **Total Samples** | 3 | 4 | 4 | 3 | 3 |
| **Peak RSS Memory** | 21.92 MB | 23.42 MB | 25.78 MB | 23.26 MB | 23.44 MB |
| **Peak Storage Size** | 222 KB | 1,309 KB | 3,773 KB | 955 KB | 839 KB |
| **Peak Write IO** | 271 KB/s | 453 KB/s | 3,679 KB/s | 860 KB/s | 696 KB/s |
| **Mean Process CPU** | ~0.19% | ~0.28% | ~0.26% | ~0.21% | ~0.38% |
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
3. **Queue Backpressure & Admission Boundedness**: In all scenarios, queue depth and saturation remained strictly bounded. In the burst scenario (72 events), the bounded queue and consumer drained events cleanly without memory growth or data loss.
4. **Storage Exponential Backoff**: When an upstream connection fails, storage marks events with exponential backoff (`next_attempt_at = now + 10s + jitter`). On reconnect, the backlog is processed once eligible and cleanly removed from SQLite.
5. **Truthful Outcome Finalization**: All reports finalized after durable SQLite row count capture and database close, correctly recording `MEASUREMENT_COMPLETE` on disk.

---

## Open Tasks & Next Steps (Phase 4 Remains Open)

1. **Linux CI Race & Workload Evidence**: Execute equivalent bounded workload scenarios under Ubuntu Linux CI environment with `-race` and YARA-X native tags.
2. **Representative Low-Spec Qualification**: Hardware qualification on constrained OT-representative nodes (e.g., 2 vCPU, 2-4 GB RAM) remains open. The developer-host numbers in this document must not be used to calibrate production governor thresholds.
3. **Governor Threshold Calibration**: Resource governor thresholds (`CPUSaverThreshold: 80%`, `MemorySaverBytes: 200MB`, etc.) remain observational and provisional until low-spec qualification evidence is collected.
4. **Authoritative Local Storage Policy**: Storage pressure evaluation remains intentionally blocked (`StorageBudgetBytes: 0`) pending an authoritative host disk budget policy. Phase 5 has not begun.
