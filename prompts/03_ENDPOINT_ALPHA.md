# Phase 2 — Endpoint Alpha

Build the lightweight Go endpoint agent for older Windows/Linux hosts.

## Core functions
- process lifecycle telemetry
- file activity
- host network connection metadata
- removable-media/USB signals where supported
- software inventory
- SQLite/WAL
- sync queue
- heartbeat
- local rule cache
- monotonic per-device sequence numbers
- health telemetry
- resource governor
- batching and compression

Keep Windows/Linux collectors isolated under platform-specific packages.

## Local data
- events
- sync_queue
- dead_letter_queue
- agent_config
- rules_cache
- asset_snapshot
- baseline_state

## Offline requirements
- local collection continues without WAN
- local detections supported by this phase continue
- events persist
- sequence ordering preserved
- retries after reconnect
- queue age/size visible
- critical events are retained

## Resource requirements
NORMAL / RESOURCE-SAVER / EMERGENCY.

Critical telemetry remains active in all modes.

## Tests
- collectors
- SQLite
- retry/queue
- sequence/gap handling
- offline/reconnect
- resource modes

## Exit criterion
A representative older host produces telemetry and survives WAN outage without losing local events.
