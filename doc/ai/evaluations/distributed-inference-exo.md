---
name: fleet-exo-evaluation
description: >-
  Evaluation of exo (exo-explore/exo) for distributed local-network LLM inference behind Fleet's local model gateway.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Distributed Local-Network LLM Inference Evaluation: exo (`exo-explore/exo`)

## 1. Executive Summary & Purpose

This document provides a comprehensive engineering evaluation of **`exo`** ([github.com/exo-explore/exo](https://github.com/exo-explore/exo)) as a distributed inference engine operating behind **Fleet's host-bridged local LLM gateway** (`http://llm-gateway:<port>/v1`).

As autonomous AI coding agents execute inside Fleet container sandboxes, they query local LLM inference engines on physical workstations to avoid cloud vendor lock-in, recurring API costs, and corporate code egress. While single-node engines like Ollama and `llama.cpp` excel on individual workstations, larger frontier open-weights models (e.g., Llama 3.3 70B, Qwen 2.5 72B, DeepSeek-R1 70B) exceed the unified VRAM/RAM capacity of a single consumer device.

`exo` addresses this limitation by pooling heterogenous consumer hardware nodes—such as Apple Silicon MacBooks, Mac Studios, and Linux CUDA/ROCm workstations—over a local area network (LAN) into a unified cluster capable of distributed LLM tensor and pipeline parallelism.

---

## 2. API Protocol Compatibility

### 2.1 OpenAI Specification Surface
`exo` provides a native HTTP REST server that exposes OpenAI-compatible endpoint contracts:
- **`POST /v1/chat/completions`**: Full support for non-streaming and Server-Sent Events (SSE) streaming (`stream: true`).
- **`GET /v1/models`**: Lists loaded and available local/distributed models.
- **`POST /v1/completions`**: Support for legacy/raw text completions.

### 2.2 Integration with Fleet Cognitive Engine
Fleet's internal LLM client (`agent/pkg/engine/llm_client.go`) communicates exclusively with standard OpenAI-compatible endpoints using JSON payloads and SSE streaming (`text/event-stream`).

- **Streaming SSE**: `exo` handles chunked responses containing `choices[].delta.content`, matching Fleet's expectations for real-time agent token processing.
- **Function / Tool Calling**: `exo` relays structured tool calls or standard system prompts depending on the underlying model weights and backend execution engine (MLX / llama.cpp).
- **Model Aliases & Parameter Handling**: `exo` accepts standard parameters including `temperature`, `top_p`, `max_tokens`, and `stop` sequences.

### 2.3 Compatibility with Host Gateway Routing (`llm-gateway`)
Per **ADR 00035** (*Host-Bridged Local LLM Inference Gateway*), Fleet provisions unprivileged agent containers with `--add-host=llm-gateway:host-gateway`. Any agent configured with `--model-url http://localhost:<port>/v1` has its URL translated at provisioning time to `http://llm-gateway:<port>/v1`.

When `exo` runs as a host process binding to `0.0.0.0:52415` (or any chosen gateway port), requests originating inside rootless Podman containers seamlessly route to the host's `exo` instance. The host `exo` node acts as the entrypoint coordinator (head node), delegating sub-graph activations across LAN peer nodes transparently to Fleet agents.

---

## 3. Local Network Topology & Discovery

### 3.1 Node Discovery Protocols
`exo` relies on zero-configuration peer discovery across the local network:
- **mDNS (Multicast DNS)**: Used for initial local subnet node advertising and automatic discovery. Nodes announce their available VRAM, CPU architecture, and device profile.
- **Peer-to-Peer Gossip Protocol**: Once discovered, nodes establish P2P communication channels (via Libp2p or custom UDP/TCP gossip) to maintain dynamic cluster status, health heartbeats, and memory state.

### 3.2 LAN Security & Perimeter Risks
Deploying `exo` in local network environments introduces notable security vulnerabilities:
- **Unencrypted Inter-Node Transport**: Inter-node tensor chunking, KV-cache transfers, and activation frames travel across the LAN in plaintext gRPC/TCP streams by default.
- **Missing Authentication Tokens**: The standard `exo` node API and cluster communication lack built-in mutual authentication (mTLS) or peer token authorization. Any device joined to the same LAN subnet can issue inference requests or inject itself as a peer node.
- **Eavesdropping & Sniffing Hazard**: Prompts, agent reasoning traces, and code snippets transmitted between sharded devices can be captured via passive ARP spoofing or packet sniffing on unencrypted Wi-Fi or untrusted local networks.

#### Mitigation Strategy for Fleet Workstations
To run `exo` safely in enterprise or shared network environments:
1. **Isolated Physical VLAN / Subnet**: Constrain `exo` peer nodes to an isolated physical switch or VLAN.
2. **Encrypted Overlay Network**: Route inter-node traffic exclusively over encrypted mesh VPNs (e.g., Tailscale, WireGuard) or mTLS proxy tunnels.

---

## 4. Hardware & Operating System Support

### 4.1 Multi-Platform Heterogeneity
`exo` explicitly targets mixed hardware ecosystems:
- **macOS (Apple Silicon)**: Native acceleration via Metal and MLX (`mlx-lm`). Leverages unified memory architectures (M1/M2/M3/M4 Max/Ultra) with high memory bandwidth (up to 800 GB/s).
- **Linux / Windows**: Support for NVIDIA CUDA, AMD ROCm, and CPU backends via `llama.cpp` and PyTorch / Tinygrad drivers.

### 4.2 Dynamic Model Partitioning & Memory Sharding
Rather than requiring homogeneous nodes, `exo` dynamically partitions model weights based on available device memory:
- **Topology Awareness**: Probes each node's available VRAM/RAM and compute capacity.
- **Pipeline & Ring Parallelism**: Splits transformer layers sequentially across devices (e.g., layers 1–24 on Node A, layers 25–48 on Node B, layers 49–80 on Node C).
- **Ring Attention / Tensor Sharding**: For large context windows, attention calculations are sharded across nodes to reduce per-node memory overhead.

---

## 5. Performance & Latency Analysis

### 5.1 Time-To-First-Token (TTFT) & Tokens-Per-Second (TPS)

| Metric / Scenario | Single-Node Ollama / llama.cpp (e.g., Mac Studio M2 Ultra) | Distributed `exo` Cluster (3x Apple Silicon / Linux Nodes over 1GbE LAN) | Distributed `exo` Cluster (3x Apple Silicon Nodes over 10GbE / Thunderbolt 4) |
| :--- | :--- | :--- | :--- |
| **Model Size Limit** | Constrained by single device memory (e.g., ~32B–70B @ Q4) | Expands across pooled memory (e.g., 70B–405B @ Q4/Q8) | Expands across pooled memory (e.g., 70B–405B @ Q4/Q8) |
| **Time-To-First-Token (TTFT)** | **Lowest** (0.2s – 0.8s) | **High** (2.5s – 8.0s due to multi-node activation handshakes) | **Moderate** (0.8s – 2.0s) |
| **Tokens-Per-Second (TPS)** | **Highest for supported models** (35–60 tps) | **Severely Bottlenecked** (3–10 tps) | **High for large models** (18–35 tps) |
| **Network Dependency** | Zero (host loopback memory bus) | Critical bottleneck (1 Gbps = ~110 MB/s transfer limit) | Optimal (10 Gbps / 40 Gbps = ~1.1–4.5 GB/s) |

### 5.2 Network Bandwidth Bottleneck
In auto-regressive LLM decoding, every generated token requires transferring key-value (KV) activation states across model layer boundaries. Over a standard 1GbE local network (110 MB/s), inter-node layer transfers introduce prohibitive latency, making token generation slow for real-time interactive coding agents. High-speed interconnects (10GbE, Wi-Fi 7, or Thunderbolt 4 mesh connections) are essential for viable distributed inference.

---

## 6. Runtime & Container Architecture

### 6.1 Host Daemon vs. Rootless Podman Containerization
Evaluating running `exo` as a host-level daemon vs. inside a rootless Podman container (`fleet-infra`):

```
+-----------------------------------------------------------------------------------+
| PHYSICAL HOST WORKSTATION                                                         |
|                                                                                   |
|  +-------------------------------------+                                          |
|  | exo Host Daemon                     |                                          |
|  | - Native Metal / CUDA drivers       |                                          |
|  | - mDNS & P2P LAN Discovery (0.0.0.0)|                                          |
|  | - Port 52415                        |                                          |
|  +------------------+------------------+                                          |
|                     ^                                                             |
|                     | Bridge Gateway Routing                                      |
|                     | (http://llm-gateway:52415/v1)                               |
|                     v                                                             |
|  +------------------+----------------------------------------------------------+  |
|  | Rootless Podman Container (fleet-agent)                                     |  |
|  | - Agent Harness (OpenCode / Claude / PiG)                                   |  |
|  | - Host Gateway Resolver (--add-host=llm-gateway:host-gateway)               |  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
```

#### Architectural Friction of Containerizing `exo`:
1. **GPU & Driver Passthrough**:
   - On macOS, Apple Silicon Metal GPU access is tied to the host Darwin kernel and cannot be passed through into Linux OCI container VMs.
   - On Linux, NVIDIA CUDA requires `nvidia-container-toolkit` and explicit device passthrough (`--device nvidia.com/gpu=all`), complicating rootless Podman deployment across heterogeneous distributions.
2. **mDNS Multicast & P2P Networking**:
   - Rootless Podman isolates container network namespaces (`slirp4netns` or `netavark`). Multicast mDNS packets and dynamic UDP P2P gossip ports are blocked or dropped without host networking (`--network host`), which violates Fleet's container network perimeter controls (ADR 00013, ADR 00014).

#### Decision: Host-Level Daemon Behind `llm-gateway`
`exo` **must run as a host-level daemon** on the physical workstation. Unprivileged agent containers communicate with `exo` via Fleet's existing `llm-gateway` host-gateway bridge.

---

## 7. Maintenance & Licensing Audit

### 7.1 License Analysis
- **`exo` Licensing**: `exo` is licensed under the **GPL-3.0 (GNU General Public License v3.0)**.
- **Fleet Licensing**: Fleet is licensed under the **MIT License** by Boggy Creek Software LLC.

#### Copyleft & Architectural Isolation Assessment:
Because Fleet interacts with `exo` exclusively over network boundary protocols (HTTP REST / JSON API) via `llm-gateway`, there is **no link-time or code-level derivative work creation**. Under standard FSF and OSI legal interpretations, network API interaction with a GPL-3.0 host daemon does not trigger copyleft viral licensing obligations on Fleet's MIT-licensed Go codebase.

However, bundling or redistributing pre-compiled `exo` binaries inside Fleet installer packages or container images would require compliance with GPL-3.0 distribution rules. Therefore, `exo` must remain an external host dependency managed separately by workstation operators.

### 7.2 Project Momentum & Maturity
- **Momentum**: `exo` has high community visibility and active open-source contribution activity on GitHub.
- **Maturity**: The project is in early/rapid development. API endpoints, memory partitioning heuristics, and backend driver bindings undergo frequent breaking revisions.
- **Stability**: Occasional node desynchronization and memory leak issues during long-lived cluster execution make it less stable than single-node Ollama for production workflows.

---

## 8. Strategic Recommendation & Roadmap

### 8.1 Verdict: DEFER Core Integration; ACCEPT as Optional Host Gateway Backend

1. **DEFER Core System Integration**: Fleet will **not** embed `exo` into the default installation suite (`install.sh`), binary builds, or automated container management (`fleet infra up`). Single-node inference via Ollama or `llama.cpp` remains the primary supported local LLM runtime.
2. **ACCEPT as Optional External Gateway Provider**: Workstations equipped with multiple local devices may run `exo` independently on the host network. Fleet fully supports `exo` as an external inference endpoint via standard gateway flags:
   ```bash
   fleet agent create my-agent --model-url http://llm-gateway:52415/v1 --model-name llama-3.3-70b
   ```

### 8.2 Implementation & Follow-Up Steps

| Phase | Task Description | Target Milestone |
| :--- | :--- | :--- |
| **Phase 1** | Document `exo` setup runbook for users wishing to pool LAN hardware in `doc/ai/distributed-inference-exo.md`. | Completed (This Document) |
| **Phase 2** | Add diagnostic port probe in `fleet agent doctor` to verify response from local gateway ports (including `52415`). | Next Minor Release |
| **Phase 3** | Re-evaluate `exo` core integration if project stabilizes, introduces native mTLS/Auth, and formalizes enterprise API contracts. | Future Backlog |
