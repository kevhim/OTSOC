# RedCyberFox Architecture Rules

1. Edge components remain lightweight.
2. Local detection works without WAN connectivity.
3. OT monitoring is passive-first.
4. Monitoring failure cannot become production failure.
5. PLC/SIS control is outside the default security platform role.
6. R3 OT-sensitive actions require human approval.
7. SQLite is the edge durability layer.
8. PostgreSQL is the authoritative system-of-record for state.
9. Valkey is accessed through EventBus.
10. Security telemetry uses the canonical event schema.
11. Critical events are never silently sampled away.
12. Blind spots must be represented explicitly.
13. No paid service is required for core Alpha functionality.
14. Do not add infrastructure without a demonstrated requirement.
15. Do not claim certification or blanket protection.
