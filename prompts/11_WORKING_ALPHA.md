# Phase 10 — Working Alpha Integration

## Goal
Integrate the endpoint, OT sensor, context, correlation, alerting, offline behavior, and low-spec profile.

## Alpha acceptance
1. Agent installs on representative older Windows/Linux host.
2. Agent detects one real local security event.
3. Agent stores events during WAN outage.
4. Reconnect reconciles sequence ranges without duplication.
5. Central API receives/persists events.
6. Dashboard displays alerts/incidents.
7. Passive OT sensor observes simulated industrial traffic.
8. Asset graph records devices/normal peers.
9. At least one OT protocol anomaly is detected.
10. Endpoint + OT events correlate into one incident.
11. Risk score is explainable from stored factors.
12. Free notification works.
13. Agent/sensor failure does not affect production traffic.
14. Low-spec/resource-saver tests pass.
15. One complete adversary scenario is repeatable after clean reset.

## Killer demonstration
USB -> unknown binary -> new process -> first-seen peer -> engineering workstation -> PLC -> Modbus write burst -> baseline anomaly -> zone/conduit finding -> IOC match -> correlated incident -> explainable risk -> R3 approval -> notification.

Then repeat with WAN disconnected and demonstrate local detection + later reconciliation.

## Deferred
- ClickHouse at scale
- production-depth MISP
- HA clusters
- multi-region
- full TUF roles
- TPM identity
- huge protocol catalogues
- complex automated OT response
- LLM analyst features

## Exit criterion
Complete attack-to-incident-to-notification path works repeatedly and is reproducible.
