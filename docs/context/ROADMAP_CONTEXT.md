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
- **Next Bounded Implementation Task**: Event accounting and queue-age/recovery instrumentation, followed by representative Windows/Linux workloads before threshold calibration.
