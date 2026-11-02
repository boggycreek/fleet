---
adr: "00060"
title: "Stateless Connection Profiles"
topic: "CLI & Configuration"
theme: "THEME-SECURITY"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
tags:
  - backplane
  - cli
  - profiles
  - stateless
  - identity
  - configuration
  - concurrency
executive_summary: "Eliminates mutable global session state and active identity pointer files by enforcing stateless, per-invocation backplane connection profile resolution via explicit CLI flags (--profile), scoped environment variables (BP_PROFILE), and isolated default profiles, guaranteeing race-free multi-agent and subagent execution."
---

# 00060. Stateless Connection Profiles

## Context

The backplane communication bus enables cross-agent messaging and coordination across containerized sandboxes, host-native cognitive agents, and human operators ([ADR 00042](00042-UniversalBackplaneFabric.md)).

In complex pair-programming and multi-agent workflows, multiple processes interact with the backplane simultaneously on the physical workstation:
- Concurrent terminal shells and IDE extensions operated by the human developer.
- Autonomous background subagents and evaluation workers executing parallel tasks.
- In-container and host-native daemons polling or dispatching messages.

Earlier session management models in CLI tools commonly rely on mutable active session pointer files (for example, writing an active identity or profile name to a shared state file like `~/.local/state/bp/active_session`). In concurrent multi-agent environments, this pattern introduces severe defects:
1. **Race Conditions**: When one subagent or script switches active profiles, all concurrent processes unintentionally inherit the changed identity.
2. **Identity Crosstalk & Masquerading**: Concurrent commands dispatch signed messages using credentials unintended by the caller.
3. **Cursor Desynchronization**: Shared state files cause message stream cursors and read positions to collide across parallel workers.

---

## Decision (What)

We establish **Stateless Connection Profiles** for all backplane interactions:

### 1. Per-Invocation Profile Resolution Hierarchy

Backplane commands (`bp`) execute statelessly. The connection profile and assumed cryptographic identity are resolved per invocation using a deterministic three-tier hierarchy:

1. **Explicit CLI Flag**: `--profile <name>` (highest precedence; explicitly overrides any environment context).
2. **Scoped Environment Variable**: `BP_PROFILE=<name>` (allows scripts, subagents, or container shells to bind their session without global mutation).
3. **Default Connection Profile Fallback**: `default` profile (`$XDG_STATE_HOME/bp/profiles/default.env`), representing the workstation's default operator identity.

### 2. Elimination of Mutable Session Pointers

Shared global active identity pointer files are strictly forbidden across the codebase. No command may write or rely on a mutable "current profile" pointer file in shared filesystem locations. Every invocation must resolve its target profile independently from flags, environment, or the default profile.

### 3. Profile Storage & Isolation

Connection profiles are stored as isolated environment definitions:
- **Location**: `$XDG_STATE_HOME/bp/profiles/<name>.env`
- **Contents**: Connection parameters (`BP_VALKEY_URL`), agent identity name (`BP_AGENT_NAME`), Ed25519 private key path (`BP_PRIVATE_KEY_PATH`), and optional operator flags (`BP_OPERATOR=true`).
- **Discovery**: Profiles can be listed statelessly via `bp profile list`.

### 4. Fail-Closed Validation

If a requested profile does not exist, or if its configuration contains conflicting directives (such as asserting operator mode while specifying an agent identity), the CLI halts immediately with a non-zero exit code, preventing identity leakage or accidental unauthenticated dispatches.

---

## Status

Accepted.

---

## Consequences

### Positive
- **Complete Concurrency Safety**: Parallel subagents, background jobs, and interactive terminals operate simultaneously on the host without session collisions or race conditions.
- **Auditable Execution**: Every backplane command invocation is deterministically traceable to an explicit profile flag, environment variable, or default configuration.
- **Clean Subagent Delegation**: Subagents spawned by host orchestrators can be assigned distinct identities simply by passing `--profile <agent>` or setting `BP_PROFILE` in their execution environment.

### Negative / Trade-offs
- **Explicit Parameter Passing**: Automated scripts and background tasks operating outside the default profile must supply `--profile <name>` or set `BP_PROFILE` explicitly in their command environment.
