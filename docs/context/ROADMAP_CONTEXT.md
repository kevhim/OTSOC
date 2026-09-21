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

## Out-of-Sequence Preparatory OT Work
- **Phase 3.1A / Phase 3.1B**: Recorded as "out-of-sequence preparatory OT sensing work already present in the repository". Preserved without extension. Does not automatically alter the official milestone sequence.

## Official Roadmap Workflow
- **Phase 3**: Detection Core (Official next phase milestone)
- **Phase 4**: Rural Hardening (Includes Resource Governor; no Phase 2F/2G/2H)
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
