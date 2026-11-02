---
adr: "00042"
title: "Universal Backplane Fabric"
topic: "Security, Backplane & Messaging"
theme: "THEME-SECURITY"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
supersedes:
  - "00010"
  - "00011"
tags:
  - backplane
  - messaging
  - cryptography
  - identity
  - ed25519
  - valkey
  - security
executive_summary: "Establishes a universal, topology-agnostic backplane communication fabric governed by non-repudiable cryptographic identity, strict capability-bounded message streams, and Ed25519 digital signatures. Supersedes ADR 00010 and ADR 00011 by decoupling identity from container infrastructure, establishing first-class peer status for host-level cognitive agents and human operators, and anchoring message authenticity in cryptographic payload signatures."
---

# 00042. Universal Backplane Fabric

## Context

The initial messaging and security architecture defined in [ADR 00010](00010-ValkeyPubSubMessagingAndAclIsolation.md) (Valkey Streams Messaging Bus) and [ADR 00011](00011-Ed25519CryptographicMessageSigning.md) (Ed25519 Cryptographic Message Signing) established isolated message streams and tamper-evident signatures for containerized agents.

However, operational field tests, human-agent pair programming workflows, and multi-tier subagent coordination revealed architectural limitations in the original model:

1. **Topological Coupling**: Identity provisioning was tightly coupled to container lifecycle management (`sndbx agent create`). Host-side processes—including developer IDE extensions, background evaluation runners, and host-native cognitive agents—lacked native identities, forcing human operator masquerading or ad-hoc environment variable overloading.
2. **Compound Identity Failures**: Experiments attempting to represent host-side cognitive processes through composite namespace schemes (such as `<operator>:<agent>`) created stream routing ambiguities, cursor desynchronization across concurrent tools, and split cryptographic accountability.
3. **Machine Discovery Gaps**: Autonomous agents operating in headless or multi-agent environments require immediate, introspective discovery of their active cryptographic identity, authorization bounds, and conversational protocol rules without relying on unvalidated external prompts.

A unified, conceptual architecture is necessary to elevate the backplane from an in-container message bus to a universal coordination fabric spanning host and container boundaries. Connection profile state and resolution semantics are addressed separately in [ADR 00060](00060-StatelessConnectionProfiles.md).

---

## Decision (What)

We establish the **Universal Backplane Fabric**, superseding [ADR 00010](00010-ValkeyPubSubMessagingAndAclIsolation.md) and [ADR 00011](00011-Ed25519CryptographicMessageSigning.md).

```
 ┌────────────────────────────────────────────────────────────────────────┐
 │                     Physical Host Workstation                          │
 │                                                                        │
 │   ┌────────────────────────┐            ┌──────────────────────────┐   │
 │   │     Human Operator     │            │    Host-Native Agent     │   │
 │   │ (Identity: "operator") │            │  (Identity: "host-coder")│   │
 │   └───────────┬────────────┘            └────────────┬─────────────┘   │
 │               │                                      │                 │
 │               ▼                                      ▼                 │
 │     ┌────────────────────────────────────────────────────────────┐     │
 │     │            Universal Backplane Fabric (bp)                 │     │
 │     │  - Strict Channel Access & Mailbox Partitioning            │     │
 │     │  - Non-Repudiable Cryptographic Signatures (Ed25519)       │     │
 │     │  - Canonical Citation Lineage (<agent>#<seq>)              │     │
 │     └────────────────────────▲───────────────────────────────────┘     │
 │                              │                                         │
 │                              │ Rootless Bridge / IPC                   │
 │                              │                                         │
 │               ┌──────────────┴─────────────┐                           │
 │               │   Isolated Agent Sandbox   │                           │
 │               │  (Identity: "fleet-agent") │                           │
 │               └────────────────────────────┘                           │
 └────────────────────────────────────────────────────────────────────────┘
```

The fabric is defined by three foundational conceptual pillars:

### 1. Topology-Agnostic First-Class Identities
- Every participant on the backplane—whether an autonomous containerized sandbox, a host-native AI coding agent, or a human operator—is a **first-class, authentic identity**.
- Identity existence and cryptographic credential allocation are completely decoupled from container runtime, virtual network namespaces, or filesystem volume attachment.
- Host-resident cognitive agents operate with distinct, dedicated mailboxes and credentials identical in privilege and structure to containerized fleet members, preventing operator masquerading.

### 2. Cryptographic Authenticity & Fail-Closed Guardrails
- **Payload Verification**: Trust is anchored in cryptographic signatures rather than network locality or container boundaries. All messages carry non-repudiable Ed25519 digital signatures verified against sender public key records.
- **Fail-Closed Mode Boundaries**: The communication fabric strictly enforces role separation. Any conflicting configuration—such as asserting human operator mode while specifying an agent identity—fails closed immediately, halting execution to prevent identity pollution or accidental unauthenticated dispatches.
- **Auditable Lineage**: Every communication carries an immutable, monotonic sequence citation (`<agent>#<seq>`), enabling direct in-thread replies and deterministic causal ordering across distributed interactions.

### 3. Self-Describing Cognitive Protocol
- The backplane fabric is natively self-describing for autonomous agents.
- Participants can introspect their active identity, authorization status, and cryptographic credentials dynamically (`whoami`).
- The fabric provides an embedded, machine-readable protocol guide (`help --ai`), ensuring that any autonomous reasoning model connecting to the backplane immediately assimilates broadcast, point-to-point, citation, and behavioral conventions without human orientation.

---

## Status

Accepted (Supersedes ADR 00010 and ADR 00011).

---

## Consequences

### Positive
- **Complete Parity Across Runtimes**: Host-native cognitive tools, IDE orchestrators, and containerized sandboxes interact as true peers under uniform security semantics.
- **Zero Masquerading**: Eliminates the operational hazard of host agents masquerading as human operators, providing unambiguous audit trails.
- **Deterministic Machine Communication**: Standardized introspection and conversational lineage reduce context hallucination and prevent uncoordinated message storms.

### Negative / Trade-offs
- **Credential Storage Overhead**: Host-native agents require local secret key storage and profile persistence, requiring lifecycle synchronization when agents are deprovisioned.
