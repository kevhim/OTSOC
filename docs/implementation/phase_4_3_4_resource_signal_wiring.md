# Phase 4.3.4 — Resource Signal Wiring

The Phase 4 coordinator deterministically integrates real OS resource telemetry,
in-memory queue state, and forwarder connectivity signals into the Governor and
GovernorMetrics on demand via `Coordinator.Step(ctx, runtimeSignals)`.

The production endpoint runtime initializes the coordinator in on-demand mode.
Recurring endpoint background sampling (previously implemented as a 5-second ticker)
is not enabled in production and has been removed to preserve the synchronous,
deterministic contract. `RunSampling` is retained exclusively as a harness and test
utility.

## Signal sources

| Signal | Runtime source |
| --- | --- |
| Agent CPU and RSS | Windows or Linux process provider |
| Agent process write-I/O rate | Windows process I/O counters or Linux `/proc/self/io` |
| Persistence bytes | Regular-file sizes of `agent.db`, `agent.db-wal`, and `agent.db-shm`; absent SQLite sidecars count as zero |
| Storage pressure | UNRESOLVED. The 50 MiB `storageQuotaBytes` is the SQLite pruning threshold, not an authoritative persistence budget. `StorageBudgetBytes` remains 0 (unconfigured), so storage pressure evaluation remains blocked until an authoritative budget and disk-headroom policy are established. |
| In-memory queue saturation | Current length and capacity of the existing 200-event collector channel |
| Connectivity | Forwarder's last HTTP attempt: `UNKNOWN` before any attempt, `OFFLINE` on transport failure, `ONLINE` on any HTTP response |

The channel does not expose its oldest element without consuming it. Queue age
therefore remains explicitly unavailable in the metrics snapshot rather than
being reported as zero. Connectivity is observed from normal forwarding; the
resource monitor performs no active network probe.

## Observability and scope

The coordinator executes synchronously without hidden background tickers. Unavailable
measurements remain invalid, and the coordinator's fail-safe logic handles them.

The current CPU, memory, storage, and queue thresholds are provisional. The
governor is observational: it does not pause collectors, discard events, or change
forwarding order. Those policies require representative low-spec benchmarks and
separate Phase 4 acceptance work. Offline collection, SQLite durability, and the
Phase 2E.4 channel boundary are unchanged.

## Acceptance Status & Verification

1. **OS Resource Providers (4.3.1 - 4.3.3)**: COMPLETE. Windows and Linux providers pass unit, edge-case, and mock tests.
2. **On-Demand Coordinator Integration (4.3.4)**: STARTUP BASELINE INTEGRATED / RUNTIME INVOCATION PENDING. The live endpoint executes an initial baseline invocation of `Coordinator.Step` at startup to prime CPU baseline and capture initial state. Ongoing runtime invocation during collection remains uninvoked pending Phase 4 batch/lifecycle trigger design.
3. **Continuous Endpoint Sampling**: NOT ENABLED. Unapproved background 5-second ticker removed from endpoint runtime.
4. **Storage Budget & Headroom Policy**: UNRESOLVED. Storage pressure calculation remains explicitly blocked pending an authoritative persistence budget.
5. **Governor Race Detector Validation**: PENDING. A dedicated `Go Test Race (Governor)` step (`go test -race -v -count=1 ./agent/internal/governor/...`) has been added to `.github/workflows/ci.yml` for execution in the Linux CI workflow.

Phase 4.3 remains open until storage budget policy is established and CI race test evidence is collected.
