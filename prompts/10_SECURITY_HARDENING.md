# Phase 9 — Security Hardening

Harden the working system after the detection pipeline exists.

## Update trust
- signed rule bundles
- signed agent binaries
- version/hash metadata
- anti-rollback
- staged activation
- self-test
- health check
- rollback

Minimum signing model:
offline root + online signing key.
Full TUF separation is a later maturity path.

## PKI
one-time bootstrap -> private CA -> per-device certificate -> mTLS -> rotation/revocation

Use a maintained private-CA tool such as step-ca rather than inventing a CA.

## Edge security
Protect local SQLite, credentials/config, and evidence.
Use platform-native protection where practical.
Do not invent cryptography.

## Audit
Append-only/tamper-evident chain:
hash_n = SHA256(canonical(event_n) || hash_(n-1))
plus periodic signed checkpoints.

## IAM
- OIDC or equivalent
- MFA for admins
- RBAC
- step-up authentication for R3/sensitive actions
- break-glass process
- session expiration
- audit

## Supply chain
- CycloneDX or SPDX SBOM
- dependency scanning
- artifact digest
- source/build metadata
- signed releases
- provenance

## Exit criterion
Invalid/rollback artifacts are rejected, privileged actions are authenticated/audited, tampering is detectable, device identity lifecycle works, and releases have SBOM/provenance.
