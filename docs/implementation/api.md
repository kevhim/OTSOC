# RedCyberFox API Documentation

## Endpoints

### `POST /v1/ingest`
Ingests a single canonical event.

**Request Body:** JSON object representing `CanonicalEvent`.
**Response:** `202 Accepted` on successful queuing to Valkey stream. `400 Bad Request` if validation fails.

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
