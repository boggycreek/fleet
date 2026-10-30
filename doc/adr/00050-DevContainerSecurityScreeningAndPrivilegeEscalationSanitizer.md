---
adr: "00050"
title: "Dev Container Security Screening, Diagnostics, and Privilege Escalation Sanitizer"
topic: "Container Security & Isolation"
theme: "THEME-SECURITY"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - security
  - devcontainer
  - screening
  - privilege-escalation
  - isolation
  - policy
executive_summary: "Establishes automated static policy screening and sanitization for devcontainer.json configurations, proactively detecting and neutralizing privilege escalation vectors, forbidden capabilities, and dangerous host mounts before agent container instantiation."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# 00050. Dev Container Security Screening, Diagnostics, and Privilege Escalation Sanitizer

## Context & Problem Statement

As Fleet integrates standard Dev Container configurations (`devcontainer.json`) to allow developers and teams to Bring Your Own Environment (BYOE), arbitrary or untrusted configurations pose significant security hazards. Malicious, misconfigured, or AI-hallucinated configurations in cloned repositories can attempt to violate Fleet's strict sandbox security perimeter ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md), [ADR 00013](00013-PerInstanceNetworkIsolation.md), [ADR 00014](00014-DefaultDenyNetworkEgressFiltering.md), [ADR 00018](00018-RootOwnedIdeSettingsProtection.md)).

### Risks in Arbitrary `devcontainer.json` Configurations
Standard Dev Container specifications grant extensive control over container engine flags, mounts, Linux capabilities, and execution users. Unsanitized configurations expose critical privilege escalation vectors:
- **Privileged Flags**: `privileged: true` or `runArgs: ["--privileged"]`, bypassing container namespace isolation.
- **Excessive Capabilities**: `capAdd: ["ALL"]` or specific high-risk capabilities like `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`, and `CAP_DAC_OVERRIDE`.
- **Host Network Access**: `runArgs: ["--net=host"]` or direct host network namespace attachment, bypassing egress filters and netns isolation.
- **Dangerous Host Mounts**: Binds targeting sensitive host paths (`/var/run/docker.sock`, `/home/*/.ssh`, `/etc/shadow`, `/dev`, `/proc`, `/sys`, or host root filesystem `/`), enabling host compromise or credential extraction.
- **Root Execution**: `remoteUser: root` or `containerUser: root`, overriding unprivileged user boundaries.

### Prompt Injection & Autonomous Agent Threats
Autonomous in-container agents or subagents processing external repositories are susceptible to prompt injection or malicious code execution. Cloned repositories containing poisoned `.devcontainer/devcontainer.json` files could attempt to trick Fleet into launching escalated containers during workspace initialization, escaping rootless Podman constraints.

To prevent container synthesis from becoming an exploitation vector, all Dev Container specifications must undergo automated static screening, diagnostic linting, and deterministic policy sanitization before container instantiation.

---

## Decision (What)

We establish an automated **3-Stage Validation Pipeline** for all Dev Container configurations (`devcontainer.json` and associated overrides):

```
 ┌───────────────────┐     ┌─────────────────────┐     ┌─────────────────────┐     ┌──────────────────────┐
 │ devcontainer.json │ ──> │ Stage 1: Screening  │ ──> │ Stage 2: Sanitizer  │ ──> │ Stage 3: Attestation │
 │  (BYOE Input)     │     │ (Diagnostic Lint)   │     │ (Policy Rewriting)  │     │ (Signed Spec Digest) │
 └───────────────────┘     └─────────────────────┘     └─────────────────────┘     └──────────────────────┘
                                      │                           │                            │
                                      ▼                           ▼                            ▼
                               Emits Violation             Applies Rule Sets             Synthesizes Safe
                               Diagnostics                 (Strip / Rewrite)             Agent Container
```

### 3-Stage Validation Pipeline

1. **Stage 1: Screening (Diagnostic Linting)**
   - Parses and validates `devcontainer.json` against Fleet security schemas.
   - Audits all fields against deterministic security rules and flags policy violations without executing container builds.
   - Emits structured diagnostic findings with severity levels (`ERROR`, `WARNING`, `INFO`).

2. **Stage 2: Sanitizing (Policy Rewriting & Stripping)**
   - Automatically mutates or strips disallowed keys, forbidden flags, and dangerous parameters.
   - Overrides hazardous settings with safe system defaults (e.g., forcing unprivileged user execution, stripping host network binds) rather than failing outright when safe fallbacks exist.
   - Enforces non-negotiable hard blocks (`ERROR`) for non-sanitizable violations.

3. **Stage 3: Attestation & Verification**
   - Generates a deterministic, machine-readable effective specification (`devcontainer.effective.json`).
   - Computes a SHA-256 cryptographic digest of the sanitized configuration and records it in the container provenance record before passing arguments to the Podman synthesis engine.

---

### Deterministic Rule Sets

The validation pipeline enforces five core security rules:

#### Rule 1: Strip `privileged: true`
- **Policy**: Privileged mode is strictly forbidden.
- **Action**: Strips `privileged: true` and removes `--privileged` from `runArgs` or `containerEnv`.
- **Severity**: `ERROR` (auto-remediated via stripping).

#### Rule 2: Enforce Non-Root Execution
- **Policy**: All containers must execute under unprivileged user boundaries ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)).
- **Action**: Intercepts `remoteUser: root` or `containerUser: root`. Forces `remoteUser: agent` (UID 1000) or maps host user ID (`keep-id` per [ADR 00044](00044-HostWorkingDirectoryProjectionAndUserIdPreservation.md)).
- **Severity**: `WARNING` (rewritten to `agent`).

#### Rule 3: Deny Dangerous Linux Capabilities
- **Policy**: High-risk Linux capabilities capable of kernel escalation, container breakout, or network sniffing are forbidden.
- **Action**: Drops `ALL` capabilities by default. Rejects or strips explicit `capAdd` entries containing `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`, `CAP_DAC_OVERRIDE`, `CAP_SYS_RAWIO`, `CAP_SYS_PTRACE`, `CAP_SYS_MODULE`, or `CAP_NET_RAW`.
- **Severity**: `ERROR` (disallowed capability stripped).

#### Rule 4: Strip Forbidden Network Parameters
- **Policy**: Network access must remain constrained within isolated rootless netns and default-deny egress sidecars ([ADR 00013](00013-PerInstanceNetworkIsolation.md), [ADR 00014](00014-DefaultDenyNetworkEgressFiltering.md)).
- **Action**: Bans `runArgs: ["--net=host"]` and `--network=host`. Replaces host networking directives with dedicated `fleet-infra` bridge or isolated netns attachments.
- **Severity**: `ERROR` (rewritten to isolated network interface).

#### Rule 5: Mount Path Sanitization
- **Policy**: Mounts must not expose host system paths, administrative sockets, or operator secrets.
- **Action**: Inspects `mounts`, `workspaceMount`, and volume bindings. Blocks and strips target or source paths matching:
  - Docker/Podman sockets: `/var/run/docker.sock`, `/run/podman/podman.sock`
  - Host secret directories: `/home/*/.ssh`, `~/.aws`, `~/.kube`, `~/.gnupg`
  - Sensitive host system directories: `/etc/shadow`, `/etc/sudoers`, `/dev`, `/proc`, `/sys`, `/boot`
  - Direct host root filesystem binds: `/`
- Enforces strict workspace projection bounds ([ADR 00044](00044-HostWorkingDirectoryProjectionAndUserIdPreservation.md)).
- **Severity**: `ERROR` (forbidden mount point stripped).

---

### Diagnostic Output Format

The `fleet devcontainer lint` and screening CLI tools emit structured JSON diagnostic violations for consumption by operators, agent tools, and CI pipelines:

```json
{
  "file": ".devcontainer/devcontainer.json",
  "valid": false,
  "summary": {
    "errors": 3,
    "warnings": 1,
    "info": 0
  },
  "violations": [
    {
      "code": "DEVCONTAINER_PRIVILEGED_FORBIDDEN",
      "severity": "ERROR",
      "rule": "Rule 1: Strip privileged",
      "field": "privileged",
      "message": "Privileged container execution is strictly forbidden in Fleet sandboxes.",
      "action": "STRIPPED"
    },
    {
      "code": "DEVCONTAINER_ROOT_USER_RESTRICTED",
      "severity": "WARNING",
      "rule": "Rule 2: Enforce non-root execution",
      "field": "remoteUser",
      "message": "remoteUser 'root' overridden to unprivileged user 'agent' (UID 1000).",
      "action": "REWRITTEN",
      "replacement": "agent"
    },
    {
      "code": "DEVCONTAINER_CAPABILITY_DENIED",
      "severity": "ERROR",
      "rule": "Rule 3: Deny dangerous capabilities",
      "field": "capAdd",
      "message": "Forbidden Linux capability 'CAP_SYS_ADMIN' detected in capAdd.",
      "action": "STRIPPED"
    },
    {
      "code": "DEVCONTAINER_DANGEROUS_MOUNT_BLOCKED",
      "severity": "ERROR",
      "rule": "Rule 5: Mount path sanitization",
      "field": "mounts",
      "message": "Host bind mount to sensitive socket '/var/run/docker.sock' is prohibited.",
      "action": "STRIPPED"
    }
  ]
}
```

---

## Status

Proposed.

---

## Consequences

### Positive
- **Robust Defense-in-Depth**: Prevents prompt injection attacks or malicious repositories from escaping container isolation via `devcontainer.json` configuration vectors.
- **Automated Developer Friction Reduction**: Automatically strips or rewrites unsafe directives (like `remoteUser: root`) so that standard dev containers remain usable without requiring manual developer editing.
- **Auditability & Provenance**: Emits structured diagnostic logs and cryptographic digests of effective configurations before container launch.

### Negative / Trade-offs
- **Feature Compatibility Constraints**: Custom dev containers relying on true privileged capabilities (such as nested Docker-in-Docker via `/var/run/docker.sock` or `CAP_SYS_ADMIN`) will be restricted or sanitized, requiring Fleet-native alternatives (e.g., Podman rootless or Fleet sidecars).
- **Maintenance Overhead**: Security rules and schema parsers must be maintained and synchronized as the upstream Dev Container Specification evolves.
