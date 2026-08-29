# Testing Context

## Test Layers

- **Unit Tests**: Standard Go tests (go test -v ./...). Fast, no external dependencies, mocks where necessary.
- **Integration Tests**: Tests like DB migrations require an isolated database connection. The migration tests create temporary transient schemas to avoid interfering with development data.
- **E2E Tests**: Requires running Postgres and Valkey. Separated behind //go:build e2e tags and run via go test -tags=e2e ./server.
- **Manual Reliability Tests**: Checking database availability interruptions and DLQ (Dead Letter Queue) processing.

## How to Run
- go test -v ./... for Unit tests.
- go test -v -tags=e2e ./server for E2E tests (needs .env or equivalent environment config).
- Local infrastructure is managed via Docker Compose (deployments/dev/docker-compose.yml).

## Known Flaky Tests
None known at this time.
