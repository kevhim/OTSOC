# 0001 - Offline-First Edge

## Status
Accepted

## Context
RedCyberFox is designed for rural and low-spec OT environments where WAN connectivity is often intermittent or entirely down. Critical security telemetry must not be lost when the Internet or central SOC is unavailable.

## Decision
We will use an offline-first architecture for the edge components. The local agent will buffer events using a durable local storage mechanism (SQLite/WAL) and operate autonomously to run local detections.

## Rationale
Production safety and reliable security monitoring require that failures in external connectivity do not compromise local visibility or cause data loss. A local store-and-forward queue ensures resilience.

## Alternatives considered
- **In-memory queues only**: Rejected because a power failure or reboot during a WAN outage would lose evidence.
- **Heavy message brokers (RabbitMQ/Kafka) on edge**: Rejected because they violate the low-spec constraint for legacy hardware.

## Consequences
- Requires local disk space management (quotas, priority retention).
- Introduces complexity in sequence number tracking and reconciliation upon reconnect.

## Architectural invariants
- Local detection works without WAN.
- Internet is a synchronization path, not a prerequisite for local security.
- SQLite is the edge durability layer.
