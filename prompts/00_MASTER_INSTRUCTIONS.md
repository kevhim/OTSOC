# RedCyberFox — Antigravity Master Instructions

## Source of truth
Before making architectural changes, read:
1. `docs/architecture/architecture.md`
2. `docs/architecture/OT_SOC_Architecture_Master_V6.pdf`
3. `ARCHITECTURE_RULES.md`
4. `DEFINITION_OF_DONE.md`
5. relevant ADRs in `docs/architecture/decisions/`

Do not silently redesign the architecture.

## Product objective
RedCyberFox is an offline-first, passive-first, safety-first OT security platform for rural/small environments, legacy/low-spec Windows/Linux hosts, mixed IT/OT environments, intermittent WAN links, and zero-paid-service core operation.

Core capabilities:
- lightweight endpoint telemetry
- passive OT network sensing
- industrial protocol awareness
- asset / zone / conduit / process context
- deterministic detection
- behavioral baselines
- cross-source correlation
- explainable risk scoring
- safe incident response
- offline operation

## Non-negotiable principles
1. Edge-light, central-heavy.
2. Local detection must continue during WAN/Internet outages.
3. OT monitoring is passive by default.
4. Monitoring failure must fail to visibility, never to production.
5. Do not make PLC/SIS control actions the default.
6. Disruptive OT actions require explicit human approval.
7. Keep the endpoint agent lightweight.
8. No new infrastructure without demonstrated need.
9. Prefer mature open-source components where the architecture calls for adoption.
10. Security decisions must be deterministic and explainable.
11. Never silently sample away critical events.
12. Represent serial/non-IP blind spots honestly.
13. Preserve the canonical event model and interfaces unless a justified change is approved.
14. No paid SaaS dependency in the core Alpha.
15. Never claim IEC 62443 certification or customer Security Level achievement.

## Development behavior
Before coding:
- identify the phase/issue
- inspect existing code
- inspect relevant architecture/ADR
- identify dependencies and interfaces
- state assumptions briefly

During coding:
- keep changes incremental
- test security-critical behavior
- avoid unnecessary dependencies
- preserve offline operation
- never weaken security to make tests pass
- never disable tests

After coding:
1. format
2. unit tests
3. relevant integration tests
4. static/security checks
5. inspect failures
6. fix real issues
7. report changed files, tests, failures, assumptions

## OT safety
Do not implement active scanning, packet injection, inline blocking, PLC writes, SIS control, or disruptive OT automation unless explicitly requested for an isolated lab.

## Final report for every task
- What changed
- Why
- Tests run
- Results
- Remaining problems
- Architectural assumptions
