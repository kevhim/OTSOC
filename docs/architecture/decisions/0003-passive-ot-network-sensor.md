# 0003 - Passive OT Network Sensor

## Status
Accepted

## Context
Industrial control systems (ICS/OT) involve fragile and critical equipment (PLCs, SIS) where active scanning or inline security appliances can cause physical damage or process interruption.

## Decision
We will use Zeek and Suricata deployed on a dedicated sensor via TAP or SPAN for passive network observation.

## Rationale
Passive observation guarantees that the security monitoring system cannot interfere with or block critical production traffic. Suricata provides signature/app-layer detection, while Zeek provides rich protocol metadata.

## Alternatives considered
- **Inline IPS**: Rejected as it violates the passive-first safety constraint.
- **Active OT Scanning**: Rejected as the default posture due to the risk of knocking legacy devices offline.

## Consequences
- Requires dedicated hardware or VM for the sensor to handle packet parsing without loading legacy endpoints.
- Cannot automatically prevent network attacks (relies on out-of-band response).

## Architectural invariants
- OT monitoring is passive by default.
- No production-path interference.
- Failure cannot affect control traffic.
