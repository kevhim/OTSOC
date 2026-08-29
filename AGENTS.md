# AI Coding Agent Instructions

When working on this repository, you MUST:

- Read the V6 architecture (`docs/architecture/architecture.md`).
- Read `ARCHITECTURE_RULES.md` and `DEFINITION_OF_DONE.md`.
- Read relevant ADRs in `docs/architecture/decisions/`.
- Inspect existing code before modifying it.
- Work ONLY on the requested phase/task.
- Avoid silent architecture changes.
- Preserve offline operation.
- Preserve passive OT safety.
- Consider low-spec constraints.
- Run tests and report failures.
- NEVER implement unsafe OT actions unless explicitly requested for an isolated lab.

## Git Workflow

- **Never perform implementation directly on `main`.** Work on feature/fix/security branches.
- **Always inspect git status before making changes.**
- **Never discard user changes.** Preserve existing developer work. Ask for approval before any operation that could discard or rewrite work.
- **Never force-push.**
- **Never rewrite published history.**
- **Never use `git reset --hard` unless explicitly approved.**
- **Never use `git clean -fd` unless explicitly approved.**
- **Never commit secrets.**
- **Keep commits logically scoped.** Use Conventional Commits.
- **Run relevant tests before committing.**
- **Report branch, commits, tests and working-tree state at task completion.**
- **Do not use destructive Git commands to solve implementation problems.**

## Context System

Before substantial work:
- Read `docs/context/CONTEXT_INDEX.md`.
- Read `docs/context/PROJECT_CONTEXT.md`.
- Read the task-specific context file.
- Read the relevant ADRs.
- Inspect the source code.
- Treat source/tests as the implementation truth.

After substantial work:
- Update relevant context files.
- Update `docs/context/CURRENT_STATE.md`.
- Update roadmap context when phase status changes.

Never use context files to override actual source/test evidence.
