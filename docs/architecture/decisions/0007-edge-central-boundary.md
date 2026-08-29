# 0007 - Edge / Central Boundary

## Status
Accepted

## Context
RedCyberFox targets rural, small, and regional sites with legacy/low-spec devices and intermittent Internet connectivity. Heavy infrastructure cannot be deployed to these sites.

## Decision
RedCyberFox follows an edge-light / central-heavy architecture.

**Edge responsibilities:**
- telemetry collection
- lightweight local detection
- local normalization
- local buffering
- health reporting
- offline operation
- low-spec resource adaptation

**Central responsibilities:**
- heavy cross-device correlation
- historical analytics
- centralized threat intelligence
- long-term telemetry
- fleet management
- dashboard services
- reporting

## Rationale
Old customer machines cannot run heavy databases or threat intelligence platforms. The edge must focus purely on visibility and survivability. Heavy computation must remain centralized where resources are available.

## Alternatives considered
- **Full local SIEM**: Rejected. Impossible to run on 15-year-old engineering workstations.
- **Cloud-only (No local detection)**: Rejected. OT environments require local visibility and detection even when the WAN is severed to protect physical processes in real time.

## Consequences
- Requires store-and-forward synchronization over the WAN.
- Internet is a synchronization path, not a prerequisite for local security.

## Architectural invariants
- Edge components remain lightweight.
- Local detection works without WAN.
- This boundary must not be silently changed by future implementations.
