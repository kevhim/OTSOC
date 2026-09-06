# Implementation Roadmap

This roadmap tracks the incremental delivery of the RedCyberFox OT SOC platform.

## Phase 0 — Bootstrap: COMPLETE

- Git repository, project structure, coding standards, Docker Compose, local Linux environment, issue tracking, basic CI.

## Phase 1 — Vertical Slice: COMPLETE

- One simple agent event -> HTTPS API -> queue -> worker -> PostgreSQL -> dashboard alert.
- Free stack: Go, PostgreSQL, Valkey, React
- Exit / proof: One test endpoint produces a visible alert end-to-end.

## Phase 2 — Endpoint Alpha

2A — Agent Foundation: COMPLETE
2B — SQLite + Offline Sync: COMPLETE
2C — Process Telemetry: COMPLETE
2D — Filesystem Telemetry: OPEN

## Future Phases
- **Phase 3:** Detection Core
- **Phase 4:** Rural Hardening
- **Phase 5:** OT Lab
- **Phase 6:** Passive OT Sensor
- **Phase 7:** OT Context
- **Phase 8:** Correlation + Risk
- **Phase 9:** Security Hardening
- **Phase 10:** Working Alpha
- **Phase 11:** Intelligence / Search
- **Phase 12:** Resilience / Pilot
- **Phase 13:** Sector Expansion

## GitHub Milestones / Phase Mapping

GitHub Milestones should map directly to the Phases defined above:

- Phase 0 — Bootstrap
- Phase 1 — Vertical Slice
- Phase 2 — Endpoint Alpha
- Phase 3 — Detection Core
- Phase 4 — Rural Hardening
- Phase 5 — OT Lab
- Phase 6 — Passive OT Sensor
- Phase 7 — OT Context
- Phase 8 — Correlation + Risk
- Phase 9 — Security Hardening
- Phase 10 — Working Alpha
- Phase 11 — Intelligence / Search
- Phase 12 — Resilience / Pilot
- Phase 13 — Sector Expansion

## Git Tagging Policy

A tag may be created only when:
- implementation for that milestone is complete
- required tests pass
- acceptance criteria pass
- working tree is clean
- no known critical security issue remains for that milestone

A tag is a known-good checkpoint, not just a date marker.
