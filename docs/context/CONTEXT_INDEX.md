# RedCyberFox Persistent Context System

This directory acts as the persistent AI-readable project memory layer for RedCyberFox. It is intended to help AI coding agents quickly orient themselves to the current state of the project without relying on large chat histories.

## Source of Truth Hierarchy

These context files summarize the current working state and point to authoritative documents. They do not replace authoritative documentation.

The exact precedence is:
1. Current source code + tests
2. V6 architecture (docs/architecture/architecture.md)
3. ADRs (docs/architecture/decisions/)
4. Implementation documentation (docs/implementation/)
5. Context files (docs/context/)
6. Prompts

If a context file contradicts source code, do not blindly trust the context. Inspect the source/tests, determine the actual state, and update the context file accordingly. However, when deciding whether a new architectural change is permitted, V6 + accepted ADRs take precedence over AI-generated implementation choices.

## Context Map

| Topic | Context File | Primary Authority |
|---|---|---|
| Project | PROJECT_CONTEXT.md | README + V6 |
| Architecture | ARCHITECTURE_CONTEXT.md | V6 + ADRs |
| Current state | CURRENT_STATE.md | Git + roadmap |
| Decisions | DECISIONS_CONTEXT.md | ADRs |
| Security | SECURITY_CONTEXT.md | Security docs + ADRs |
| Development | DEVELOPMENT_CONTEXT.md | AGENTS.md |
| Testing | TESTING_CONTEXT.md | DoD + tests |
| API | API_CONTEXT.md | API docs + source |
| Events | EVENT_CONTEXT.md | event schema + source |
| Endpoint | EDGE_AGENT_CONTEXT.md | V6 + implementation |
| OT sensor | OT_SENSOR_CONTEXT.md | V6 |
| Detection | DETECTION_CONTEXT.md | V6 + detection docs |
| Deployment | DEPLOYMENT_CONTEXT.md | deployment docs |
| Roadmap | ROADMAP_CONTEXT.md | roadmap |

## Current Project Phase
The project has successfully completed Phase 1.2 (Final reliability corrections). Phase 2 (Endpoint Alpha) has NOT started. See CURRENT_STATE.md for living memory.

## Context Maintenance Rules
Context files must be updated when:
- Architecture changes
- An ADR is created
- Implementation state changes
- A phase completes
- A known defect is discovered/resolved
- API changes
- Event contract changes
- Security posture changes
- Deployment behavior changes

Do NOT update context files merely to make the project look complete. Context must describe reality.
