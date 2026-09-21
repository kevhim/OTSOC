# Current State

- **Current Git Branch**: `feature/phase-2d-filesystem-telemetry`
- **Current Commit Hash**: `HEAD`

## Phase Status
- **Phase 2A (Foundation)**: COMPLETE
- **Phase 2B (Agent Storage & Integrity)**: COMPLETE / FROZEN
- **Phase 2C.0 (Process Telemetry Contract)**: COMPLETE
- **Phase 2C.1 (Process Lifecycle Engine)**: COMPLETE
- **Phase 2C.2 (Linux Process Collector)**: COMPLETE
- **Phase 2C.3 (Windows Collector)**: COMPLETE (Windows snapshot/reconciliation)
- **Phase 2C.4**: COMPLETE / CLOSED (Cross-Platform Validation - Fixed non-determinism, temporary unobservability identity loss, and Linux nanosecond precision defects. Validation gates passed on both Windows and Linux).
- **Phase 2C.5-A (Endpoint Runtime Wiring & Lifecycle Synchronization)**: COMPLETE (Synchronized shutdown sequence and pipeline draining to prevent race conditions).
- **Phase 2C.5-B/C (Integration tests & Next steps)**: COMPLETE (Endpoint-to-Server pipeline verified).
- **Phase 2C (Overall)**: COMPLETE
- **Phase 2D (Filesystem Telemetry)**: COMPLETE
- **Phase 2E.1 (Inventory Collector)**: COMPLETE
- **Phase 2E.2 (Network Collector)**: COMPLETE
- **Phase 2E.3.1 (Windows USB)**: COMPLETE (Physical Win32 events captured via real-host testing)
- **Phase 2E.3.2 (Linux USB)**: COMPLETE (Real-Host Validated)
# P2 Cyber OT SOC - Current State

## Current Development Phase
**Phase 2E.4 (Cross-Source Regression & Data Integrity)** - BLOCKER FIXES IMPLEMENTED

## Recent Accomplishments
- **Blocker 1 Fix (Storage Loss):** Replaced arbitrary 2-second timeout loop with natural backpressure (lifecycle context) and a single global bounded shutdown drain deadline.
- **Blocker 2 Fix (`event_id` Ownership):** Verified strict provenance of `event_id` in collectors and enforced invariant via storage tests.
# Current State

- **Current Git Branch**: `feature/phase-2d-filesystem-telemetry`
- **Current Commit Hash**: `HEAD`

## Phase Status
- **Phase 2A (Foundation)**: COMPLETE
- **Phase 2B (Agent Storage & Integrity)**: COMPLETE / FROZEN
- **Phase 2C.0 (Process Telemetry Contract)**: COMPLETE
- **Phase 2C.1 (Process Lifecycle Engine)**: COMPLETE
- **Phase 2C.2 (Linux Process Collector)**: COMPLETE
- **Phase 2C.3 (Windows Collector)**: COMPLETE (Windows snapshot/reconciliation)
- **Phase 2C.4**: COMPLETE / CLOSED (Cross-Platform Validation - Fixed non-determinism, temporary unobservability identity loss, and Linux nanosecond precision defects. Validation gates passed on both Windows and Linux).
- **Phase 2C.5-A (Endpoint Runtime Wiring & Lifecycle Synchronization)**: COMPLETE (Synchronized shutdown sequence and pipeline draining to prevent race conditions).
- **Phase 2C.5-B/C (Integration tests & Next steps)**: COMPLETE (Endpoint-to-Server pipeline verified).
- **Phase 2C (Overall)**: COMPLETE
- **Phase 2D (Filesystem Telemetry)**: COMPLETE
- **Phase 2E.1 (Inventory Collector)**: COMPLETE
- **Phase 2E.2 (Network Collector)**: COMPLETE
- **Phase 2E.3.1 (Windows USB)**: COMPLETE (Physical Win32 events captured via real-host testing)
- **Phase 2E.3.2 (Linux USB)**: COMPLETE (Real-Host Validated)
# P2 Cyber OT SOC - Current State

## Current Development Phase
**Phase 2E.4 (Cross-Source Regression & Data Integrity)** - BLOCKER FIXES IMPLEMENTED

## Recent Accomplishments
- **Blocker 1 Fix (Storage Loss):** Replaced arbitrary 2-second timeout loop with natural backpressure (lifecycle context) and a single global bounded shutdown drain deadline.
- **Blocker 2 Fix (`event_id` Ownership):** Verified strict provenance of `event_id` in collectors and enforced invariant via storage tests.

## Known Issues & Blockers
- **Race Detector Toolchain:** `go test -race ./...` currently fails on the Windows CGO environment (`cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`).
- **Linux USB Collector**: Verified on real host. Cannot be automatically validated in standard CI without a hardware-in-the-loop Linux VM.
- No remote deployment E2E performed.

### Phase 2E.4 Implementation Record

### 1. SHUTDOWN SEMANTICS
- **Normal Operation:** `db.Store()` uses the endpoint lifecycle context. Storage contention naturally applies bounded backpressure.
- **Shutdown:** When shutdown begins, **ONE GLOBAL 10-second drain deadline** is established. All remaining `centralEvents` are drained using contexts derived from that single deadline (the deadline is *not* reset for each event). Once the global deadline expires, remaining events enter explicit observable terminal handling (`FAILED_BEFORE_COMMIT`). 
- **Rationale:** 10 seconds is chosen as a provisional deterministic global drain bound. This balances flushing up to 200 buffered events while preventing unbounded shutdown hangs. This bound will be formally validated in Phase 2E.4 regression testing.

### 2. STORAGE OUTCOME CONTRACT
- **COMMITTED:** SQLite successfully committed. Retry is unnecessary. Endpoint wakes up forwarder and proceeds normally.
- **FAILED_BEFORE_COMMIT:** SQLite affirmatively aborted before durability. If the cause is retryable, retry MAY be safe using the SAME `event_id`, subject to policy and context. If the cause is non-retryable (e.g., quota full, context canceled), it requires explicit terminal handling. Endpoint logs the explicit failure and drops the event.
- **UNCERTAIN:** The commit outcome is ambiguous (e.g., driver IO error during `tx.Commit()`). The transaction might have succeeded. Retry is safe because `event_id` idempotency guarantees no duplicate telemetry. Endpoint performs bounded retries.

### 3. UNCERTAIN COMMIT TESTING
- **Test Limitation:** True post-commit ambiguity is not deterministically reproducible with the current SQLite driver abstraction. The `testFaultInjectCommit` hook triggers immediately before `tx.Commit()`, which technically simulates a `FAILED_BEFORE_COMMIT` rather than a true ambiguous outcome.
- **Verification:** The focused tests instead explicitly prove the key idempotency invariant: retrying the SAME `event_id` cannot create duplicate durable telemetry.

### 4. UNCERTAIN RETRY EXHAUSTION
- **Flow:** UNCERTAIN → bounded retry budget (3 attempts) → if still uncertain: recovery via SQLite DLQ → if DLQ fails: best-effort emergency spill → if emergency spill also fails: `log.Fatalf()`.
- **Semantics:** The same original `event_id` is preserved throughout. The primary record may already exist because the outcome was uncertain. The DLQ recovery mechanism must not fabricate a new telemetry identity.
- **Emergency Spill:** Local emergency spill is BEST-EFFORT, NOT a guaranteed persistence mechanism (can fail due to permissions, full disk, IO).

### 5. DLQ SEMANTICS
- **Meaning for Uncertain Commits:** If an uncertain commit falls back to the DLQ, the DLQ record is strictly **recovery metadata** keyed by the original `event_id`, not a second logical telemetry event with a new identity.

### 6. EVENT_ID OWNERSHIP
- **Provenance:** Process, Filesystem, Inventory, Network, USB Windows, and USB Linux all assign `event_id` exactly once before `centralEvents`.
- **Invariant:** `collector event_id = CanonicalEvent event_id = SQLite event_id = retry event_id`. `event_id != seq_no`. SQLite rejects empty `event_id` and preserves existing `event_id` unchanged.

### 7. INTEGRATION TESTS
- Integration tests exercise the actual ingestion behavior using cancellable test contexts and assert observable outcomes. They do not duplicate production branching logic.
- Tests verify: successful persistence, failed-before-commit handling, uncertain retry behavior, `event_id` preservation, no duplicate durable event, and bounded shutdown behavior.

### 8. QUOTA / PRUNING
- **Distinction:** Intentional quota-based telemetry pruning (dropping lower-severity events like `DEBUG` or `INFO` when space is low) is an expected operational bound and is entirely distinct from actual unexpected storage failure.

### 9. CENTRAL EVENTS
- Capacity remains strictly **200** (unchanged).

### 10. ACCEPTANCE CRITERIA
- **AC-1:** No event silently disappears merely because the old arbitrary 2-second Store timeout elapsed.
- **AC-2:** Every active collector assigns `event_id` before persistence.
- **AC-3:** Existing `event_id` is preserved through storage and retry.
- **AC-4:** Shutdown uses ONE global bounded drain deadline.
- **AC-5:** Events remaining after the global shutdown deadline enter an explicit, observable terminal state; no event is silently discarded.
- **AC-6:** Uncertain recovery never fabricates a new event identity.

## Current Development Phase
**Phase 3.1B (Passive Industrial Protocol Identification & Modbus/TCP Foundation)** - FOUNDATION VALIDATED (IN PROGRESS)

- **Current Git Branch**: `feature/phase-3.1b-modbus-passive-decoder`
- **Current Commit Hash**: `HEAD`

## Phase Status
- **Phase 1**: COMPLETE
- **Phase 2 (Overall)**: COMPLETE / FROZEN
- **Phase 2E.4 (Cross-Source Regression & Data Integrity)**: COMPLETE
- **Phase 3.1A (Passive Capture & Observation Foundation)**: COMPLETE / FROZEN
- **Phase 3.1B (Modbus/TCP Foundation & Evidence Identification)**: IN PROGRESS (FOUNDATION VALIDATED)
- **Phase 3.1C+ (Additional OT Decoders, Asset Graph, Discovery)**: PENDING / NOT STARTED

## Phase 3.1A Implementation Record
- Status: COMPLETE / FROZEN
- Boundary: Replaceable `CaptureAdapter` and offline `ReplayAdapter`.
- Decoders: Ethernet II, 802.1Q VLAN, ARP, IPv4, TCP, UDP, ICMP.
- Verification: 19 unit tests, full pipeline integration test.

## Phase 3.1B Implementation Record

### 1. Purpose & Scope
Phase 3.1B establishes evidence-based industrial protocol decoding for Modbus/TCP on top of the Phase 3.1A passive observation foundation:
- **MBAP Parsing:** Decodes Transaction ID, Protocol ID, Length, Unit ID, Function Code.
- **Evidence-Based Confidence:** Elevates from `ConfidenceInferred` (port 502 candidate) to `ConfidenceKnown` only when valid MBAP framing (Protocol ID == 0, Length >= 2) is observed.
- **Function Code Handling:** Recognizes standard Modbus function codes (FC 1, 2, 3, 4, 5, 6, 15, 16) and exception responses (FC >= 0x80) without inferring asset roles.
- **Direction Heuristic:** Infers intra-packet direction (`request`, `response`, or `unknown`) based on port orientation and exception bit without maintaining a global stateful transaction map.
- **Explicit Invariant:** `REQUEST/RESPONSE CORRELATION = FUTURE PHASE`.

### 2. Non-Goals (Strictly Out of Scope for 3.1B)
- Active Modbus requests, polling, or PLC state querying.
- Protocol fuzzing or packet crafting/injection.
- Port scanning or active host probing.
- Decoders for DNP3, EtherNet/IP, or S7 (extension points remain inferred).
- Asset graph or device inventory persistence (Purdue level, PLC identity).
- Automated or disruptive OT response.

### 3. Passive Safety Invariant
- STRICTLY PASSIVE: Zero network transmission, zero socket writing, zero probing.
- Decoder operates exclusively on already-observed byte slices.

### 4. False-Positive Safety & Confidence Semantics
- TCP 502 with non-Modbus payload (e.g. HTTP GET or random bytes) strictly fails MBAP validation, emits `MODBUS_INVALID_MBAP`, and NEVER yields `ConfidenceKnown`.
- Valid Modbus traffic produces protocol evidence but **NEVER** fabricates an asset role or labels an endpoint as a PLC.

### 5. Malformed Input & Bounded Resource Model
- **Bounds-Checked Parsing:** All slice offsets and lengths are strictly bounds-checked; parser never panics on arbitrary or malformed input (tested via fuzz inputs).
- **Quality Flags:** Malformed frames emit explicit quality flags (`MODBUS_INVALID_MBAP`, `MODBUS_TRUNCATED_PAYLOAD`, `MODBUS_UNSUPPORTED_FUNCTION`).
- **Memory Boundedness:** No heap byte slices or raw packet payloads are stored in `CanonicalEvent.Metadata`.
- **Zero Stateful Leaks:** No transaction correlation tables or goroutines per packet.

### 6. Event Integration & AC Verification
- `event_id` is preserved end-to-end and generated strictly once by the owning collector.
- Meets AC-1 through AC-6. All unit tests, integration tests, and repository tests pass.

## Next Approved Task
- Phase 3.1C: Passive DNP3 / EtherNet-IP Identification & Framing Decoders (PENDING)


