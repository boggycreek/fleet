---
name: fleet-howto-enterprise-bifrost-sidecar
description: Operational runbook for deploying Bifrost as a multi-cloud AI gateway sidecar container on the fleet-infra network.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Deploying Bifrost as an Enterprise Multi-Cloud AI Gateway Sidecar

This runbook guides platform operators through deploying [Bifrost](https://github.com/maximhq/bifrost) (`maximhq/bifrost`) as an optional, high-throughput multi-cloud AI gateway sidecar within Fleet's rootless container infrastructure.

---

## Architecture & Gateway Comparison

Fleet agents require fast, reliable access to Large Language Models. Depending on security postures, deployment targets, and cloud agreements, operators can select between three inference routing tiers:

```
+--------------------------------------------------------------------------------------------------+
|                                    Fleet Agent Sandbox Container                                 |
+--------------------------------------------------------------------------------------------------+
              |                                  |                                   |
              | (Direct Bridge)                  | (Cloud Aggregator)                | (Enterprise Multi-Cloud)
              v                                  v                                   v
+-----------------------------+    +-----------------------------+    +-----------------------------+
| Host Model Gateway          |    | fleet-openrouter Sidecar    |    | fleet-bifrost Sidecar       |
| http://llm-gateway:11434/v1 |    | http://fleet-openrouter:... |    | http://fleet-bifrost:8080/v1|
| (Ollama, llama.cpp, vLLM)   |    | (OpenRouter proxy)          |    | (Bedrock, Vertex, Azure)    |
+-----------------------------+    +-----------------------------+    +-----------------------------+
              |                                  |                                   |
              v                                  v                                   v
    Local Host Hardware               OpenRouter Unified API             Direct Cloud VPCs / IAM
```

### When to Use Bifrost vs. Native Fleet Routing

| Routing Strategy | Primary Target | Strengths | Trade-Offs |
| :--- | :--- | :--- | :--- |
| **Direct Host Bridge (`llm-gateway`)** | Host-local inference (Ollama, llama.cpp, vLLM, Colibri, exo) | **0.00ms proxy overhead**, zero memory footprint, zero credential risk. | Single-workstation only; no multi-cloud failover or cloud IAM auth. |
| **OpenRouter Sidecar (`fleet-openrouter`)** | Cloud frontier models via OpenRouter (ADR 00052) | Zero-dependency, 140-line stdlib Go proxy, single secret injection, minimal attack surface. | Relies on OpenRouter; cannot directly authenticate to private AWS/GCP/Azure VPC endpoints. |
| **Bifrost Sidecar (`fleet-bifrost`)** | Enterprise multi-cloud (AWS Bedrock, GCP Vertex, Azure OpenAI, Anthropic) | Multi-provider routing, weighted fallback chains, startup SSE chunk buffering, virtual key governance. | Additional container process (~35–60 MB RAM); external dependency tree isolated in OCI container. |

> [!NOTE]
> Per the architectural evaluation in [`doc/ai/evaluations/bifrost-llm-gateway.md`](../ai/evaluations/bifrost-llm-gateway.md), Fleet preserves its stdlib-only Go codebase invariant by rejecting Bifrost as an embedded Go SDK library. Instead, Bifrost is deployed as an **isolated OCI sidecar container** attached to the shared `fleet-infra` network bridge.

---

## 1. Prerequisites

Before deploying the Bifrost sidecar container, ensure the following requirements are met on the host workstation:

1. **Rootless Podman**: Podman 4.4+ installed and operational.
2. **Shared Infrastructure Running**: The `fleet-infra` rootless network bridge must be active.
   ```bash
   fleet infra up
   ```
   Verify that `fleet-infra` exists:
   ```bash
   podman network exists fleet-infra && echo "Network ready."
   ```
3. **Enterprise Provider Credentials**: Valid API keys or cloud service tokens for your target providers (e.g., Anthropic, AWS Bedrock IAM keys, Azure OpenAI endpoints, or GCP Vertex service account credentials).

---

## 2. Credential Security & Secret Injection

Fleet mandates strict zero-leakage secret hygiene: credentials must **never** be passed as CLI command-line arguments (`--env KEY=...` in process tables), which leak into `/proc/<pid>/cmdline`. Instead, store credentials in a restricted environment file with `0600` permissions.

Create the configuration directory and credentials file:

```bash
mkdir -p ~/.config/fleet
touch ~/.config/fleet/bifrost.env
chmod 0600 ~/.config/fleet/bifrost.env
```

Populate `~/.config/fleet/bifrost.env` with your upstream cloud provider keys:

```bash
# Upstream Enterprise Cloud Credentials
ANTHROPIC_API_KEY=sk-ant-api03-...
OPENAI_API_KEY=sk-proj-...
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=us-east-1
AZURE_OPENAI_API_KEY=...
AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/

# Bifrost Server Configuration
BIFROST_PORT=8080
BIFROST_HOST=0.0.0.0
```

> [!IMPORTANT]
> Ensure `~/.config/fleet/bifrost.env` is readable only by the host user (`chmod 0600`). Never commit this file or include it in Git tracking.

---

## 3. Podman Container Deployment

You can deploy Bifrost using either interactive `podman run` commands or persistent systemd Quadlets for daemon management.

### Option A: Direct Podman Execution

Run the official Bifrost container attached directly to the `fleet-infra` bridge network. By naming the container `fleet-bifrost`, other containers on `fleet-infra` can resolve it via internal DNS at `http://fleet-bifrost:8080`.

```bash
podman run -d \
  --name fleet-bifrost \
  --network fleet-infra \
  --restart unless-stopped \
  --env-file ~/.config/fleet/bifrost.env \
  -p 127.0.0.1:8088:8080 \
  docker.io/maximhq/bifrost:latest
```

**Key Parameters:**
- `--name fleet-bifrost`: Assigns predictable DNS resolution within the `fleet-infra` bridge.
- `--network fleet-infra`: Connects the gateway to the same network namespace as Fleet agents and shared backplanes.
- `--env-file ~/.config/fleet/bifrost.env`: Loads secrets securely without process table exposure.
- `-p 127.0.0.1:8088:8080`: Optionally exposes port `8088` on host `127.0.0.1` for operator diagnostics and telemetry inspection while avoiding collision with host inference engines on port `8080`.

### Option B: Systemd Quadlet Unit (Recommended for Production)

For enterprise servers and persistent workstations, manage the Bifrost sidecar lifecycle via Podman Quadlet systemd user units.

Create the unit file at `~/.config/containers/systemd/fleet-bifrost.container`:

```ini
# ~/.config/containers/systemd/fleet-bifrost.container
[Unit]
Description=Fleet Bifrost Enterprise AI Gateway Sidecar
After=network-online.target
Wants=network-online.target

[Container]
ContainerName=fleet-bifrost
Image=docker.io/maximhq/bifrost:latest
Network=fleet-infra
EnvironmentFile=%h/.config/fleet/bifrost.env
PublishPort=127.0.0.1:8088:8080

[Service]
Restart=always

[Install]
WantedBy=default.target
```

Reload systemd and start the unit:

```bash
systemctl --user daemon-reload
systemctl --user start fleet-bifrost.service
systemctl --user status fleet-bifrost.service
```

---

## 4. Configuring Fleet Agents

With the `fleet-bifrost` sidecar running on the `fleet-infra` network, provision Fleet agents to route requests through Bifrost's OpenAI-compatible endpoint at `http://fleet-bifrost:8080/v1`.

### Creating a New Agent Targeting Bifrost

Use `fleet agent create` specifying `--model-url` and the desired target model:

```bash
fleet agent create enterprise-analyst as opencode \
  --role "Enterprise Multi-Cloud Code Reviewer" \
  --model-url http://fleet-bifrost:8080/v1 \
  --model-name claude-3-7-sonnet
```

For AWS Bedrock or Azure OpenAI deployments:

```bash
fleet agent create bedrock-engineer as base \
  --role "AWS Infrastructure Architect" \
  --model-url http://fleet-bifrost:8080/v1 \
  --model-name bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0
```

### Starting the Agent

Start the agent container and begin execution:

```bash
fleet agent start enterprise-analyst
```

The agent container connects to `fleet-infra`, resolves `fleet-bifrost:8080`, and forwards all inference requests through Bifrost's resilience pipeline without holding direct cloud credentials.

---

## 5. Healthcheck & Operational Verification

Validate that the gateway is functioning and routing requests properly.

### 1. Test Gateway Ingress from Host

Query the gateway's OpenAI-compatible model catalog from your host workstation:

```bash
curl -s http://127.0.0.1:8088/v1/models | jq .
```

**Expected Output:**
```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-3-7-sonnet",
      "object": "model",
      "owned_by": "anthropic"
    },
    {
      "id": "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
      "object": "model",
      "owned_by": "aws-bedrock"
    }
  ]
}
```

### 2. Verify Internal Network Resolution from Within `fleet-infra`

Confirm that containers attached to `fleet-infra` can resolve and communicate with `fleet-bifrost`:

```bash
podman run --rm --network fleet-infra alpine/curl:latest -s \
  http://fleet-bifrost:8080/v1/models | jq .
```

### 3. Test Chat Completion with Streaming

Send a test completion request through the gateway:

```bash
curl -s -X POST http://127.0.0.1:8088/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-7-sonnet",
    "messages": [
      {"role": "user", "content": "Respond with: BIFROST_ONLINE"}
    ],
    "max_tokens": 10
  }' | jq -r '.choices[0].message.content'
```

**Expected Output:**
```text
BIFROST_ONLINE
```

---

## 6. Monitoring & Troubleshooting

### Inspecting Container Logs
Inspect real-time gateway routing and failover events:

```bash
podman logs -f fleet-bifrost
```

### Common Issues & Remediation

| Symptom | Probable Cause | Remediation |
| :--- | :--- | :--- |
| **`curl: (7) Failed to connect to fleet-bifrost` inside agent** | Agent container is not attached to `fleet-infra` or `fleet-bifrost` stopped. | Run `fleet infra list` to verify network health and `podman inspect fleet-bifrost --format '{{.NetworkSettings.Networks}}'`. |
| **`HTTP 401 Unauthorized` returned by upstream** | Missing or malformed provider credentials in `~/.config/fleet/bifrost.env`. | Verify keys in `bifrost.env` and restart the container with `podman restart fleet-bifrost`. |
| **Port `8088` bind failure on host** | Another process is bound to host port `8088`. | Check port usage with `lsof -i :8088` or change the host publish port mapping (e.g. `-p 127.0.0.1:8089:8080`). |
| **Upstream timeout on large context requests** | Network egress throttling or cloud quota exhaustion. | Review Bifrost's retry configuration in `bifrost.env` and verify provider quota limits. |

---

## Related Documentation

- **[Architectural Evaluation: Bifrost as Drop-In Gateway](../ai/evaluations/bifrost-llm-gateway.md)**: Design tradeoffs and performance benchmark analysis.
- **[Shared Infrastructure Management](shared-infrastructure.md)**: Operating core Valkey and Forgejo services.
- **[Zero-Leakage OpenRouter Inference Proxy (ADR 00052)](../adr/00052-ZeroLeakageOpenRouterInferenceProxy.md)**: Default cloud proxy design.
- **[Agent Lifecycle Operations](agent-lifecycle.md)**: Agent creation, management, and retirement.
