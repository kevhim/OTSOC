# Current State

- **Current Git Branch**: `feature/phase-2c-process-telemetry`
- **Current Commit Hash**: `HEAD`

## Phase Status
- **Phase 2A (Foundation)**: COMPLETE
- **Phase 2B (Agent Storage & Integrity)**: COMPLETE / FROZEN
- **Phase 2C.0 (Process Telemetry Contract)**: COMPLETE
- **Phase 2C.1 (Process Lifecycle Engine)**: COMPLETE
- **Phase 2C.2 (Linux Process Collector)**: COMPLETE
- **Phase 2C.3 (Windows Collector)**: COMPLETE (Windows snapshot/reconciliation)
- **Phase 2C.4**: NOT STARTED
- **Phase 2C.5**: NOT STARTED
- **Phase 2C (Overall)**: NOT COMPLETE

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

## Next Approved Task
- Proceed to Phase 2C (if and only if explicitly instructed).

## Forbidden Components (DO NOT implement until 2C or later)
- Process collectors
- Filesystem collectors
- Network collectors
- USB collection
- Inventory
- Governor
- Service packaging
- Detection engine
