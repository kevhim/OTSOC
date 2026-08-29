# RedCyberFox

RedCyberFox is an offline-first, passive-first OT security platform designed for rural, small, and resource-constrained environments.

### Project Purpose
To provide robust, deterministic, and safe security monitoring for environments that cannot sustain heavy centralized databases at the edge, rely on intermittent internet connectivity, and operate fragile OT infrastructure.

### High-level Architecture
The architecture is fundamentally divided into an edge-light / central-heavy model. Edge components (agents, OT sensors, SQLite buffers) handle passive collection, local detections, and store-and-forward resilience. Central components (PostgreSQL, Valkey, workers) handle heavy correlation, analytics, and incident management.

### Development Status
Development stage: `Repository preparation / Phase 0`

### Architecture Documentation
The architecture is detailed across multiple files in this repository:
- `docs/architecture/architecture.md`: Machine-readable master specification (derived from the V6 PDF).
- `docs/architecture/decisions/`: Architecture Decision Records (ADRs) explaining core design choices.
- `ARCHITECTURE_RULES.md`: Core invariants and constraints that all code must obey.

### Phase Prompts
Implementation instructions for each phase are organized in the `prompts/` directory. Each prompt dictates a vertical slice of development to be executed sequentially.
