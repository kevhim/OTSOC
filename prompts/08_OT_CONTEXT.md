# Phase 7 — OT Context / Purdue / IEC 62443

## Asset model
Support:
- identity
- type
- vendor/model/serial where available
- IP/MAC/interfaces
- role
- criticality
- safety_critical
- process association
- visibility confidence

## Security graph
Represent in PostgreSQL:
- asset relationships
- observed peers
- zones
- conduits
- processes

Do not add a graph DB.

## Zones/conduits
Support:
- zone
- conduit
- direction
- allowed protocols/assets
- review date
- exceptions
- maintenance windows
- intended vs observed relationships

## Purdue
Represent alignment separately from observed topology:
L0, L1, L2, L3, L3.5/IDMZ, L4, L5.
Safety zone extensions are internal conventions, not official Purdue level names.

## IEC 62443
Support evidence/mapping around 3-2, 3-3, 4-1, 4-2.
Never claim RedCyberFox grants a customer a Security Level or certification.

## Structural detections
- level skip
- safety-zone ingress
- unsanctioned conduit
- reverse direction
- IDMZ bypass
- dual-homed asset
- role inversion

## Visibility
Track endpoint/network/protocol/serial/log visibility.
Ethernet sensing does not equal serial visibility.

## Exit criterion
Lab demonstrates asset discovery, peer relationships, intended-vs-observed comparison, segmentation findings, and visibility gaps.
