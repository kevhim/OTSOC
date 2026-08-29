# Security Context

## Security Principles
- least privilege
- tenant isolation
- secure defaults
- no secret commits
- auditability
- explicit OT safety boundaries
- no silent telemetry loss

## Current Authentication State
- **Development authentication**: API operates openly or relies on basic dev scopes (e.g., query params).
- **Production authentication**: NOT IMPLEMENTED. Do not claim production security is implemented.

## Current Trust Boundaries
- **Endpoint**: NOT IMPLEMENTED
- **Network Sensor**: NOT IMPLEMENTED
- **API**: IMPLEMENTED (Internal boundary, ingests data)
- **Event Bus**: IMPLEMENTED (Valkey Streams, internal boundary)
- **Database**: IMPLEMENTED (PostgreSQL, internal boundary)
- **Dashboard**: IMPLEMENTED (Web frontend)
- **Administrator**: NOT IMPLEMENTED

## Known Security Limitations
- 	enant_id scope isolation relies purely on unauthenticated query parameters during development.
- No mTLS, PKI, or encrypted transports configured for internal services yet.
