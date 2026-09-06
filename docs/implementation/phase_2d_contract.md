# Phase 2D Filesystem Telemetry Contract

This document formally captures the approved Phase 2D contract semantics and acceptance criteria, ensuring future development adheres to the established architectural constraints.

## 1. Backpressure and Loss Prevention
- **No Silent Loss**: The filesystem collector must *never* silently discard an observed filesystem event.
- **Observable Failure**: The collector must not use arbitrary timeouts (e.g. 50ms) to drop events. If the downstream pipeline (SQLite storage) is stalled, the collector must block and safely propagate backpressure.
- **Health Logging**: If blocked for more than 1 second, the collector emits an observable health failure log (`[ERROR] Filesystem collector output channel blocked...`) but **continues to block indefinitely**. It must not establish an unbounded internal queue.
- **Pipeline Reuse**: The collector directly feeds the existing SQLite durable ingestion path without introducing redundant buffering layers.

## 2. Event Sequencing (`seq_no`)
- The filesystem collector is strictly prohibited from assigning the `seq_no`.
- `seq_no` semantics remain exclusively owned and assigned by the durable SQLite storage layer during insertion to preserve exact ordering identity.

## 3. `FILE_RENAME` Semantics
- **New Path only**: For `FILE_RENAME` events, the `file_path` metadata field contains the new/destination path.
- **No Fabricated Old Paths**: `old_file_path` may only be included if the underlying OS explicitly and atomically provides it. 
- Because `fsnotify` does not reliably guarantee atomic cross-platform rename context, `old_file_path` is actively omitted rather than dangerously fabricated. A filesystem event represents an *observed OS notification*, not necessarily a fully reconstructed user operation.

## 4. Dependencies
- **`fsnotify`**: `github.com/fsnotify/fsnotify` is utilized as an implementation dependency, directly declared in `go.mod`. It is treated as an isolated implementation detail, not a hard architectural requirement that binds the rest of the agent framework.

## 5. Phase 2C.5-C Regression Boundary
- The legacy process-telemetry integration tests (`agent/internal/integration/endpoint_pipeline_test.go`) must remain entirely unaware of Phase 2D logic.
- Phase 2D verification is wholly encapsulated within its own dedicated suite (`filesystem_pipeline_test.go`), maintaining the integrity of closed Phase 2C acceptance evidence.
