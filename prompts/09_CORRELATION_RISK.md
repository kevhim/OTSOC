# Phase 8 — Correlation + Risk

## Baselines
Start with explainable statistics:
- peer sets
- first-seen peers
- time of day
- maintenance windows
- protocol mix
- read/write frequency
- event volume
- direction
- role behavior

Do not introduce ML without a measured requirement.

## Correlation
Support:
- temporal correlation
- endpoint + OT fusion
- asset/zone/protocol enrichment
- threat-intel enrichment when available
- multi-stage attack-chain grouping
- duplicate suppression

Example:
USB -> unknown binary -> new process -> new OT peer -> Modbus write burst -> zone violation
becomes one incident.

## Risk
Factors:
- base severity
- asset impact
- safety weight
- segmentation violation
- confidence
- novelty
- temporal correlation
- threat intelligence

Every score must expose contributors and be reproducible from stored evidence.

## Incident lifecycle
new -> triaged -> investigating -> contained -> resolved -> closed

## Response
R0 inform
R1 analyst review
R2 controlled IT response
R3 OT-sensitive response requiring human approval

No automatic disruptive OT response.

## Exit criterion
A multi-event attack chain becomes one explainable incident with reproducible risk.
