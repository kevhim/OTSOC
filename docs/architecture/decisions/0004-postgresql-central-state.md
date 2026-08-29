# 0004 - PostgreSQL Central State

## Status
Accepted

## Context
The central SOC requires an authoritative system-of-record for multi-tenant state, including assets, zones, conduits, policy, incidents, and audit metadata.

## Decision
We will use PostgreSQL as the authoritative state database.

## Rationale
PostgreSQL provides robust ACID guarantees, excellent JSON support, and built-in Row-Level Security (RLS) which is critical for enforcing tenant isolation in a multi-tenant platform.

## Alternatives considered
- **MySQL/MariaDB**: Lacks the advanced RLS features of Postgres.
- **MongoDB / NoSQL**: Rejected because the data is highly relational (asset graphs, RBAC) and requires strict transactional consistency.

## Consequences
- The schema requires careful migration management.
- Tenant isolation relies heavily on RLS policies.

## Architectural invariants
- PostgreSQL is the authoritative state storage.
