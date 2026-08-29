# Phase 4 — Rural / Low-Spec Hardening

Make the agent suitable for the real customer target.

## Resource governor
### NORMAL
- full lightweight monitoring
- scans during idle time

### RESOURCE-SAVER
- defer deep scans
- reduce low-value telemetry
- increase batching
- increase deduplication
- preserve P0/P1 telemetry

### EMERGENCY
- preserve critical security telemetry and health
- disable expensive scheduled work
- return automatically after pressure clears

## Measure
- CPU
- RAM
- disk space
- disk I/O where practical
- network state
- battery where relevant

## Storage protection
- quotas
- priority retention
- aggregation
- critical-event preservation
- cleanup

## WAN efficiency
- compressed batches
- adaptive batch sizes
- deduplication
- priority queues
- store-and-forward

## Benchmark
Use representative old/low-spec profiles. Record CPU, RAM, disk I/O, event loss, queue age, recovery time, battery impact where relevant.

Do not invent universal minimum hardware requirements.

## Exit criterion
Agent remains usable under CPU/RAM/disk/WAN stress while preserving critical telemetry.
