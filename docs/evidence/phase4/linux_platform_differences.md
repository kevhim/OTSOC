# Linux vs Windows Platform Differences and CI Validation Analysis

**Status**: Active Analysis / Phase 4 Gate Documentation  
**Scope**: RedCyberFox Edge Agent Cross-Platform Mechanics  
**Environment Status**: Windows 11 Workstation Executed; Linux CI Executed in GitHub Actions; Native Low-Spec Linux Field Hardware **OPEN**  

---

## 1. Overview and Anti-Inference Principle

The RedCyberFox Edge Agent is designed for dual deployment:
- **Windows IoT / Server**: Substation HMI, engineering workstations, SCADA servers.
- **Embedded Linux**: DIN-rail industrial gateways, remote terminal units (RTUs), edge protocol converters.

> [!WARNING]
> **Anti-Inference Rule**: Passing test suites and benchmark results on Windows **MUST NOT** be used to infer Linux runtime safety or performance. Differences in kernel scheduling, asynchronous I/O, process hierarchy, and file locking require independent qualification on Linux environments.

---

## 2. Platform Mechanical Differences

### 2.1 Subprocess Management and Lifecycle Control

| Aspect | Windows (Win32) | Linux (POSIX) | Agent Implementation Impact |
| :--- | :--- | :--- | :--- |
| **Process Tree Termination** | Job Objects (`AssignProcessToJobObject`) or `taskkill /PID ... /T /F` | POSIX Process Groups (`setpgid`, `kill(-pgid, SIGTERM)`) | On Windows, subprocess leaks are prevented using Job Objects or explicit child traversal. On Linux, graceful shutdown requires `SIGTERM` followed by `SIGKILL` to the process group. |
| **Termination Semantics** | `TerminateProcess` is immediate, unconditional, and non-trappable | `SIGTERM` is trappable; allows graceful WAL flush; `SIGKILL` is non-trappable | Agent shutdown must give collectors and forwarders 5 seconds to finish in-flight work before exit on both platforms. |
| **Standard I/O Pipes** | Win32 Anonymous Pipes with synchronous or overlapped I/O | UNIX domain pipes with `O_NONBLOCK` support | Subprocess runners must use bounded buffers (256 KB) to prevent deadlocks when child processes fill pipe buffers. |

### 2.2 Filesystem Semantics and Concurrency

| Aspect | Windows (NTFS / ReFS) | Linux (ext4 / xfs / overlayfs) | Agent Implementation Impact |
| :--- | :--- | :--- | :--- |
| **Case Sensitivity** | Case-insensitive, case-preserving | Strictly case-sensitive | Configuration paths and file event matching must preserve exact casing on Linux. |
| **File Locking** | Mandatory file locking by default (`FILE_SHARE_*` flags) | Advisory file locking (`fcntl`, `flock`) | On Windows, opening files without sharing flags blocks deletion and checkpointing. On Linux, deleting an open file unlinks it from the directory while existing descriptors stay valid until closed. |
| **External Interferences** | Windows Defender and Search Indexer actively lock newly created `.exe` and `.db` files | Inotify consumers and auditd typically do not hold exclusive locks | Windows tests require increased timeouts (e.g. 30s) to tolerate transient antivirus file inspection delays. |

### 2.3 Timing, Clocks, and Metric Sampling

| Aspect | Windows | Linux | Agent Implementation Impact |
| :--- | :--- | :--- | :--- |
| **Timer Resolution** | Default 15.6 ms quantum (reducible to 1 ms via multimedia timers) | Sub-millisecond high-resolution timers (`nanosleep`) | Governor tickers and measurement loops on Windows must account for up to 15.6 ms jitter per tick. |
| **Process CPU Accounting** | `GetProcessTimes` returns cumulative kernel/user times in 100ns units | `/proc/self/stat` reads `utime` + `stime` in clock ticks (`USER_HZ`) | Both implementations normalize CPU delta over wall clock elapsed and CPU core count, but Linux provider parses `/proc` while Windows uses system DLL calls. |
| **Memory Resident Set (RSS)** | `GetProcessMemoryInfo` -> `WorkingSetSize` | `/proc/self/status` -> `VmRSS` | Both measure resident physical memory, but Windows working set can be trimmed by the OS under system-wide memory pressure. |

### 2.4 SQLite Storage and WAL Checkpointing

| Aspect | Windows | Linux | Agent Implementation Impact |
| :--- | :--- | :--- | :--- |
| **Shared Memory (`-shm`)** | Win32 Memory Mapped Files (`MapViewOfFile`) | POSIX Shared Memory (`mmap`) | SQLite WAL mode requires robust file mapping. Windows requires `PRAGMA busy_timeout = 5000` to handle transient lock contention. |
| **Sync Barriers** | `FlushFileBuffers` | `fsync` / `fdatasync` | On industrial Linux systems with flash media, `PRAGMA synchronous = NORMAL` provides optimal performance without risking DB corruption on power loss. |

### 2.5 Event Ingestion and Collector Subsystems

| Aspect | Windows | Linux | Agent Implementation Impact |
| :--- | :--- | :--- | :--- |
| **Filesystem Monitoring** | `ReadDirectoryChangesW` | `inotify` / `fanotify` | Inotify has kernel queue limits (`max_queued_events`). The Linux collector must detect queue overflow (`IN_Q_OVERFLOW`) and initiate resynchronization. |
| **Process Execution Events** | Event Tracing for Windows (ETW) / Process Creation auditing | `auditd` / Netlink connector / eBPF | Cross-platform event normalization relies on CanonicalEvent schema, but collector capabilities differ by OS. |

---

## 3. Linux CI Status and Qualification Boundary

- **GitHub Actions Linux CI (`.github/workflows/ci.yml`)**:
  - Compiles and links against native `libyara_x_capi` v1.20.0 under `ubuntu-latest`.
  - Executes unit and integration test suites with `-race`.
  - Verifies database migrations and API worker startup.
- **Qualification Gap**:
  - `ubuntu-latest` is a high-spec cloud virtual machine (2-core / 7 GB RAM on modern Xeon/EPYC).
  - It does **not** represent an embedded Linux OT device (e.g. ARM Cortex-A53 or Intel Atom with slow eMMC flash storage).
  - Therefore, **Linux Qualification on target low-spec hardware remains OPEN**.

