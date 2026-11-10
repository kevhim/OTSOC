# Investigation Report: Degraded Resource Samples in Phase 4 Workload Runs

**Status**: Verified Technical Investigation  
**Reference Evidence**: `docs/evidence/phase4/runs/run_20261110_064202_a56298ce/`  
**Auditor**: Senior Systems Reliability Engineer, Security QA, and Independent Performance Auditor  

---

## 1. Executive Summary

In the latest verified source-stable Windows workload runs (`run_20261110_064202_a56298ce`), all 25 recorded resource samples across 5 workload profiles are classified as degraded:

| Workload Profile | Total Samples | Degraded Samples | Reported `sampling_error` |
| :--- | :--- | :--- | :--- |
| `windows_idle_report.json` | 3 | 3 (100%) | `"storage budget is unconfigured or unresolved"` |
| `windows_sustained_report.json` | 4 | 4 (100%) | `"storage budget is unconfigured or unresolved"` |
| `windows_burst_report.json` | 4 | 4 (100%) | `"storage budget is unconfigured or unresolved"` |
| `windows_offline_report.json` | 3 | 3 (100%) | `"storage budget is unconfigured or unresolved"` |
| `windows_reconnect_report.json` | 11 | 11 (100%) | `"storage budget is unconfigured or unresolved"` |
| **Total** | **25** | **25 (100%)** | — |

This investigation was commissioned to determine:
1. Exactly why every sample was marked degraded.
2. The integrity and validity of each individual resource counter (Valid vs Missing vs Estimated).
3. Whether this represents a measurement defect, and if so, how to address it without concealing or relabeling degraded measurements.

---

## 2. Root Cause Analysis

Tracing the execution path from sampler initialization to JSON report generation revealed the exact sequence:

### Step 1: Storage Budget Configuration
In `agent/cmd/endpoint/main.go:159-163`:
```go
// Authoritative storage budget is unresolved; StorageBudgetBytes is 0 to ensure
// storage pressure evaluation remains blocked rather than assuming arbitrary quotas.
coordinator, coordErr = governor.NewCoordinator(resourceProvider, resourceGovernor, resourceMetrics,
    governor.CoordinatorConfig{StorageBudgetBytes: 0}, time.Now)
```
The endpoint intentionally initializes the `governor.Coordinator` with `StorageBudgetBytes = 0` because no authoritative host disk allocation policy has been approved for production deployment.

### Step 2: Storage Pressure Calculation Semantics
In `agent/internal/governor/coordinator.go:80-87`:
```go
func CalculateStoragePressure(usedBytes ValidUint64, budgetBytes uint64) (ValidFloat64, error) {
    if budgetBytes == 0 {
        return ValidFloat64{Valid: false}, ErrMissingStorageBudget
    }
    ...
}
```
`ErrMissingStorageBudget` is defined as:
`errors.New("storage budget is unconfigured or unresolved")`.

The governor contract explicitly forbids:
- Inventing an arbitrary denominator (e.g., defaulting to 50 MiB or 100 MiB).
- Silently falling back to zero pressure.
- Assuming infinite disk headroom.

### Step 3: Governor Step Invocation
In `agent/internal/governor/coordinator.go:213-216`:
```go
storagePressure, spErr := CalculateStoragePressure(storageMetricUsed, c.config.StorageBudgetBytes)
if spErr != nil {
    result.StorageBlocked = true
    stepErr = errors.Join(stepErr, spErr)
}
```
Because `StorageBudgetBytes` is 0, `spErr` is `ErrMissingStorageBudget`. This error is joined into `stepErr` and returned from `Step()`.

### Step 4: Measurement Runner Sample Assembly
In `agent/internal/resourcereport/runner.go:111-115, 136`:
```go
var errStrPtr *string
if stepErr != nil {
    s := stepErr.Error()
    errStrPtr = &s
}
...
sample := Sample{
    ...
    SamplingError: errStrPtr,
}
```
`sample.SamplingError` is set to `"storage budget is unconfigured or unresolved"`.

### Step 5: Degraded Accounting in Reporter
In `agent/internal/resourcereport/reporter.go:51-53`:
```go
if sample.GovernorState != "NORMAL" || sample.SamplingError != nil {
    r.degradedSamples++
}
```
Because `sample.SamplingError` is non-nil, `r.degradedSamples` increments on every single sample.

---

## 3. Resource Counter Audit: Valid, Missing, or Estimated

Every counter in the raw JSON samples was independently inspected to verify whether the underlying OS measurement is valid, missing, or estimated:

| Metric Counter | JSON Field | Underlying OS Source / Syscall | Status | Audit Findings |
| :--- | :--- | :--- | :--- | :--- |
| **Agent CPU** | `cpu_percent` | `GetProcessTimes` (kernel32.dll) | **VALID** | Measures exact process user and kernel time in 100ns units. Normalized across elapsed wall clock and core count. Initial sample returns 0% (primed baseline); subsequent samples record true delta. |
| **Agent RSS** | `rss_bytes` | `GetProcessMemoryInfo` (psapi.dll) | **VALID** | Reads `WorkingSetSize` directly from Win32 process memory counters. No estimation. |
| **Persistence Storage** | `persistence_bytes` | `os.Stat` on `agent.db`, `agent.db-wal`, `agent.db-shm` | **VALID** | Direct filesystem metadata via regular file size summation. |
| **Contributing Files** | `contributing_files` | `os.Stat` per file | **VALID** | Records exact byte sizes and existence of `agent.db` (4096), `agent.db-wal` (varying), `agent.db-shm` (32768), and spill log. |
| **Write I/O Rate** | `write_io_bytes_per_sec` | `GetProcessIoCounters` (kernel32.dll) | **VALID** | Measures process `WriteTransferCount` rate in bytes/sec over elapsed sampling interval. Direct kernel accounting. |
| **Queue Depth** | `queue_depth` | `eventQ.Depth()` | **VALID** | Direct atomic read of in-memory ring buffer occupancy. |
| **Queue Capacity** | `queue_capacity` | `eventQ.Capacity()` | **VALID** | Configured buffer size (200). |
| **Queue Saturation** | `queue_saturation_percent` | `Depth / Capacity` | **VALID** | Exact quotient. |
| **Queue Oldest Age** | `queue_oldest_event_age_seconds` | `time.Since(oldest.Timestamp)` | **VALID** | Accurate elapsed time of head item; null when depth is 0. |
| **Enqueue Saturation** | `blocked_enqueue_attempts_total` | `QueueStatsProvider` | **VALID** | Monotonic count of blocked producer admissions. |
| **Governor State** | `governor_state` | `Governor.CurrentState()` | **VALID** | Evaluated as `NORMAL` for CPU, RSS, and Queue. |
| **Connectivity** | `connectivity` | `Forwarder.NetworkState()` | **VALID** | Synchronized state machine (`UNKNOWN`, `ONLINE`, `OFFLINE`). |
| **Storage Pressure** | *(not present in sample)* | `CalculateStoragePressure` | **MISSING (BLOCKED)** | Cannot be computed without an authoritative budget. Blocked by design. |
| **Dropped / Aggregated** | `events_dropped_total`, `events_aggregated_total` | N/A | **NULL / INACTIVE** | Priority shedding is a Phase 5 feature; explicitly null. |
| **Retention Pruned** | `pruned_events_total` | N/A | **NULL / INACTIVE** | Retention pruning is a Phase 5 feature; explicitly null. |
| **Estimated Metrics** | N/A | N/A | **NONE** | No metric is estimated or synthesized. |

---

## 4. Assessment and Resolution

### Is this a Measurement Defect?
**No.** The measurement subsystem and OS providers (`provider_windows.go`, `sqlite_provider.go`) are operating with complete fidelity. All hardware and OS counters (CPU, WorkingSetSize, WriteTransferCount, SQLite file sizes) are valid and directly measured from the Windows kernel.

The degraded status is an **intentional architectural safeguard**:
1. Without an authoritative persistence budget approved for the host OT environment, the agent cannot determine what percentage of its storage budget has been consumed.
2. The governor refuses to guess, default, or fabricate a denominator.
3. Therefore, storage pressure evaluation is blocked (`StorageBlocked = true`), returning `ErrMissingStorageBudget`.
4. The measurement reporter truthfully logs this condition as a degraded sampling state.

### Compliance with Non-Concealment Directive
The directive states:
> *"Do not conceal or relabel degraded measurements to produce passing results."*

In accordance with this directive:
1. **No Artificial Storage Budget Added**: `StorageBudgetBytes` remains `0` in production configuration until formally calibrated against qualified OT hardware constraints.
2. **No Error Suppression**: `runner.go` and `reporter.go` continue to record `SamplingError` and increment `DegradedSamples` whenever `stepErr != nil`.
3. **Truthful Reporting**: Reports will continue to report `degraded_samples = total_samples` until an authoritative budget policy is established.
4. **Phase 4 Impact**: This constitutes one of the explicit qualification gaps that prevents Phase 4 from closing prematurely.
