# RedCyberFox API Documentation

## Endpoints

### `POST /v1/ingest`
Ingests a single canonical event.

**Request Body:** JSON object representing `CanonicalEvent`.
**Response:** `202 Accepted` after Valkey accepts the event. `400 Bad Request` on invalid event, malformed JSON, or trailing JSON. `413 Request Entity Too Large` when the request body exceeds 1 MiB.

The API currently trusts the tenant ID in the query string and event body; it is **not authenticated**. Deploy only on trusted local networks for development until authenticated tenant authorization is added.

### `GET /v1/events`
Returns recent events.

**Query Parameters:**
- `tenant_id` (required): The tenant ID to scope the read to (**DEVELOPMENT ONLY**, not for production authorization).

**Response:** `200 OK` with JSON array of events.

### `GET /v1/alerts`
Returns recent alerts.

**Query Parameters:**
- `tenant_id` (required): The tenant ID to scope the read to (**DEVELOPMENT ONLY**, not for production authorization).

**Response:** `200 OK` with JSON array of alerts.

### Dashboard reverse proxy
The dashboard uses same-origin `/v1/*` calls. Vite proxies them locally to `127.0.0.1:8081`. Production hosting must route `/v1/*` to the API on the dashboard's origin and enforce authentication/authorization before accepting external traffic.
