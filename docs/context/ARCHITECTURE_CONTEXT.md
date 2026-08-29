# Architecture Context

## Architectural Model

`	ext
Endpoint / Edge
    ↓
Local durability
    ↓
Synchronization
    ↓
Central ingestion
    ↓
Event bus
    ↓
Processing
    ↓
Storage
    ↓
Correlation / risk
    ↓
Dashboard / response
`

## Edge / Central Boundary

### Edge
- collection
- lightweight local processing
- buffering
- offline operation
- health/resource management

### Central
- heavy processing
- historical analysis
- correlation
- intelligence
- fleet management
- dashboards

## Operational Rules
- **Passive OT principle**: Monitoring failure must never become production failure.
- **Control action**: PLC/SIS control is not a default automated action.
- **Approval**: R3 OT-sensitive actions require human approval.

*For authoritative constraints, see the ADRs and V6 architecture document.*
