# Roadmap Context

## Phase Status Summary
- **Phase 0 (Bootstrap)**: COMPLETE
- **Phase 1 (Vertical Slice)**: COMPLETE
- **Phase 2A (Agent Foundation)**: COMPLETE
- **Phase 2B (Agent Storage & Integrity)**: COMPLETE / FROZEN
- **Phase 2C.0 (Process Telemetry Contract)**: COMPLETE
- **Phase 2C.1 (Process Lifecycle Engine)**: COMPLETE
- **Phase 2C.2 (Linux Process Collector)**: COMPLETE
- **Phase 2C.3 (Windows Collector)**: COMPLETE
- **Phase 2C.4 (Cross-Platform Validation)**: COMPLETE / CLOSED
- **Phase 2C.5 (Endpoint Runtime Wiring & Pipeline)**: COMPLETE
- **Phase 2C (Overall)**: COMPLETE
- **Phase 2D (Filesystem Telemetry)**: COMPLETE
- **Phase 2E.1 (Inventory Collector)**: COMPLETE
- **Phase 2E.2 (Network Collector)**: COMPLETE
- **Phase 2E.3.1 (Windows USB)**: COMPLETE
- **Phase 2E.3.2 (Linux USB)**: COMPLETE
- **Phase 2E.4 (Cross-Source Regression & Data Integrity)**: COMPLETE / FROZEN
- **Phase 2 (Overall)**: COMPLETE / FROZEN

## Phase 3 Status
- **Phase 3 (Detection Core)**: COMPLETE
  - **Gate 1 (Boundary Setup)**: CLOSED
  - **Gate 2 (Foundation Hardening)**: CLOSED
  - **Gate 3 (Detection Engine Robustness)**: CLOSED
## Out-of-Sequence Preparatory OT Work
- **Phase 3.1A / Phase 3.1B**: Recorded as "out-of-sequence preparatory OT sensing work already present in the repository". Preserved without extension. Does not automatically alter the official milestone sequence.

## Official Roadmap Workflow
- **Phase 3**: Detection Core
- **Phase 4**: Rural Hardening (ACTIVE)
  - **Phase 4.1**: Resource Governor Core & Margins (CLOSED)
  - **Phase 4.2**: Observable Resource Governor Metrics (CLOSED)
  - **Phase 4.3**: Real Resource Signal Collection (IN PROGRESS / ACCEPTANCE BLOCKERS)
    - **Phase 4.3.1**: OS Provider Contract & Mock Verification (CLOSED)
    - **Phase 4.3.2**: Windows OS Resource Provider (CLOSED)
    - **Phase 4.3.3**: Linux OS Resource Provider (CLOSED)
    - **Phase 4.3.4**: Resource Signal Wiring & Integration (PARTIAL / ACCEPTANCE BLOCKERS)
- **Phase 5**: OT Lab
- **Phase 6**: Passive OT Sensor
- **Phase 7**: OT Context
- **Phase 8**: Correlation + Risk
- **Phase 9**: Security Hardening
- **Phase 10**: Working Alpha
- **Phase 11**: Intelligence / Search
- **Phase 12**: Resilience / Pilot
- **Phase 13**: Sector Expansion

## Phase 2 Architectural Notes
- Phase 2A provides the robust Agent-Server core.
- Phase 2B implements resilient disk buffering (Storage) and Identity mechanics.
- Phase 2C defines the generalized Process Telemetry contract (`CanonicalEvent`, Categories) with platform-specific collectors and lifecycle deduplication.
- Phase 2D implements real-time filesystem telemetry with recursive directory watching.
- Phase 2E.1, 2E.2, 2E.3 implement system inventory, network active connections, and hardware USB change tracking across Windows and Linux.
- Phase 2E.4 completes cross-source regression with a shared production ingestion engine, unified event ID ownership, single 10-second global shutdown drain deadline, SQLite contention resilience, and deterministic error cause preservation.


## Phase 4 Correctness-Remediation Checkpoint (2026-11-01)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - Authoritative endpoint identity anchored strictly in SQLite `agent_state`, verified across missing, conflicting, and corrupt legacy identity files across multiple boots.
  - Deterministic UTC clock seam and parameterized retry queries in SQLite storage; fake retry database manipulation removed from integration tests.
  - Store and DLQ duplicate integrity checks executed prior to quota pruning or failure.
  - Dashboard read contract repaired with configurable API base, encoded tenant query parameters, visible error handling, and clean OxLint verification.
  - Unified verification script `scripts/verify-phase4.ps1` added with explicit blocked-dependency accounting.
- **Milestone Status**: Phase 4 acceptance remains **OPEN** pending low-spec hardware benchmarks, authoritative storage budget policy, real detection workloads, and safe resource adaptation.

## Phase 4 Measurement-Readiness Checkpoint (2026-11-01)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - Audited and accounted for 7 measurement capabilities (bounded measurement runner in endpoint, global shutdown drain deadline, bounded forwarder response reads, missing standalone binaries and empirical datasets).
  - Clarified and resolved sampling semantics: periodic background ticker removal justified under Architecture Rules 1 & 4; on-demand startup step primed without continuous background polling; bounded measurement mode provided via `-measure-duration`.
  - Implemented and verified dashboard tenant request isolation via `queryGenRef` and `AbortController` cancellation, preventing delayed responses from overwriting current tenant state.
  - Specified storage budget contract distinguishing SQLite soft quota, total persistence footprint, reserved headroom, physical free-disk observations, and future hard enforcement.
  - Specified real detection workload (`RealIOCExecRule`, `LOCAL-IOC-EXEC-001`, `v1.0`), evaluated inputs, artifact bounds, and harmless fixture, distinguishing from native YARA-X rules.
- **Milestone Status**: Phase 4 acceptance remains **OPEN**; Phase 5 has not begun. Representative low-spec hardware benchmarks, queue-age/recovery instrumentation, and safe priority adaptation remain open.

## Phase 4 Bounded Measurement Reports & Workload Demonstration Checkpoint (2026-11-01)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - Implemented `agent/internal/resourcereport` providing opt-in machine-readable JSON reports, duration/interval validation, sample and output byte bounds, exclusive creation (`O_EXCL`), persistence file conflict rejection, watched path exclusion, and null preservation for unmeasured metrics.
  - Implemented `resourcereport.ShutdownCoordinator` guaranteeing a single shared shutdown path across measurement timer expiry and OS signals, starting the single global 10-second drain deadline exactly once.
  - Implemented production-path integration test `agent/internal/integration/workload_demonstration_test.go` demonstrating harmless bounded event ingestion, synchronous evaluation of `RealIOCExecRule` (`LOCAL-IOC-EXEC-001`), derived finding generation with deterministic UUIDv5, explicit evidence event ID linkage, and durable SQLite presence after clean shutdown.
  - Demonstrated endpoint execution producing a bounded Windows developer-host smoke-run report (`windows_smoke_report.json`), and documented the platform mismatch between live Windows process monitoring and the Linux fixture path.
  - Extracted shared request execution logic `fetchTenantData` in `dashboard/src/tenant_query.ts`, tested directly in `dashboard/src/request_isolation.test.mjs`.
- **Milestone Status**: Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

## Phase 4.4 Measurement Finalization, Backpressure Semantics & Ingestion-Outcome Reconciliation Checkpoint (2026-11-05)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Unified Production Shutdown (`resourcereport.ExecuteShutdown`)**: Shared single authoritative shutdown implementation between `endpoint/main.go` and integration tests. Stops collectors, closes queue, waits for ingestion drain, stops forwarder, joins measurement runner (`MeasureJoin`), checks global drain deadline timer, inspects durable storage before DB close, finalizes report with point-in-time accounting, and closes SQLite. Collector, forwarder, runner, report, and DB errors are captured, modify `ShutdownOutcome` with `_WITH_ERRORS`, and propagate to process exit status (`os.Exit(1)`).
  - **Ingestion-Outcome Reconciliation**: Explicitly named and scoped the accounting equation:
    $$\text{IngestedTotal} = \text{CommittedTotal} + \text{FailedBeforeCommitTotal} + \text{RecoveredDLQTotal} + \text{SpilledDiskTotal} + \text{DroppedTotal}$$
    Enforces local ingestion-outcome reconciliation (protected with mutex snapshotting against atomic skew). Strictly not labeled end-to-end reconciliation or proof of zero loss.
  - **Accepted HTTP Responses Accounting**: `HTTPAcceptedTotal` in `ForwarderAccounting` defined strictly as total accepted HTTP responses (HTTP 202) received, including repeats/retries caused by local deletion failures or network retries. Does not claim unique-event delivery.
  - **Explicit Durable Storage Inventory**: Distinguished source telemetry from derived detection findings, pending events from DLQ, and startup backlog. Unsupported metrics (retention pruning) remain explicitly `null`.
  - **Instrumented Enqueue & Deterministic Backpressure**: Replaced inferred channel counting with instrumented `Enqueue(ctx, ev)` and `TryEnqueue(ev)` on `agent/internal/eventqueue`. Handles multiple concurrent blocked producers and context cancellation via FIFO waiter queue with zero data races. Tested under full-queue pressure, 10 concurrent producers, and cancellation under 64-bit UCRT64 CGO race detector.
  - **Queued Event Drain Regression**: Updated `agent/internal/integration/queued_drain_report_test.go` to hold ingestion behind an explicit barrier (`cfg.BeforeProcess`), buffer 20 events in memory, trigger actual measurement expiry via `RunMeasurementRunner` duration timer, release ingestion barrier, execute shared `ExecuteShutdown`, and verify durable SQLite commitment and report accounting without duplicating shutdown code.
  - **Verification**: `scripts/verify-phase4.ps1 -Race -NativeDetection` 9/9 PASS, dashboard request isolation tests PASS, native endpoint build PASS.

## Phase 4 Queue Memory Bounding, Production Metric Scoping & Truthful DB Close Checkpoint (2026-11-05)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Bounded Queue Memory & Mixed-API Saturation**: Added `maxWaiters int` to `EventQueue` (defaulting to `capacity`). Submissions beyond `maxWaiters` fail with `ErrQueueAtCapacity`. Total queue-owned memory strictly bounded by `capacity + maxWaiters + 1`. Resolved mixed channel/direct-API saturation so that submitting via `In()` on full ring and waiters does NOT drop events or terminate the queue on `ErrQueueAtCapacity`. Repaired `Close()` so closing while the adapter is blocked unblocks all pending waiters with `ErrQueueClosed` and unblocks the adapter without deadlock while preserving accepted ring items for draining.
  - **Production Metric Scoping**: Scoped `AdapterBlockedEnqueueAttemptsTotal` (and aliases) to input adapter waits against the queue rather than an exact count of all concurrent OS collectors waiting on the Go channel rendezvous. Tested sustained saturation, concurrent producers, cancellation, and close with pending submissions.
  - **Truthful Report Finalization**: Durable storage inventory captured before DB close, DB close executed and captured, report finalized after DB close incorporating DB close results into `ShutdownOutcome` on disk (`<OUTCOME>_WITH_ERRORS: storage close: ...`). Verified simulated close failure produces both a failed report outcome on disk and a non-zero exit code (`os.Exit(1)`).
  - **Verification**: `scripts/verify-phase4.ps1 -Race -NativeDetection` 9/9 PASS, dashboard lint & build PASS, native endpoint and server API build PASS.
## Phase 4 Bounded Windows Workload Measurement Evidence Checkpoint (2026-11-05)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Automated Workload Report Suite (`agent/internal/integration/workload_reports_test.go`)**: Built end-to-end integration suite running the compiled production endpoint binary (`endpoint.exe`) as an isolated external process under four operational scenarios:
    1. **Idle (`windows-smoke-idle`)**: Bounded 3s run, 1s interval. 2 baseline startup events (inventory, network) ingested, committed, and forwarded cleanly. RSS 21.9 MB, CPU ~0.19%, queue depth 0, 0 dropped.
    2. **Sustained (`windows-smoke-sustained`)**: Bounded 4s run, 1s interval. Sustained 200ms file creation stream generating 26 total events. All 26 ingested, committed to SQLite, forwarded (HTTP 202 accepted), and transactionally removed. RSS 23.4 MB, WAL 1.27 MB, IO 362-453 KB/s.
    3. **Burst (`windows-smoke-burst`)**: Bounded 4s run, 1s interval. Rapid burst of 35 files generating 72 total events. All 72 ingested, committed to SQLite, forwarded, and removed without queue overflow or data loss. Peak RSS 25.78 MB, peak WAL 3.73 MB, peak write IO 3.68 MB/s.
    4. **Offline & Real Retry Recovery (`windows-smoke-offline` / `windows-smoke-reconnect`)**: 2-phase lifecycle on identical database:
       - *Offline*: 3s run against unreachable port (`http://127.0.0.1:54321`), 22 events ingested and durably committed to SQLite `state = 'PENDING'`. Forwarder attempted 22 and retried with exponential backoff (`next_attempt_at`). HTTP accepted: 0. Retained pending: 22.
       - *Real Retry Reconnect*: Server online, same DB reopened without SQL alteration, 22 initial backlog detected. Ran natively for 22s allowing the 10s–19s exponential backoff window to naturally expire. Forwarder drained all 22 backlog + 2 startup events = 24 accepted via HTTP 202, local removed: 24, remaining pending in SQLite: 0.
  - **Evidence Artifacts & Comprehensive Analysis**: Stored raw report JSON files in `docs/evidence/phase4/` and compiled complete comparative analysis in `docs/evidence/phase4/windows_workload_evidence.md`.
  - **Developer-Host Smoke Evidence Disclaimer**: Explicitly documented that developer-workstation measurements do NOT substitute for low-spec qualification. Resource governor thresholds remain observational and uncalibrated. Phase 4 remains open; Phase 5 not begun.

## Phase 4 Retry Evidence Validity, Fresh Binary Provenance & Adapter-Held Event Preservation Checkpoint (2026-11-05)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Preserve Adapter-Held Events During Close**: Removed `closeAbortedAdapter`. Once an event is sent to `In()`, the adapter preserves it for admission into the ring buffer as soon as capacity frees during drain. All accepted events drain via `Out()` in exact FIFO order. Queue-retained memory strictly bounded by $\le \text{capacity} + \text{maxWaiters} + 1$. Added deterministic barrier regressions with zero data races.
  - **Fresh Binary Provenance**: Subprocess tests compile fresh `endpoint.exe` directly into `t.TempDir()`, inspect `vcs.revision` and `vcs.modified` via `go version -m`, and validate against git state. Never reuses repository-root binary.
  - **Real Retry Recovery & Mock Receiver**: Removed fake SQL that cleared retry deadlines. Preserved natural backoff timestamps across shutdown and restart. 22-second reconnect run verified natural deadline expiration, mock HTTP 202 acknowledgement, and local SQLite removal. Documented mock receiver boundary and harmless rule match scope.
  - **Verification**: `scripts/verify-phase4.ps1 -Race -NativeDetection` 9/9 PASS.
- **Milestone Status**: Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

## Phase 4 Measurement Evidence Provenance, Report Preservation & Subprocess Bounds Checkpoint (2026-11-10)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Subprocess Execution Bounding**: Corrected documentation that prematurely claimed all commands were bounded. Applied explicit timeout contexts to all external commands (`git rev-parse HEAD` 5s, `git status` 5s, `go build` 90s, `go version -m` 10s, `endpoint.exe` scenarios 13s–37s) via `ExecCommandWithTimeout` with captured diagnostics and clear timeout reporting.
  - **Toolchain Provenance & Binary Hashing**: Implemented `ParseBinaryProvenanceOutput` handling Windows and POSIX absolute executable paths and strictly requiring `toolchain`, `revision`, and `vcs.modified` presence. Computed binary SHA-256 (`ComputeFileSHA256`) and recorded in `provenance.json`.
  - **Historical Preservation & Run Isolation**: Recovered original reports from commits `d6fd8bd` and `5cbe40b` into dedicated `docs/evidence/phase4/historical/` directories. Generated new evidence exclusively (`WriteEvidenceExclusive`) into a unique run directory (`docs/evidence/phase4/runs/run_20261110_054251_8240a569/`) alongside `commands.log` and `provenance.json`.
  - **Committed Clean Baseline Verification**: Verified binary provenance against clean committed source (`dirty: false`, commit `8240a56...`). Validated every scenario report manifest against binary provenance.
  - **Truthful Degraded Measurement Accounting**: Explicitly documented why samples report `degraded_samples = total_samples` (raw signals fully available; storage pressure evaluation intentionally blocked at `StorageBudgetBytes: 0` pending authoritative policy).
  - **Verification**: 9/9 PASS on `verify-phase4.ps1 -Race -NativeDetection`, 5/5 PASS on provenance unit tests, 4/4 PASS on Windows measurement workload suite.
- **Milestone Status**: Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.

## Phase 4 Evidence Export Protection, Source Stability Boundaries & Diagnostic Capture Checkpoint (2026-11-10)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Overwrite Path Elimination**: Removed automatic canonical-report overwriting in `workload_reports_test.go`. Evidence exports exclusively via `ExportEvidenceRun` into unique run directories. External `EVIDENCE_EXPORT_DIR` causes zero writes into the repository. All export/write/close errors are strictly checked and propagated. Added regression `TestExportEvidenceRun_RefusesOverwriteAndPreservesUnrelatedFiles`.
  - **Source Stability Boundaries**: Implemented `CheckSourceBoundary` across `pre-compilation`, `post-compilation`, and `post-workload` boundaries. Requires clean committed checkout (`dirty: false`) for qualification evidence. Fails visibly if revision changes or working tree becomes dirty, refusing export without modifying or cleaning working tree. Disclosed boundary-consistency semantics. Added regression `TestCheckSourceBoundary_ValidAndRecordsStatus`.
  - **Subprocess Diagnostics & Truthful Outcomes**: Unified all subprocess execution under `ManagedSubprocess` and `RunBoundedCommand`. Bounded stdout/stderr capture (256 KB) with `[OUTPUT TRUNCATED]` tagging. `ExitCode` is typed `*int` and set to `nil` when a process never starts (no invented exit codes). Wrapped `*exec.ExitError` exit codes extracted via `errors.As`. Distinguishes `SUCCESS`, `NONZERO_EXIT`, `TIMEOUT`, `CANCELLED`, and `LAUNCH_FAILURE`. Added 5 focused subprocess regressions.
  - **Raw Evidence Reconciliation**: Reconciled documentation and summary tables with exact raw JSON evidence from clean run `run_20261110_054251_8240a569` (5 sustained samples, 12 reconnect samples, exact memory/storage/IO metrics, and timer boundary variation explanation).
- **Milestone Status**: Phase 4 acceptance remains **OPEN**; Phase 5 has not begun.
- **Next Bounded Implementation Task**: Execute equivalent Linux CI race & native workload evidence collection and plan representative low-spec qualification.

## Phase 4 Remaining Qualification Gates & Delivery Failure Boundary Validation Checkpoint (2026-11-10)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Degraded Resource Samples Investigation**: Verified why all 25 reported samples across Windows workload profiles report `sampling_error: storage budget is unconfigured or unresolved`. Traced root cause to `StorageBudgetBytes: 0` in `endpoint/main.go`. Proved all hardware and OS counters (CPU, RSS, storage file sizes, write I/O rate, queue depth/saturation) are valid direct OS measurements; storage pressure is intentionally blocked pending authoritative policy. Documented in `docs/evidence/phase4/degraded_samples_investigation.md`.
  - **Realistic YARA-X Detection Workload**: Implemented `TestWorkload_YaraX_PositiveVersusHarmlessDetection` in `agent/internal/integration/workload_detection_yarax_test.go` with native `libyara_x_capi`. Compared 30 harmless events (25.2 ms, 425 µs avg latency, 0 findings) against 30 positive detection events (35.5 ms, 868 µs avg latency, exactly 30 derived findings with deterministic UUIDv5 identities). Verified 2.03x storage amplification and zero event loss. Passes with `-race`.
  - **Delivery Failure Boundary Validation**: Added comprehensive test suite `TestDeliveryBoundary_*` in `agent/internal/integration/delivery_boundaries_test.go` covering 7 failure modes: receiver unavailable, receiver reconnecting, upload timeout, duplicate ack/retry, agent restart with pending events, abrupt process termination during forwarding, and ack without durable receiver persistence. Added `SetHTTPTimeout` on `Forwarder`. Formally documented delivery guarantees and architectural boundaries in `docs/architecture/DELIVERY_GUARANTEES.md`.
  - **Target Low-Spec Hardware Acceptance Criteria**: Defined quantitative acceptance criteria for 2 vCPU / 2-4 GB RAM / eMMC OT edge gateways (steady-state CPU $\le 5\%$, burst CPU $\le 20\%$, RSS $\le 100 \text{ MB}$, write I/O $\le 500 \text{ KB/s}$, queue saturation $< 50\%$, detection latency $\le 50 \text{ ms}$, zero event loss). Enforced anti-extrapolation rule: workstation results must NOT be extrapolated to qualify field hardware. Documented in `docs/evidence/phase4/low_spec_acceptance_criteria.md`.
  - **Linux Platform Differences & CI Validation**: Documented mechanics differences across subprocess lifecycle, mandatory vs advisory file locking, clock resolution, SQLite WAL shared memory, and event ingestion (ReadDirectoryChangesW vs inotify). Enforced anti-inference rule. Documented in `docs/evidence/phase4/linux_platform_differences.md`.
- **Milestone Decision**: **`PHASE 4 PARTIAL — REMAINING QUALIFICATION GAPS`**
  - Rationale: While algorithmic correctness, memory bounds, delivery failure boundaries, and YARA-X detection pipelines are proven, qualification on representative physical low-spec hardware and Linux runtime qualification remain open. Phase 4 remains **OPEN**; Phase 5 has not begun.

## Phase 4 Qualification Test Assertions, Crash-Test Claims & Failed-Run Evidence Retention Checkpoint (2026-11-10)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Latency Statistics & Nearest-Rank Percentiles**: Replaced ad-hoc percentile estimates in `workload_detection_yarax_test.go` with Nearest-Rank method ($\lceil P \times N \rceil - 1$) on an isolated sorted copy via `CalculateLatencySummary`. Handles empty and single-element samples. Verified via `TestLatencyPercentiles_UnsortedAndEdgeCases` with deliberately unsorted inputs. Separates pure scanner duration (`yaraRule.Evaluate`) from total finding persistence (`detEngine.Evaluate` + SQLite insert). Emits machine-readable component JSON evidence. Explicitly notes Go runtime heap vs native CGO memory boundary.
  - **Finding Identity & Causal Linkage**: Deserialized stored finding payloads from SQLite `events`, asserting rule ID (`YARA-OT-MALWARE-001`), version (`1.0`), severity (`CRITICAL`), category (`detection/finding`), and source (`local_detection`). Enforces exact bijective 1-to-1 linkage in `Metadata["evidence_event_ids"]` matching positive source events, verifies deterministic UUIDv5 generation, proves 0 false findings on harmless inputs, verifies `rows.Err()`, and re-asserts all findings after storage close and independent database reopen.
  - **Truthful Subprocess Crash Coverage**: Re-labeled graceful restart (`TestDeliveryBoundary_GracefulRestartWithPendingSQLiteEvents`) and rollback (`TestDeliveryBoundary_StorageTransactionRollbackDurability`) tests. Implemented bounded child-process crash regressions via `cmd.Process.Kill()`:
    - `TestCrashBoundary_SubprocessStorageCrash_ProcessTermination`: child process terminated after SQLite commit; restarts and verifies all events recovered in `PENDING` state.
    - `TestCrashBoundary_SubprocessUncommittedDelete_ProcessTermination`: child terminated during uncommitted DELETE transaction; verifies transaction rolled back and event preserved.
    - `TestCrashBoundary_SubprocessForwarder_ProcessTermination`: child terminated while forwarder request is in-flight; verifies event was not prematurely removed from SQLite.
    - Explicitly removed unsupported physical power-loss claims, documenting the boundary between software process termination and hardware disk-cache flush barriers.
  - **Detection Evidence Scoping & Blocker Analysis**: Authored `docs/evidence/phase4/detection_evidence_scoping.md` documenting that YARA-X tests are component integration tests bypassing collectors and eventqueue. Identified concrete blocker in `agent/cmd/endpoint/main.go` (hardcoded `RealIOCExecRule` without dynamic `.yar` compilation) and outlined smallest design change (`RulesDir` in config and CLI flag).
  - **Persistent Diagnostics Retention**: Implemented `RunDiagnosticManager` in `provenance_helpers.go` exporting `diagnostic_manifest.json`, `commands.log` (via `FormatCommandsLog`), and partial reports to `docs/evidence/phase4/diagnostics/` labeled `FAILED` or `INVALID`. Wired into `workload_reports_test.go` across all boundary checks and scenarios. Tested via `TestDiagnosticRetention_FailedAndInvalidRuns` using child helper processes.
  - **Loss & Delivery Claims Clarification**: Updated `docs/architecture/DELIVERY_GUARANTEES.md` with complete equation $\text{Ingested} = \text{Committed} + \text{FailedBeforeCommit} + \text{RecoveredDLQ} + \text{SpilledDisk} + \text{Dropped}$, collector admission boundaries, power-loss limitation notes, and wall-clock vs injected-clock distinctions. Updated `docs/evidence/phase4/low_spec_acceptance_criteria.md` re-labeling criteria as Proposed Acceptance Criteria with documented industrial engineering rationale and resolved conflicting limits.
  - **Verification**: 9/9 PASS on `verify-phase4.ps1 -Race -NativeDetection`, zero vet warnings, successful native `endpoint.exe` build.
- **Milestone Decision**: **`PHASE 4 PARTIAL — REMAINING QUALIFICATION GAPS`**
  - Rationale: While all qualification assertions, crash regressions, diagnostics retention, and delivery boundary specifications are complete, qualification on representative physical low-spec hardware and Linux runtime qualification remain open. Phase 4 remains **OPEN**; Phase 5 has not begun.

## Phase 4 Platform Test Compilation Separation, Strict YARA-X Provenance, Bounded Crash Output & Cleanup Survival Checkpoint (2026-11-10)
- **Status**: Completed on branch `fix/correctness-remediation-checkpoint`.
- **Key Changes**:
  - **Platform-Specific Compilation Separation**: Moved platform-dependent unreadable-file fixtures from `provenance_test.go` into `provenance_windows_test.go` (`//go:build windows`, Win32 exclusive lock) and `provenance_unix_test.go` (`//go:build !windows`, broken symlink deterministic under root). Verified that shared integration tests cross-compile to Linux cleanly (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./agent/internal/integration`) without undefined Windows `syscall` symbols. Cross-compilation is labeled as compilation evidence only; Linux runtime execution gate remains explicitly marked `BLOCKED` without a native Linux runner.
  - **Strict YARA-X Component Evidence Provenance**: Added dual-boundary source verification (`preCheck` and `postCheck`) via `CheckSourceBoundary` in `workload_detection_yarax_test.go`. Enforces clean committed checkout (`dirty: false`) for qualification evidence export; fails visibly and invalidates run if revision changes or working tree is dirty. Evaluates all assertions (Phases A–E, finding identity, reopen durability, performance invariants) before evidence export. Preserves historical dirty smoke run `yarax_20261110_090634`. Exported clean evidence from commit `b31da96` to `docs/evidence/phase4/runs/yarax_20261110_093542_b31da96c/yarax_component_evidence.json` with `dirty: false` and full nearest-rank latency percentiles.
  - **Bounded Crash Subprocess Output**: Replaced unbounded `*bytes.Buffer` in `ManagedCrashProcess` (`delivery_boundaries_test.go`) with thread-safe `*boundedBuffer` (64 KB stream limit). Added `noisy_child` subprocess mode and regression test `TestCrashBoundary_NoisyChildProcess_BoundedOutputAndReap` proving output capping, `Truncated() == true`, `[OUTPUT TRUNCATED]` tagging, and 5s reap deadline completion. Distinguishes expected forced termination (`Process.Kill`) from reap timeout.
  - **Component Evidence Cleanup Survival**: Defined typed `YaraXComponentEvidence` and `ExportYaraXComponentEvidence`. Updated `TestWorkload_YaraX_ComponentEvidenceExport` to use an isolated subtest staging in `subT.TempDir()` and exporting to a parent persistent directory. Asserts staging artifacts are wiped on cleanup, exported evidence remains readable/complete with full provenance/timing fields without dummy JSON substitutes, repeated export strictly refuses overwrite, and target write failures are safely propagated.
  - **Strengthened Mandatory Verification**: Updated `scripts/verify-phase4.ps1` to detect Go's `[no tests to run]` output, treat unexecuted suites as failures, check Linux cross-compilation, truthfully mark Linux runtime execution gate as `BLOCKED`, and identify CGO-disabled omission of native tests. Full verification transcript recorded in `docs/evidence/phase4/verification_latest.txt` (13/13 runnable checks PASS, 1 BLOCKED dependency).
- **Milestone Decision**: **`PHASE 4 PARTIAL — REMAINING QUALIFICATION GAPS`**
  - Rationale: Test compilation, component evidence provenance, crash output bounding, and cleanup survival are verified. Phase 4 remains **OPEN**; endpoint dynamic rule loading (`--rules-dir`) and target low-spec physical hardware qualification remain subsequent tasks. Phase 5 has not begun.



