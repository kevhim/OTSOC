# Historical Workload Run: Initial Smoke Evidence (Commit `d6fd8bd`)

## Provenance
- **Source Revision**: `459dce1bfe48be0d92886e495bc3321c40cf65a1`
- **Committed In**: `d6fd8bd091bcbda92d760803b9233830d3bc0fe4`
- **Dirty State**: `false`
- **Host**: Windows 11 Home (amd64, 22 logical cores, Intel Core Ultra 7 155H)
- **Toolchain**: `go1.27.0` (windows/amd64), MSYS2 UCRT64 GCC 14.2.0

## Characteristics & Caveats
- **Offline / Reconnect**: Historical reconnect run manually modified SQLite (`UPDATE events SET next_attempt_at = NULL`) to force immediate redelivery during reconnect.
- **Queue Semantics**: Ran prior to adapter-held event preservation fix on queue `Close()`.
- **Purpose**: Preserved as historical record of initial Phase 4 smoke evidence. Superseded by natural retry backoff and clean committed runs.

