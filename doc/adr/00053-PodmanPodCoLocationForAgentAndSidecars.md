---
adr: "00053"
title: "Evaluate Podman Pod Co-location for Agent and Sidecars"
topic: "Network Isolation & Perimeter Defense"
theme: "THEME-NETWORKING"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - networking
  - podman
  - pods
  - sidecar
  - egress
  - architecture
  - evaluation
executive_summary: "Evaluates co-locating agent containers and companion sidecars inside rootless Podman pods sharing a single network namespace, comparing pod orchestration against standalone bridge-networked containers in terms of network egress isolation, lifecycle management, port mapping, and migration complexity."
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# 00053. Evaluate Podman Pod Co-location for Agent and Sidecars

## Context

In the current Fleet architecture, agent containers (`fleet-agent-<name>`) and companion egress sidecar containers (`fleet-agent-<name>-egress`) run as independent standalone containers attached to the shared `fleet-infra` bridge network ([ADR 00013](00013-PerInstanceNetworkIsolation.md), [ADR 00014](00014-DefaultDenyNetworkEgressFiltering.md)). Outbound egress traffic from an agent container is intercepted and filtered at the `fleet-infra` bridge level or redirected via container-to-container network routes.

While functional, this model has architectural limitations:
1. **Network Boundary Interception**: Egress filtering operates at the bridge network level rather than within the pod-local network namespace, requiring explicit network aliases and routing rules between separate container network interfaces.
2. **Lifecycle Decoupling**: Standalone containers must be created, started, stopped, and removed individually via distinct Podman invocations, creating potential state desynchronization if a sidecar is stopped while the main agent container remains running.
3. **Loopback Scope**: Companion sidecars (such as local egress proxies or inference gateways) cannot communicate over localhost loopback (`127.0.0.1`), requiring inter-container bridge DNS resolution.

Podman natively supports **pods** (`podman pod create`, `podman pod start`, `podman pod stop`, `podman pod rm`), where multiple containers share a single, unified Linux network namespace (identical to Kubernetes Pods) without requiring Kubernetes, `kubelet`, or any external daemon.

This decision record evaluates the technical feasibility, architectural trade-offs, lifecycle impact, port mapping, volume persistence, and migration path for adopting Podman pod co-location for Fleet agent workloads (`sndbx-tm8`).

## Technical Evaluation & Architectural Findings

### 1. Pod Architecture & Network Namespace Co-location

Under Podman pod orchestration, a pod infrastructure container (`infra` container) is created to manage the shared Linux namespaces (Network, UTS, IPC, and optionally PID):
```bash
podman pod create \
  --name fleet-<name> \
  --network fleet-infra \
  -p 127.0.0.1::2222
```

Containers join the pod via the `--pod fleet-<name>` argument:
- **Agent Container**: `podman run -d --pod fleet-<name> --name fleet-agent-<name> ...`
- **Egress Sidecar**: `podman run -d --pod fleet-<name> --name fleet-agent-<name>-egress ...`

#### Key Architectural Observations:
- **Shared Loopback Network Stack**: All containers in the pod share `127.0.0.1`. The agent container can bind or connect to egress filters, local HTTP proxies, or inference helper daemons on `127.0.0.1:<port>` directly, bypassing bridge routing overhead.
- **Egress Interception**: Outbound network connections originating from the agent container inside the pod share the netns with the egress sidecar, allowing `nftables` / `iptables` socket filtering or local loopback proxying (`HTTP_PROXY=127.0.0.1:3128`) without leaving the network namespace.
- **Port Namespace Ownership**: Port mappings (`-p 127.0.0.1::2222`) are bound to the pod infrastructure container rather than individual member containers.

### 2. Lifecycle Orchestration (`sandbox/pkg/runtime/podman.go`)

Transitioning from standalone containers to Podman pods alters the Go runtime execution flow:

| Lifecycle Action | Current Standalone Model | Podman Pod Model |
| :--- | :--- | :--- |
| **Creation** | `podman run -d --name fleet-agent-<name>` and `podman run -d --name fleet-agent-<name>-egress` | `podman pod create --name fleet-<name>` followed by `podman run --pod fleet-<name>` for each container |
| **Start** | `podman start fleet-agent-<name>` & `podman start fleet-agent-<name>-egress` | `podman pod start fleet-<name>` (starts all member containers atomically) |
| **Stop** | `podman stop fleet-agent-<name>` & `podman stop fleet-agent-<name>-egress` | `podman pod stop fleet-<name>` (stops all member containers gracefully) |
| **Cleanup / Remove** | `podman rm -f fleet-agent-<name>` & `podman rm -f fleet-agent-<name>-egress` | `podman pod rm -f fleet-<name>` (purges pod infra container and all member containers) |

#### Lifecycle Impact Analysis:
- **Simplified Lifecycle Methods**: Atomic operations (`podman pod start/stop/rm`) prevent orphaned sidecars and desynchronized container states.
- **Infrastructure Overhead**: Podman creates an unprivileged infra pause container (`k8s.gcr.io/pause` or `quay.io/libpod/pause`) per pod, adding ~2-5MB RAM per agent pod.
- **Rootless Netns Healing**: Self-healing netns logic ([ADR 00015](00015-RootlessNetnsRuntimeDirectoryAutoHealing.md)) must check pod network namespace existence in addition to container existence.

### 3. Port Mapping & SSH Connectivity

Dynamic SSH port mapping ([ADR 00016](00016-OneShotIdeRemoteDevelopmentAndHostEnsembling.md), [ADR 00017](00017-ManagedOpenSshConfigurationInclude.md)) is configured on the pod definition:
```bash
podman pod create --name fleet-<name> --network fleet-infra -p 127.0.0.1::2222
```

- **Port Inspection**: `podman pod inspect fleet-<name>` or `podman port fleet-<name> 2222` exposes the host-assigned ephemeral port (e.g., `127.0.0.1:34521`).
- **Compatibility**: `GetAgentSSHPort()` in `sandbox/pkg/runtime/podman.go` inspects the pod or member container. OpenSSH host blocks in `~/.local/share/fleet/ssh_config` (`Host fleet-<name>`) and thin-client IDE attachment via `fleet agent open` function identically without contract changes.

### 4. Volume & Persistence Bounds

Persistent volume bounds ([ADR 00005](00005-PersistedHomeVolumeAcrossRecreation.md)) remain strictly enforced:
- **Agent Home Volume**: `fleet-agent-<name>-home` is mounted specifically at `/home/agent:z` on the agent container inside the pod.
- **Sidecar Volume Isolation**: The egress sidecar container maintains separate rootless storage bounds and does not mount the agent home volume unless explicitly required for read-only audit logging.

### 5. Migration Strategy & Recommendations

#### Migration Phases:
1. **Phase 1 (Experimental Pod Flag)**: Introduce `--use-pod` flag in `fleet agent start` / `fleet agent create` and `sandbox/pkg/runtime` to create pod-backed agent instances alongside existing standalone containers.
2. **Phase 2 (Default Pod Orchestration)**: Transition default runtime methods (`StartAgentContainer`, `StopAgentContainer`, `CleanAgentContainer`) to manage `fleet-<name>` pods, maintaining fallback inspection for standalone legacy containers.
3. **Phase 3 (Deprecation of Standalone Mode)**: Require pod orchestration across all agent deployments.

## Decision (What)

Fleet adopts **Podman Pod Co-location as the target architecture** for agent containers and companion sidecars:

1. **Pod Unit Standard**: Every agent instance is encapsulated within a dedicated Podman pod named `fleet-<name>`.
2. **Shared Netns & Loopback**: The main agent container (`fleet-agent-<name>`) and egress sidecar container (`fleet-agent-<name>-egress`) run within `--pod fleet-<name>`, communicating via shared netns and loopback `127.0.0.1`.
3. **Atomic Operations**: Runtime methods in `sandbox/pkg/runtime/podman.go` manage pod lifecycle (`podman pod create`, `start`, `stop`, `rm`) rather than disconnected standalone container operations.
4. **Port Mapping Placement**: Ephemeral host SSH port mapping (`-p 127.0.0.1::2222`) is declared at pod creation time.

## Status

Proposed (Architectural evaluation complete; runtime implementation planned for progressive rollout under `sndbx-tm8`).

## Consequences

### Positive
- **Strict Egress Isolation**: Egress sidecar filters network traffic directly within the pod's shared network namespace, preventing bypass via bridge network interfaces.
- **Atomic Container Lifecycles**: `podman pod start/stop/rm` guarantees companion sidecars start, stop, and clean up in lockstep with the agent.
- **Simplified Local Communication**: Agent and sidecar containers communicate over `127.0.0.1` loopback without bridge DNS lookups.
- **Full Backward Compatibility**: External OpenSSH configuration, host port resolution, persistent named volumes, and IDE launching (`fleet agent open`) remain completely unchanged.

### Negative / Trade-offs
- **Infra Container Overhead**: Each pod runs a minimal pause container (~2-5MB RAM per agent instance).
- **Port Namespace Collision Caution**: Containers within the same pod share the port space on `127.0.0.1`; agent and sidecar services must bind to distinct container ports (e.g., SSH on 2222, egress filter control on 8080).
