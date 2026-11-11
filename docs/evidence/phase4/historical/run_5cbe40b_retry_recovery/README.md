# Historical Workload Run: Real Retry Recovery (Commit `5cbe40b`)

## Provenance
- **Source Revision**: `d6fd8bd091bcbda92d760803b9233830d3bc0fe4`
- **Committed In**: `5cbe40b37f48037fa979dc5a3d0ec4272118f6f5`
- **Dirty State**: `true` (binary was built from working tree before commit `5cbe40b`)
- **Host**: Windows 11 Home (amd64, 22 logical cores, Intel Core Ultra 7 155H)
- **Toolchain**: `go1.27.0` (windows/amd64), MSYS2 UCRT64 GCC 14.2.0

## Characteristics & Caveats
- **Offline / Reconnect**: Demonstrated natural exponential backoff elapsing over 22 seconds without SQL deadline manipulation. Verified exact 22 offline event IDs and sequence numbers arrived at mock receiver.
- **Queue Semantics**: Executed with adapter-held event preservation fix in place.
- **Limitation**: The binary was built from a working tree with uncommitted changes (`dirty: true`). Preserved as empirical validation of real retry recovery timing.

