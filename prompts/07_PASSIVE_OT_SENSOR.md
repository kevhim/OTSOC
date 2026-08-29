# Phase 6 — Passive OT Sensor

## Goal
Add passive OT network visibility without becoming inline with production.

## Pipeline
SPAN/TAP -> capture -> flow extraction -> Zeek + Suricata -> normalization -> local buffer -> RedCyberFox ingestion

## Responsibilities
### Suricata
- IDS detections
- application-layer events
- supported OT protocol signals

### Zeek
- network metadata
- session context
- protocol context
- asset/peer observations

Avoid unnecessary duplication.

## Data reduction
Do not send every packet centrally.
Prefer:
raw packets -> local parsing -> security-relevant events -> central SOC

Keep targeted PCAP only for selected evidence windows.

## Safety
- not inline by default
- no packet injection
- no blocking
- no production-path modification
- sensor failure cannot affect production

## Initial protocols
- Modbus/TCP
- DNP3
- EtherNet/IP/CIP
- S7/S7comm
- OPC UA
- BACnet

Roadmap:
- PROFINET
- IEC 61850
- IEC 60870-5-104
- serial/gateway visibility

## Exit criterion
Sensor generates normalized OT events from the lab without affecting traffic.
