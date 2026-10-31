---
name: fleet-colibri-evaluation
description: >-
  Architectural evaluation of JustVugg/colibri for NVMe-streamed frontier MoE model inference on Fleet physical workstations.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Architectural Evaluation: Colibri (JustVugg/colibri) for NVMe-Streamed MoE Inference in Fleet

## Executive Summary

This document evaluates **JustVugg/colibri** ([github.com/JustVugg/colibri](https://github.com/JustVugg/colibri)) — a pure C, zero-dependency inference engine designed to execute frontier-scale Mixture-of-Experts (MoE) models (e.g., DeepSeek-V4, GLM-5.2/5.3, Qwen-MoE, Kimi K3) on consumer physical workstations by streaming sparse routed expert weights from NVMe SSDs with dynamic RAM/VRAM caching.

In the context of **Fleet**, autonomous coding agents execute inside rootless Podman containers and access host inference via the local model gateway (`http://llm-gateway:<port>/v1` per ADR 00018 / ADR 00041). Colibri provides a novel pathway to run 200B+ to 700B+ parameter MoE models on physical developer workstations without multi-GPU clusters.

**Strategic Verdict**: **PROTOTYPE** for Tier-1/Tier-2 MoE models (e.g., Qwen3-Coder-30B, Qwen3.6-35B) where experts can be resident or semi-resident in system RAM/VRAM. **DEFER** for frontier 200B+–744B MoE models (GLM-5.2, DeepSeek-V4) in real-time interactive coding agent loops due to prompt prefill (TTFT) latency bottlenecks on CPU/NVMe streaming paths.

---

## Engine Architecture & Memory Hierarchy

Colibri's central architectural thesis is that Mixture-of-Experts models are massive on disk but activate only a small fraction of parameters per token. For example, GLM-5.2 contains 744B total parameters, but activates only ~40B parameters per token, with only ~11 GB of routed expert weights changing between adjacent tokens.

```
+-----------------------------------------------------------------------------------+
| System RAM / VRAM (Resident Tier)                                                 |
| - Token Embeddings & LM Head                                                      |
| - Multi-Head Attention / MLA Compressed KV Cache                                  |
| - Shared Experts & Dense Layers                                                   |
| - Hot Routed Experts (LRU Cache + .coli_usage Pinned Set)                         |
+-----------------------------------------------------------------------------------+
                                   ^
                                   | Cache Hit / Prefetch
                                   v
+-----------------------------------------------------------------------------------+
| NVMe SSD Storage (Streaming Tier)                                                 |
| - Cold Sparse Routed Experts (int4-g64 / fp8 Safetensors)                         |
| - Accessed via O_DIRECT pread pool & Router Lookahead Prefetch                    |
+-----------------------------------------------------------------------------------+
```

### 1. Resident vs. Streaming Partitioning
- **Dense Resident Layer (RAM/VRAM)**: Embeddings, Attention (QKV/O matrices), MLA (Multi-head Latent Attention) compressed KV state, shared experts, and LM projection head remain permanently resident in system RAM or GPU VRAM.
- **Sparse Expert Streaming (NVMe)**: Sparse routed expert Feed-Forward Networks (FFNs) reside on NVMe disk storage and are read on demand layer-by-layer during inference forward passes.

### 2. Dynamic LRU Caching & `.coli_usage` Pinned Hot Set
- **Layer-wise LRU Cache**: Each layer maintains a bounded LRU cache in system RAM or GPU VRAM for routed experts.
- **Learned Pinned Hot Set (`.coli_usage`)**: Colibri records routing heat history across turns in a persistent `.coli_usage` file. Frequently activated experts ("hot experts") are pinned into RAM/VRAM at startup.
- **Locality in Coding Workloads**: Multi-turn coding conversations exhibit high spatial expert locality (e.g., syntax parsing, language-specific constructs, tool-calling schemas repeatedly trigger the same expert clusters).

### 3. I/O Optimizations
- **Single-pread Requests**: An expert's three weight matrices (gate, up, down projections) are packed contiguous in storage and fetched in a single OS `pread` call.
- **Direct I/O (`DIRECT=1` / `O_DIRECT`)**: Bypasses the OS page cache on Linux/macOS, eliminating double-buffering overhead and memory copy penalties on fast NVMe drives.
- **Router Lookahead Prefetching**: A background worker thread evaluates router outputs for layer $N+1$ while layer $N$ computes, prefetching missing experts asynchronously from NVMe.
- **Multi-SSD Mirroring (`COLI_MODEL_MIRROR`)**: Supports striping expert reads across two independent PCIe NVMe drives on separate controllers, yielding up to a +37.5% decode speedup.

---

## Model Support & Quantization

Colibri implements a dedicated C engine per model family, eliminating runtime Python and heavy matrix library dependencies.

| Engine C Source | Model Family | Total Parameters | Active Parameters | Native Quantization | Recommended Disk Size |
|---|---|---|---|---|---|
| `qwen36.c` | Qwen3-Coder-30B-A3B | 30B | ~3.3B | int4-g64 / fp8 | 19 GB |
| `qwen36.c` | Qwen3.6-35B-A3B | 35B | ~3.5B | int4-g64 / fp8 | 23 GB |
| `deepseek_v4.c` | DeepSeek V4 Flash | 284B | 132/256 experts | int4-g64 | 167 GB |
| `deepseek_v41.c`| DeepSeek V4.1 Flash | 552B | Sparse MoE | int4-g64 / fp8 | 510 GB |
| `colibri.c` | GLM-5.2 / GLM-5.3 | 744B | ~40B | int4-g64 + int8 MTP | 429 GB |
| `glm53.c` | GLM-5.3-Flash | 321B | Sparse MoE | int4-g64 | 195 GB |
| `mimo.c` | MiMo-V2.6 Flash / Pro | 309B / 1.02T | Sparse MoE | int4-g64 | 172 GB / 564 GB |
| `kimi_k3.c` | Kimi K3 | 2.8T | Sparse MoE | int4-g64 | 1.56 TB |

### Quantization Architecture
- **int4-g64 Container**: 4-bit integer quantization with group scaling (group size 64) for sparse expert weights, preserving accuracy over traditional per-row quantization.
- **int8 MTP Head**: Multi-Token Prediction (MTP) draft heads use int8 quantization to draft 2.2–2.8 tokens per step on supported GLM models.
- **fp8 / bf16 / f16 Dense Weights**: Attention and dense layer projections maintain higher precision (fp8/bf16) in system memory to maintain model reasoning fidelity.

---

## OpenAI-Compatible Gateway Integration

Colibri includes a lightweight Python HTTP gateway wrapper (`openai_server.py` via `coli serve`) that invokes the compiled C engine.

### Server Endpoints & Protocol Support
- `POST /v1/chat/completions`: Full OpenAI-compatible chat endpoint supporting SSE streaming (`stream: true`), JSON mode (`response_format`), custom `stop` sequences, and `logprobs`.
- `POST /v1/messages`: Anthropic-compatible Messages API endpoint, enabling direct integration with coding CLIs (e.g., Claude Code, Aider, Crush, Pi) without proxy shimming.
- `POST /v1/systemone`: Native probability scoring endpoint for System One decision models (Laya, GLiNER2.5-Decide, Clef) and chat model choice evaluation without token generation.
- **Tool Calling Matrix**: Native XML/DSML/XTML tool parsing and generation for GLM, DeepSeek V4, Qwen3.6/3.8, Kimi K3, and MiMo engines.
- **Multi-Tenant Context Isolation (`--kv-slots N`)**: Allocates up to 16 isolated KV contexts in persistent memory. Concurrent requests on different slots are batched during decode steps while maintaining separate attention states.
- **Bounded Admission Scheduler**: Queue management via `--max-queue` and `--queue-timeout` returns OpenAI-standard 429 HTTP errors on saturation.

### Integration with Fleet `llm-gateway`

Fleet agents running inside Podman containers route LLM requests to `http://llm-gateway:<port>/v1`. The container runtime passes host resolution via `--add-host=llm-gateway:host-gateway`.

```
+------------------------------------------------------------+
| Podman Rootless Container (Fleet Agent)                    |
| - Agent CLI / Tool Harness (OpenCode, Claude, Native)      |
| - Sends POST http://llm-gateway:8000/v1/chat/completions   |
+------------------------------------------------------------+
                             |
                   Host Gateway Bridge
                             v
+------------------------------------------------------------+
| Host Workstation (Physical Host)                           |
| - `coli serve --model /nvme/qwen36 --port 8000`            |
| - Pure C Engine (`qwen36`) + Safetensors on NVMe           |
+------------------------------------------------------------+
```

To integrate Colibri into Fleet's gateway:
1. Start `coli serve` on the physical workstation host:
   ```bash
   coli serve --model /nvme/qwen36-35b --host 0.0.0.0 --port 8000
   ```
2. Configure Fleet's environment configuration (`~/.local/share/fleet/.env`):
   ```env
   FLEET_LLM_GATEWAY_URL=http://llm-gateway:8000/v1
   FLEET_LLM_MODEL=qwen3.6-35b
   ```
3. Agent containers query `http://llm-gateway:8000/v1` seamlessly without container modification.

---

## Performance & Developer Usability

### 1. Token Generation Latency (Decode)

Decode throughput depends primarily on NVMe read bandwidth, system RAM speed, and GPU VRAM capacity.

| Hardware Configuration | Model | PCIe Gen / Storage | Decode Throughput |
|---|---|---|---|
| Ryzen 7 8700GE (Integrated 780M Vulkan) | Qwen3.6-35B-A3B (Resident) | PCIe Gen4 NVMe | **9.9 tok/s** |
| RTX 3070 8GB + CPU (CUDA Tier) | Qwen3.6-35B-A3B (32 experts in RAM) | PCIe Gen4 NVMe | **25.4–30.0 tok/s** |
| Ryzen 9 5950X CPU-only | DeepSeek V4 Flash (284B) | Dual PCIe Gen4 NVMe | **1.24 tok/s** |
| RTX 5080 32GB + CPU (CUDA Tier) | DeepSeek V4 Flash (284B) | Dual PCIe Gen4 NVMe | **1.5–1.6 tok/s** |
| Ryzen AI Max+ 395 (128GB Unified) | GLM-5.2 (744B) | PCIe Gen4 NVMe | **1.83 tok/s** |
| 12-Core CPU / 25GB RAM Laptop | GLM-5.2 (744B) | Single Gen4 NVMe (Cold) | **0.05–0.10 tok/s** |
| 6x NVIDIA RTX 5090 (Full Resident VRAM) | GLM-5.2 (744B) | Resident VRAM | **9.0–9.2 tok/s** |

### 2. The Prompt Prefill Bottleneck (The "1-Hour Wall")

While streaming sparse experts from NVMe enables high-parameter models to decode tokens on workstations, **prompt prefill (prompt compilation) is attention-bound and compute-heavy**.

- **Agent Context Size**: Autonomous coding agents (such as OpenCode, Claude Code, or Aider) transmit system prompts, repository maps, file contents, and tool schemas totaling **10,000 to 20,000 tokens** per turn.
- **Prefill Computation**: During prefill, all attention layers across all prompt tokens must be computed sequentially. On CPU/NVMe streaming execution paths, prefill processes at **3 to 10 tokens per second**.
- **Impact on Time-To-First-Token (TTFT)**:
  - A 15,000-token prompt prefill on a 744B or 284B model running on a CPU workstation requires **25 to 50 minutes** before emitting the first generated token.
  - While KV slot prefix caching (`--kv-slots`) mitigates prefill latency on subsequent turns with identical prefixes, any turn with modified file context or new tool calls re-triggers significant prefill delays.

### 3. Viability for Agent Turn Loops

- **200B+ MoE Models (GLM-5.2, DeepSeek-V4, Kimi K3)**: **Unviable** for interactive multi-turn developer agent loops on consumer workstations. The combination of multi-minute TTFT prefill delays and ~1 tok/s decode throughput disrupts agent turn execution.
- **30B–35B MoE Models (Qwen3-Coder-30B, Qwen3.6-35B)**: **Highly Viable**. When dense layers and active experts fit in system RAM or VRAM, prefill completes in under 10 seconds and decode reaches 10–30 tok/s.

---

## Runtime & Host Isolation

### 1. Engine Build & Compilation Requirements
- **Dependencies**: Pure C architecture (`c/*.c`, `c/*.h`). No CMake, BLAS, Python, or CUDA/Vulkan libraries required for basic CPU execution. Compiles with standard `gcc` or `clang` and OpenMP (`-fopenmp`).
- **macOS Backend**: Native Apple Silicon Metal support (`backend_metal.m`) using unified memory zero-copy buffers (`newBufferWithBytesNoCopy`).
- **Linux Backend**: CUDA 11/12/13 support (`backend_cuda.cu`) and Vulkan 1.2 SPIR-V backend (`backend_vulkan.c`) for NVIDIA, AMD, and Intel GPUs.

### 2. Containerized vs. Physical Host Execution

| Execution Pattern | Performance & I/O | Hardware Access | Recommendation for Fleet |
|---|---|---|---|
| **Host Native Execution** | Maximum (`O_DIRECT`, direct `pread`, zero copy) | Direct NVMe, CUDA, Metal, Vulkan drivers | **Recommended**: Run `coli serve` as a host background daemon. |
| **Rootless Podman Container** | 5–15% I/O degradation via volume mounts | Requires `/dev/dri`, `/dev/nvme*` or `--gpus` pass-through | **Discouraged**: Adds unnecessary container translation layers for host hardware. |

Colibri performs heavy low-level disk I/O (`O_DIRECT`) and hardware acceleration. Executing `coli serve` directly on the physical host workstation and routing in-container agent traffic via `http://llm-gateway:<port>/v1` provides optimal performance while maintaining strict container isolation for agent execution.

---

## Lumabri Peer-to-Peer Cluster Inference

Colibri includes a companion cluster mode (formerly Lumabri) for clustering memory budgets across multiple local workstations over a LAN:

```
+------------------------------------------------------------+
| Coordinator Workstation (Token Generation & KV Cache)      |
| `./coli serve --cluster-coordinator http://127.0.0.1:8765` |
+------------------------------------------------------------+
         |                                          |
   10GbE / 2.5GbE LAN                        10GbE / 2.5GbE LAN
         v                                          v
+-------------------------------+          +-------------------------------+
| Worker 1 (Workstation A)      |          | Worker 2 (Workstation B)      |
| `./coli cluster worker ...`   |          | `./coli cluster worker ...`   |
| Stores Experts Layer 1..N/2   |          | Stores Experts Layer N/2..N   |
+-------------------------------+          +-------------------------------+
```

### Key Cluster Mechanics
- **Layer-wise Batch Requests**: The coordinator aggregates layer-level routed expert activations into a single TCP payload per layer, avoiding per-expert round trips.
- **Distributed Memory**: Allows 2 to 4 developer workstations with 64GB RAM each to pool memory, keeping 300B+ MoE model experts fully resident across the LAN.
- **Evaluation**: For engineering teams with multiple LAN-connected workstations, Lumabri converts disk-streaming latency into network-bounded bandwidth, increasing 300B model decode speeds from 1 tok/s to ~4–8 tok/s.

---

## Strategic Recommendation for Fleet

### Verdict Matrix

| Model Tier & Architecture | Recommended Verdict | Target Fleet Use Case |
|---|---|---|
| **Tier 1: Qwen3-Coder-30B / Qwen3.6-35B** | **PROTOTYPE** | Local coding agent turn loops (OpenCode, Claude, Native) via `llm-gateway`. |
| **Tier 2: System One Decision Models (Laya, Clef)** | **ADOPT** | Fast deterministic agent routing, review classifications, and binary checks (`/v1/systemone`). |
| **Tier 3: Frontier 200B+ MoE (DeepSeek-V4, GLM-5.2)** | **DEFER** | Real-time agent loops (deferred due to prompt prefill TTFT delays on CPU/NVMe). |

### Actionable Integration Plan for Fleet
1. **Model Gateway Standard**: Standardize local developer workstation inference on **Qwen3-Coder-30B-A3B** and **Qwen3.6-35B-A3B** using `coli serve` as an alternative local engine alongside Ollama and llama.cpp.
2. **System One Agent Integration**: Leverage `POST /v1/systemone` for agent workflow decisions (e.g., automated code review pass/fail classification) where prompt evaluation completes in <300ms without full token generation.
3. **Host Service Deployment**: Document runbooks for running `coli serve` natively on host workstations, binding to port 8000, and mapping `http://llm-gateway:8000/v1` into Fleet Podman agent containers.
