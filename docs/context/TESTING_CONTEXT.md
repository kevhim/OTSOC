# Testing Context

Overview of the testing strategy for the RedCyberFox OT SOC platform.

## Test Categories
- **Standard Tests**: Unit tests covering core logic, parsing, and data structures.
- **Storage Tests**: Tests verifying SQLite interactions, storage limits, atomicity, quota enforcement, and identity migration (e.g. legacy identity ignored when DB exists).
- **Forwarder Tests**: Tests validating the `HTTP POST -> 202 -> remove` flow, including exponential backoff, 400 DLQ routing, and transient error retries.
- **Offline Integration Tests**: End-to-end tests validating that the agent can queue events locally while offline and successfully flush them sequentially when the network is restored.
- **Lost-Response/Idempotency Tests**: Tests simulating a dropped HTTP response after a successful server ingest, ensuring that the duplicate retry is handled gracefully by the server using `event_id`.

## Known Testing Limitations
- Actual network partition simulation is limited to mock HTTP clients.
- File system space exhaustion is not reliably tested in CI.
- True concurrency testing is limited due to the intentional serialized SQLite access design in Alpha.
