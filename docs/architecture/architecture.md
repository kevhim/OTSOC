# OT SOC Consolidated Technical Architecture & Feature / Function Specification

Master architecture combining the original endpoint/XDR foundation, the OT-native evolution, and the subsequent standards, resilience, safety, visibility, supply-chain, and validation improvements.

**Architectural position:** Offline-first. Passive-first. Safety-first. Deterministic detection. Standards-aligned segmentation. Measurable visibility. Self-hostable. No paid cloud dependency required for the core system.

- **Version:** Master architecture baseline V6 - rural / low-spec implementation roadmap
- **Status:** Implementation-ready design; exact versions and benchmark thresholds require target-environment validation
- **Primary scope:** Rural, small and regional sites with legacy/low-spec devices; self-hosted OT and mixed IT/OT environments, with a scale-out path for larger deployments
- **Core differentiation:** Endpoint + passive OT network fusion + asset/zone/process context + industrial protocol awareness + offline operation
- **Standards posture:** NIST SP 800-82 alignment; IEC 62443 evidence/support; Enterprise and ICS ATT&CK coverage

---

## 1. Executive Summary
The RedCyberFox OT SOC is a security platform for environments where cyber events can affect industrial or physical processes. It combines lightweight host telemetry with passive OT network sensing, structured asset/zone/process context, deterministic detection, threat intelligence, behavioral baselines, cross-source correlation, explainable risk scoring, and safety-gated response.

**Simple definition:** See what is happening, understand what the device/process is, compare activity with approved relationships and normal behavior, detect suspicious changes, correlate the evidence, explain the risk, and alert the right human - while remaining useful when the WAN or Internet is down.

### 1.1 Architecture evolution
| Generation | Center of gravity | Key limitation | Current position |
|------------|-------------------|----------------|------------------|
| V1 | Endpoint XDR/SIEM | No true OT network layer | Retained as endpoint foundation |
| V2 | OT-native pivot | Needed deeper standards/safety/resilience treatment | Passive OT sensing + asset graph introduced |
| V3 | OT-native refinement | Still missing some standards / DR / validation depth | Strong architecture baseline |
| Master | OT-native, standards-backed | Implementation and field validation remain | Architecture + full feature/function specification |

---

## 2. Design Principles and Constraints

### 2.1 Non-negotiable principles
- **Passive before active:** PLCs, SIS, field devices and fragile OT equipment are observed rather than instrumented wherever possible.
- **Production safety is a hard constraint:** monitoring failure must fail to visibility, not fail to production.
- **Offline is normal:** local rule cache, local detections, SQLite/WAL and store-and-forward are first-class.
- **Context before severity:** asset criticality, safety class, zone, conduit, role, maintenance window, novelty and peers influence risk.
- **Deterministic real-time core:** YARA-X, Sigma-compatible compiled rules, Zeek, Suricata, OT protocol rules, segmentation rules and compact baselines.
- **Open interfaces:** Valkey sits behind EventBus; telemetry storage sits behind a storage abstraction.
- **Visibility honesty:** serial/non-IP gaps, parser limitations and collector failures are represented explicitly.
- **Compliance honesty:** the product supplies evidence and verification; it does not grant a customer security level.
- **Edge-light, central-heavy:** low-spec sites run only the collection, local detection and buffering they need; expensive analytics and long-term storage stay centralized.
- **Graceful degradation:** under CPU, RAM, disk, battery or network pressure, low-priority work backs off before critical security telemetry.

### 2.2 Constraints
- No paid service is required for core central security functions.
- Central services can be self-hosted on Linux.
- OT connectivity may be intermittent, asymmetric, or outbound-only.
- Legacy systems must be supported without aggressive scanning.
- Disruptive OT response requires policy gates and human approval by default.
- Customer sites may contain old Windows/Linux hardware with limited RAM, slow storage and unreliable WAN links.

---

## 3. System Architecture

### 3.1 Logical planes
| Plane | Responsibilities | Core components |
|-------|------------------|-----------------|
| Edge / Sensor | Collection, local detection, protocol parsing, buffering, health | Go agent, Zeek, Suricata, SQLite |
| Data | Authentication, normalization, queuing, correlation, enrichment | Ingestion API, EventBus/Valkey, workers |
| Context | Assets, relationships, zones, conduits, processes, maintenance | PostgreSQL security graph |
| Intelligence | IOC feeds, MISP, rule authoring/compilation, promotion | MISP, feed importers, rule compiler |
| Control | Provisioning, certificates, policy, updates | step-ca, signed artifacts, provisioning service |
| Response | Incident lifecycle, escalation, approvals, adapters | Incident manager, response policy engine |
| Trust / Observability | Audit, metrics, logs, backups, SBOM/provenance | Prometheus/Grafana/Loki or equivalent |

### 3.2 End-to-end path
```
Endpoint / OT traffic -> local collection / passive capture -> local detection + normalization ->
authenticated ingestion -> Valkey Streams -> detection / context / intel -> correlation -> risk -> incident
-> PostgreSQL + telemetry tier -> dashboard / notifications

Control path: rule/intel source -> compiler/bundler -> signed artifact -> distribution -> verify -> stage ->
self-test -> activate -> rollback on failure

Identity path: offline root -> private CA -> device certificate -> mTLS -> rotation/revocation
```
**Invariant:** no external notification channel, cloud API, LLM, or central server may be required for local detection to continue.

---

## 4. Deployment Models

### 4.1 What "edge" means in this architecture
Edge means the customer's local site where the protected devices and local sensors are located. Edge components should collect telemetry, run lightweight local detections, buffer data and continue operating during WAN outages. The central SOC performs heavier cross-device correlation, long-term analytics, threat-intelligence processing and fleet management.

```
RURAL SITE / EDGE CENTRAL SOC
----------------- -----------------------------
Old Windows/Linux PC PostgreSQL
 -> Go agent ClickHouse (P1)
 -> local detection Valkey
 -> SQLite/WAL MISP / threat intel
 -> store-and-forward Correlation / risk
 Dashboard / reports
Optional OT Sensor Fleet / rule management
 -> Zeek + Suricata
 -> OT protocol parsing
 -> local buffer

Internet is a synchronization path, not a prerequisite for local security.
```

### 4.2 Rural / low-spec deployment profiles
| Profile | Typical site | Local software | Design objective |
|---------|--------------|----------------|------------------|
| A - Endpoint-only | 5-50 old PCs / servers | Go agent, SQLite, YARA-X, compiled behavior rules | Minimum CPU/RAM/disk overhead |
| B - Endpoint + OT sensor | 20-150 endpoints/assets + small OT network | Profile A + dedicated Zeek/Suricata sensor | Passive OT visibility without loading legacy PCs |
| C - Multi-site regional | Several rural sites | Edge collectors + central analytics | Centralize heavy computation and cross-site correlation |

### 4.3 Resource governor
```
Resource state = NORMAL | RESOURCE-SAVER | EMERGENCY

NORMAL
- Full lightweight monitoring
- Scheduled scans when idle
- Normal event detail

RESOURCE-SAVER
- Defer deep scans
- Reduce low-priority telemetry
- Increase batching and deduplication
- Keep P0/P1 detections intact

EMERGENCY
- Preserve only critical security telemetry
- Disable expensive scheduled work
- Preserve evidence and health signals
- Return automatically when pressure clears
```

### 4.4 Placement rules
- Use TAP/SPAN for passive sensor capture.
- Separate sensor capture and management/export interfaces when practical.
- Agents initiate outbound HTTPS only.
- Prefer IDMZ/jump-host capture points for legitimate IT-to-OT paths.
- Never make monitoring inline unless specifically engineered and approved.
- Do not deploy heavy central databases or CTI platforms on old customer PCs; keep them centralized.

### 4.5 Edge failure behavior
| Failure | Expected behavior |
|---------|-------------------|
| Internet/WAN down | Local detection continues; queue persists; sync resumes later |
| Central SOC unavailable | Edge keeps collecting/detecting and stores events locally |
| Disk pressure | Aggregate low-value telemetry; preserve critical evidence |
| CPU pressure | Enter resource-saver/emergency mode; never disable critical detections first |
| Sensor process failure | Production traffic remains unaffected; emit visibility-health alert when reporting returns |

---

## 5. Endpoint Agent
The Go agent is installed on agentable Windows/Linux hosts such as engineering workstations, HMIs, SCADA servers, historians, jump hosts and site IT. It is not the default mechanism for PLC/SIS monitoring.

### 5.1 Local data
```
events(event_id, device_id, seq_no, occurred_at, received_at, type, payload, rule_version, sync_state)
sync_queue(event_id, attempt_count, next_retry_at, last_error)
dead_letter_queue(event_id, reason, first_seen, last_attempt)
agent_config(device_id, tenant_id, certificate_id, policy_version, last_rule_version)
rules_cache(bundle_version, hash, signature, installed_at, status)
asset_snapshot(asset_id, software_inventory, interfaces, timestamps)
baseline_state(asset_id, metric_key, window, counters, updated_at)
```

---

## 6. Passive OT Network Sensor
**Safety requirement:** sensor failure must leave production traffic unaffected. No inline blocking or injection is part of the default architecture.

### 6.1 Data reduction
```
Raw packets -> local protocol/flow parsing -> security-relevant events -> central SOC
Targeted PCAP is evidence for selected incidents; the SOC is not an unlimited packet archive.
```

---

## 7. Industrial Protocol Coverage and Visibility
**Do not overclaim:** an Ethernet sensor cannot automatically see RS-485, PROFIBUS or fieldbus traffic. The dashboard must expose these blind spots.

---

## 8. Asset / Zone / Conduit / Process Model
### 8.1 Intended vs observed
```
INTENDED
Engineering ->[approved conduit, peers, protocols]-> Control

OBSERVED
Engineering ->[new peer, unexpected write]-> Control

RECONCILE
missing conduit / reverse direction / IDMZ bypass / new peer -> finding
```
The security graph is logical and should remain in PostgreSQL. A graph database is deferred until measured workload demands it.

---

## 9. Purdue Model and IEC 62443 Formalization
**Terminology discipline:** 1S and L3.5 are internal alignment conventions/extensions, not official Purdue level names. Purdue is used to express segmentation intent; the observed graph represents actual topology.

The platform supports evidence and verification. It does not confer an asset-owner Security Level.

---

## 10. SIS and Safety Monitoring
**Safety rule:** SIS and other safety-related systems are modeled separately. Monitoring observes access and visibility; it does not become a control authority.

### 10.1 Example
```
ENG-04 -> SIS-01
new peer + control-system protocol + outside maintenance window
= Critical incident candidate
```

---

## 11. Detection Engine
The detection engine is layered so cheap/high-confidence decisions execute first and expensive analysis is reserved for events that justify it.

### 11.1 Sigma decision
**Use Sigma as an authoring/distribution language where appropriate; compile a supported subset into target-specific evaluators.** Do not make the endpoint a full Sigma engine unless a measured need justifies the complexity.

---

## 12. ATT&CK Coverage Model
Use both Enterprise ATT&CK and ATT&CK for ICS. The current live ICS matrix exposes 12 tactics and the techniques catalog lists 79 ICS techniques. Coverage status is measured from tests rather than claimed globally.

---

## 13. Behavioral Baselines
Start with explainable statistics rather than heavy ML. Baselines are compact enough to execute locally and centrally.

### 13.1 Backpressure-aware handling
```
Normal -> preserve detail
Burst -> aggregate repetitive low-value events
Critical -> never sample away; preserve event, count, timestamps, evidence
```

---

## 14. Event Model and Ingestion
### 14.1 Canonical event envelope
```json
{
 event_id,
 tenant_id,
 site_id,
 asset_id,
 sensor_id,
 occurred_at,
 received_at,
 seq_no,
 source,
 category,
 severity,
 confidence,
 protocol,
 src, dst,
 action,
 metadata,
 rule_id,
 rule_version,
 attck_enterprise[],
 attck_ics[],
 quality_flags[],
 schema_version
}
```

---

## 15. Correlation and Risk Engine
### 15.1 Risk factors
```python
risk = normalize(
 base_severity
 + asset_impact
 + safety_weight
 + segmentation_violation
 + confidence
 + novelty
 + temporal_correlation
 + threat_intel
)
```
Risk weights must be calibrated through testing. Every score must expose its contributors and be reproducible from stored evidence.

---

## 16. Incident and Response Orchestration
**Default:** detection is automatic; disruptive OT response is not.

---

## 17. Threat Intelligence
MISP can serve as the self-hosted structured intelligence hub. Feed importers normalize approved public/community intelligence into the internal model.

### 17.1 Privacy pipeline
```
customer telemetry -> strip tenant/path/topology identifiers -> feature extraction -> aggregation -> minimum
threshold -> shared intel object
```
Cross-client intelligence is opt-in and policy controlled. Rare values can identify a customer even without a tenant_id.

---

## 18. Control Plane, PKI, and Updates
### 18.2 Update pipeline
```
offline root -> online signer -> signed artifact -> version/hash/metadata -> verify -> anti-rollback -> stage
-> self-test -> activate -> health check -> rollback if failed
```

---

## 19. Storage Architecture
Use hot, historical and evidence tiers. Raw PCAP is targeted evidence, not an infinite archive.

---

## 20. Audit, Observability, and Sensor Health
### 20.1 Tamper-evident audit chain
```
hash_n = SHA256(canonical(event_n) || hash_(n-1))
periodic signed checkpoint
verification during audit/restore
```

---

## 21. Backup, Disaster Recovery, and High Availability
### 21.1 HA philosophy
Single-node is a deployment phase, not a design ceiling. API and workers scale horizontally; database and event-store resilience are added when operational requirements justify them.

---

## 25. Performance, Capacity, and Backpressure
### 25.1 Priority classes
```
P0 Active safety/control manipulation
P1 Confirmed compromise / critical asset
P2 Strong anomaly / policy violation
P3 Suspicious behavior
P4 Routine telemetry

Overload policy:
- never sample P0/P1
- aggregate repetitive low-value events
- preserve first_seen, last_seen, count, and representative evidence
- enter resource-saver/emergency mode on constrained endpoints
```

### 25.3 Benchmarking policy
All performance values are engineering targets, not unconditional claims. Acceptance tests must use representative old-hardware profiles (for example, low-RAM Windows/Linux endpoints, slow disks and constrained WAN links) and record CPU, RAM, disk I/O, event loss, battery impact where relevant, queue age and recovery time.

**Do not optimize by deleting detection:** optimize by local parsing, event reduction, prioritized queues, adaptive resource modes and tiered correlation.

---

## 26. Offline Operations
### 26.2 Reconnect
```
reconnect -> authenticate -> compare sequence ranges -> upload priority batches -> reconcile gaps -> fetch
signed rules -> verify -> stage -> activate -> clear acknowledged queue -> report health
```

---

## 27. Testing, Validation, and Reproducible Attack Lab
### 27.1 Killer demonstration chain
```
USB insertion
 -> unknown binary
 -> new process
 -> first-seen peer
 -> engineering workstation -> PLC
 -> Modbus write burst
 -> baseline anomaly
 -> zone/conduit finding
 -> IOC match
 -> correlated incident
 -> explainable risk
 -> R3 human approval
```
**Goal:** prove the whole pipeline with reproducible lab inputs, not only mocked alerts.

---

## 28. OT Honeypot / Early Warning
**Production warning:** do not place a research honeypot into a production control path without explicit engineering approval.

---

## 30. Open-Source Technology Matrix
**Excluded from core:** Grafana OnCall is not used; MinIO is not a dependency. The architecture uses a native escalation service and an object-storage abstraction so implementation choices can follow maintained open-source projects.

---

## 31. Implementation Roadmap - Free to Working Stage
**Implementation goal:** reach a genuinely working RedCyberFox Alpha using only free/open-source software and existing development hardware. Do not attempt the entire architecture at once. Build a vertical slice first, then add OT capability, then harden it.

### 31.1 Development rule
**Working product before enterprise infrastructure.** The first release is not expected to run ClickHouse, MISP, HA databases, full PKI, or every OT protocol. Those remain architecture-approved expansion paths. The first objective is a low-spec endpoint agent + central SOC + passive OT sensor that can detect, correlate, alert, survive a WAN outage, and demonstrate the complete attack-to-incident path.

### 31.2 What counts as "working"
```
WORKING ALPHA DEFINITION
1. Agent installs on a representative older Windows/Linux machine.
2. Agent detects at least one real security event locally.
3. Agent stores events during WAN outage.
4. Agent reconnects and reconciles sequence ranges without duplication.
5. Central API receives and persists events.
6. Dashboard displays alerts and incidents.
7. Passive OT sensor observes simulated industrial traffic.
8. Asset graph records devices and normal peers.
9. At least one OT protocol anomaly is detected.
10. Endpoint + OT events correlate into one incident.
11. Risk score is explainable from stored factors.
12. Alert reaches a free notification channel.
13. Sensor/agent failure does not affect production traffic.
14. Low-spec/resource-saver tests pass.
15. One complete adversary scenario is repeatable from a clean lab reset.
```

### 31.3 Intentionally deferred until after Alpha
- ClickHouse at scale, MISP at production depth, HA database clusters and multi-region deployment.
- Full TUF role separation, TPM-backed identity and advanced secret-management infrastructure.
- Large protocol catalogues and specialized utility-sector parsers.
- Complex automated OT response.
- LLM-assisted analyst features.

### 31.4 Development cost model
- **Software licensing:** ₹0 for the core development stack using open-source/free tools.
- **Cloud hosting:** ₹0 initially - run central services locally in Docker and use LAN/VM simulation.
- **Threat intelligence:** Use free/open feeds and self-hosted MISP where needed; never make a paid feed a core dependency.
- **Hardware:** Use existing PCs/VMs for Alpha. Physical OT sensor hardware is a later field-deployment cost, not an Alpha prerequisite.
- **Testing:** Use simulated PLC/OT traffic, PCAP replay and isolated VMs before touching real control environments.

**Rule for the team:** every new subsystem must justify itself with a demonstrated product requirement. Do not add infrastructure merely because it appears in the long-term architecture.

---

## 32. Acceptance Criteria
- L4-to-L1 direct communication without approved path triggers structural finding with protocol parsers disabled.
- Observed zone pair without conduit appears as unsanctioned flow and can be excepted without deleting observation.
- Dual-homed host is detected from passive observation.
- Unauthorized safety-zone ingress becomes Critical.
- Offline agent continues local detection and reconciles sequence ranges after reconnect.
- Invalid or rollback signed bundle is rejected.
- Database restore drill recovers state and audit chain.
- Audit chain verification detects tampering.
- Sensor failure creates visibility alert without affecting production traffic.
- Risk explanation is reproducible from stored factors.
- End-to-end lab scenario reaches R3 approval gate.
- ATT&CK Navigator coverage is evidence-backed, not blanket.
- Serial/non-IP blind spots are visible in the dashboard.
- P0 architecture has no required paid SaaS dependency.
- Endpoint agent passes low-spec benchmark profiles without violating resource budgets defined for the target hardware class.
- Resource-saver/emergency modes preserve P0/P1 telemetry during CPU, disk and WAN stress.
- A rural site can operate through a representative WAN outage and reconcile backlog without losing sequence integrity.

---

## 33. Known Gaps and Non-goals
### 33.1 Visibility gaps
- Non-IP serial fieldbus visibility requires specialized taps or gateway telemetry.
- Deep IEC 61850 GOOSE/Sampled Values coverage requires dedicated parser/test maturity.
- Cloud/IIoT and converged networks can violate Purdue layering; observed graph remains authoritative.

### 33.2 Rural / low-spec assumptions
- The smallest sites do not run the full central stack locally.
- OT packet inspection is delegated to a dedicated sensor so old endpoints remain lightweight.
- Exact minimum hardware requirements are defined by benchmark profiles, not guessed globally.

### 33.3 Non-goals
- Replacing PLC/SIS safety logic or plant firewall enforcement.
- Claiming IEC 62443 certification or Security Level achievement.
- Default active scanning inside control networks.
- LLM-based authoritative real-time decisions.
- Dependence on a paid notification provider.
- Large distributed architecture without measured operational need.

### 33.4 Implementation caveat
Exact parser depth, package versions, performance ceilings, cryptographic key ceremonies, and sector-specific semantics are engineering acceptance items. They must be validated in the target environment before becoming production claims.
