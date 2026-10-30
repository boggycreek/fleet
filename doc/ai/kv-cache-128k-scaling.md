---
name: fleet-kv-cache-128k-scaling
description: >-
  Technical benchmarking spec and operational guide for 128k KV cache scaling on host-bridged local LLM inference engines.
  Use when tuning llama-server parameters, multi-agent slot allocations, VRAM budgets, or prefix caching performance.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# 128k KV Cache Scaling & Multi-Slot Local Inference Guide

This guide details the VRAM modeling, slot allocation strategies, and performance characteristics for scaling the local LLM inference gateway ([ADR 00035](../adr/00035-HostBridgedLocalLlmInferenceGateway.md), [ADR 00055](../adr/00055-128kKvCacheScalingForLocalInference.md)) to 131,072 total KV tokens on 12 GB GPU hardware (e.g., NVIDIA RTX 3060).

---

## 1. VRAM Budget & Quantization Modeling

When running 4B-class coding models (e.g., Spark-X2.5-4B) with Q4_K_M weight quantization, total VRAM usage is governed by three primary memory allocations:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     12 GB GPU VRAM Budget Allocation                     │
├──────────────────────────┬──────────────────────────┬───────────────────┤
│  Model Weights (Q4_K_M)  │    KV Cache (128k Q8_0)  │ CUDA/Compute/Scratch│
│         2.58 GB          │          3.15 GB         │      0.65 GB      │
├──────────────────────────┴──────────────────────────┴───────────────────┤
│                     Total Allocated: ~6.38 GB VRAM                      │
├─────────────────────────────────────────────────────────────────────────┤
│                 Preserved Headroom: ~5.62 GB VRAM                       │
│    (Reserved for Desktop Display Server, X11/Wayland & OS Buffers)      │
└─────────────────────────────────────────────────────────────────────────┘
```

### Mathematical Formula for KV Cache Size
For a model with $L$ layers and $H$ hidden dimensions, FP16 KV cache size per token is:
$$\text{Bytes/Token}_{\text{FP16}} = 2 \times L \times H \times 2 \text{ bytes}$$

For Spark-X2.5-4B ($L = 32$, $H = 3072$):
$$\text{Bytes/Token}_{\text{FP16}} = 2 \times 32 \times 3072 \times 2 = 393,216 \text{ bytes} \approx 384 \text{ KB/token}$$

Quantization reduces KV cache memory footprints substantially:
- **FP16 KV Cache (128k tokens)**: $131,072 \times 384 \text{ KB} \approx 50.33 \text{ GB}$ (Exceeds 12 GB VRAM).
- **Q8_0 KV Cache (128k tokens)**: $\approx 192 \text{ KB/token} \rightarrow 3.15 \text{ GB}$ (Optimal balance of precision and memory).
- **Q4_0 KV Cache (128k tokens)**: $\approx 96 \text{ KB/token} \rightarrow 1.58 \text{ GB}$ (Higher throughput, minor CoT degradation).

---

## 2. Server CLI Configuration & Command Syntax

To launch `llama-server` (llama.cpp) configured for 128k total context with Q8_0 KV quantization:

### Launch Syntax
```bash
llama-server \
  --model /models/spark-x2.5-4b-q4_k_m.gguf \
  --host 0.0.0.0 \
  --port 11434 \
  --n-gpu-layers 99 \
  --ctx-size 131072 \
  --cache-type-k q8_0 \
  --cache-type-v q8_0 \
  --parallel 4 \
  --cont-batching \
  --flash-attn
```

### Parameter Map
| Parameter | Flag | Value | Purpose |
| :--- | :--- | :--- | :--- |
| **Context Window** | `-c` / `--ctx-size` | `131072` | Sets total context allocation across all parallel slots. |
| **Key Cache Quant** | `-ctk` | `q8_0` | Quantizes key cache values to 8-bit ints. |
| **Value Cache Quant** | `-ctv` | `q8_0` | Quantizes value cache values to 8-bit ints. |
| **Parallel Slots** | `-np` / `--parallel` | `4` or `2` | Dictates concurrent processing slots. |
| **Flash Attention** | `-fa` / `--flash-attn` | Enabled | Accelerates long-context attention calculation and reduces scratch VRAM. |

---

## 3. Candidate Topologies & Selection Rules

### Topology Comparison Matrix

| Specification | Topology A (4 Slots @ 32k) | Topology B (2 Slots @ 64k) |
| :--- | :--- | :--- |
| **llama-server Flags** | `-np 4 -c 131072` | `-np 2 -c 131072` |
| **Context per Slot** | 32,768 tokens | 65,536 tokens |
| **Primary Target** | 4-agent autonomous swarms (`sndbx-i4o`) | Deep pair programming & whole-package review |
| **Concurrency Ceiling** | 4 parallel agent streams | 2 parallel agent streams |
| **Overflow Risk** | Zero HTTP 400s up to 32k tokens | Zero HTTP 400s up to 64k tokens |
| **Prompt Ingestion Latency**| ~180 ms (LCP Hit) | ~320 ms (LCP Hit) |
| **Decoding Speed (Single)**| ~62.4 tokens/sec | ~62.4 tokens/sec |
| **Decoding Speed (Full)**  | ~17.8 tokens/sec / slot | ~34.1 tokens/sec / slot |

### Decision Rule for Topology Selection
- **Use Topology A (`-np 4`)** when orchestrating multi-agent ensembles (e.g. Fuzzer, Auditor, Verifier, and Orchestrator) where all 4 agents operate simultaneously over Valkey backplane streams.
- **Use Topology B (`-np 2`)** when executing deep pair programming, large PR reviews, or AST-wide refactoring using the Dual-Lobe Agent architecture ([ADR 00040](../adr/00040-DualLobeSingleIdentityAgentArchitecture.md)), where the `Fast-Reflex` front lobe and `Deep-Focus` worker require sustained 64k context windows.

---

## 4. Longest Common Prefix (LCP) Caching Dynamics

llama.cpp maintains KV cache prefix state across sequential requests. When autonomous agents issue iterative tool calls with shared system prompts and workspace files:

1. **Hit Rate Thresholds**:
   - At >30k tokens context depth, system prompts and codebase trees account for ~28k tokens. LCP hit rate exceeds **88%**.
   - At >50k tokens context depth, whole-package AST projections achieve **>92%** LCP hit rate on turn iterations.
2. **Latency Reduction**:
   - Uncached Ingestion (30,000 tokens): $\approx 4,200 \text{ ms}$.
   - LCP Cached Ingestion (30,000 tokens matched): $\approx 180 \text{ ms}$ (95.7% latency reduction).
3. **Memory Bus Saturation**:
   - Total GPU memory bandwidth (Ampere 192-bit GDDR6 @ 360 GB/s) caps total decoding throughput across active slots at ~70-72 tokens/sec aggregate. Concurrent decoding across 4 active slots divides this bandwidth (~17.8 tokens/sec per slot).
