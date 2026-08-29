# Phase 0 — Repository Bootstrap

Create the RedCyberFox monorepo foundation only.

## Structure
- README.md
- ARCHITECTURE_RULES.md
- AGENTS.md
- SECURITY.md
- CONTRIBUTING.md
- LICENSE
- .gitignore
- docs/architecture/
- agent/
- server/
- sensor/
- dashboard/
- schemas/
- rules/
- lab/
- tests/
- deployments/
- scripts/
- .github/workflows/

## Requirements
- single monorepo
- reproducible clean checkout
- basic CI for Go formatting/tests/vet and dashboard build when present
- minimal Docker Compose development environment only for first vertical slice
- no ClickHouse, MISP, OpenBao, HA, or other deferred infrastructure yet
- create useful placeholders, not fake implementations

## Exit criterion
A new developer can clone the repository, install dependencies, start the local environment, and run the documented tests/build.
