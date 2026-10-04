# Phase 4 Gate-0 Contract: Resource Governor

This document defines the contract and scope for Phase 4 (Resource Governor) of the OT-SOC agent, adhering strictly to the frozen Master Architecture and ensuring safe, bounded operation in constrained OT and low-bandwidth environments.

## 1. Resource States and Signals
The agent governs behavior based on two distinct sets of pressure signals: **Host Resource Pressure** and **Network Pressure**. 

### Host Resource States
1. **NORMAL**: System is operating within healthy margins. Full data collection, standard transmission intervals, and standard retention are applied.
2. **RESOURCE-SAVER**: Thresholds for CPU, memory, or disk are elevated. The agent extends polling intervals for non-critical collectors, prioritizes aggregation over deletion, applies strict priority sampling for low-priority events, and limits new memory allocations.
3. **EMERGENCY**: Critical host resource limits are breached, threatening the stability of the OT asset. The agent immediately suspends non-critical collections, limits data flow to P0/P1 and heartbeat status, drops/aggregates lower-priority queue items, and minimizes CPU/memory footprint until the host stabilizes.

### Network State
Network state (offline, high-latency, low-bandwidth) is evaluated independently from the Host Resource State. A site can be in NORMAL host resource state but offline, or in RESOURCE-SAVER while online.

## 2. State Transition Inputs
State transitions are triggered by observable, measurable signals.
- **CPU Utilization**: Measured as a moving average. *Threshold: PROVISIONAL.*
- **Memory (RAM) Usage**: Total process physical memory (RSS). *Threshold: PROVISIONAL.*
- **Local Storage Budget**: Bounded budget covering the database and associated persistence files. *Threshold: PROVISIONAL.*
- **Queue Saturation (Backpressure)**: Fill percentage of internal bounded memory queues. *Threshold: PROVISIONAL.*
- **Network Pressure**: Latency or consecutive send failures indicating offline status or saturation.

**Important - Signal Validity Rule:**
The caller MUST NOT pass unavailable measurements as `0`. A `0` value architecturally means "zero usage" or "perfectly healthy," which is dangerous if the collector simply failed to obtain a measurement. In the next slice (Phase 4.2), signal presence and measurement validity will be explicitly designed and represented (e.g., via `NaN`, pointer indirection, or validity booleans) to prevent silent fallback to healthy states during collector failure.

## 3. Priority Policy
All generated events must be tagged with a priority level that dictates retention, sampling, and transmission order:
- **P0**: Active safety/control manipulation or equivalent catastrophic-risk telemetry.
- **P1**: Confirmed compromise / critical asset events.
- **P2**: Strong anomaly / policy violation.
- **P3**: Suspicious behavior / lower-confidence activity.
- **P4**: Routine telemetry / diagnostics.

## 4. Retention and Sampling Rules
- **P0**: Never sampled and never intentionally dropped by the Resource Governor. When storage is completely exhausted, the governor must enter a critical failure state rather than silently evict P0 telemetry.
- **P1**: Never intentionally sampled or discarded by the Resource Governor. Guaranteed retention in NORMAL and RESOURCE-SAVER modes. If storage becomes unavailable, the system must preserve existing P1 data and enter an observable critical-storage state rather than silently evict P1.
- **P2**: Sampled, batched, or aggregated in RESOURCE-SAVER mode. Aggregation is always preferred over blindly deleting telemetry while preserving metadata (first_seen, last_seen, count, evidence).
- **P3 & P4**: Aggressively aggregated or intentionally dropped in RESOURCE-SAVER and EMERGENCY modes to preserve resources for higher-priority events.

## 5. Queue & Backpressure Invariants
- **Bounded Memory**: Memory queues must be strictly bounded. Unbounded buffering is explicitly prohibited.
- **No Silent Loss**: Every intentional loss/reduction must be observable, reporting:
  - `reason`
  - `priority`
  - `time window`
  - `affected source/collector`
- **Durable-Before-Send**: High-priority events (P0/P1) must be written to the local database before transmission.

## 6. Storage Quota Behavior
- **The Resource Governor maintains a bounded local-storage budget with sufficient safety headroom to prevent uncontrolled host-disk exhaustion.**
- The exact accounting mechanisms (database pages, WAL, associated persistence files, reserved safety headroom) will be defined during Phase 4 implementation.
- If the storage budget is approached, the system triggers automatic aggregation or reduction of the oldest P3/P4 events to reclaim space.
- The agent must never exhaust the host's physical disk space.

## 7. WAN / Low-Bandwidth / Offline Behavior
- **P0/P1 Transmission Guarantee**: P0 and P1 must always be retained durably and prioritized for forwarding. Transmission occurs when connectivity is available. Offline operation never makes P0/P1 dependent on network availability.
- **Batching & Compression**: Transmission shifts to batching to minimize overhead. Payloads use compression if supported.
- **Priority Retention**: The transmission scheduler dequeues P0 and P1 events first.
- **Store-and-Forward**: Intermittent connectivity relies entirely on local persistent storage. 

## 8. Emergency-Mode Behavior
When entering EMERGENCY mode:
- All active non-essential collection routines are paused or drastically slowed.
- **Safety Recursion Bound**: Emitting the emergency transition telemetry (`ResourceGovernor_Emergency_Triggered`) must be bounded, rate-limited/idempotent, and must not recursively trigger additional governor transitions.

## 9. Recovery & Hysteresis Requirements
- Transitions back to lower severity states must employ **hysteresis** (e.g., dropping from 90% to 75% for 60 seconds). *All hysteresis values are strictly PROVISIONAL engineering targets.*
- Recovery is deterministic and testable.

## 10. Governor Work & Safety Invariants
- **Governor Execution Bounding**: The Resource Governor must execute in bounded time, must not perform expensive scans on every telemetry event, and must not allocate unbounded state proportional to event volume.
- **Safety Invariant 1**: The agent must never permit unbounded memory growth. When configured resource protections are insufficient, it must enter a bounded fail-safe state rather than continue allocating without limit.
- **Safety Invariant 2**: The governor cannot take automatic disruptive actions against the OT host (e.g., killing host processes or disabling host interfaces).

## 11. Metrics Required to Validate the Governor
Observable metrics must include:
- `agent_cpu_percent` (moving average)
- `agent_rss_bytes` (process memory)
- `queue_depth_count`
- `queue_saturation_percent`
- `queue_capacity_count`
- `queue_backpressure_events_total`
- `queue_oldest_event_age_seconds`
- `disk_storage_used_bytes`
- `disk_write_pressure_bytes_per_sec`
- `events_aggregated_total` (labels: `reason`, `priority`, `source`)
- `events_dropped_total` (labels: `reason`, `priority`, `source`)
- `governor_state` (NORMAL/SAVER/EMERGENCY)
- `time_in_emergency_mode_seconds`

## 12. Acceptance Tests
1. **CPU Pressure Test**: Simulate high CPU load. Verify the agent transitions to EMERGENCY mode with correct hysteresis and rate-limited emergency telemetry.
2. **Memory Bounding Test**: Attempt to ingest a massive burst of events. Measure and verify queue depth, process memory/RSS, queue age, dropped/aggregated counts, and recovery after pressure.
3. **Storage Budget Test**: Reduce the storage budget. Verify that once full, older low-priority events are aggregated/reduced to make room, and the total storage never exhausts host disk limits. Verify P0/P1 are not evicted, entering an observable critical-storage state if no room remains.
4. **Hysteresis Test**: Rapidly fluctuate simulated CPU usage above and below the threshold. Verify the state does not flap continuously.
5. **Network Partition Test**: Simulate offline status using a deterministic injected clock/simulated elapsed time (not `time.Sleep`). Verify P0/P1 events are durably stored on disk, memory does not leak over the simulated period, and transmission resumes prioritized correctly upon reconnection.

## 13. Frozen Architectural Invariants
1. No unbounded memory.
2. No silent event loss.
3. P0/P1 are preserved durably.
4. P0 is never intentionally evicted by the governor.
5. Offline operation never makes P0/P1 dependent on network availability.
6. Low-priority reduction is observable and accounted for.
7. Aggregation is preferred to deletion where practical.
8. Resource pressure and network connectivity are distinct signals.
9. Governor transitions use hysteresis.
10. Governor work itself is bounded.
11. Emergency behavior cannot recursively amplify resource pressure.
12. Thresholds remain benchmark-derived until Phase 4 measurements exist.
13. No automatic disruptive OT action.
14. Recovery is deterministic and testable.
