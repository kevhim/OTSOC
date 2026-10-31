# RedCyberFox — Commercial-Safe Open-Source Architecture Plan

**Status:** Architecture update / pre-implementation policy  
**Date:** 2026-10-04  
**Applies to:** Phase 4 onward, especially Phase 6 Passive OT Sensor and later response integrations

> **Important:** This document is an engineering architecture and release-planning policy, not legal advice. Before a commercial release, obtain a qualified software/IP license review for the exact release artifact, third-party notices, rules, feeds, trademarks, and distribution model.

---

## 1. Purpose

RedCyberFox is intended to become a self-hostable, offline-first security platform that can provide:

- lightweight endpoint IDS;
- local detection and YARA-X scanning;
- guarded host-firewall / IPS-style response;
- passive OT/network IDS through a dedicated sensor;
- central correlation, risk, incident handling, and SOC presentation.

The project must remain suitable for constrained environments, including low-end endpoints such as approximately **4 GB RAM / dual-core CPU / HDD** systems.

The endpoint agent should remain **edge-light**. Heavy packet inspection belongs on a dedicated OT/network sensor rather than every endpoint.

The commercial architecture must also minimize licensing surprises by preferring permissive dependencies and by treating **software code, rules/signatures, threat-intelligence feeds, documentation, trademarks, and cloud services as separate licensing subjects**.

---

## 2. Architectural Policy

### 2.1 Endpoint

The low-end endpoint is responsible for:

- process telemetry;
- filesystem telemetry;
- host/network telemetry;
- USB telemetry;
- local YARA-X detection;
- local policy evaluation;
- durable SQLite storage;
- offline store-and-forward;
- resource governance;
- guarded native firewall response where supported.

The endpoint **must not** run a full packet-inspection stack by default.

### 2.2 Dedicated OT / Network Sensor

The dedicated sensor is responsible for:

- TAP/SPAN packet capture;
- passive network IDS;
- protocol-aware network analysis;
- flow metadata;
- OT protocol visibility;
- Suricata / Zeek integration where benchmark results justify it.

The sensor is a different deployment profile from the low-end endpoint.

### 2.3 Central SOC

The central tier remains responsible for:

- heavy correlation;
- risk scoring;
- incident aggregation;
- search;
- long-term storage;
- SOC dashboards;
- cross-endpoint / cross-sensor analysis.

---

# 3. Open-Source Component Decision Matrix

The following decisions apply to the candidate projects reviewed for future RedCyberFox work.

| Project | License posture | Intended RedCyberFox role | Low-end endpoint fit | Commercial architecture decision |
|---|---|---|---|---|
| **YARA-X** | BSD-3-Clause | Local signature detection | **Excellent** | **Already adopted** |
| **Zeek** | BSD-3-Clause | Passive OT/network sensor | Medium–High depending on traffic | **Adopt for dedicated sensor** |
| **nftables** | GPL-2.0 userspace / Linux ecosystem | Linux host firewall enforcement | **Excellent as OS facility** | **Adopt through host/system interface; do not embed unnecessarily** |
| **iptables** | GPL-2.0-only | Legacy Linux firewall compatibility | Excellent | **Compatibility only** |
| **ipset** | GPL-family/netfilter | Legacy IP-set handling | Excellent | **Do not build core architecture around it** |
| **Suricata** | GPL-2.0 source | Passive/inline network IDS/IPS sensor | Heavy relative to endpoint agent | **Adopt only on dedicated sensor; commercial distribution requires GPL compliance or OISF licensing** |
| **Snort 3** | GPL-2.0 source | Alternative network IDS/IPS | Heavy relative to endpoint agent | **Do not use alongside Suricata; keep out of core plan** |
| **CrowdSec Security Engine** | MIT | Optional behavioral/reputation integration | Potentially good | **Optional; benchmark before endpoint use; do not assume CrowdSec data is freely redistributable** |
| **Fail2ban** | GPL-2.0-or-later | Log-based banning | Lightweight enough | **Do not make core dependency; RedCyberFox already owns broader detection/policy flow** |
| **Maltrail engine** | MIT | Optional lightweight IOC network sensor | Potentially suitable | **Optional only** |
| **Maltrail Trails** | Separate data terms | Threat-intelligence content | N/A | **Do not bundle/use commercially without a separately verified license** |
| **Unbound** | BSD-3-Clause | Optional local validating DNS resolver | Good | **Optional; only add if DNS use case justifies it** |
| **OPNsense** | BSD-2-Clause core, plus bundled components | Full firewall OS | Poor fit for endpoint | **Do not base RedCyberFox on OPNsense** |
| **ET Open rules** | Current OISF source index identifies license as MIT | Suricata detection rules | N/A | **Good candidate, but pin exact release and retain provenance/license record** |
| **abuse.ch feeds** | Feed-specific terms | IOC / threat intelligence | N/A | **Use feed-by-feed; commercial/API rights must be verified before product inclusion** |

---

# 4. Preferred Commercial-Safe Choices

## Tier A — Preferred

### YARA-X
Already used by RedCyberFox.

Use for local file/signature matching.

YARA-X is BSD-3-Clause licensed, making it a strong fit for proprietary commercial software with normal notice preservation.

Source:
https://github.com/VirusTotal/yara-x

### Zeek
Use for the dedicated passive network/OT sensor.

Zeek's project explicitly states that its BSD license does not restrict integrating or bundling Zeek in commercial products, while its name/logo remain subject to trademark rules.

Source:
https://zeek.org/faq/

### Unbound
Use only where a local validating/caching DNS resolver materially improves the architecture.

The Unbound repository is BSD-3-Clause licensed.

Source:
https://github.com/NLnetLabs/unbound

### nftables
Use as the Linux host's native firewall enforcement mechanism.

Do not copy nftables implementation into the RedCyberFox proprietary binary unless there is a specific architectural reason.

Use a RedCyberFox abstraction such as:

```text
FirewallProvider
    └── LinuxNftablesProvider
```

and communicate with the host's installed firewall facility.

Source:
https://netfilter.org/licensing.html

---

# 5. Conditional / Special Handling

## Suricata

Suricata is technically one of the best fits for the dedicated OT/network sensor, but it is GPL-2.0.

Commercial distribution therefore requires deliberate licensing architecture:

- distribute Suricata under GPLv2 and satisfy its obligations; or
- obtain an appropriate non-GPL license from OISF if the intended proprietary distribution model requires it.

RedCyberFox must not assume that "open source" means "no commercial obligations."

Do not modify/link proprietary libraries into Suricata and then distribute the result without a license review.

Source:
https://suricata.io/features/open-source/
https://suricata.io/gpl-faqs/

**Policy:** Suricata is approved for the architecture as a **dedicated sensor component**, but commercial release requires an explicit license decision recorded before packaging.

## CrowdSec

The CrowdSec Security Engine and remediation components are MIT licensed.

However, the engine, detection content, community blocklists, console/services, and other data/services must be treated separately for commercial planning.

Use the engine only where its functionality adds value beyond RedCyberFox's existing detection/policy pipeline.

Source:
https://docs.crowdsec.net/u/troubleshooting/security_engine/
https://github.com/crowdsecurity/crowdsec

**Policy:** optional integration, not a mandatory RedCyberFox dependency.

## ET Open rules

The current OISF Suricata intelligence index identifies Emerging Threats Open as MIT-licensed, while ET Pro is commercial.

For every shipped ET Open release:

- pin the exact version/release;
- record source URL;
- record checksum;
- record license metadata;
- retain attribution/notices;
- do not silently replace ET Open with ET Pro content.

Sources:
https://github.com/OISF/suricata-intel-index/blob/master/index.yaml
https://docs.suricata.io/en/latest/rule-management/suricata-update.html

## abuse.ch feeds

Do not use "abuse.ch is mostly CC0" as a universal commercial rule.

Each feed/API must be evaluated on its current terms.

Examples include URLhaus, ThreatFox, and MalwareBazaar, which publish specific usage/API terms and distinguish commercial/for-profit usage from ordinary community use.

Sources:
https://urlhaus.abuse.ch/api/
https://threatfox.abuse.ch/faq/
https://bazaar.abuse.ch/faq/

**Policy:** threat-intelligence feeds require a feed-level license/terms record before they become part of a commercial release.

---

# 6. Components We Are Explicitly Not Making Core

## Snort 3

Do not deploy Snort and Suricata simultaneously for the same sensor role.

Reasons:

- duplicate packet capture and inspection;
- duplicate rule processing;
- extra CPU/RAM;
- additional operational complexity;
- unnecessary licensing/content-management complexity.

Suricata is the canonical network IDS/IPS engine for this architecture.

Snort may be used for research or comparison benchmarks, but it is not part of the production architecture.

## Fail2ban

Do not make Fail2ban a RedCyberFox core dependency.

RedCyberFox already has:

```text
telemetry → detection → policy → guarded response
```

Adding another GPL log-ban subsystem adds complexity without filling a major architectural gap.

Fail2ban remains a possible interoperability target.

Source:
https://github.com/fail2ban/fail2ban

## ipset

Do not build the firewall abstraction around ipset.

Modern Linux should use nftables sets/maps.

Source:
https://netfilter.org/

## OPNsense

Do not fork OPNsense as the RedCyberFox product base.

Although OPNsense itself is BSD-2-Clause licensed, it is a broader firewall distribution with its own set of components and trademark considerations. It is also a poor fit for the lightweight endpoint architecture.

Source:
https://docs.opnsense.org/legal.html

## Maltrail Trails

Do not bundle Maltrail Trails into a commercial RedCyberFox release without obtaining the required permissions/license.

The Maltrail engine is MIT, but current Trails content has separate terms and commercial use of the dataset as an intelligence source requires additional permission/licensing.

Sources:
https://github.com/stamparm/maltrail
https://github.com/stamparm/trails

---

# 7. Low-End Hardware Strategy

## Endpoint target

The commercial product must not require:

- Suricata;
- Zeek;
- Snort;
- OPNsense;
- CrowdSec;
- Fail2ban

to all run on the endpoint.

A low-end endpoint target profile is approximately:

```text
CPU: 2 cores
RAM: 4 GB
Storage: HDD
Network: low bandwidth possible
```

The RedCyberFox endpoint should primarily run:

```text
RedCyberFox Agent
├── Process telemetry
├── Filesystem telemetry
├── Network/USB telemetry
├── YARA-X
├── Detection engine
├── Resource Governor
├── SQLite
└── guarded native firewall response
```

The target of **~500 MB RSS for the RedCyberFox agent** is an engineering target to be benchmarked, not a current guarantee.

It must never be documented as "the entire computer uses <500 MB RAM."

## Dedicated network sensor

A separate profile handles:

```text
TAP/SPAN
   ↓
Suricata
   +
Zeek
   ↓
OT/network telemetry
   ↓
Central SOC
```

Sensor resource limits must be benchmark-derived from:

- packets per second;
- bandwidth;
- protocol mix;
- rule count;
- capture method;
- parser workload;
- CPU cores;
- RAM;
- packet loss;
- queue depth.

The endpoint and sensor must therefore have **different resource budgets and acceptance tests**.

---

# 8. Recommended Deployment Profiles

## Profile A — Low-End Endpoint

Designed for the 4 GB / dual-core / HDD class.

Required:

- RedCyberFox Agent
- YARA-X
- SQLite
- Resource Governor
- native OS firewall integration where available

Not required:

- Suricata
- Zeek
- OPNsense
- Snort

## Profile B — Endpoint + OT Sensor

Endpoint stays lightweight.

Dedicated sensor runs:

- Suricata
- Zeek
- OT protocol parsing
- TAP/SPAN capture

## Profile C — Central SOC

Central system runs:

- PostgreSQL
- Valkey
- APIs/workers
- dashboard
- correlation
- risk
- search

---

# 9. Commercial Compliance Architecture

From this point onward, every third-party component must have a machine-readable record before release.

Create/maintain:

```text
docs/legal/third_party_software.md
docs/legal/third_party_content.md
docs/legal/feed_registry.md
docs/legal/trademark_registry.md
```

Each software component must record:

- component name;
- exact version;
- source URL;
- SPDX license identifier;
- copyright holders;
- redistribution obligations;
- modification obligations;
- whether source must be offered;
- whether trademark restrictions apply;
- whether commercial license is required;
- whether the component is bundled or merely invoked as an OS facility;
- exact notices that ship with the product.

Each threat-intelligence source must additionally record:

- exact feed/API;
- source URL;
- version/date;
- terms URL;
- redistribution permission;
- commercial-use permission;
- attribution requirements;
- retention/caching rules;
- whether derived data may be redistributed;
- approval owner/date.

---

# 10. Mandatory Release Controls

No commercial release may ship until:

1. An SBOM is generated.
2. Every dependency has an SPDX license classification.
3. GPL components are identified separately.
4. GPL components distributed in the product have a documented compliance plan or separate commercial license.
5. Rules/signatures/feeds are reviewed separately from their engine.
6. Trademarks/logos are inventoried separately from software licenses.
7. Cloud/API dependencies are reviewed separately from local open-source components.
8. The exact source/release artifact can be reconstructed.
9. Third-party notices are included where required.
10. A legal/IP review signs off on the release manifest.

---

# 11. Architecture Changes to the Official Roadmap

These are architectural additions, not immediate implementation tasks.

### Phase 4 — Rural Hardening
Continue with the Resource Governor and low-end endpoint optimization.

Add benchmark profiles for:

- 4 GB / dual-core / HDD;
- 4 GB / quad-core / HDD;
- low-bandwidth WAN;
- high event rate;
- offline operation.

Primary target:

> Keep the RedCyberFox endpoint bounded, responsive, and evidence-preserving under resource pressure.

### Phase 5 — OT Lab
Validate the separation between endpoint and dedicated OT sensor.

### Phase 6 — Passive OT Sensor
Canonical network sensor stack:

```text
TAP/SPAN
  ↓
Suricata + Zeek
  ↓
normalized passive telemetry
  ↓
RedCyberFox pipeline
```

Suricata licensing/distribution strategy must be resolved before commercial packaging.

### Phase 7 — OT Context
Add industrial protocol context without moving heavy OT parsing to every endpoint.

### Phase 9 — Security / Release Hardening
Add:

- SBOM;
- license scanning;
- third-party notice generation;
- dependency provenance;
- feed/license registry;
- release reproducibility;
- trademark inventory;
- commercial license verification.

---

# 12. Design Rule for Future Dependencies

Before adding any future open-source project, answer these questions:

```text
1. What problem does it solve?
2. Do we already solve that problem?
3. Does it run on the endpoint or dedicated sensor?
4. RAM/CPU/disk impact on the target profile?
5. License?
6. Exact SPDX identifier?
7. Can we modify it?
8. Can we distribute a modified binary commercially?
9. Does it contain separately licensed rules/data?
10. Are its names/logos/trademarks restricted?
11. Does it depend on a cloud service?
12. Are commercial/API terms different from open-source terms?
13. Can it be isolated as a separate process?
14. Can we remove it without redesigning RedCyberFox?
15. Has the exact version been recorded in the SBOM?
```

A project must not become a core dependency merely because it is popular or open source.

---

# 13. Final Approved Future Stack

### Low-end endpoint

```text
Go / RedCyberFox
├── YARA-X
├── SQLite
├── Resource Governor
├── existing host telemetry
└── native OS firewall integration
```

### OT/network sensor

```text
Linux sensor
├── Suricata
├── Zeek
├── TAP/SPAN
└── OT protocol analysis
```

### Optional

```text
CrowdSec engine
Unbound
Maltrail engine
approved threat-intel feeds
```

### Not core

```text
Snort 3
Fail2ban
ipset
OPNsense
Maltrail Trails without commercial permission
unverified threat-intel feeds
```

---

# 14. Commercialization Principle

RedCyberFox should remain commercially deployable as a proprietary product **without depending on the proprietary licensing of another vendor's cloud console or threat-intelligence service**.

The architecture therefore prefers:

- permissively licensed embedded components;
- native operating-system facilities;
- isolated GPL components with explicit compliance strategy;
- dedicated sensor processes;
- independently sourced detection content;
- version-pinned rules and feeds;
- complete provenance;
- automated SBOM/license checks.

This keeps the product technically lightweight and gives the project a defensible path toward later commercialization without assuming that every open-source engine, rule set, feed, or trademark carries the same rights.

---

## 15. Sources Reviewed

- YARA-X — BSD-3-Clause: https://github.com/VirusTotal/yara-x
- Zeek commercial/license FAQ: https://zeek.org/faq/
- Suricata open-source licensing / commercial licensing: https://suricata.io/features/open-source/
- Suricata GPL FAQ: https://suricata.io/gpl-faqs/
- CrowdSec engine licensing and resource information: https://docs.crowdsec.net/u/troubleshooting/security_engine/
- CrowdSec source repository: https://github.com/crowdsecurity/crowdsec
- Unbound — BSD-3-Clause: https://github.com/NLnetLabs/unbound
- Netfilter / iptables licensing: https://netfilter.org/licensing.html
- OPNsense legal notices: https://docs.opnsense.org/legal.html
- Suricata intelligence index / ET Open metadata: https://github.com/OISF/suricata-intel-index/blob/master/index.yaml
- Suricata rule update documentation: https://docs.suricata.io/en/latest/rule-management/suricata-update.html
- Maltrail engine: https://github.com/stamparm/maltrail
- Maltrail Trails terms: https://github.com/stamparm/trails
- URLhaus API terms: https://urlhaus.abuse.ch/api/
- ThreatFox FAQ: https://threatfox.abuse.ch/faq/
- MalwareBazaar FAQ: https://bazaar.abuse.ch/faq/
- Fail2ban: https://github.com/fail2ban/fail2ban
- SQLite public-domain statement: https://www.sqlite.org/copyright.html

> License information can change. The exact version shipped in a commercial release must be rechecked at release time. This document intentionally treats licensing as an ongoing release-control process rather than a one-time approval.
