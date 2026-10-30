---
name: fleet-multi-machine-network-federation
description: >-
  Architecture evaluation and operational guide for multi-machine local network Fleet pod deployment, federated backplane relaying, and gateway DNS resolution.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
-->

# Multi-Machine Local Network Federation & Gateway DNS Evaluation Guide

This guide provides an architectural trade-off analysis and operational evaluation for deploying Fleet across multi-workstation physical clusters on a Local Area Network (LAN) (`sndbx-re1`).

---

## 1. Context & Physical Topology Overview

In single-node deployments, Fleet runs rootless Podman containers where shared infrastructure (`fleet-valkey`, `fleet-forgejo`, `fleet-postgres`, `fleet-sonarqube`) resides on a host-local bridge network (`fleet-infra`), and agents query local LLM inference engines using `--add-host=llm-gateway:host-gateway` ([ADR 00035](../adr/00035-HostBridgedLocalLlmInferenceGateway.md)).

In multi-machine environments, teams and single operators run heterogeneous physical hardware across a local network:
- **Workstation 1 (Primary Linux Workstation)**: Primary development host, hosting agent pods, IDE sessions, and cluster management tooling.
- **Workstation 2 (Apple Silicon Mac Studio)**: Specialized inference rig running local LLM engines (Ollama, llama.cpp, vLLM, LiteLLM) accelerated by Unified Memory / Apple Metal GPU.
- **Workstation 3 (CI / Build Server)**: Offload server for heavy compilation, matrix testing, and background subagent execution.

---

## 2. Valkey Topology Analysis

Inter-agent communication and telemetry rely on Valkey Streams ([ADR 00042](../adr/00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)). Scaling Valkey across physical nodes presents two principal architectural options:

### Option A: Centralized Infrastructure Node (Primary Valkey Endpoint)

A single primary physical workstation hosts `fleet-valkey`. Remote workstations connect over the LAN to this primary instance.

| Architectural Metric | Centralized Node Evaluation |
| :--- | :--- |
| **Stream Consistency** | **High**: Single source of truth for stream offsets, consumer groups, and XADD sequences. Zero sync conflicts. |
| **Latency** | **Moderate**: Local agents experience sub-millisecond loopback IPC; remote agents incur 1–5 ms LAN network latency per operation. |
| **Fault Tolerance** | **Low**: Primary node offline halts cross-host messaging across the cluster. |
| **Complexity** | **Low**: Standard RESP client connections over Mutual TLS (mTLS); no distributed state reconciliation required. |

### Option B: Peered Backplane Relay Daemon (`bpd` Mesh)

Every physical node runs a local `fleet-valkey` instance and a local `bpd` daemon. Host `bpd` instances establish peer-to-peer or star relay links, forwarding signed backplane streams across nodes.

| Architectural Metric | Peered `bpd` Relay Mesh Evaluation |
| :--- | :--- |
| **Stream Consistency** | **Eventual**: Requires stream deduplication, sequence reconciliation, and vector/timestamp tracking across relays. |
| **Latency** | **Ultra-Low**: Local agents read and write exclusively to local loopback Valkey instances. |
| **Fault Tolerance** | **High**: Local backplane messaging survives temporary LAN network partitions or remote node outages. |
| **Complexity** | **High**: Involves inter-daemon handshake protocols, buffer queuing during disconnections, and subscription filtering. |

### Strategic Recommendation
Adopt a **phased rollout**:
1. **Phase 1 (v0.2.0 Baseline)**: Centralized Primary Node using Mutual TLS (`fleet-valkey` listening on LAN mTLS port `6380`).
2. **Phase 2 (v0.3.0 Extension)**: Peered `bpd` Relay Mesh for multi-operator environments requiring fault isolation and local offline operation.

---

## 3. Cross-Host Name Resolution Analysis

Rootless Podman containers operate inside isolated network namespaces where physical LAN broadcast/multicast packets are isolated. The system must resolve hostnames (`llm-gateway`, `forgejo`, `valkey`, `node-b.fleet`) deterministically.

### Option Comparison

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Cross-Host Resolution Trade-Offs                         │
├───────────────────┬──────────────────────────┬──────────────────────────────┤
│ Strategy          │ Pros                     │ Cons                         │
├───────────────────┼──────────────────────────┼──────────────────────────────┤
│ 1. Pure mDNS      │ Zero-config on host OS   │ Container network namespaces │
│    (Avahi/Bonjour)│ (.local resolution).     │ cannot receive multicast UDP │
│                   │                          │ packets directly.            │
├───────────────────┼──────────────────────────┼──────────────────────────────┤
│ 2. CoreDNS        │ Full TLD control         │ Requires additional container│
│    Sidecar        │ (.fleet resolution),     │ dependency and port 53       │
│    (`fleet-dns`)  │ dynamic records.         │ listener configuration.      │
├───────────────────┼──────────────────────────┼──────────────────────────────┤
│ 3. Automated Host │ Lightweight, zero runtime│ Requires background re-sync  │
│    Injection      │ overhead, uses standard  │ when physical host DHCP      │
│    (`--add-host`) │ Podman parameters.       │ addresses change.            │
└───────────────────┴──────────────────────────┴──────────────────────────────┘
```

### Strategic Recommendation
Deploy a **Hybrid Resolution Architecture**:
- **Host Discovery**: Workstations use native mDNS (`<hostname>.local`) for physical node peer discovery.
- **Container Injection**: `fleet infra up` and `fleet node sync` resolve physical host IP addresses on the host and inject container mappings via `--add-host` arguments during agent container instantiation.
- **Cluster Option**: For large clusters (>5 nodes), deploy an optional `fleet-coredns` sidecar container providing recursive `.fleet` domain resolution directly to container network bridges (`--dns <coredns-bridge-ip>`).

---

## 4. Inference Gateway Routing (`llm-gateway`)

Local LLM inference often executes on specialized physical hardware (such as an Apple Silicon Mac Studio with 128GB Unified Memory or a multi-GPU Linux server) distinct from the developer workstation.

### Gateway Resolution Patterns

1. **Local Host Gateway** (Single Node):
   ```bash
   # Podman flag for local host inference
   --add-host=llm-gateway:host-gateway
   # Translates OPENAI_BASE_URL=http://localhost:11434/v1 -> http://llm-gateway:11434/v1
   ```

2. **Remote Physical Rig** (Multi-Machine LAN):
   ```bash
   # Fleet resolves mac-studio.local -> 192.168.1.150 and passes:
   --add-host=llm-gateway:192.168.1.150
   # Agent connects to OPENAI_BASE_URL=http://llm-gateway:11434/v1 transparently
   ```

3. **Fallback & Failover Cascade**:
   - `fleet` probes inference engines in order:
     1. Local GPU / host loopback (`http://host-gateway:<port>`).
     2. Configured LAN inference rig (`FLEET_LLM_GATEWAY=http://mac-studio.local:11434`).
     3. Shared cluster inference proxy.

---

## 5. Security & Perimeter Isolation

Exposing infrastructure services across a physical Local Area Network expands the attack surface. Security boundaries are maintained through a multi-layered defense model:

```
 ┌────────────────────────────────────────────────────────────────────────┐
 │                      Multi-Host LAN Security Model                     │
 │                                                                        │
 │  ┌──────────────────────────────────────────────────────────────────┐  │
 │  │                  Application Layer Security                      │  │
 │  │  - Non-Repudiable Ed25519 Message Signing (ADR 00042)            │  │
 │  │  - Capability-Bounded Stream Access & ACL Isolation              │  │
 │  └──────────────────────────────────┬───────────────────────────────┘  │
 │                                     │                                  │
 │  ┌──────────────────────────────────┴───────────────────────────────┐  │
 │  │                  Transport Layer Security                        │  │
 │  │  - Mutual TLS (mTLS) with Internal Fleet CA Certificates          │  │
 │  │  - AES-256-GCM / ChaCha20-Poly1305 Wire Encryption                │  │
 │  └──────────────────────────────────┬───────────────────────────────┘  │
 │                                     │                                  │
 │  ┌──────────────────────────────────┴───────────────────────────────┐  │
 │  │                  Perimeter & Network Isolation                   │  │
 │  │  - Plaintext Port 6379 strictly bound to Loopback / Bridge       │  │
 │  │  - LAN Exposure restricted to mTLS Listener (Port 6380)           │  │
 │  └──────────────────────────────────────────────────────────────────┘  │
 └────────────────────────────────────────────────────────────────────────┘
```

### Key Security Requirements

1. **Mutual TLS (mTLS) Transport Encryption**:
   - Cross-host TCP traffic (Valkey Streams, Forgejo Git operations, API calls) requires mTLS.
   - Internal Fleet CA (`~/.config/fleet/ca/`) generates host certificates during node pairing (`fleet node join`).

2. **Preservation of Ed25519 End-to-End Signatures**:
   - Transport encryption (mTLS) protects LAN payload secrecy.
   - Ed25519 payload signatures ([ADR 00042](../adr/00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)) guarantee non-repudiable message authenticity and auditability regardless of intermediary network relays.

3. **Perimeter Isolation Rules**:
   - Unauthenticated raw Valkey sockets (port `6379`) MUST NOT listen on physical LAN interfaces (`0.0.0.0`). Raw sockets remain bound strictly to `127.0.0.1` and container bridge interfaces (`fleet-infra`).
   - LAN access is routed strictly through the mTLS-enabled port (`6380`).

---

## Further Reading
- [ADR 00035 — Host-Bridged Local LLM Inference Gateway](../adr/00035-HostBridgedLocalLlmInferenceGateway.md)
- [ADR 00042 — Universal Cryptographic Backplane Fabric and Stateless Identity Profiles](../adr/00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)
- [ADR 00056 — Multi-Machine Local Network Fleet Pod Deployment and Federated Gateway DNS](../adr/00056-multi-machine-local-network-federation.md)
