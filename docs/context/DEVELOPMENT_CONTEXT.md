# Development Context

*For authoritative development guidelines, read AGENTS.md.*

## Language & Toolchain
- **Backend**: Go (Go modules)
- **Database**: PostgreSQL (Migrations in server/migrations/)
- **Queue**: Valkey Streams
- **Dashboard**: Node.js/React (likely)

## Repository Layout
- server/: Central API, worker, DB, and internal packages
- pkg/: Shared domain models (e.g. events)
- gent/: Endpoint agent code (synthetic only for now)
- docs/: Architecture, Context, and Implementation documentation
- schemas/: JSON schemas for contracts

## Git Workflow
- Branch naming: eature/*, ix/*, docs/*
- Commits: Conventional commits (e.g., ix(core): description)
- Safe Git rules apply: Never rewrite history or drop existing tags.

## Standard Commands
- Tests: go test ./...
- E2E Tests: go test -tags=e2e ./server
- Formatting: gofmt
- Run local infra: docker compose up (in deployments/dev/)
