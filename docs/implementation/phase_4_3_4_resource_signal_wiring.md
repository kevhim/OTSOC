# Phase 4.3.4 — Resource Signal Wiring

The endpoint now runs the Phase 4 coordinator on a five-second timer. The first
sample establishes the CPU baseline; subsequent samples feed the governor and
the in-memory metrics snapshot. The monitor stops on agent cancellation before
SQLite is closed. Sampling failure is logged and never stops collection or
interferes with the monitored host.

## Signal sources

| Signal | Runtime source |
| --- | --- |
| Agent CPU and RSS | Windows or Linux process provider |
| Agent process write-I/O rate | Windows process I/O counters or Linux `/proc/self/io` |
| Persistence bytes | Regular-file sizes of `agent.db`, `agent.db-wal`, and `agent.db-shm`; absent SQLite sidecars count as zero |
| Storage pressure | Persistence bytes divided by the same 50 MiB quota passed to SQLite storage |
| In-memory queue saturation | Current length and capacity of the existing 200-event collector channel |
| Connectivity | Forwarder's last HTTP attempt: `UNKNOWN` before any attempt, `OFFLINE` on transport failure, `ONLINE` on any HTTP response |

The channel does not expose its oldest element without consuming it. Queue age
therefore remains explicitly unavailable in the metrics snapshot rather than
being reported as zero. Connectivity is observed from normal forwarding; the
resource monitor performs no active network probe.

## Observability and scope

State changes and changed sampling errors are logged immediately. A resource
snapshot is logged every twelfth sample. Unavailable measurements remain
invalid, and the coordinator's existing fail-safe logic handles them.

The current CPU, memory, storage, and queue thresholds are provisional. The
governor is observational in this slice: it does not pause collectors, discard
events, or change forwarding order. Those policies require representative
low-spec benchmarks and separate Phase 4 acceptance work. Offline collection,
SQLite durability, and the Phase 2E.4 channel boundary are unchanged.

## Verification

The governor and forwarder Go test packages pass locally on Windows. The
ingestion package compiles and has no direct tests. The broader storage suite
currently fails its existing corrupt-database backup assertion on Windows;
that test is outside this resource wiring slice. Sampling tests cover SQLite
WAL growth and removal, queue saturation, network state, invalid sidecars,
and timed recovery through the coordinator. Provider tests sample both a
temporary DB/WAL fixture and a live SQLite database with the real OS provider.
The [Linux PR CI run](https://github.com/kevhim/OTSOC/actions/runs/39019997303)
passed the real YARA-X C API build, formatting, Go vet, unit/integration and
race tests, migrations, E2E, dashboard build, and Docker Compose validation.
This closes the Phase 4.3.4 integration gate. A native Windows endpoint build
remains blocked by the existing YARA-X Go package build constraints; the
endpoint compiled locally with a temporary YARA overlay.
