# Roadmap Context

Phase 2A - COMPLETE
Phase 2B - COMPLETE / FROZEN
Phase 2C.0 - COMPLETE
Phase 2C.1 - COMPLETE
Phase 2C.2 - COMPLETE
Phase 2C.3 - COMPLETE
Phase 2C (Overall) - NOT COMPLETE

## Notes
- Phase 2A provides the robust Agent-Server core.
- Phase 2B implements resilient disk buffering (Storage) and Identity mechanics.
- Phase 2C.0 defines the generalized Process Telemetry contract (`CanonicalEvent`, Categories).
- Phase 2C.1 implements the `LifecycleEngine` to deduplicate snapshots into discrete START/EXIT events.
- Phase 2C.2 implements the Linux `ProcessCollector` (via `/proc`), extracting process metadata and feeding it to the Lifecycle Engine.
- Phase 2C.3 implements the Windows `ProcessCollector` (via Toolhelp32 snapshot/reconciliation), feeding the Lifecycle Engine.
