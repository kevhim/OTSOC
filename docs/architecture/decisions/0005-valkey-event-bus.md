# 0005 - Valkey Event Bus

## Status
Accepted

## Context
The central SOC must rapidly ingest, validate, and coordinate the processing of bursts of telemetry from many edge endpoints, particularly when WAN connections are restored and queues are flushed.

## Decision
We will use Valkey Streams as the event bus for transport and processing queues.

## Rationale
Valkey provides an open-source, high-performance, in-memory stream processing system with consumer groups, ideal for decoupling ingestion from expensive correlation and detection work.

## Alternatives considered
- **Kafka**: Rejected for the baseline architecture due to high operational complexity and JVM overhead.
- **RabbitMQ**: Valkey Streams offer a simpler, faster model for append-only telemetry streams and consumer group distribution.

## Consequences
- Requires persistence configuration and recovery testing for Valkey to prevent data loss.
- Memory capacity of Valkey dictates the maximum burst absorption size before backpressure is required.

## Architectural invariants
- Valkey is accessed through EventBus.
