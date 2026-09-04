# OS Contract Audit

This document defines the strict semantic boundaries and contracts between OS-specific process telemetry adapters (Linux `procfs`, Windows `Toolhelp32`, etc.) and the cross-platform `LifecycleEngine`.

## 1. Process Identity

A process is uniquely identified by the combination of its `PID` and `StartTime`.

- **PID**: The OS-assigned integer identifier.
- **StartTime**: The exact timestamp when the OS created the process.
- **PID Reuse**: The `LifecycleEngine` detects PID reuse exclusively by observing a change in `StartTime` for a given `PID`. OS adapters **must** provide accurate, consistent `StartTime` values. 

## 2. Mandatory vs Optional Metadata

- **Mandatory Fields**: `PID` and `StartTime`. If an OS adapter cannot determine these fields for a process, that process **MUST** be skipped (omitted from the `Snapshot`).
- **Optional Fields**: `Name`, `ParentPID`, `User`, `ExecutablePath`, `CommandLine`.
- **Metadata Asymmetry**: It is explicitly expected and permitted for different operating systems (or different privilege levels on the same OS) to yield different optional metadata. 
  - E.g., Linux may provide `CommandLine` via `/proc/pid/cmdline`, while Windows may yield `nil` due to ETW/PEB constraints.
  - OS adapters must never fabricate or panic on missing optional metadata. They must silently fall back to `nil`.

## 3. Snapshot and Collection Failure Degradation

The OS adapter acts as an observer that captures a point-in-time `Snapshot` of running processes.

### Error/Degradation Matrix

| Observation State | Adapter Action | Lifecycle Engine Result |
| :--- | :--- | :--- |
| **Success** | Return `Snapshot` containing all observed processes. | Diffs against prior state, emits `START`/`EXIT`. |
| **Empty Success** (no processes found) | Return `Snapshot` with 0 instances. | Assumes all previously known processes exited. Emits `EXIT` for all. |
| **Single Process Identity Failure** (cannot read PID/StartTime) | Skip process, exclude from `Snapshot`. | If previously known, engine treats it as exited (`EXIT`). If unknown, remains ignored. |
| **Single Process Metadata Failure** (Access Denied for Exe/User) | Include process in `Snapshot` with missing fields as `nil`. | Emits `START` (if new) with reduced metadata. Existing state preserved. |
| **Complete Snapshot Failure** (e.g. `/proc` unreadable, Toolhelp fails midway) | Return `error`. **DO NOT** return a partial list. | Engine skips reconciliation. **Zero** `EXIT` events are emitted. Prior state is frozen until recovery. |

## 4. Lifecycle Semantics

Regardless of the OS, the `LifecycleEngine` guarantees the following behavioral semantics based on the snapshots provided:

- **Baseline Initialization**: The first successful `Snapshot` initializes the baseline state. Processes in the baseline **do not** emit `PROCESS_START` events; they only populate the engine's known state.
- **Process Start**: A process present in the current `Snapshot` but absent from the known state (by `PID`+`StartTime`) emits a `PROCESS_START` event.
- **Process Exit**: A process present in the known state but absent from a *successful* `Snapshot` emits a `PROCESS_EXIT` event.
- **Deduplication**: If a process exists in both the known state and the current `Snapshot` with identical `PID`+`StartTime`, no lifecycle event is emitted.
