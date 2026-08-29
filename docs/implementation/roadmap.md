# Implementation Roadmap

Implementation goal: reach a genuinely working RedCyberFox Alpha using only free/open-source software and existing development hardware.

## Phase 0 — Bootstrap: IN PROGRESS

- Git repository, project structure, coding standards, Docker Compose, local Linux environment, issue tracking, basic CI.

## Phase 1 — Vertical Slice: READY

- One simple agent event -> HTTPS API -> queue -> worker -> PostgreSQL -> dashboard alert.
- Free stack: Go, PostgreSQL, Valkey, React
- Exit / proof: One test endpoint produces a visible alert end-to-end.

## Future Phases
- **Phase 2:** Endpoint Alpha
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
