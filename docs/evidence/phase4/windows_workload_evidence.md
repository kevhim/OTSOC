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
> **Retry Deadline Preservation & Historical Correction**: An earlier iteration of the reconnect test manually cleared retry deadlines via SQL (`UPDATE events SET next_attempt_at = NULL`) to force immediate redelivery. That historical approach was invalidated and rerun. The corrected scenario (`Scenario_OfflineAndRealRetryRecovery`) **strictly preserves SQLite retry timestamps across shutdown and restart without SQL modification**. The reconnect phase executes for 22 seconds, allowing the exponential backoff window ($10\text{s} + 9\text{s}\text{ jitter} = 10\text{--}19\text{s}$) to naturally elapse so the forwarder's 5s retry ticker discovers and drains all 22 offline-buffered events natively.

### Host Hardware & Environment

| Parameter | Host Specification |
| :--- | :--- |
| **Operating System** | Microsoft Windows 11 Home Single Language (Version 10.0.26300, amd64) |
| **Processor** | Intel(R) Core(TM) Ultra 7 155H |
| **Topology** | 16 physical cores, 22 logical processors |
| **Physical Memory** | 32 GB RAM (32,961,912 KB visible) |
| **Go Toolchain** | go1.27.0 (windows/amd64) |
| **CGO / Toolchain** | MSYS2 UCRT64 GCC 14.2.0, libyara_x_capi 1.20.0 |
| **Source Revision** | `d6fd8bd091bcbda92d760803b9233830d3bc0fe4` |
| **Working Tree State**| Verified fresh build in isolated `t.TempDir()` with matching VCS buildinfo |

### Production Detection Rule Workload

All scenarios executed with the active production detection engine loaded with the deterministic rule set:
- **Rule ID**: `LOCAL-IOC-EXEC-001`
- **Version**: `1.0`
- **Severity**: `CRITICAL`
- **Linkage**: Explicit evidence event ID linkage (`evidence_event_ids: [source_event_id]`), deterministic UUIDv5 identity.
- **Match Outcome**: 0 rule matches (harmless background activity; matches zero files).

---

## Workload Scenarios & Empirical Evidence

Workload scenarios were executed using freshly compiled native `endpoint.exe` binaries in isolated directories with dedicated persistence files (`agent.db`, `identity.json`, `agent.json`) and opt-in machine-readable JSON reports:

```
docs/evidence/phase4/
├── windows_idle_report.json
├── windows_sustained_report.json
├── windows_burst_report.json
├── windows_offline_report.json
├── windows_reconnect_report.json
└── windows_retry_recovery_report.json
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

### 4. Offline & Real Retry Recovery Workload Scenario (`windows-smoke-offline` / `windows-smoke-reconnect`)

This scenario was executed in two consecutive phases using the **exact same SQLite database and identity**, verifying both offline buffering and subsequent real retry recovery:

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
  - Exponential Backoff Timestamp: Each failed event received `next_attempt_at` between 10s and 19s in the future.
  - Ingestion-Outcome Reconciliation Valid: **TRUE**
  - Final Shutdown Outcome: `MEASUREMENT_COMPLETE`

#### Phase 4B: Reconnection & Real Retry Recovery
- **Profile**: `windows-smoke-reconnect` (persisted as both `windows_reconnect_report.json` and `windows_retry_recovery_report.json`)
- **Duration**: 22,000 ms | **Sampling Interval**: 2,000 ms (11 samples)
- **Workload**: Upstream mock server online (`http://127.0.0.1:<port>`), same SQLite database reopened without modifying any row or column.
- **Retry Mechanism**: The forwarder runs a background retry loop every 5 seconds. Within the 22-second execution window, the 10s–19s exponential backoff window naturally elapsed without any SQL clock resetting or test intervention.
- **Event Identity & Ordering Verification**:
  - The test harness recorded the exact IDs and sequence numbers of all 22 offline-buffered events before starting Phase 4B.
  - After Phase 4B shutdown, the test verified that **every single recorded offline event ID and sequence number** arrived at the mock receiver.
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
| **Report File** | `windows_idle_report.json` | `windows_sustained_report.json` | `windows_burst_report.json` | `windows_offline_report.json` | `windows_retry_recovery_report.json` |
| **Duration** | 3.0 s | 4.0 s | 4.0 s | 3.0 s | 22.0 s |
| **Total Samples** | 3 | 4 | 4 | 3 | 11 |
| **Peak RSS Memory** | 21.92 MB | 23.42 MB | 25.78 MB | 23.26 MB | 22.89 MB |
| **Peak Storage Size** | 222 KB | 1,309 KB | 3,773 KB | 955 KB | 296 KB |
| **Peak Write IO** | 271 KB/s | 453 KB/s | 3,679 KB/s | 860 KB/s | 292 KB/s |
| **Mean Process CPU** | ~0.19% | ~0.28% | ~0.26% | ~0.21% | ~0.07% |
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
