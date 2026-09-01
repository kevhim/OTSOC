# API Context

Defines the contract between the edge agent and the central server.

## Architectural Invariants
- **POST /v1/ingest**
  - Accepts exactly one `CanonicalEvent`.
  - HTTP 202 (Accepted) means the event was accepted into the central processing queue. The agent removes the local event **only** after receiving this 202.
  - HTTP 400 (Bad Request) means a validation or permanent failure. The agent moves the event to the local DLQ.
  - HTTP 429/5xx mean a transient failure. The agent applies exponential backoff and retries.
  - The `tenant_id` in the event payload must strictly match the configured agent tenant identity.

- **GET /v1/events** & **GET /v1/alerts**
  - These currently require a development-only `tenant_id` selector.
  - They are **not** using production authentication or authorization mechanisms yet.
