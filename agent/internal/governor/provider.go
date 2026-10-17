package governor

import (
	"context"
	"time"
)

// OSResourceProvider defines the platform-neutral contract for gathering operating system telemetry
// for the agent process. Implementations are expected to be OS-specific (Windows, Linux, etc.).
//
// Return Semantics:
// - Valid=true + nil error = measured value, including zero.
// - Valid=false + nil error = measurement unavailable.
// - non-nil error = sampling failure or invalid measurement.
// - Never interpret zero values from an invalid/unavailable measurement as real data.
//
// Freshness Semantics:
// All methods return a sampledAt time.Time. Measurements gathered synchronously as a single
// logical batch should share the exact same timestamp to form a consistent snapshot.
type OSResourceProvider interface {
	// SampleCPU returns the current CPU utilization of the agent process as a normalized percentage (0.0 to 100.0).
	// 100.0 represents full utilization of the normalized agent-process CPU capacity.
	// Implementations MUST NOT report >100.0 due to multi-core systems.
	SampleCPU(ctx context.Context) (val ValidFloat64, sampledAt time.Time, err error)

	// SampleMemory returns the current Resident Set Size (RSS) of the agent process in bytes.
	SampleMemory(ctx context.Context) (val ValidUint64, sampledAt time.Time, err error)

	// SampleStorage returns the storage volume currently consumed by the agent.
	// - usedBytes explicitly represents the agent's dedicated persistence footprint (e.g., SQLite data files, agent logs).
	//   Storage paths MUST resolve to regular files; directories and other non-regular filesystem objects are
	//   strictly prohibited to prevent unbounded traversal and unpredictable I/O blocking. If a directory or
	//   other non-regular filesystem object is encountered, the measurement MUST fail and return an error.
	//   Symlinks are resolved if the underlying OS stat call resolves them, but they must resolve to a single regular file.
	//   It MUST NOT silently conflate the agent's isolated storage budget with total host filesystem pressure.
	// - writeBytesPerSec is the agent process's observed write-I/O transfer rate, calculated from
	//   counter deltas over a positive elapsed interval. It is not a direct measurement of physical
	//   disk throughput or host disk pressure. Unavailable baselines, counter discontinuities, and
	//   invalid elapsed intervals remain explicitly unavailable rather than producing fabricated rates.
	//   Implementations MUST NOT substitute physical disk throughput, volume metrics, or host disk pressure.
	//   Process attribution caveat: OS-level process I/O attribution semantics vary between platforms.
	//   For example, Linux /proc/self/io includes write-I/O from waited-for terminated children in the parent's
	//   accounting, whereas Windows GetProcessIoCounters attributes I/O strictly to the target process handle.
	//   The shared contract requires implementations to measure process-level write-I/O transfer rate without
	//   substituting physical disk metrics, but does not falsely promise identical cross-OS child process attribution.
	SampleStorage(ctx context.Context) (usedBytes ValidUint64, writeBytesPerSec ValidFloat64, sampledAt time.Time, err error)
}
