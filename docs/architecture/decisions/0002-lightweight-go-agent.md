# 0002 - Lightweight Go Agent

## Status
Accepted

## Context
Endpoints in target environments are often legacy Windows or Linux systems with limited CPU and RAM (e.g., 5-15 year old HMIs or engineering workstations).

## Decision
The endpoint agent will be written in Go.

## Rationale
Go compiles to a static, cross-platform binary with a minimal footprint, low memory usage, and no external runtime dependencies (unlike Java or Python). It supports concurrent operations efficiently.

## Alternatives considered
- **Python**: Rejected due to runtime dependency overhead and memory footprint.
- **Rust**: Considered, but Go offers a better ecosystem for rapid cross-platform development of the required OS-level telemetry while still meeting performance constraints.
- **C/C++**: Rejected due to memory safety risks and slower development velocity.

## Consequences
- Requires careful management of Go routines to avoid CPU spikes.
- We must implement adaptive resource governors to strictly bound resource usage.

## Architectural invariants
- Edge components remain lightweight.
- Monitoring failure cannot become production failure (e.g., causing a host to freeze).
