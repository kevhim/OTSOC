# RedCyberFox source audit and corrective patch

Review date: 2026-11-01. Input: `test1.4.zip` (305 archived entries; Go endpoint, Go API/worker, React dashboard, configuration, migrations, tests, docs, and bundled Windows Go distribution).

## Corrected issues

1. **Emergency event-recovery data loss (critical).** `agent/internal/ingestion/engine.go` previously used `os.WriteFile` for every emergency spill, overwriting earlier recovery records. Replaced with append-only, restrictive-permission JSON-lines writing and a disk sync. Added a regression test that spills two distinct records and verifies both survive.
2. **Invalid events stuck after queuing (high).** `pkg/events/canonical.go` did not enforce PostgreSQL `VARCHAR` limits, allowing impossible-to-store events into the stream. Validate tenant/site IDs and event string columns against database limits before publishing. Reject NaN/Infinity confidence values.
3. **Malformed/oversized API payloads (high).** `server/internal/api/ingest.go` accepted arbitrarily large JSON bodies and unexamined trailing JSON. Cap the request body at 1 MiB (`413`), require exactly one JSON value (`400`), and add regression tests. This limit should be reviewed against real sensor event sizes.
4. **Silent dashboard record omission (high).** Both read endpoints previously ignored `rows.Scan` errors, resulting in incomplete `200 OK` arrays. Return an explicit `500` and log scan failures.
5. **Worker recovery delay and fragile error matching (medium).** Trigger pending-message recovery immediately on consumer startup, accept `BUSYGROUP` error variants, and exit without sleeping after cancellation. Existing minimum-idle policy remains intact.
6. **Dashboard refresh and origin problems (medium).** Replace hard-coded `localhost:8081` with same-origin `/v1` requests (Vite dev proxy). Poll only after the prior request completes, abort on tenant changes/unmount, update both datasets together, reject non-array JSON, and clear stale tenant data.
7. **Invalid endpoint configuration reaching runtime (medium).** `agent/internal/config` now validates an HTTP(S) base URL and schema-compatible tenant/site IDs. The forwarder tolerates trailing slashes in the API address. Added negative configuration tests.
8. **Unsafe/default operational choices (medium).** Remove wildcard CORS from the read API (dashboard uses proxy), bound HTTP request/response times, avoid explicitly logging the database connection URL on ping errors, and make CI npm installation reproducible with `npm ci` and existing lockfile.
9. **Formatting check (low).** Format the existing `agent/internal/ingestion/engine_test.go` and constrain the CI Go formatting scan to the project source rather than bundled `.tools/go` files.

## What was actually checked here

- Inspected source in edge collectors, endpoint wiring, ingestion, forwarder, server ingest/read/queue, canonical event model, migration SQL, dashboard, CI, configuration, and platform context.
- `GO111MODULE=off GOTOOLCHAIN=local go test ./agent/internal/config -count=1 -v`: **PASS**, including new configuration rejection tests.
- `gofmt -l` on every Go source/test in `agent`, `server`, `pkg`: **PASS**, zero unformatted files. Gofmt also parsed each source file.
- Dashboard `App.tsx`: **PASS** TypeScript structural type-check using the global TypeScript 5.8 compiler and minimal React type shims; **not** a substitute for the repository's actual TypeScript 6 dependency build.
- `App.tsx` and `vite.config.ts`: **PASS** TypeScript transpilation diagnostics, using global TypeScript compiler.
- Included `agent.db`: SQLite `PRAGMA quick_check` returned `ok`; original database contents were not modified.
- JSON syntax validated for ordinary JSON files; `tsconfig.*.json` are JSON-with-comments, so they are not valid strict JSON but are valid TypeScript configuration syntax.

## Not executable in this review environment

- Root Go test suite, `go vet`, Go race tests, Windows collector host tests: project requires **Go 1.26** and external Go/native YARA-X dependencies. The local Linux Go is 1.23.2, and Go's network toolchain download failed.
- `npm ci`, `npm run lint`, Vite/React production build: npm packages are not cached and network installation is unavailable. `npm ci --offline` returned `ENOTCACHED` for Vite.
- Live PostgreSQL/Valkey migration/queue/E2E tests, Docker Compose validation, physical USB and process capture: dependencies/services/devices are unavailable here.
- Newly added queue, ingest, canonical-model, and spill regression tests are included but **not executed** here.

## Known limitations remaining (not claimed as fixed)

- **Authentication/authorization missing:** the Phase-1 API identifies the tenant from a user-supplied query parameter. CORS and URL validation do **not** provide authorization. Protect behind a trusted network until authenticated identities and server-side tenant scoping exist.
- **Detection rules not active in production:** the endpoint starts `detection.NewEngine(db, []detection.Rule{})`; the engine has no actual production rules loaded. Server-side Phase-1 severity alerts are synthetic and do not prove endpoint detection coverage.
- **Phase 4 governor incomplete:** only an initial baseline sample is wired; recurring evaluation, authoritative storage budget, and acceptance tests remain pending, as documented by the existing project context.
- **Filesystem monitoring opt-in:** the supplied `agent.json` has no `monitor_paths`, consistent with the included log indicating the filesystem collector skipped monitoring.
- Physical USB events and all Windows-only code paths require a real Windows validation environment; the supplied older USB log does not establish successful new USB event capture.
- A single shared emergency-spill JSON-lines file can still be subject to partial writes or concurrent process access; recovery replay and durability policy need separate design/test coverage.

## Recommended verification on the project host

1. Use Go 1.26, Node 22+, Docker, Postgres 16, and Valkey. Provision the YARA-X C API native dependency per CI and the repository security setup.
2. Run `go test ./...`, `go vet ./...` and targeted race/host tests in a properly configured Go 1.26 environment (migration tests require the configured database).
3. In `dashboard`, run `npm ci && npm run lint && npm run build`.
4. Run `docker compose -f deployments/dev/docker-compose.yml up -d`; verify migration, API/worker, E2E ingestion/alerts, and dashboard's `/v1` proxy routing.
5. For Windows sensor validation, test on a real machine with permitted USB and network events. Verify events persist locally, forward when network reconnects, and appear under the expected tenant.
6. Before production exposure, implement authentication/authorization, rule loading, production reverse-proxy/TLS configuration, and close the documented Phase 4 acceptance blockers.

**Patch scope:** original `agent.db`, `agent.json`, `usb_test.log`, and bundled `.tools` contents remain unchanged. No new external dependencies were added.
