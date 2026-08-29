# Edge Agent Context

*IMPORTANT: The real endpoint agent is currently NOT IMPLEMENTED (Not Started). Phase 2 targets this.*

## Intended Responsibilities
- process telemetry
- filesystem telemetry
- network metadata
- USB/removable media
- inventory
- health
- resource governor
- SQLite/WAL (offline queue)
- synchronization

## Constraints
- low CPU
- low RAM
- low disk I/O
- offline operation
- Windows/Linux compatibility
- event-driven OS mechanisms where practical
- no heavy central services on endpoints

## Current State
**NOT STARTED**. Only a synthetic test generator (gent/cmd/synthetic) currently exists.
