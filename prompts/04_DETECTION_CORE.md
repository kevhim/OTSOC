# Phase 3 — Detection Core

Implement a small high-confidence deterministic detection layer.

## Tracks
1. YARA-X for local file/artifact matching.
2. Sigma as authoring/distribution where appropriate; compile a supported subset into target-specific evaluators.
3. Local IOC matching.

Do not make the endpoint a full general-purpose Sigma engine unless measured requirements justify it.

## Initial scope
Start with roughly 10–20 high-confidence detections:
- suspicious executable from removable media
- suspicious process-parent relationship
- abnormal mass file modification/rename
- known malicious hash
- suspicious authentication behavior
- unexpected external peer

## Finding metadata
Every finding retains:
- rule_id
- rule_version
- severity
- confidence
- reason
- evidence references
- ATT&CK mapping where applicable

## Rule lifecycle
author/import -> validate -> map -> compile -> positive test -> negative test -> sign later -> publish -> measure FP/performance -> review/retire

## Exit criterion
Local detections work, false-positive behavior is measured, alert deduplication works, and rules can be added without rewriting the agent.
