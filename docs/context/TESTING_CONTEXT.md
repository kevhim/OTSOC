# Testing Context

Overview of the testing strategy for the RedCyberFox OT SOC platform.

## Test Categories
- **Standard Tests**: Unit tests covering core logic, parsing, and data structures.
- **Storage Tests**: Tests verifying SQLite interactions, storage limits, atomicity, quota enforcement, and identity migration (e.g. legacy identity ignored when DB exists).
- **Forwarder Tests**: Tests validating the `HTTP POST -> 202 -> remove` flow, including exponential backoff, 400 DLQ routing, and transient error retries.
- **Offline Integration Tests**: End-to-end tests validating that the agent can queue events locally while offline and successfully flush them sequentially when the network is restored.
- **Lost-Response/Idempotency Tests**: Tests simulating a dropped HTTP response after a successful server ingest, ensuring that the duplicate retry is handled gracefully by the server using `event_id`.
- **Measurement Report & Safety Tests (`agent/internal/resourcereport`)**: Unit tests covering duration/interval range validation, sample limits, output byte limits, refusal of overwrite (`O_EXCL`), persistence file conflict rejection (DB/WAL/SHM/identity/config/spill), watched path conflict rejection, visible write error handling, null metric preservation, and timer/signal shutdown overlap.
- **Shared Request Isolation Tests (`dashboard/src/request_isolation.test.mjs`)**: Direct exercise of production `fetchTenantData` request logic verifying generation counter advances, abort signal propagation, and discarding delayed responses from previous tenants upon rapid switching.
- **Workload Demonstration Tests (`agent/internal/integration/workload_demonstration_test.go`)**: Production pipeline verification of harmless bounded events, metadata IOC rule matching (`LOCAL-IOC-EXEC-001`), derived finding generation with deterministic UUIDv5 identity, explicit evidence event ID linkage, and durable presence after clean shutdown.
- **Instrumented Event Queue Tests (`agent/internal/eventqueue/queue_test.go`)**: FIFO delivery, non-destructive $O(1)$ oldest event age, instrumented `Enqueue(ctx, ev)` API, exact blocked enqueue attempts under capacity, multiple concurrent blocked producers, context cancellation unblocking, `TryEnqueue`, pressure episodes, and concurrent senders under the 64-bit CGO race detector.
- **Ingestion-Outcome Reconciliation Tests (`agent/internal/ingestion/engine_test.go`)**: Deterministic accounting verification across committed, pre-commit failure, DLQ recovery, disk spill, and drop outcomes with zero count skew.
- **Queued Drain & Finalization Tests (`agent/internal/integration/queued_drain_report_test.go`)**: Production-path regression holding ingestion behind an explicit barrier, buffering events in memory, verifying actual measurement runner duration expiry, releasing barrier, executing shared `ExecuteShutdown`, and reconciling terminal durable outcomes; plus error capture testing confirming collector and forwarder stop failures propagate to report outcome and exit status.

## Known Testing Limitations
- Actual network partition simulation is limited to mock HTTP clients.
- File system space exhaustion is not reliably tested in CI.
- True concurrency testing is limited due to the intentional serialized SQLite access design in Alpha.
- Linux runtime validation must be executed in a real Linux environment (CI workflow) and not inferred from local Windows test runs.
- Windows developer-host smoke-run evidence is developer-machine telemetry and does not substitute for low-spec hardware qualification.
