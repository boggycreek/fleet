---
adr: "00056"
title: "Multi-Machine Local Network Fleet Pod Deployment and Federated Gateway DNS"
topic: "Networking & Multi-Host Federation"
theme: "THEME-NETWORKING"
status: "accepted"
version: "v0.1.0-alpha"
as_built: false
tags:
  - networking
  - multi-machine
  - federation
  - valkey
  - dns
  - coredns
  - mdns
  - bpd
  - mtls
  - llm-gateway
executive_summary: "Evaluates multi-machine physical host deployment, federated backplane bus access, and local network DNS resolution for multi-workstation Fleet clusters. Establishes a hybrid cross-host federation architecture featuring a phased Valkey relay topology, container-aware mDNS/CoreDNS host resolution, remote inference gateway routing, and mutual TLS (mTLS) transport security while preserving end-to-end Ed25519 message signatures."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
-->

# 00056. Multi-Machine Local Network Fleet Pod Deployment and Federated Gateway DNS

## Context

Fleet currently deploys rootless Podman containers on a single physical host workstation. In this single-node topology:
1. Shared infrastructure services (`fleet-valkey`, `fleet-forgejo`, `fleet-postgres`, `fleet-sonarqube`) run on a host-local bridge network (`fleet-infra`).
2. Agents access local LLM inference engines on the host via Podman host-gateway resolution (`--add-host=llm-gateway:host-gateway`) as specified in [ADR 00035](00035-HostBridgedLocalLlmInferenceGateway.md).
3. Backplane messaging (`bp`) coordinates containerized agents and host processes using local Valkey Streams authenticated via local ACL credentials and signed via Ed25519 digital signatures as specified in [ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md).

As engineering teams and single operators scale Fleet workloads across multiple physical nodes on a Local Area Network (LAN)—for example, a primary Linux development workstation, an Apple Silicon Mac Studio dedicated to local LLM inference, and a high-core offload build/CI server—the single-host assumption breaks down:

- **Cross-Host Agent Discovery & Bus Access**: Agents running on Host A (Linux dev workstation) must discover and exchange backplane streams with agents or shared services hosted on Host B (Mac Studio or build server).
- **Deterministic Name Resolution**: Containerized agents across different physical hosts require deterministic hostname resolution (`llm-gateway`, `forgejo`, `valkey`) without hardcoding dynamic DHCP or ephemeral physical LAN IP addresses.
- **Perimeter & Network Security**: Cross-host communication across an untrusted or shared LAN must maintain strict isolation boundaries without exposing unauthenticated raw TCP sockets, plain secrets, or unencrypted Valkey Stream traffic to the local network.

---

## Decision Drivers

1. **Deterministic Connectivity Across Physical Nodes**: Containers operating inside isolated rootless network namespaces on separate physical machines must seamlessly resolve core services (`valkey`, `forgejo`, `llm-gateway`) regardless of host OS or physical IP address changes.
2. **Zero-Trust Network Perimeter**: Unauthenticated raw sockets or plaintext backplane credentials must never be exposed on the LAN. Cross-host traffic must be encrypted and authenticated at the transport layer.
3. **Preservation of Cryptographic Lineage**: Cross-host federation must preserve end-to-end Ed25519 message signing, stateless identity profiles, and capability boundaries established in [ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md).
4. **Developer Ergonomics & Frictionless Onboarding**: Adding a physical node to a local Fleet cluster should require minimal manual configuration, avoiding fragile static routing tables or manual `/etc/hosts` edits.
5. **Decoupled Local & LAN Inference Routing**: Agents must transparently route inference requests to local host GPUs or remote LAN inference rigs without modifying code or agent definitions.

---

## Considered Options

### 1. Valkey Inter-Host Messaging Topology
- **Option A (Centralized Infrastructure Host)**: A single designated physical host acts as the central `fleet-valkey` node. All nodes connect to this primary Valkey instance over the LAN.
- **Option B (Peered Backplane Relay Daemon - `bpd` Mesh)**: Each physical node runs a local `fleet-valkey` instance and a local Backplane Daemon (`bpd`). Host `bpd` instances establish peer-to-peer or star relay tunnels over LAN, syncing backplane streams across local instances.

### 2. Cross-Host Hostname Resolution
- **Option A (Multicast DNS - mDNS / Avahi / Bonjour)**: Rely purely on `.local` mDNS resolution across physical hosts.
- **Option B (Embedded CoreDNS Sidecar Container - `fleet-coredns`)**: Deploy an embedded CoreDNS container on primary infrastructure nodes, serving internal `.fleet` or `.local` TLDs to container bridges.
- **Option C (Automated Host Entry Injection via `fleet node join` / `fleet infra up`)**: Automate container extra hosts (`--add-host`) and host `/etc/hosts` entry updates using `fleet` CLI cluster discovery.

### 3. Inference Gateway Routing (`llm-gateway`)
- **Option A (Static Single-Host Gateway)**: Hardcode `llm-gateway` to the local host gateway IP (`--add-host=llm-gateway:host-gateway`).
- **Option B (Dynamic Multi-Rig Profile Resolution)**: Configure `llm-gateway` dynamically per node or cluster profile, pointing to local host-gateway or remote physical inference rigs across the LAN.

---

## Decision Outcome

We establish the **Multi-Machine Local Network Federation Architecture** for multi-workstation Fleet clusters (`sndbx-re1`), adopting a phased and layered approach:

### 1. Phased Valkey Bus Federation Model
- **Phase 1 (Centralized Primary Node with mTLS)**: In initial multi-machine setups (`v0.2.0`), a designated primary node runs `fleet-valkey` exposed to the LAN over a Mutual TLS (mTLS) listener (`6380`). Remote hosts connect their local `bpd` clients directly to the primary mTLS endpoint.
- **Phase 2 (Peered `bpd` Relay Mesh)**: For multi-operator or resilient multi-node clusters, each host runs a local `fleet-valkey` instance for ultra-low latency local IPC. Local `bpd` daemons establish encrypted mTLS relay links, selectively forwarding signed streams based on channel subscription filters.

### 2. Container-Aware Hybrid Name Resolution (mDNS + CoreDNS / Host Sync)
- **Host Layer**: Nodes advertise their identities on the LAN using standard mDNS (`<hostname>.local`).
- **Container Layer**: Because rootless Podman network namespaces cannot natively receive mDNS multicast UDP directly, `fleet` manages name resolution via a hybrid mechanism:
  - In basic setups, `fleet node sync` / `fleet infra up` resolves `.local` mDNS endpoints on the host and injects container mapping rules (`--add-host=<service>:<lan-ip>`) into container startup parameters.
  - In managed cluster setups, an embedded `fleet-coredns` sidecar container runs on the primary node, bridging DNS resolution for `.fleet` domains directly to agent container network namespaces.

### 3. Federated Inference Gateway (`llm-gateway`)
- `llm-gateway` endpoints are decoupled from hardcoded host loopback addresses.
- Nodes with local hardware accelerators (e.g., Apple Silicon Mac Studio or CUDA rigs) expose inference endpoints bound to LAN or mTLS proxies.
- Operators specify the active inference target via node profile configuration (`FLEET_LLM_GATEWAY=http://mac-studio.local:11434`), and `fleet` injects appropriate container host aliases (`--add-host=llm-gateway:<rig-ip>`) or transparent proxy rules.

### 4. Transport Security Perimeter & End-to-End Signature Preservation
- **Transport Layer**: All cross-host TCP connections (Valkey Streams, Forgejo Git operations, API calls) are encrypted and authenticated using Mutual TLS (mTLS) with certificates issued by an internal Fleet Cluster Authority (`~/.config/fleet/ca/`).
- **Application Layer**: End-to-end Ed25519 message signing ([ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)) remains strictly enforced. Transport encryption protects payload secrecy on the LAN, while Ed25519 signatures guarantee non-repudiable message authenticity regardless of transit path.
- **Perimeter Isolation**: Unauthenticated raw Valkey sockets (port `6379`) remain strictly bound to host loopback / internal `fleet-infra` bridge interfaces and are never exposed to the physical LAN.

---

## Consequences

### Positive
- **Seamless Scalability**: Enables hybrid physical development topologies (e.g., combining Linux workstations, Apple Silicon Mac Studio inference rigs, and CI build servers).
- **Deterministic Service Discovery**: Containers resolve shared infrastructure and inference gateways deterministically across physical machines without hardcoded IP addresses.
- **Zero-Trust LAN Security**: Mutual TLS and Ed25519 message signatures ensure zero plaintext credential exposure and prevent unauthenticated LAN access.
- **Resilient Architectural Evolution**: Phased migration path from centralized Valkey primary node to full `bpd` relay mesh minimizes upfront complexity while supporting future decentralization.

### Negative / Trade-offs
- **Certificate Lifecycle Management**: Internal Cluster CA management introduces certificate generation, distribution, and renewal steps during node pairing (`fleet node join`).
- **DNS Sync Overhead**: Relying on container host injection requires background IP re-synchronization when host DHCP addresses change on dynamic local networks.
