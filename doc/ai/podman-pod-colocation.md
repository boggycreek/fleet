---
name: fleet-podman-pod-colocation
description: >-
  Architectural evaluation and operational guide for Podman pod co-location of agent containers and companion sidecars.
  Use when designing network isolation, sidecar proxying, or runtime container orchestration in Fleet.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Podman Pod Co-Location for Agent Containers and Sidecars

## Overview & Background

In the Fleet containerized agent platform, agent sandboxes run in rootless Podman containers attached to the `fleet-infra` bridge network. To enforce default-deny network egress policy ([ADR 00014](../adr/00014-DefaultDenyNetworkEgressFiltering.md)), companion sidecars (`fleet-agent-<name>-egress`) intercept outbound connections.

Historically, the agent container and egress sidecar container ran as independent standalone containers on `fleet-infra`. Transitioning to **Podman Pods** (`podman pod create`, `podman pod start`, etc.) co-locates member containers inside a single, unified Linux network namespace (`netns`), sharing loopback (`127.0.0.1`) and IPC while maintaining container storage and filesystem isolation.

This document serves as an architectural reference and operational guide for Podman pod integration in Fleet (`sndbx-tm8`).

---

## Architectural Comparison: Standalone vs Pod Co-location

```
STANDALONE MODEL (Legacy)                  PODMAN POD CO-LOCATION (ADR 00053)
+------------------------------------+     +-------------------------------------------------+
| Bridge Network: fleet-infra        |     | Pod: fleet-<name> (Shared netns & 127.0.0.1)    |
|                                    |     |                                                 |
|  +------------------------------+  |     |  +--------------------+   +-------------------+ |
|  | Agent: fleet-agent-<name>    |  |     |  | Agent Container   |   | Egress Sidecar    | |
|  | IP: 10.88.0.10             |  |     |  | (fleet-agent-bot) |   | (fleet-agent-...  | |
|  +--------------+---------------+  |     |  +---------+----------+   |  -egress)         | |
|                 | (Bridge route)|  |     |            |              +---------+---------+ |
|                 v                  |     |            | (Loopback              |           |
|  +--------------+---------------+  |     |            v  127.0.0.1)            v           |
|  | Egress Sidecar:              |  |     |  +---------+------------------------+---------+ |
|  | fleet-agent-<name>-egress    |  |     |  | Infra Pause Container / Shared Netns       | |
|  | IP: 10.88.0.11             |  |     |  +------------------------+------------------+ |
|  +------------------------------+  |     +---------------------------|---------------------+
+------------------------------------+                                 | -p 127.0.0.1::2222
                                                                       v
                                                              Host SSH Port (e.g. 127.0.0.1:34521)
```

### Comparative Metrics

| Characteristic | Standalone Containers | Podman Pod Co-location |
| :--- | :--- | :--- |
| **Network Namespace** | Independent netns per container | Single shared netns across member containers |
| **Local Proxy Address** | Inter-container DNS / IP (e.g., `10.88.0.11:3128`) | Loopback local interface (`127.0.0.1:3128`) |
| **Egress Interception** | Bridge interface routing / iptables forward | Pod-local socket inspection & iptables |
| **Lifecycle Command Count**| `N` start/stop/rm commands per sidecar | 1 atomic `podman pod <start\|stop\|rm>` command |
| **Port Mapping Binding** | Bound directly to agent or egress container | Bound to pod infrastructure pause container |
| **Memory Footprint** | Base RAM per container | +2–5MB RAM for infra pause container per pod |

---

## Detailed Technical Requirements & Evaluation

### 1. Architecture & Feasibility

#### Pod Creation & Orchestration
Creating a pod configures the shared network namespace and publishes host ports:
```bash
# Create pod with host port mapping for container SSH (2222)
podman pod create \
  --name fleet-demo \
  --network fleet-infra \
  -p 127.0.0.1::2222
```

#### Joining Containers to the Pod
Member containers specify `--pod fleet-<name>` instead of individual `-p` port mappings:
```bash
# Start Agent container inside pod
podman run -d \
  --pod fleet-demo \
  --name fleet-agent-demo \
  --hostname demo-sandbox \
  -v fleet-agent-demo-home:/home/agent:z \
  fleet-base:latest

# Start Companion Egress Sidecar inside the same pod
podman run -d \
  --pod fleet-demo \
  --name fleet-agent-demo-egress \
  --cap-add NET_ADMIN \
  --cap-add NET_RAW \
  fleet-egress:latest
```

#### Shared Loopback & Inter-Process Communication
Because both containers share the pod netns:
- `curl http://127.0.0.1:8080` inside `fleet-agent-demo` connects directly to a sidecar service listening on port 8080 inside `fleet-agent-demo-egress`.
- Traffic avoids virtual bridge packet traversal (`veth` pairs) between containers, reducing inter-sidecar latency.

---

### 2. Port Mapping & SSH Connectivity

#### Dynamic SSH Port Inspection
Host port mappings belong to the pod infrastructure container. To inspect assigned ports:
```bash
# Method A: podman port
podman port fleet-demo 2222
# Output: 127.0.0.1:41235

# Method B: podman pod inspect
podman pod inspect fleet-demo --format '{{range .InfraConfig.PortBindings}}{{range .}}{{.HostPort}}{{end}}{{end}}'
```

#### OpenSSH & IDE Integration Compatibility
In `sandbox/pkg/runtime/podman.go`, `GetAgentSSHPort()` queries `podman port <pod-or-container-name> 2222`.

- **SSH Config Sync** (`fleet agent ssh-config`): Generates host blocks pointing to `127.0.0.1:<discovered_port>` targeting `~/.local/share/fleet/ssh_config`.
- **One-Shot IDE Launching** (`fleet agent open`): Connects seamlessly via OpenSSH host entry `fleet-<name>` ([ADR 00016](../adr/00016-OneShotIdeRemoteDevelopmentAndHostEnsembling.md)).

---

### 3. Volume & Persistence Bounds

Co-locating containers in a pod **does not** automatically share filesystems or volume mounts:
- The persistent named volume `fleet-agent-<name>-home` (mounted at `/home/agent:z`, [ADR 00005](../adr/00005-PersistedHomeVolumeAcrossRecreation.md)) remains exclusively mounted to `fleet-agent-<name>`.
- The egress sidecar maintains isolated rootless storage bounds. Read-only host certificates or config files can be selectively mounted to specific containers within the pod without exposing them to other pod members.

---

### 4. Lifecycle Methods in Go Runtime (`sandbox/pkg/runtime/podman.go`)

Below is a comparison of runtime implementation patterns in Go:

#### Pod-based Lifecycle Pseudocode
```go
// StartAgentPod creates or starts a pod and its member containers
func StartAgentPod(ctx context.Context, cfg *config.AgentConfig) error {
    podName := fmt.Sprintf("fleet-%s", cfg.Name)

    // 1. Ensure Pod exists
    checkPod := CommandContext(ctx, "podman", "pod", "exists", podName)
    if err := checkPod.Run(); err != nil {
        createPod := CommandContext(ctx, "podman", "pod", "create",
            "--name", podName,
            "--network", infraNetwork,
            "-p", "127.0.0.1::2222",
        )
        if err := createPod.Run(); err != nil {
            return fmt.Errorf("failed creating pod %s: %w", podName, err)
        }
    }

    // 2. Start Pod (starts all member containers atomically if created)
    startPod := CommandContext(ctx, "podman", "pod", "start", podName)
    return startPod.Run()
}

// CleanAgentPod stops and removes the pod and all member containers
func CleanAgentPod(ctx context.Context, agentName string) error {
    podName := fmt.Sprintf("fleet-%s", agentName)
    _ = CommandContext(ctx, "podman", "pod", "stop", podName).Run()
    return CommandContext(ctx, "podman", "pod", "rm", "-f", podName).Run()
}
```

---

## Migration Path & Recommendations

1. **Progressive Rollout**: Implement Podman pod orchestration behind a configuration flag or runtime feature check (`FLEET_ENABLE_PODS=1`).
2. **Fallback Port Discovery**: Update `GetAgentSSHPort()` to check `podman port fleet-<name> 2222` first, falling back to container inspection for legacy non-pod instances.
3. **Netns Healing Alignment**: Ensure preflight health checks (`EnsureRootlessNetNS`) reconcile desynchronized network mounts for pod infrastructure pause containers.
