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
- **Phase 2E.3.2 (Linux USB)**: NOT STARTED
- **Phase 2E.4 (Cross-source)**: NOT STARTED
- **Phase 3**: NOT STARTED

## Completed Phase 2B Features
- SQLite Durability for events (atomic inserts, offline queuing).
- Legacy Identity Migration.
- Forwarder with offline buffering, exponential backoff, DLQ handling, and proper 202/400 processing.
- Identity cleanup and fallback handling.

## Known Limitations
- End-to-end integration with the central server relies on mocks in current test suites.
- Serialized SQLite access acts as a bottleneck under extreme concurrent load, by design for Alpha stability.
- Storage quota is configurable at storage initialization, but a complete dynamic runtime configuration/policy system is not yet implemented.
- **Windows Process Collector**: `CommandLine` is unavailable in this phase (Phase 2C.3) because it requires brittle PEB reading.
- **Windows Process Collector**: No ETW (real-time event-driven) implementation yet; strictly snapshot/reconciliation based.
- No real-host validation performed for network and inventory beyond unit/integration tests.
- **Linux USB Collector**: Linux USB not implemented and not validated (NOT VERIFIED).
- No remote deployment E2E performed.

## Next Approved Task
- Pending Assignment

## Forbidden Components (DO NOT implement until explicitly started)
- Linux USB Collector (Do NOT implement Linux USB)
- Phase 2E.4 (Do NOT start 2E.4)
- Phase 3 (Do NOT start Phase 3)
- Governor
- Service packaging
- Detection engine
