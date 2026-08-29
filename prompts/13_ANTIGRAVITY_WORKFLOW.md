# Antigravity Workflow for RedCyberFox

## Before every task
Read:
- AGENTS.md
- ARCHITECTURE_RULES.md
- relevant V6 architecture section
- relevant phase prompt
- relevant ADRs

## Use issue-sized tasks
Bad:
"Build the whole OT SOC."

Good:
"Implement Phase 2 endpoint SQLite persistence. Read the architecture and existing agent code first. Preserve the canonical event schema and offline behavior. Add tests and run them."

## Execution discipline
- inspect existing code before changes
- implement only requested scope
- no silent architecture redesign
- no unnecessary dependencies
- no security bypasses
- no disabled tests

## After task
Run:
1. formatter
2. unit tests
3. integration tests if relevant
4. static/security checks

Then report:
- files changed
- design impact
- tests
- failures
- limitations
- resource impact

## Model usage
Use cheaper/fast models for boilerplate, UI, docs, simple tests.
Use stronger models for concurrency, cross-platform agent logic, correlation, security-sensitive code, difficult debugging, and architecture review.

The AI is an implementation assistant, not the final security authority.
