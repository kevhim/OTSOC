# Current State

- **Current Git Branch**: `feature/phase-4-3-4-resource-wiring`
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
Phase 4.3 — Real Resource Signal Collection: CLOSED
- **Phase 4.3.1 (OS Provider Contract & Mock Verification)**: CLOSED
- **Phase 4.3.2 (Windows OS Resource Provider)**: CLOSED (cumulative source review approved; CPU continuity, overflow guards, storage configuration integrity, and checked storage accumulation verified under Windows UCRT64/CGO)
- **Phase 4.3.3 (Linux OS Resource Provider)**: CLOSED (cumulative source review approved; CPU continuity, overflow guards, RSS estimate/overflow guards, regular-file storage checks, storage configuration integrity/defensive copy, real Linux-source smoke test, sleep-free deterministic clock advancement, and checked storage accumulation verified under Ubuntu Go 1.26)
- **Phase 4.3.4 (Resource Signal Wiring & Integration)**: CLOSED / LINUX CI VERIFIED. The endpoint samples process resources, SQLite DB/WAL/SHM against its 50 MiB quota, bounded ingress queue saturation, and forwarder-observed connectivity. Transitions and changed sampling errors are logged. The monitor remains observational; collector throttling and priority policy require later Phase 4 work. Local Windows governor and forwarder tests pass; the existing `TestStorage_CorruptDB` backup assertion fails on this Windows host. The [Linux PR CI run](https://github.com/kevhim/OTSOC/actions/runs/39019997303) passed the real YARA-X build, Go vet, unit/integration and race tests, migrations, E2E, dashboard build, and Docker Compose check. See `docs/implementation/phase_4_3_4_resource_signal_wiring.md`.

The next Phase 4 work is to benchmark representative low-spec endpoints, calibrate provisional thresholds, and define safe enforcement and priority behavior against the Phase 4 gate-0 acceptance criteria.
