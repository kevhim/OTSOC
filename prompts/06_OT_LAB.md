# Phase 5 — OT Laboratory

Create an isolated, repeatable OT simulation environment.

## Lab
Use free/open-source/simulated components:
- PLC simulator such as OpenPLC
- simulated HMI/SCADA
- Windows/Linux VMs
- Modbus traffic generation
- PCAP replay

## Normal traffic
- HMI <-> PLC
- SCADA <-> PLC
- approved engineering host <-> PLC

## Abnormal traffic
- first-seen peer
- unexpected Modbus write
- write burst
- out-of-window engineering access
- unauthorized zone flow

Store scenario inputs, expected telemetry, expected findings, expected incident, and expected risk factors.

Reset scenarios to a clean state before each run.

Never connect experiments to production OT networks.

## Exit criterion
Normal and controlled abnormal OT traffic can be generated repeatedly.
