# Context Index

This directory contains crucial architectural and domain context for AI coding agents and human developers. Read the appropriate file before making architectural changes or when starting a new task in a specific domain.

- **CURRENT_STATE.md**: The exact current state of the project, including current branch, completed features, known limitations, and forbidden components. Read first to understand where we are in the roadmap.
- **EDGE_AGENT_CONTEXT.md**: Architectural invariants and durability contracts for the edge agent. Read when modifying agent initialization, storage logic, or event handling.
- **EVENT_CONTEXT.md**: Semantics for canonical events, identities (event_id, seq_no), and grouping (tenant, site, asset). Read when modifying event schemas or deduplication logic.
- **TESTING_CONTEXT.md**: Breakdown of testing strategy, suites, and known simulation limitations. Read when adding new tests or if confused about CI failures.
- **ARCHITECTURE_CONTEXT.md**: High-level system architecture and component boundaries.
- **PROJECT_CONTEXT.md**: Business goals and top-level constraints of the OT SOC project.
- **DECISIONS_CONTEXT.md**: A log of critical architectural decisions (ADRs) and why they were made.
- **SECURITY_CONTEXT.md**: Threat model, zero-trust assumptions, and mandatory security boundaries.
- **DEVELOPMENT_CONTEXT.md**: Setup instructions, coding standards, and commit guidelines.
- **API_CONTEXT.md**: Contracts between the edge agent and central server.
- **OT_SENSOR_CONTEXT.md**: OT-specific constraints (e.g. passive monitoring, safety criticality).
- **DETECTION_CONTEXT.md**: Future detection engine rules and schemas (currently forbidden).
- **DEPLOYMENT_CONTEXT.md**: Packaging and deployment strategies.
- **ROADMAP_CONTEXT.md**: Long-term vision and phase definitions.
