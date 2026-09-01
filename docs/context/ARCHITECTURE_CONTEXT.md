# Architecture Context

High-level architecture rules and constraints for the OT SOC platform.

## Architectural Invariants
- **Topology**: The system strictly follows an edge-light / central-heavy architecture to minimize impact on constrained OT environments.
- **Endpoint Agent**: The agent runs locally on the endpoint, prioritizing safety and low resource usage.
- **Offline Store**: The agent uses an SQLite (with WAL mode) offline store for local durability and event queuing.
- **Forwarder**: A background forwarder continuously reads from the SQLite store and pushes events to the central server via HTTPS.
- **Central API**: The central ingest endpoint is `/v1/ingest`.
- **Event Bus**: The central server uses Valkey as its internal event bus/message queue.
- **State Store**: PostgreSQL is used as the durable relational state store on the central server.
- **Dashboard**: A central dashboard exists for visualization and management.
- **Passive OT Sensor**: A passive OT sensor is planned as a future architectural component (currently forbidden to implement).
- **OT Safety Invariant**: The system operates with a strict "passive-first" safety invariant. We must not disrupt or probe OT environments actively unless explicitly authorized.
