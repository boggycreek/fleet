---
name: fleet-doctor-diagnostics
description: >-
  Operational runbook for the diagnostic doctor commands (`fleet agent doctor` and `fleet infra doctor`).
  Use when diagnosing fleet or infrastructure issues, checking component health, or troubleshooting defects.
---

# Diagnostic Doctor & Auto-Healing

## Design Philosophy
- The `doctor` verb is the single authoritative entry point for system self-inspection and automated defect repair.
- Two scopes:
  1. `fleet agent doctor <name>`: Agent-specific health checks.
  2. `fleet infra doctor`: Shared Valkey backplane, Forgejo server, and network health checks.
- When an operator reports an issue or unexpected state, run the relevant `doctor` command first.

## Check Categories & Statuses
- **Status Symbols**:
  - `[✓] StatusOK`: Component healthy and properly configured.
  - `[⚡] StatusHealed`: Component had a defect that was automatically repaired.
  - `[-] StatusWarning`: Non-fatal warning (e.g. image not cached locally, service offline).
  - `[✗] StatusError`: Fatal/unrepairable defect requiring manual intervention.
- **Agent Checks**:
  - Configuration file presence and `0600` permissions.
  - Ed25519 signing key integrity and synchronization.
  - Dedicated host IDE keypair (`~/.ssh/fleet`) and OpenSSH `Include` directive.
  - Podman network (`fleet-infra`) and persistent home volume (`fleet-<name>-home`).
  - Agent container image presence (Tier 1-3 resolution from ADR 00043).
  - Valkey backplane ACL provisioning.
  - Forgejo user account, SSH public key, and `fleet` org membership.
  - Container status and dynamic SSH port discovery.

Further reading:
- [ADR 00023 — Comprehensive Diagnostic Doctor and Self-Healing](../adr/00023-ComprehensiveDoctorAndSelfHealing.md)
