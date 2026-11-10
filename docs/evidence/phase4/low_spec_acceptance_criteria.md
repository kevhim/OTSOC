# Proposed Low-Spec Hardware Acceptance Criteria and Qualification Specification

**Status**: Proposed Acceptance Criteria / Phase 4 Gate Specification  
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

## 3. Proposed Acceptance Criteria (Target Engineering Baselines)

The following quantitative thresholds define the proposed pass/fail criteria for Phase 4 qualification on target low-spec hardware. Each budget is established with an explicit engineering rationale based on industrial OT operational constraints:

### 3.1 Resource Utilization Criteria

| Metric | Proposed Threshold | Measurement Boundary | Engineering Rationale |
| :--- | :--- | :--- | :--- |
| **Steady-State CPU** | $\le 5.0\%$ of 2 cores (avg over 5 min at 20 eps) | OS process CPU times (`GetProcessTimes` / `/proc/self/stat`) | Prevents interference with concurrent real-time PLC communication and SCADA polling (Modbus RTU/TCP, DNP3, EtherNet/IP) which operate on 50–100ms cycle times on 2-core IPCs. |
| **Standard Burst Peak CPU** | $\le 20.0\%$ peak process CPU (at 100 eps for 30s) | Instantaneous sampling at 1s intervals | Leaves 80% CPU headroom for safety instrumentation systems and OS housekeeping during incident bursts, avoiding premature governor SAVER mode. |
| **Stress Burst Peak CPU** | $\le 30.0\%$ peak process CPU (at 200 eps for 10s) | Instantaneous sampling at 1s intervals | Ensures extreme event spikes do not exhaust scheduling slots, allowing recovery to normal state within 30 seconds. |
| **Resident Memory (RSS)** | $\le 100 \text{ MB}$ steady-state and burst peak | `WorkingSetSize` (Windows) / `VmRSS` (Linux) | On 2 GB RAM industrial nodes where the OS and SCADA runtime occupy 1.2–1.5 GB, a 100 MB budget prevents triggering the Linux `oom-killer` or Windows paging thrashing. |
| **Storage Growth Rate** | $\le 10 \text{ MB/hour}$ sustained at 20 eps | Sum of regular files (`agent.db` + WAL + SHM) | Prevents rapid exhaustion of local disk partitions (often limited to 4–8 GB dedicated storage) during extended multi-hour network disconnects. |
| **Write I/O Rate** | $\le 500 \text{ KB/second}$ sustained write rate | Process I/O transfer counters | Protects industrial eMMC / pSLC flash storage from premature cell endurance wear-out over an expected 7–10 year operational lifecycle. |

### 3.2 Operational Durability and Pipeline Criteria

| Metric | Proposed Threshold | Measurement Boundary | Engineering Rationale |
| :--- | :--- | :--- | :--- |
| **Queue Saturation** | $< 50\%$ sustained under 20 eps; $< 85\%$ during burst | In-memory ring buffer depth / capacity | Prevents queue overrun and ensures headroom for bursty collector notifications. |
| **Event Loss** | **Strictly 0 events dropped** during normal/burst | Ingestion-outcome reconciliation | Essential for high-integrity security audit logging in OT environments. |
| **Ingestion Reconciliation** | `reconciliation_valid: true` (100% accounting match) | Complete equation: $\text{Ingested} = \text{Committed} + \text{Failed} + \text{DLQ} + \text{Spilled} + \text{Dropped}$ | Mathematically accounts for every admitted event, preventing unaccounted telemetry leaks. |
| **Detection Latency (Event Scan)** | $\le 5 \text{ ms}$ average latency per event | Time from event commit to rule evaluation complete | Real-time threat detection on telemetry streams without backpressuring the ingestion pipeline. |
| **Detection Latency (Artifact Scan)**| $\le 50 \text{ ms}$ average latency per 1 MB artifact | Time for full YARA-X rule scan of 1 MB binary | Accounts for absence of AVX2/AES hardware acceleration on low-power Intel Atom and ARM Cortex-A53 processors. |
| **Offline Recovery Rate** | $\ge 50 \text{ eps}$ drain rate upon reconnect | Forwarder backlog draining throughput | Ensures rapid backlog drainage after network restoration without starving ongoing event capture. |

---

## 4. Mandatory Workload Scenarios on Target Hardware

Before Phase 4 can be declared complete, the following test scenarios must be executed and logged on physical or container-constrained target hardware matching Section 2:

1. **Extended Idle Scenario (1 Hour)**:
   - Zero telemetry ingestion.
   - Assert: Average CPU $< 0.5\%$, RSS growth $< 1 \text{ MB}$, zero unexpected I/O.
2. **Sustained Load Scenario (2 Hours)**:
   - Constant 20 eps mixed telemetry (process, network, file events).
   - Assert: Steady-state CPU $\le 5\%$, RSS $\le 80 \text{ MB}$, zero queue saturation episodes, zero event loss.
3. **Burst Stress Scenario (15 Minutes)**:
   - 10 standard bursts of 100 eps lasting 30 seconds each, spaced by 60-second recovery intervals.
   - Assert: Peak CPU $\le 20\%$, queue saturation $< 85\%$, recovery to NORMAL state within 30 seconds.
4. **Extreme Stress Burst Scenario (5 Minutes)**:
   - 3 extreme bursts of 200 eps lasting 10 seconds each, spaced by 60-second recovery intervals.
   - Assert: Peak CPU $\le 30\%$, zero unhandled event drops.
5. **Extended Offline & Reconnect Scenario (4 Hours)**:
   - Gateway disconnected for 2 hours while ingesting 10 eps (accumulating 72,000 events in local SQLite).
   - Gateway reconnected; monitor drain rate.
   - Assert: Zero dropped events; complete backlog drain without WAL file explosion; 100% reconciliation match.
6. **Positive Detection Stress Scenario (30 Minutes)**:
   - 5 eps positive detection triggers with native YARA-X scanning.
   - Assert: 100% finding generation and durability; no memory leak in CGO/native scanner boundary.

---

## 5. Qualification Gate Status

| Scenario | Development Workstation (i5-10300H) | Target Hardware (2 vCPU / 2-4 GB) | Gate Status |
| :--- | :--- | :--- | :--- |
| **Idle** | Executed (3s smoke test, 0% CPU, 22 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Sustained** | Executed (4s smoke test, 0.46% CPU, 21 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Standard Burst** | Executed (4s smoke test, 1.09% CPU, 25 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Offline** | Executed (3s smoke test, 0% CPU, 22 MB RSS) | **NOT EXECUTED** | **OPEN** |
| **Reconnect** | Executed (22s smoke test with native retry recovery) | **NOT EXECUTED** | **OPEN** |
| **YARA-X Detection** | Executed (30-event component benchmark, 1.17ms avg) | **NOT EXECUTED** | **OPEN** |

**Conclusion**: While workstation smoke testing proves architectural correctness and algorithmic bounds, **Target Low-Spec Hardware Qualification remains an OPEN gate**. Phase 4 cannot be closed until physical hardware testing is executed.
