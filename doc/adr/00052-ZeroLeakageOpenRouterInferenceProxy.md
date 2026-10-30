---
adr: "00052"
title: "Zero-Leakage OpenRouter Inference Gateway Proxy"
topic: "Inference & Secret Isolation"
theme: "THEME-SECURITY"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - inference
  - openrouter
  - proxy
  - security
  - secret-isolation
  - llm-gateway
executive_summary: "Establishes a dedicated, unprivileged reverse proxy container (fleet-openrouter) on the shared infrastructure bridge network that holds the master OpenRouter API key and injects authorization headers on-the-fly, allowing autonomous agent containers to query cloud frontier models without exposing raw access tokens to untrusted agent environments."
---

# 00052. Zero-Leakage OpenRouter Inference Gateway Proxy

## Context
Fleet provides an autonomous execution environment where AI coding agents operate decisivly inside rootless Podman containers with full access to compilers, git, package managers, and shell execution. Under Fleet's core security perimeter (ADR 00004, ADR 00018, ADR 00022), host secrets and cloud credentials must never leak into the agent container's environment or filesystem.

While local models (served via Ollama, llama.cpp, or vLLM over `http://llm-gateway:<port>/v1` per ADR 00035) do not require cloud API tokens, developers frequently require frontier external models (via OpenRouter or cloud inference providers) for complex reasoning, architectural evaluation, and multi-modal tasks.

Directly injecting an `OPENROUTER_API_KEY` into an agent container introduces severe security risks:
1. **Arbitrary Code Exfiltration**: Autonomous agents or third-party dependencies can read process environment variables (`/proc/<pid>/environ` or `env`) and transmit raw API keys over unmonitored network connections.
2. **Process `argv` Exposure**: CLI flags or sub-process invocations expose secrets across process tables (`ps aux`, `/proc/<pid>/cmdline`).
3. **Repository Leakage**: Careless or hallucinated commits by autonomous agents can accidentally stage environment files containing raw credentials to local or remote Git repositories.

## Decision (What)
We establish an isolated, single-responsibility **OpenRouter Inference Gateway Proxy** (`fleet-openrouter`) operating as a shared infrastructure service:

1. **Dedicated Proxy Container**:
   - A lightweight, statically compiled Go reverse proxy runs as `fleet-openrouter` on the `fleet-infra` bridge network, listening on internal port `8080`.
   - The container is provisioned during `fleet infra up` alongside `fleet-valkey` and `fleet-forgejo`.

2. **Isolated Host Secret Storage**:
   - The master `OPENROUTER_API_KEY` resides strictly on the physical host at `${XDG_DATA_HOME:-~/.local/share}/fleet/secrets/openrouter.key` (enforcing strict `0600` permissions) or in the host-side environment `${XDG_DATA_HOME:-~/.local/share}/fleet/.env`.
   - The key is mounted **exclusively** into `fleet-openrouter` and is never projected or mounted into any agent container.

3. **On-the-Fly Header Injection & Streaming**:
   - Agent containers query `http://fleet-openrouter:8080/v1/chat/completions` using standard OpenAI-compatible JSON-RPC payloads without providing an authorization header (or supplying a dummy placeholder).
   - The proxy intercepts requests, injects `Authorization: Bearer <OPENROUTER_API_KEY>`, injects optional attribution headers (`HTTP-Referer: https://github.com/boggycreek/fleet`, `X-Title: Fleet Agent Platform`), and forwards the request over HTTPS to `https://openrouter.ai/api/v1/chat/completions`.
   - The proxy provides full bidirectional Server-Sent Events (SSE) streaming support with zero buffering delay for real-time token delivery.

4. **Transparent URL Mapping**:
   - During `fleet agent create <name> --model-url openrouter`, the host CLI automatically resolves the endpoint to `http://fleet-openrouter:8080/v1` and sets `OPENAI_BASE_URL=http://fleet-openrouter:8080/v1` in the container configuration.
   - The agent cognitive harness interacts with OpenRouter models seamlessly through standard OpenAI client abstractions.

## Status
Proposed (Single-topic decision for OpenRouter secret isolation).

## Consequences
### Positive
- **Guaranteed Zero Secret Leakage**: The agent container has no cryptographic or filesystem path to discover the raw `OPENROUTER_API_KEY`.
- **Decoupled Key Management**: Operators can rotate or revoke keys on the host machine without rebuilding agent containers or restarting in-flight workloads.
- **Auditability & Cost Governance**: The proxy can log model usage, request timestamps, and token counts centrally without consuming agent context window tokens.
- **Turnkey OpenAI Compatibility**: Standard OpenAI-compatible client libraries inside agent harnesses require zero modifications.

### Negative / Trade-offs
- An additional lightweight container (`fleet-openrouter`) must be running on the `fleet-infra` bridge network.
- Requires network egress access to `openrouter.ai` from the proxy container, which must be permitted in firewall and egress rules.
