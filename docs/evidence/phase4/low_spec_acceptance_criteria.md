# Target Low-Spec Hardware Acceptance Criteria and Qualification Specification

**Status**: Active Specification / Phase 4 Gate Requirement  
**Target Profile**: Constrained Industrial OT Edge Node / DIN-Rail Gateway  
**Hardware Baseline**: 2 vCPU, 2–4 GB RAM, Industrial Flash/eMMC  
**Development Host Status**: Intel Core i5-10300H @ 2.50GHz (8 threads, 16 GB RAM) — **SMOKE ONLY (EXTRAPOLATION STRICTLY PROHIBITED)**  

---

## 1. Objective and Anti-Extrapolation Rule

The RedCyberFox Edge Agent must operate reliably on constrained operational technology (OT) edge gateways without starving concurrent programmable logic controller (PLC) communication, SCADA data acquisition, or safety instrumentation systems.

> [!CRITICAL]
> **Anti-Extrapolation Rule**: Performance results obtained on multi-core developer workstations (such as the 8-thread Intel Core i5-10300H development machine used for smoke testing) **MUST NOT** be extrapolated to qualify low-spec field hardware. All acceptance criteria defined herein must be measured and verified directly on representative physical or precisely constrained containerized/virtual hardware.

---

## 2. Target Hardware Profiles

| Parameter | Profile A: Minimum Constrained Field Gateway | Profile B: Standard Substation Edge Node |
| :--- | :--- | :--- |
| **Form Factor** | Fanless DIN-rail industrial PC / IoT gateway | Rackmount industrial server / IPC |
| **CPU Architecture** | x86_64 or ARM64 (2 cores / 2 threads @ 1.5–2.0 GHz) | x86_64 (4 cores / 4 threads @ 2.4 GHz) |
| **Reference CPU** | Intel Atom® x6413E / N6210, ARM Cortex-A53 | Intel Core i3 / Xeon E3, ARM Cortex-A72 |
| **System RAM** | 2048 MB (2 GB) total system memory | 4096 MB (4 GB) total system memory |
| **Storage Medium** | Industrial eMMC 5.1 (pSLC mode) or SATA DOM | NVMe or Industrial SATA SSD |
| **Operating System** | Embedded Linux (Debian 12 / Yocto / Alpine), Linux RT | Windows 10/11 IoT Enterprise, Ubuntu 24.04 LTS |

---

## 3. Measurable Acceptance Thresholds

The following quantitative thresholds define the mandatory pass/fail criteria for Phase 4 qualification on target hardware:

### 3.1 Resource Utilization Thresholds

| Metric | Threshold Limit | Measurement Method | Failure Consequence |
| :--- | :--- | :--- | :--- |
| **Steady-State CPU** | $\le 5.0\%$ of 2 cores (avg over 5 min at 20 eps) | OS process CPU times (`GetProcessTimes` / `/proc/self/stat`) | Fails Qualification; risk of starving field bus polling |
| **Burst Peak CPU** | $\le 20.0\%$ peak process CPU (at 100 eps for 30s) | Instantaneous sampling at 1s intervals | Fails Qualification; triggers premature governor SAVER mode |
| **Resident Memory (RSS)** | $\le 100 \text{ MB}$ steady-state and burst peak | `WorkingSetSize` (Windows) / `VmRSS` (Linux) | Fails Qualification; risk of OOM-killer termination on 2GB hosts |
| **Storage Growth Rate** | $\le 10 \text{ MB/hour}$ sustained at 20 eps | Sum of regular files (`agent.db` + WAL + SHM) | Fails Qualification; rapid storage exhaustion |
| **Write I/O Rate** | $\le 500 \text{ KB/second}$ sustained write rate | Process I/O transfer counters | Fails Qualification; excessive flash wear on eMMC media |

### 3.2 Operational Durability and Pipeline Thresholds

| Metric | Threshold Limit | Measurement Method | Failure Consequence |
| :--- | :--- | :--- | :--- |
| **Queue Saturation** | $< 50\%$ sustained under 20 eps; $< 85\%$ during burst | In-memory ring buffer depth / capacity | Fails Qualification; buffer overrun risk |
| **Event Loss** | **Strictly 0 events dropped** during normal/burst | Ingestion-outcome reconciliation | **BLOCKING DEFECT** (loss of critical security telemetry) |
| **Ingestion Reconciliation** | `reconciliation_valid: true` (100% accounting match) | `committed_total == ingested_total - failed_before_commit` | **BLOCKING DEFECT** (unaccounted telemetry leak) |
| **Detection Latency (YARA-X)** | $\le 50 \text{ ms}$ average latency per 1 MB artifact | Time from commit to finding persistence | Fails Qualification; delayed response to threat indicators |
| **Offline Recovery Rate** | $\ge 50 \text{ eps}$ drain rate upon reconnect | Forwarder backlog draining throughput | Fails Qualification; inability to catch up after outage |

---

## 4. Mandatory Workload Scenarios on Target Hardware

Before Phase 4 can be declared complete, the following test scenarios must be executed and logged on physical/containerized target hardware matching Section 2:

1. **Extended Idle Scenario (1 Hour)**:
   - Zero telemetry ingestion.
   - Assert: Average CPU $< 0.5\%$, RSS growth $< 1 \text{ MB}$, zero unexpected I/O.
2. **Sustained Load Scenario (2 Hours)**:
   - Constant 20 eps mixed telemetry (process, network, file events).
   - Assert: Steady-state CPU $\le 5\%$, RSS $\le 80 \text{ MB}$, zero queue saturation episodes, zero event loss.
3. **Burst Stress Scenario (15 Minutes)**:
   - 10 bursts of 200 eps lasting 10 seconds each, spaced by 60-second recovery intervals.
   - Assert: Peak CPU $\le 20\%$, queue saturation $< 85\%$, recovery to NORMAL state within 30 seconds.
4. **Extended Offline & Reconnect Scenario (4 Hours)**:
   - Gateway disconnected for 2 hours while ingesting 10 eps (accumulating 72,000 events in local SQLite).
   - Gateway reconnected; monitor drain rate.
   - Assert: Zero dropped events; complete backlog drain without WAL file explosion; 100% reconciliation match.
5. **Positive Detection Stress Scenario (30 Minutes)**:
   - 5 eps positive detection triggers with native YARA-X scanning.
   - Assert: 100% finding generation and durability; no memory leak in CGO/native scanner boundary.

---

## 5. Qualification Gate Status

| Scenario | Development Workstation (i5-10300H) | Target Hardware (2 vCPU / 2-4 GB) | Gate Status |
| :--- | :--- | :--- | :--- |
| **Idle** | Executed (3s smoke test, 0% CPU, 22 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Sustained** | Executed (4s smoke test, 0.46% CPU, 21 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Burst** | Executed (4s smoke test, 1.09% CPU, 25 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Offline** | Executed (3s smoke test, 0% CPU, 22 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Reconnect** | Executed (11s smoke test, 0.28% CPU, 22 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **YARA-X Detection** | Executed (30-event comparative benchmark, 1.17ms avg) | **NOT EXECUTED** | **OPEN** |

**Conclusion**: While workstation smoke testing proves architectural correctness and algorithmic bounds, **Target Low-Spec Hardware Qualification remains an OPEN gate**. Phase 4 cannot be closed until physical hardware testing is executed.

