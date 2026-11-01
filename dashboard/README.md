# RedCyberFox Dashboard

Minimal Phase-1 live event and alert dashboard for a development environment.

## Run locally

1. Start PostgreSQL and Valkey from `deployments/dev` using `docker compose up -d`.
2. Start the Go worker: `go run ./server/cmd/worker` (from repository root).
3. Start the Go API: `go run ./server/cmd/api` (from repository root).
4. Run `npm ci && npm run dev` here and open the Vite address.

The browser fetches `/v1/events` and `/v1/alerts` on **the same origin**. Vite
proxies `/v1` to `http://127.0.0.1:8081` during local development. For a
production deployment, configure your reverse proxy to forward `/v1/*` to the
API server on the same origin as the dashboard. Direct access to a separately
hosted API will not work without deliberate CORS setup.

**Security:** The tenant ID query parameter is a development convenience, **not
authentication**. Do not expose this dashboard/API as a public or multi-tenant
service before implementing access control and authenticated tenant scoping.
