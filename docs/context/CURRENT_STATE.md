# Current State

- **Current Git Branch**: `fix/correctness-remediation-checkpoint`
- **Current Commit Hash**: `HEAD`

## Phase Status
- **Phase 0 (Bootstrap)**: COMPLETE
- **Phase 1 (Vertical Slice)**: COMPLETE
- **Phase 2A (Agent Foundation)**: COMPLETE
- **Phase 2B (Agent Storage & Integrity)**: COMPLETE / FROZEN
- **Phase 2C.0 (Process Telemetry Contract)**: COMPLETE
- **Phase 2C.1 (Process Lifecycle Engine)**: COMPLETE
- **Phase 2C.2 (Linux Process Collector)**: COMPLETE
- **Phase 2C.3 (Windows Collector)**: COMPLETE (Windows snapshot/reconciliation)
- **Phase 2C.4 (Cross-Platform Validation)**: COMPLETE / CLOSED
- **Phase 2C.5 (Endpoint Runtime Wiring & Integration)**: COMPLETE
- **Phase 2C (Overall)**: COMPLETE
- **Phase 2D (Filesystem Telemetry)**: COMPLETE
- **Phase 2E.1 (Inventory Collector)**: COMPLETE
- **Phase 2E.2 (Network Collector)**: COMPLETE
- **Phase 2E.3.1 (Windows USB)**: COMPLETE (Physical Win32 events captured via real-host testing)
- **Phase 2E.3.2 (Linux USB)**: COMPLETE (Real-Host Validated)
- **Phase 2E.4 (Cross-Source Regression & Data Integrity)**: COMPLETE / FROZEN
- **Phase 2 (Overall)**: COMPLETE / FROZEN

## Phase 3 Status
- **Phase 3 (Detection Core)**: COMPLETE
  - **Gate 1 (Boundary Setup)**: CLOSED
  - **Gate 2 (Foundation Hardening)**: CLOSED
  - **Gate 3 (Detection Engine Robustness)**: CLOSED

## Out-of-Sequence Preparatory Work
- **Phase 3.1A / Phase 3.1B (Passive Capture & Modbus/TCP Foundation)**: OUT-OF-SEQUENCE PREPARATORY WORK / FROZEN. Recorded as "out-of-sequence preparatory OT sensing work already present in the repository". It is preserved without extension. It does not alter the official phase sequence automatically.

## Official Next Phase
- **Phase 4 (Rural Hardening)**: The official next milestone per `docs/implementation/roadmap.md` and master prompts.
- *Note*: No Phase 2F, 2G, or 2H exist. Resource governor belongs to Phase 4 (Rural Hardening).

---

## Phase 2E.4 Final Closure Record

### 1. Error Cause Preservation
- Storage error wrapping now preserves both `ErrStoreFailedBeforeCommit` and underlying context cancellations:
  `fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)`.
- Asserted deterministically in unit tests:
  - `errors.Is(err, ErrStoreFailedBeforeCommit) == true`
  - `errors.Is(err, context.Canceled) == true`

### 2. Event ID Ownership & Invariants
- All 6 active collectors (Process, Filesystem, Inventory, Network, USB Windows, USB Linux) strictly generate `event_id` (UUID v4) prior to queuing into `centralEvents`.
- SQLite storage strictly rejects empty `event_id` (`ErrStoreFailedBeforeCommit`) and never fabricates replacement identities.
- Idempotent upsert preserves `event_id` through bounded retries. Obsolete/dead event ID generation code removed.

### 3. Shared Production Ingestion Engine
- Extracted `agent/internal/ingestion/engine.go` providing one authoritative ingestion loop used identically by:
  - `agent/cmd/endpoint/main.go` (production runtime)
  - `agent/internal/integration/ingestion_test.go` (focused integration suite)
  - `agent/internal/integration/cross_source_test.go` (broad cross-source regression suite)
- Completely eliminated production logic duplication across test suites.

### 4. Global Shutdown Semantics & Drain Boundary
- Shutdown is governed by a single global 10-second drain deadline:
  - Collectors stop immediately upon lifecycle cancellation (`ctx.Done()`).
  - Remaining `centralEvents` (bounded at 200) are drained under a context derived from the global deadline.
  - The deadline never resets per event.
  - In-flight cancellations with `FAILED_BEFORE_COMMIT` and `context.Canceled` are retried under the global drain context with the SAME `event_id`.
  - Uncommitted events remaining after global deadline expiration transition to observable terminal handling (`FAILED_BEFORE_COMMIT`); no events are silently discarded.

### 5. DLQ & Recovery Semantics
- UNCERTAIN outcomes trigger bounded retry (3 attempts) preserving the original `event_id`.
- DLQ records serve strictly as recovery metadata keyed by `event_id` (not a new telemetry event).
- DLQ context derives from the active store context (no `context.Background()` escapes).
- Emergency spill to disk is best-effort fallback before terminal exit.

### 6. Broad Cross-Source Regression Gate
- Comprehensive regression suite in `agent/internal/integration/cross_source_test.go` covering all 6 sources:
  1. Process Collector
  2. Filesystem Collector
  3. Inventory Collector
  4. Network Collector
  5. Windows USB (live or simulation harness)
  6. Linux USB (appropriate platform path)
- Validated:
  - Concurrent collection across all sources under active load.
  - Channel pressure against exact production capacity (200).
  - Durable SQLite state verification via direct row count and query inspection.
  - Storage contention across 10 concurrent goroutines storing 300 events with 0 errors/deadlocks.
  - Shutdown under active collection and blocked storage conditions.

### 7. Acceptance Criteria Verification
- **AC-1 (No Silent Drops)**: PASS. Arbitrary 2s timeout eliminated; natural backpressure and global bounded drain enforced.
- **AC-2 (Strict Event ID Ownership)**: PASS. All collectors assign `event_id`; SQLite validates non-empty.
- **AC-3 (Event ID Preservation)**: PASS. Same `event_id` preserved across retries and DLQ recovery.
- **AC-4 (Single Global Shutdown Deadline)**: PASS. 10s drain boundary without per-event reset.
- **AC-5 (Explicit Terminal Handling)**: PASS. Events after deadline expire with explicit terminal failure.
- **AC-6 (No Fabricated DLQ Identity)**: PASS. DLQ recovery strictly preserves original `event_id`.

## Known Platform Limitations
- **Race Detector Toolchain**: `go test -race ./...` on local Windows MinGW fails due to toolchain limitation (`cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`). Linux CI race validation is configured in the CI workflow and has been verified.

## Phase 3 Gate 4 Status
- Gate 4: CLOSED
- Linux race: VERIFIED
- Real YARA-X Linux CI: VERIFIED

## Official Next Phase
Phase 4: ACTIVE
Phase 4.1 / Gate 1: CLOSED
Phase 4.2: CLOSED
Phase 4.3 — Real Resource Signal Collection: IN PROGRESS (Acceptance Blockers Identified)
- **Phase 4.3.1 (OS Provider Contract & Mock Verification)**: CLOSED
- **Phase 4.3.2 (Windows OS Resource Provider)**: CLOSED (cumulative source review approved; CPU continuity, overflow guards, storage configuration integrity, and checked storage accumulation verified under Windows UCRT64/CGO)
- **Phase 4.3.3 (Linux OS Resource Provider)**: CLOSED (cumulative source review approved; CPU continuity, overflow guards, RSS estimate/overflow guards, regular-file storage checks, storage configuration integrity/defensive copy, real Linux-source smoke test, sleep-free deterministic clock advancement, and checked storage accumulation verified under Ubuntu Go 1.26)
- **Phase 4.3.4 (Resource Signal Wiring & Integration)**: PARTIAL / ACCEPTANCE BLOCKERS IDENTIFIED:
  - **OS Resource Providers**: COMPLETED.
  - **On-Demand Coordinator Integration**: COMPLETED STARTUP BASELINE (constructed in main.go; invokes initial baseline Step synchronously at startup to prime CPU continuity and sample initial metrics; ongoing runtime invocation remains uninvoked pending Phase 4 batch/lifecycle policy).
  - **Continuous Endpoint Sampling**: NOT ENABLED (unapproved background 5s ticker removed from endpoint runtime).
  - **Storage-Pressure Policy**: UNRESOLVED (missing authoritative persistence budget policy; StorageBudgetBytes remains 0, leaving storage pressure blocked).
  - **Governor Race Validation**: PENDING CI EXECUTION (dedicated governor race step added to .github/workflows/ci.yml; awaiting Linux CI run).

Phase 4.3 remains open until storage budget policy is established and CI race test evidence is collected. See docs/implementation/phase_4_3_4_resource_signal_wiring.md.

The next Phase 4 work is to benchmark representative low-spec endpoints, calibrate provisional thresholds, and define safe enforcement and priority behavior against the Phase 4 gate-0 acceptance criteria.

## 2026-11-01 Targeted Source Audit (non-phase changing)

A maintenance review of the exported ZIP corrected emergency-spill append durability,
server ingress bounding/validation, event VARCHAR length validation,
read-handler row scanning, consumer restart recovery/cancellation,
endpoint configuration validation, and the dashboard's same-origin polling flow.
The full Go 1.26 test matrix, PostgreSQL/Valkey integration suite, and npm build
remain **unverified** in this review environment; they must be run on a properly
provisioned host/CI before a production-readiness claim. See
`docs/implementation/2026-11-01-project-audit.md`. Existing Phase 4
acceptance blockers are unchanged.


## 2026-11-01 Phase 4 Correctness-Remediation Checkpoint (non-phase changing)

A focused correctness remediation checkpoint was executed on branch `fix/correctness-remediation-checkpoint`.
Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

### 1. Authoritative Endpoint Identity
- **Defect/Risk**: Endpoint startup previously generated an ephemeral identity from file prior to storage initialization; SQLite subsequently migrated or retained its own authoritative identity and unlinked the file, allowing subsequent restarts or conflicting files to drift runtime and health identities.
- **Remediation**:
  - SQLite storage (`agent_state`) is established as the sole authoritative source of truth for runtime consumers (`db.GetDeviceID()`).
  - Added accessor `healthMgr.DeviceID()` in `agent/internal/health/health.go` ensuring health reporting stays synchronized with SQLite.
  - Ingestion engine explicitly binds `AssetID` matching `db.GetDeviceID()`.
  - Added comprehensive multi-boot regression test in `agent/internal/ingestion/engine_test.go` (`TestIngestion_EndpointIdentityLifecycleAcrossRestarts`) proving:
    1. First boot: legacy identity migrated into SQLite and unlinked.
    2. Second boot: missing legacy file successfully opens existing SQLite database and preserves identity and sequence continuity (`SeqNo = 2`).
    3. Third boot: conflicting legacy `identity.json` file is ignored, SQLite retains authoritative identity, rogue file is unlinked, and sequence continuity (`SeqNo = 3`) is preserved without altering historical events.
    4. Fourth boot: corrupt/malformed legacy file is unlinked, SQLite opens normally, sequence continuity (`SeqNo = 4`) and health identity remain consistent.

### 2. Deterministic Retry Eligibility
- **Defect/Risk**: `GetPendingEvents` evaluated retry eligibility using driver-dependent time representation or host clock (`CURRENT_TIMESTAMP`), causing potential flakiness around date boundaries, timezone offsets, and high attempt backoffs. Tests previously relied on fake `UPDATE events SET next_attempt_at = NULL` manipulation.
- **Remediation**:
  - Added thread-safe deterministic clock seam `s.getClock()` and `s.SetClock(clock func() time.Time)` in `SQLiteStorage`.
  - Standardized all retry timestamp persistence and queries to UTC formatted string (`2006-01-02 15:04:05`) evaluated via parameterized SQL: `AND (next_attempt_at IS NULL OR datetime(next_attempt_at) <= datetime(?))`.
  - `MarkFailed` computes bounded exponential backoff clamped at 1 hour using the deterministic UTC clock.
  - Added `TestStorage_RetryEligibility_DeterministicClock` in `sqlite_test.go` validating ineligibility before deadline, eligibility at/after deadline, non-UTC timezone offsets (`+05:30 IST`), UTC midnight date-boundary rollover (`23:59:30 UTC -> 00:00:15 UTC`), and database reopen schedule preservation.
  - Updated `offline_test.go` integration tests to advance the deterministic clock rather than clearing database columns.

### 3. Idempotency Before Quota Side Effects
- **Defect/Risk**: Event duplicate check previously risked triggering quota pruning or failing with false quota exhaustion before checking whether the incoming event was already committed or dead-lettered.
- **Remediation**:
  - `SQLiteStorage.Store` checks both `events` and `dead_letter_queue` tables for duplicate `event_id` and matching serialized payload BEFORE calling `enforceQuota`.
  - Added `TestStorage_IdempotencyDLQ_IntegrityAndPressureContract` proving identical retries under severe quota pressure succeed without pruning or seq_no changes, while payload conflicts return explicit integrity errors without corrupting evidence.

### 4. Dashboard Read Contract
- **Defect/Risk**: Dashboard lacked configurable API base, had fixed endpoints, and triggered cascading render warnings in OxLint due to synchronous state resets inside `useEffect`.
- **Remediation**:
  - Provided configurable API base supporting `VITE_API_BASE_URL` environment variable and runtime UI input bar.
  - Properly encoded `tenant_id` query parameter (`encodeURIComponent`) on `/v1/events` and `/v1/alerts`.
  - Added visible error banner displaying HTTP error status and body.
  - Eliminated synchronous `setState` in `useEffect` by moving reset logic into input change handlers (`handleTenantChange`, `handleApiBaseChange`), resolving OxLint warnings.
  - Verified dashboard: 0 lint errors, 0 warnings, build succeeded.

### 5. Unified Phase 4 Verification Harness
- Created `scripts/verify-phase4.ps1` supporting `-Race` and `-NativeDetection` flags.
- Reports environment-blocked dependencies (local Windows MinGW missing 64-bit CGO/gcc compiler, missing local `pkg-config`/`yara_x_capi`) separately from runnable tests without substituting stubs or weakening assertions.
- Verification results: All runnable checks PASSED (100%).

### 6. Remaining Acceptance Gaps (Phase 4 Remains OPEN)
- Authoritative storage budget policy (`StorageBudgetBytes`) remains unresolved.
- Calibration of resource thresholds from real low-spec endpoint hardware benchmarks.
- Explicit real detection workload and queue-age/recovery measurements.
### 6. Remaining Acceptance Gaps (Phase 4 Remains OPEN)
- Authoritative storage budget policy (`StorageBudgetBytes`) remains unresolved.
- Calibration of resource thresholds from real low-spec endpoint hardware benchmarks.
- Explicit real detection workload and queue-age/recovery measurements.
- Safe resource adaptation and priority queue enforcement.

## 2026-11-01 Phase 4 Bounded Measurement-Readiness Checkpoint (non-phase changing)

A focused measurement-readiness checkpoint was completed on branch `fix/correctness-remediation-checkpoint`.
Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

### 1. Repository Provenance & Baseline Relationship
- **Repository Path**: `D:\Desktop\OT-SOC\redcyberfox-ot-soc`
- **Active Branch**: `fix/correctness-remediation-checkpoint`
- **Remotes**: `origin: git@github.com:kevhim/OTSOC.git`, `otsoc: git@github.com:kevhim/OTSOC.git`
- **Ancestry**: The previous Phase 4 reference commit `57e8b4b` on `feature/phase-4-3-4-resource-wiring` is an ancestor of `main` (merged via PR #2 at `2eb305d`). `fix/correctness-remediation-checkpoint` branched from `main` at `cd08520` (incorporating PR #2, #3, #4, and #5) and contains commits `bb02344` (correctness fixes) and `1eaa923` (UCRT64 CGO toolchain discovery for race and native detection).

### 2. Accounting of Measurement Capabilities
- **`agent/cmd/resourcebench`**: MISSING. Zero occurrences across all git branches/commits; never existed in repository.
- **`agent/internal/resourcereport`**: MISSING. Zero occurrences across all git branches/commits; never existed in repository.
- **Endpoint measurement options (`-measure-duration`, `-measure-interval`)**: SUPERSEDED / IMPLEMENTED. Implemented in `agent/cmd/endpoint/main.go` as a dedicated bounded measurement mode. Defaults to 0 (normal runtime remains completely passive without background tickers).
- **Report destination isolation & bounded output**: MISSING dedicated file sink; current console logger outputs bounded `[MEASUREMENT SAMPLE #N]` snapshots.
- **Single global shutdown drain deadline**: PRESENT. Enforced in `agent/cmd/endpoint/main.go` (10s timer on `drainCtx`) and `agent/internal/ingestion/engine.go` (global drain boundary).
- **Bounded forwarder response reads**: PRESENT. Enforced in `agent/internal/forwarder/forwarder.go` via `io.LimitReader(resp.Body, 64*1024)` to cap HTTP response reads.
- **Measurement documentation & raw evidence**: MISSING raw empirical datasets; provisional metrics and contract defined in `phase_4_gate_0_resource_governor.md`.

### 3. Sampling Semantics & Execution Model
- **Periodic Ticker Removal**: Commit `ba41e75` removed the unapproved 5-second background sampling ticker per Architecture Rules 1 and 4, and Gate-0 Section 10 (prohibiting unapproved background polling).
- **Execution Model**: Normal runtime calls `Coordinator.Step` synchronously once at startup to establish CPU continuity baselines. Describing startup-only invocation as "continuous monitoring" is architecturally inaccurate.
- **Bounded Measurement Mode**: Configured via `-measure-duration` and `-measure-interval`. Spawns a bounded goroutine invoking `governor.RunSampling`, logging structured metrics snapshots, and initiating clean shutdown upon completion. Normal production runtime (`-measure-duration 0`) remains completely passive and unaffected. Resource enforcement remains strictly observational.

### 4. Dashboard Request Isolation Verification
- **Defect/Risk**: Rapid tenant switching in the dashboard could cause delayed responses from Tenant A to arrive after Tenant B was selected, corrupting UI state with stale data.
- **Remediation**: Added `queryGenRef` (`useRef<number>(0)`) in `dashboard/src/App.tsx`. Both `/v1/events` and `/v1/alerts` responses verify `!abortController.signal.aborted && currentGen === queryGenRef.current` before committing state.
- **Verification**: Added `dashboard/src/request_isolation.test.mjs` verifying that delayed Tenant A responses are discarded and Tenant B responses are committed cleanly. Ran `npm --prefix dashboard test` (1/1 PASS).

### 5. Storage Budget Contract Specification
- **SQLite Soft Quota (50 MiB)**: Pruning threshold internal to SQLite (`enforceQuota` in `sqlite.go`) targeting low-priority unforwarded events (DEBUG/INFO/WARNING).
- **Total Persistence Footprint**: Sum of physical regular-file sizes: `agent.db` + `agent.db-wal` + `agent.db-shm` + `agent_emergency_spill.log`.
- **Reserved Headroom**: Minimum physical host disk free space reserved for host OS and industrial control processes.
- **Physical Free-Disk Observations**: OS-level free space queries. Missing or failed measurements must be reported with `Valid: false`, never zero or infinite.
- **Hard Enforcement (Future Work)**: In Phase 4.3, enforcement remains observational. `StorageBudgetBytes` remains 0 until calibrated against realistic OT workloads and hardware constraints.

### 6. Real Detection Workload Specification
- **Active Rule**: Production endpoint (`agent/cmd/endpoint/main.go`) loads only `RealIOCExecRule` (`LOCAL-IOC-EXEC-001`, `v1.0`, `CRITICAL`), which evaluates `ev.Metadata["executable_path"] == "/opt/malicious/bin/miner"`.
- **Finding Generation**: Deterministic UUIDv5 finding identity based on tenant, rule ID, rule version, and evidence event ID; stored durably as `detection/finding`.
- **Native YARA-X Separation**: Native YARA-X rules (`YaraRule`) compile under CGO with `libyara_x_capi` (v1.20.0) but are NOT loaded by default on ordinary endpoints. Active detection is not inferred from native linkage.

### 7. Verification Outcomes
- `scripts/verify-phase4.ps1 -Race -NativeDetection`: 9/9 PASS (including UCRT64 64-bit CGO race detection and native YARA-X suite).
- `npm --prefix dashboard test`: PASS.
- `npm --prefix dashboard run lint`: PASS (0 errors, 0 warnings).
- `go build -v ./agent/cmd/endpoint`: PASS.
- Linux runtime validation: Outstanding on local Windows host; verified via GitHub Actions CI workflow.

## 2026-11-01 Phase 4 Bounded Endpoint Measurement Reports & Workload Demonstration (non-phase changing)

A focused checkpoint implementing machine-readable measurement reports, shared shutdown coordination, and a reproducible harmless workload demonstration was completed on branch `fix/correctness-remediation-checkpoint`.
Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

### 1. Bounded Machine-Readable Measurement Reports (`agent/internal/resourcereport`)
- **Package Architecture**: Extracted reporting logic into `agent/internal/resourcereport` containing `types.go`, `config.go`, `manifest.go`, `reporter.go`, `runner.go`, `shutdown.go`, and `report_test.go`.
- **CLI Options**: Added opt-in flags `-measure-report <path>`, `-measure-profile <desc>`, `-measure-duration <duration>`, and `-measure-interval <interval>` in `agent/cmd/endpoint/main.go`. Defaults to disabled (`0`), preserving zero-overhead passive production runtime.
- **Bounding & Safety Validations**:
  - Validates `duration > 0`, `interval >= 100ms`, `interval <= duration`, and bounds sample limits (`expected <= maxSamples`, default 10,000).
  - Validates report file size limits (`MaxOutputBytes`, default 10 MiB) before writing.
  - Refuses overwrite using exclusive creation (`os.O_CREATE|os.O_EXCL|os.O_WRONLY`).
  - Rejects destination conflicts against `agent.db`, `agent.db-wal`, `agent.db-shm`, `identity.json`, `agent.json`, and `agent_emergency_spill.log`.
  - Rejects report paths inside monitored directories (`cfg.MonitorPaths`) to prevent self-generated filesystem telemetry.
  - Visible logging on write errors without panicking or interrupting ordinary monitoring.
  - Preserves nulls for missing/unready measurements (`*float64`, `*uint64`), never fabricating zero.
- **Manifest & Contributing Files**: Captures git commit SHA, dirty state, OS, arch, toolchain, logical CPU count, profile description, loaded rule IDs/versions, sampling bounds, and lists every persistence file with path, size, and existence boolean.
- **Summary**: Records total samples, degraded samples, reporting errors, shutdown outcome, and the mandatory non-qualification notice.

### 2. Single Shared Shutdown Path & Drain Deadline Coordination
- **Coordinator**: `resourcereport.ShutdownCoordinator` unifies shutdown initiation across measurement timer expiry and OS signals (SIGINT, SIGTERM).
- **Single Drain Deadline**: Starts the single global 10-second drain deadline timer exactly once via `sync.Once`.
- **Invariant**: Bypassing the deadline with `cancel()` alone is prevented; `drainCtx` is guaranteed to expire if draining stalls. Tested under concurrent timer/signal overlap and pending ingestion.

### 3. Harmless Workload Demonstration & Rule Verification
- **Production-Path Integration Test (`agent/internal/integration/workload_demonstration_test.go`)**:
  - Exercises collector event creation -> enrichment boundary (`TenantID`, `SiteID`, `AssetID`) -> SQLite durability -> detection engine -> derived finding durability -> clean shutdown.
  - Ingests bounded recipe of 11 source events: 5 benign process events, 1 harmless IOC fixture event (`executable_path == "/opt/malicious/bin/miner"`), and 5 benign filesystem events.
  - Detection rule `RealIOCExecRule` (`LOCAL-IOC-EXEC-001`, `v1.0`, `CRITICAL`) fires synchronously on the IOC event.
  - Generated finding is assigned a deterministic UUIDv5 identity and explicitly linked via `evidence_event_ids: [iocSourceEventID]`.
  - Reopened SQLite database durably confirms 12 events total (11 source + 1 derived finding) with intact evidence linkage after clean shutdown.
- **Windows Collector Platform Scope Mismatch**: Explicitly documented that the executable-path IOC fixture is a metadata-matching test rule, not YARA file scanning. Real Windows process collectors monitor real Windows executables (`C:\...`) and cannot emit the Linux fixture path `/opt/malicious/bin/miner` during normal OS monitoring.
- **Windows Developer-Host Smoke-Run**:
  - Executed compiled `endpoint.exe` in an isolated directory with `-measure-report windows_smoke_report.json -measure-profile "windows-developer-host-smoke-run" -measure-duration 3s -measure-interval 1s`.
  - Produced 3-sample 3975-byte JSON report accurately capturing process CPU, RSS growth, write-I/O rate, and contributing persistence files (`agent.db`: 4096B, `agent.db-wal`: 201912B, `agent.db-shm`: 32768B).
  - Explicitly labeled as **developer-host smoke-run evidence, not representative low-spec qualification**.

### 4. Shared Dashboard Tenant Request Isolation
- Extracted shared request execution logic into `dashboard/src/tenant_query.ts`.
- Both production UI ([`App.tsx`](file:///d:/Desktop/OT-SOC/redcyberfox-ot-soc/dashboard/src/App.tsx)) and automated test ([`request_isolation.test.mjs`](file:///d:/Desktop/OT-SOC/redcyberfox-ot-soc/dashboard/src/request_isolation.test.mjs)) import and exercise this exact shared function.
- Proves delayed responses from Tenant A cannot commit state after switching to Tenant B.

### 5. Verification Outcomes
- `scripts/verify-phase4.ps1 -Race -NativeDetection`: 9/9 PASS (Gofmt, Go Vet, Dashboard Lint/Build, Storage, Ingestion, Core Components/Reports, Integration/Workload, UCRT64 64-bit Race, Native YARA-X).
- `npm --prefix dashboard test`: 1/1 PASS.
- `npm --prefix dashboard run lint`: 0 errors, 0 warnings.
- `go build -v ./agent/cmd/endpoint`: PASS.
- Linux runtime validation: Outstanding on local Windows host; verified via GitHub Actions CI workflow.

## 2026-11-01 Phase 4.4 Event Accounting & Queue Instrumentation Checkpoint (non-phase changing)

A focused checkpoint implementing queue-age and backpressure tracking, end-to-end event accounting reconciliation, and wiring into governor metrics and measurement reports was completed on branch `fix/correctness-remediation-checkpoint`.
Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

### 1. Instrumented Bounded Event Queue (`agent/internal/eventqueue`)
- **Ring Buffer Design**: Fixed-capacity ring buffer (`ring []item`, `head`, `tail`, `count`) pre-allocated with zero heap allocations during steady-state pushing/popping.
- **Oldest Event Age Exposure**: Computes `time.Since(ring[head].enqueuedAt).Seconds()` non-destructively in $O(1)$ time without requiring event dequeue. When empty, safely returns `governor.ValidFloat64{Valid: false}`.
- **Queue Metrics**: Exposes `Depth()`, `Capacity()`, `SaturationPercent()`, `OldestEventAge()`, and `BackpressureEventsTotal()`.
- **Channel Backpressure**: Disables receiving from `In()` channel when `count == capacity` (200), exercising natural Go channel backpressure on collectors and incrementing `backpressureEventsTotal`.
- **Graceful Draining**: `Close()` closes `In()` channel without blocking; the internal pump delivers all buffered events to `Out()` until empty, then closes `Out()`. `WaitDrain()` blocks until drainage completes.
- **Unit & Race Tests (`agent/internal/eventqueue/queue_test.go`)**: 4 test suites covering FIFO ordering, age progression, backpressure blocking, graceful drain, and concurrent senders. Verified under UCRT64 64-bit CGO race detection (0 data races).

### 2. End-to-End Event Accounting Reconciliation
- **Ingestion Accounting (`agent/internal/ingestion/engine.go`)**:
  - Atomic tracking across all pipeline terminal and durability outcomes: `ingestedTotal`, `committedTotal`, `failedBeforeCommitTotal`, `recoveredDLQTotal`, `spilledDiskTotal`, `droppedTotal`.
  - Enforces the strict pipeline accounting invariant:
    $$\text{IngestedTotal} = \text{CommittedTotal} + \text{FailedBeforeCommitTotal} + \text{RecoveredDLQTotal} + \text{SpilledDiskTotal} + \text{DroppedTotal}$$
  - Verified in `agent/internal/ingestion/engine_test.go` (`TestIngestion_EventAccountingReconciliation`) across 40 events traversing committed, pre-commit failure, DLQ recovery, emergency disk spill, and drop outcomes.
- **Forwarder Accounting (`agent/internal/forwarder/forwarder.go`)**:
  - Atomic tracking across upstream delivery outcomes: `attemptsTotal`, `forwardedTotal` (202), `dlqTotal` (400, tenant mismatch, serialization error), `retryTotal` (429, network offline).
  - Verified in `agent/internal/forwarder/forwarder_test.go` (`TestForwarder_Accounting`).

### 3. Production Runtime & Measurement Report Wiring
- **Endpoint Production Wiring (`agent/cmd/endpoint/main.go`)**:
  - Replaced raw channel with `eventqueue.New(200)`.
  - All collectors (`procCol`, `fsCol`, `invCol`, `netCol`, `usbCol`) write to `eventQ.In()`.
  - Ingestion loop drains from `eventQ.Out()`.
  - Governor coordinator baseline step and measurement sampling dynamically read `eventQ.Depth()`, `eventQ.Capacity()`, and `eventQ.OldestEventAge()`.
  - Graceful shutdown stops collectors, calls `eventQ.Close()`, drains ingestion, and waits on `eventQ.WaitDrain()`.
- **Measurement Report Integration (`agent/internal/resourcereport`)**:
  - `Sample` records live `QueueOldestEventAgeSeconds` and cumulative `BackpressureEventsTotal`.
  - `Summary` embeds `EventAccountingSummary` detailing pipeline totals and reporting `ReconciliationValid`.
  - Verified in `agent/internal/resourcereport/report_test.go` (`TestReport_EventAccounting`).

### 4. Verification Outcomes
- `scripts/verify-phase4.ps1 -Race -NativeDetection`: 9/9 PASS.
- `npm --prefix dashboard test`: 1/1 PASS.
- `npm --prefix dashboard run lint`: 0 errors, 0 warnings.
- `go build -v ./agent/cmd/endpoint`: PASS.
- Linux runtime validation: Outstanding on local Windows host; verified via GitHub Actions CI workflow.
