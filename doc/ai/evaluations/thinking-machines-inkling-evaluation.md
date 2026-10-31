---
name: fleet-inkling-evaluation
description: >-
  Architectural evaluation of Thinking Machines Inkling (975B/41B MoE) featuring controllable reasoning budget
  and Colibri NVMe sparse expert streaming for Fleet agent cognitive harnesses.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Architectural Evaluation: Thinking Machines Inkling (975B / 41B MoE)

## Executive Summary

**Thinking Machines Inkling** is an open-weights (Apache 2.0) frontier-scale Mixture-of-Experts (MoE) model comprising roughly **975 billion total parameters** with **41 billion active parameters** per forward pass across **256 fine-grained experts**. It introduces two innovations directly applicable to Fleet's autonomous agent harness:

1. **Programmatically Controllable Reasoning Budget**: A parameter (scaling from `0.2` to `0.99`) that allows runtime adjustment of inference compute and chain-of-thought (CoT) depth per turn based on task complexity.
2. **Extreme Sparsity (256 Experts)**: With only 41B parameters active per token (~4.2% active parameter ratio), Inkling is the primary target for NVMe SSD sparse expert streaming via **Colibri** (issue #107), enabling workstation-class hardware to run a ~1-trillion parameter model locally.

This evaluation assesses Inkling's architecture, reasoning budget API mechanics, hardware execution requirements, alignment profile, and strategic integration into Fleet's `llm-gateway` and agent harnesses.

---

## 1. Model Architecture & Routing Mechanisms

### Parameter Allocation & Sparsity
- **Total Parameters**: ~975 Billion.
- **Active Parameters**: ~41 Billion per forward pass.
- **Expert Topography**: 256 fine-grained experts with top-k dynamic routing (typically top-8 or top-16 experts activated per token).
- **Sparsity Efficiency**: The ~4.2% active parameter ratio enables frontier-level knowledge capacity and long-context reasoning while maintaining the inference compute budget and VRAM throughput of a ~40B dense model.

### Context Window & Attention Mechanisms
- **Context Length**: Native 128k token context window.
- **Attention Architecture**: Grouped-Query Attention (GQA) with an 8:1 key-value head ratio, significantly reducing KV cache memory footprint during long autonomous agent reasoning loops.
- **Positional Embeddings**: Rotary Position Embeddings (RoPE) calibrated for extended sequence stability across multi-file code editing, execution logs, and long-horizon tool interaction trace histories.

### Native Multimodal Tokenization
- **Unified Multimodal Vocabulary**: Unlike hybrid models that graft vision or audio adapters onto a text backbone, Inkling is pre-trained on an interleaved token stream spanning text, code, audio waveforms, and spatial vision tokens.
- **Agent Capabilities**: Enables direct processing of UI screenshots, architectural diagrams, audio logs, and codebases in a unified representation without cross-modality transformation bottlenecks or external OCR/adapter layers.

---

## 2. Programmatically Controllable Reasoning Budget

### API Mechanism
Inkling exposes an explicit inference parameter—`reasoning_budget`—ranging from `0.20` to `0.99`. This parameter directly controls the search depth, token budget allocated to internal reasoning steps, and self-reflection loops prior to producing output tokens.

```json
{
  "model": "thinking-machines/inkling-975b",
  "messages": [{"role": "user", "content": "Refactor package authentication middleware to support OIDC"}],
  "reasoning_budget": 0.85,
  "temperature": 0.2
}
```

### Trade-offs: Latency, Token Output, and Code Accuracy

| Budget Range | Mode | Behavior & Compute Profile | Latency Impact | Target Workload |
| :--- | :--- | :--- | :--- | :--- |
| **0.20 – 0.39** | *Fast Direct* | Minimizes internal CoT reasoning tokens; directly generates action or code response. | Very Low (1x baseline) | Syntax formatting, lint fixes, single-line edits, status checks. |
| **0.40 – 0.69** | *Balanced CoT* | Standard reasoning chain; evaluates primary solution path before emitting response. | Moderate (2x - 3x baseline) | Unit test writing, localized bug fixes, standard function implementations. |
| **0.70 – 0.89** | *Deep Search* | Extended deliberation, multi-branch exploration, self-correction, edge-case evaluation. | High (4x - 6x baseline) | Multi-file refactoring, API design, security perimeter validation. |
| **0.90 – 0.99** | *Maximum Deliberation* | Full hypothesis tree expansion, exhaustive failure analysis, cross-file invariant check. | Extreme (8x - 12x baseline) | Architectural migrations, complex spike investigations (e.g., `sndbx-7kb`), diagnostic auto-healing. |

### Mapping Reasoning Budgets to Fleet & Beads Task Priorities

Fleet autonomous agent harnesses receive tasks assigned via Beads (`bd`). Agents dynamically adjust `reasoning_budget` based on task priority (`P1` through `P4`):

- **P4 (Background / Low Severity / Maintenance)**:
  - `reasoning_budget: 0.25`
  - *Tasks*: Formatting (`make format`), dependency updates, documentation comment fixes.
- **P3 (Standard Bug / Feature)**:
  - `reasoning_budget: 0.50`
  - *Tasks*: Localized bug fixes, unit test coverage additions, CLI flag updates.
- **P2 (Major Feature / Refactoring)**:
  - `reasoning_budget: 0.75`
  - *Tasks*: Cross-package refactoring, protocol additions, database schema updates.
- **P1 (Critical / Architecture / System Spike)**:
  - `reasoning_budget: 0.95`
  - *Tasks*: Core security boundary modifications, architectural spikes, agent runner core upgrades.

---

## 3. Hardware Requirements & Local Workstation Execution

### VRAM & Memory Footprint

| Component | Quantization | Required Memory / Storage | Hosting Location |
| :--- | :--- | :--- | :--- |
| **Active Resident Weights** (41B parameters + Attention + Base Layers) | FP16 / FP8 / INT4 | **22 GB – 82 GB** | Pinned in GPU VRAM |
| **Total Expert Weights** (256 Experts, 934B parameters) | FP8 / INT4 | **468 GB – 935 GB** | Stored on Gen4/Gen5 NVMe SSD |
| **KV Cache** (128k context, GQA) | FP8 | **~12 GB – 16 GB** | GPU VRAM |

On a developer physical workstation equipped with a single 24GB or 48GB VRAM GPU (e.g., NVIDIA RTX 4090 or RTX 6000 Ada) and a high-throughput PCIe 5.0 NVMe SSD (7,000+ MB/s read speed), active base weights and KV cache reside in VRAM while expert weights are prefetched on demand.

### Colibri NVMe Expert Streaming Integration (Issue #107)
Because only 41B out of 975B parameters are active per token pass across 256 fine-grained experts, Inkling is a primary target for Colibri streaming:
- **Router Prediction**: Attention layers predict expert routing 1–2 layers in advance.
- **NVMe Asynchronous Prefetch**: Non-resident expert weights are streamed via direct DMA from NVMe storage into pinned host RAM and GPU VRAM staging buffers.
- **Result**: Enables workstation local execution of a 1-trillion parameter class model at practical streaming speeds (10–25 tokens/second) without requiring multi-node enterprise GPU clusters.

### Serving Runtimes & Fleet `llm-gateway` Placement
- **Inference Runtimes**: vLLM, SGLang, or Tinker platform engine configured with expert streaming plugins.
- **Integration**: Placed behind Fleet's local `llm-gateway` (`http://llm-gateway:<port>/v1`).
- **Protocol**: `llm-gateway` handles forwarding `reasoning_budget` in request payloads to backend inference endpoints while enforcing agent timeouts and context window constraints.

---

## 4. Agentic & Alignment Profile

### Censorship Resistance & Minimal Refusal Tuning
Unlike consumer-oriented chat models, Inkling is tuned with a **minimal refusal profile**:
- **System Command Execution**: Will execute shell commands (`iptables`, `podman`, `systemctl`, socket operations) without preachy safety warnings or false refusals.
- **Autonomous Operability**: Crucial for Fleet agents operating inside rootless Podman containers, where agents must perform low-level network inspects, key generation, process management, and diagnostic auto-healing without hitting safety guardrails.

### Tool Calling & Code Synthesis Quality
- **Native Structured Tool Calling**: Out-of-the-box support for strict JSON Schema function definitions and multi-tool invocations per turn.
- **Code Generation Integrity**: Trained on high-density polyglot code repositories (Go, Rust, Python, C/C++, TypeScript, Shell).
- **Low Hallucination Rate**: High zero-shot accuracy for shell command flags and standard library APIs.

---

## 5. Strategic Recommendation

1. **Integrate `reasoning_budget` in Fleet `llm-gateway`**:
   Extend `pkg/gateway` (or `llm-gateway` configuration) to recognize and pass `reasoning_budget` downstream to supporting backends (vLLM, SGLang, Tinker).
2. **Implement Priority-Based Auto-Budgeting in Agent Harnesses**:
   Configure agent harnesses (`opencode`, `claude`, `pig`, `native`) to automatically derive `reasoning_budget` from Beads task priority (`P1`–`P4`).
3. **Adopt Inkling as Primary Local MoE Target for Colibri**:
   Use Inkling 975B/41B MoE as the benchmark target model for validating Colibri NVMe expert streaming implementation (issue #107).
4. **Deploy as Local Alternative to Cloud Frontier Models**:
   Utilize Inkling locally on physical developer workstations for sensitive, offline, or high-compute agent workloads where cloud API latency or data privacy constraints apply.
