# Roadmap Context

Phase 2A - COMPLETE
Phase 2B - COMPLETE / FROZEN
Phase 2C.0 - COMPLETE
Phase 2C.1 - COMPLETE
Phase 2C.2 - COMPLETE
Phase 2C.3 - COMPLETE
Phase 2C.4 - COMPLETE
Phase 2C (Overall) - COMPLETE
Phase 2D - COMPLETE
Phase 2E.1 (Inventory) - COMPLETE
Phase 2E.2 (Network) - COMPLETE
Phase 2E.3.1 (Windows USB) - COMPLETE
Phase 2E.3.2 (Linux USB) - COMPLETE
Phase 2E.4 (Cross-source) - COMPLETE / FROZEN
Phase 3.1A (Passive Observation Foundation) - COMPLETE / FROZEN
Phase 3.1B (Modbus/TCP Passive Decoder) - IN PROGRESS (Foundation Validated)
Phase 3.1C+ (Passive Protocol Decoders & Asset Discovery) - NOT STARTED
Phase 3 (Overall) - IN PROGRESS

## Notes
- Phase 2A provides the robust Agent-Server core.
- Phase 2B implements resilient disk buffering (Storage) and Identity mechanics.
- Phase 2C.0 defines the generalized Process Telemetry contract (`CanonicalEvent`, Categories).
- Phase 2C.1 implements the `LifecycleEngine` to deduplicate snapshots into discrete START/EXIT events.
- Phase 2C.2 implements the Linux `ProcessCollector` (via `/proc`), extracting process metadata and feeding it to the Lifecycle Engine.
- Phase 2C.3 implements the Windows `ProcessCollector` (via Toolhelp32 snapshot/reconciliation), feeding the Lifecycle Engine.
- Phase 2C.4 validates cross-platform semantic equivalence and implements real-host integration constraints.
- Phase 2E.4 validates broad cross-source regression, SQLite WAL backpressure, bounded drain, and strict event_id provenance.
- Phase 3.1A establishes the strictly passive network capture boundary, deterministic replay adapter, evidence-preserving normalizer, protocol hint extension points, and CanonicalEvent edge pipeline integration.
- Phase 3.1B implements evidence-based passive Modbus/TCP decoding, MBAP validation, standard function code recognition, intra-packet direction inference, and strict false-positive safety without asset role fabrication.


