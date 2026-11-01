---
name: fleet-architecture-review-findings
description: >-
  Comprehensive architectural review across the Fleet codebase, evaluating container vs host perimeter separation,
  subsystem decoupling, Podman pod co-location (ADR 00053), two-plane task tracking isolation, and protocol conformance.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Fleet Platform — Comprehensive Codebase Architectural Review & Boundary Integrity Audit

This report documents the architectural audit, boundary integrity assessment, and modular decoupling review conducted across the Fleet platform codebase (`cli/`, `agent/`, `backplane/`, `mcp/`, and companion OCI container assets).

---

## 1. Executive Summary

As Fleet matures from a container lifecycle utility into an orchestration platform for autonomous native cognitive agents, strict boundary discipline and modular decoupling are essential. This audit evaluates subsystem boundary contracts, container versus host perimeter guarantees, Podman pod co-location ergonomics (ADR 00053), two-plane task tracking isolation (Dolt `sndbx-*` on the host versus Forgejo `fleet/tasks.git` in-container), and multi-protocol conformance (MCP JSON-RPC 2.0, Valkey Streams RESP, and OpenSSH).

### Key Architectural Findings:
1. **Container vs Host Perimeter Discipline**: The host CLI (`cli/`) and in-container agent harness (`agent/`) maintain strong operational isolation. However, inverted module dependencies existed where in-container MCP tool harnesses (`mcp/cmd/forgejo-mcp`, `mcp/cmd/sonar-mcp`, `mcp/cmd/test-runner-mcp`) directly import host CLI packages (`cli/pkg/forgejo`, `cli/pkg/sonar`), coupling containerized execution artifacts to host management code.
2. **Podman Pod Co-Location Evolution (ADR 00053)**: Pod-based co-location provides strict netns loopback isolation (`127.0.0.1`), eliminating bridge exposure between the agent and egress filter sidecars. Runtime methods currently operate in dual mode (`--use-pod` / `FLEET_ENABLE_PODS=1`). Transitioning to pod-as-default requires harmonizing port namespace inspection, pause container resource controls, and atomic lifecycle handling.
3. **Two-Plane Task Tracking Isolation**: The physical separation between host-level platform issue tracking (Dolt database, GitHub `refs/dolt/data`, `sndbx-*`) and autonomous agent in-container task coordination (`fleet/tasks.git`, Forgejo, `~/tasks`, `bd`) is architecturally sound. Host Dolt credentials, git configuration, and data references remain unexposed to container boundaries.
4. **Monorepo Hygiene & Build Traceability**: Stale build references pointing to legacy `sandbox/` paths in OCI multi-stage Dockerfiles (`images/fleet-artifacts/Dockerfile`, `images/fleet-native-engine/Dockerfile`, `images/infra/openrouter-proxy/Dockerfile`) and `cli/cmd/fleet/update.go` have been identified and resolved to ensure clean reproducible builds.

---

## 2. Findings Matrix

| Component | Category | Severity | Finding | Recommendation |
| :--- | :--- | :--- | :--- | :--- |
| `mcp/` -> `cli/` | Modular Decoupling | **High** | Inverted module dependency: in-container MCP tools import `cli/pkg/forgejo` and `cli/pkg/sonar` directly from the host CLI module. | Extract shared Forgejo and Sonar API client logic into dedicated neutral SDK packages (e.g. `pkg/forgejo`, `pkg/sonar`, or under `mcp/pkg/`) so `mcp/` never depends on `cli/`. |
| `images/*` | Build Integrity | **Medium** | Stale `sandbox/` paths in multi-stage Dockerfiles for static engine and artifact images after module rename (ADR 00059). | Cleaned up all stale `COPY sandbox/` and `./sandbox/cmd/...` paths to reference `./cli/` directly across Dockerfiles and update logic. |
| `cli/pkg/runtime` | Isolation / Lifecycle | **Medium** | Podman pod orchestration (ADR 00053) operates in dual mode; standalone legacy container mode does not enforce loopback egress sidecar bindings. | Accelerate Phase 2 rollout of ADR 00053 to make pod co-location default for all agent instances, deprecating bridge-isolated standalone egress containers. |
| `agent/cmd/fleet-agent` | Container Perimeter | **Low** | Fallback default workspace path `/home/agent/workspace` is hardcoded in `main.go` when `WORKSPACE_DIR` is unset. | Ensure fallback behavior safely discovers `cwd` and gracefully handles non-standard mount paths without assuming static Debian `/home/agent` topology. |
| `cli/cmd/fleet` | Boundary Hygiene | **Low** | CLI commands (`fleet agent clone`, `fleet agent exec`) make static path assumptions (`/home/agent/workspace`) via `podman exec`. | Abstract in-container path resolution into agent configuration metadata (`cfg.WorkspaceDir`), enabling arbitrary container home directory layouts. |
| `mcp/cmd/beads-mcp` | Task Boundary | **Low** | `beads-mcp` defaults to `/home/agent/tasks` or `FLEET_TASKS_DIR`; lack of explicit repository discovery can cause silent failure if unmounted. | Add explicit pre-execution check verifying that the target task directory contains a valid `.beads` metadata folder before executing `bd` commands. |

---

## 3. Deep-Dive Architectural Evaluations

### 3.1 Container vs Host Perimeter Separation

#### Context & Invariants
Fleet's core architectural principle enforces that host systems and autonomous agents operate across a hard security boundary:
- **Host Domain (`cli/`)**: Orchestrates rootless Podman containers, manages host SSH configuration (`~/.local/share/fleet/ssh_config`), generates Ed25519 signing keys, and interacts with host Dolt issue tracking (`sndbx-*`).
- **Container Domain (`agent/`, `mcp/`, `images/`)**: Runs inside unprivileged rootless OCI containers (UID 1000). Autonomous agents interact with local services over loopback (`127.0.0.1`) or the internal container bridge network (`fleet-infra`), never executing host processes or mounting host root filesystems.

#### Boundary Analysis & Defect Identification
1. **Module Coupling Inversion**:
   - `mcp/cmd/forgejo-mcp/main.go` imports `github.com/boggycreek/fleet/cli/pkg/forgejo`.
   - `mcp/cmd/sonar-mcp/main.go` and `mcp/cmd/test-runner-mcp/main.go` import `github.com/boggycreek/fleet/cli/pkg/sonar`.
   - **Architectural Impact**: The `mcp` module's `go.mod` declares a dependency on `github.com/boggycreek/fleet/cli`. In-container binaries deployed to `/usr/local/bin` or `/opt/fleet/engine` should be completely independent of host orchestration logic. If the host CLI package evolves to include host-specific dependencies (such as OS keyring access or desktop notification hooks), those dependencies could inadvertently taint the in-container MCP harnesses.
   - **Target Evolution**: The REST API client implementations for Forgejo and SonarQube should be decoupled from `cli/` and relocated to a neutral shared SDK module or colocated within `mcp/pkg/` with thin wrappers used by `cli/`.

2. **In-Container Path Assumptions in Host CLI**:
   - In `cli/cmd/fleet/agent.go` (`runAgentClone`, `runAgentExec`, `runAgentOpen`), multiple commands construct `podman exec` arguments assuming `/home/agent/workspace` as the fixed working directory.
   - **Architectural Impact**: While standard Fleet images (`fleet-base`, `fleet-native`) adhere to `/home/agent/workspace`, custom developer container images (`.devcontainer/devcontainer.json` transformed via ADR 00051) may define alternative container users (e.g., `vscode`, `node`, `ubuntu`) and alternative home directories.
   - **Target Evolution**: Query the container's working directory or inspect `AgentConfig.WorkspacePath` rather than hardcoding `/home/agent/workspace`.

---

### 3.2 Subsystem Interface Decoupling & Wire Conformance

#### Dependency Graph
Fleet's monorepo structure consists of five Go workspaces:
```
agent/      -> depends on: backplane, mcp
backplane/  -> depends on: none (zero external dependencies)
cli/        -> depends on: backplane
mcp/        -> depends on: backplane, cli (architectural coupling)
images/infra/openrouter-proxy/ -> depends on: cli
```

#### Protocol Hardening & Conformance
- **MCP JSON-RPC 2.0 (`mcp/pkg/mcp`)**: Conforms strictly to the JSON-RPC 2.0 specification. It enforces mandatory `"jsonrpc": "2.0"` validation, bounded line reading (10MB maximum line length to prevent OOM denial-of-service), explicit serialization of response `"id": null` on parsing errors, and input sanitization against CLI flag injection (`-` prefix check) and directory traversal (`..`).
- **Valkey Streams RESP (`backplane/pkg/libbp`)**: Implements bounded line reading via `ReadSlice` and trailing CRLF validation on bulk strings. Destructive operations (`XTRIM`, `DEL`, `UNLINK`) on peer inboxes and outbox streams are strictly blocked via `guard.go`. Cryptographic payload validation enforces exact 32-byte Ed25519 public key checks before calling standard library functions to prevent runtime panics.
- **OpenSSH Subsystem**: Rootless SSH daemons inside containers bind to port 2222, exposed to host loopback via dynamic ephemeral ports (`-p 127.0.0.1::2222`). Zero host SSH credentials are ever mounted into containers; containers only receive host public keys (`fleet.pub`) into `/tmp/host-keys/authorized_keys`.

---

### 3.3 Podman Pod Co-Location Architecture (ADR 00053)

#### Current State & Evaluation
ADR 00053 specifies the co-location of agent containers and companion sidecars inside a single Podman pod (`fleet-<name>`).

1. **Network Namespace Loopback Sharing**:
   - Both `fleet-agent-<name>` and `fleet-agent-<name>-egress` share `127.0.0.1`. Outbound traffic from the agent is captured locally by the sidecar without passing across virtual bridge interfaces or requiring inter-container DNS lookups.
   - Port mappings are bound to the pod infrastructure pause container, providing a stable external interface regardless of member container lifecycle restarts.
2. **Sidecar Lifecycle Management**:
   - `cli/pkg/runtime/podman.go` provides atomic lifecycle operations (`podman pod start/stop/rm`). When an agent is stopped, companion egress containers are stopped atomically, eliminating orphaned sidecar containers.
3. **Rollout Transition Path**:
   - Pod support is currently gated behind the `--pod` / `--use-pod` CLI flags and the `FLEET_ENABLE_PODS=1` environment variable.
   - **Recommendation**: Transition `UsePods()` to default to `true` in a future release once all image presets (`fleet-core`, `fleet-daemon-base`) have undergone widespread testing under pod pause container cgroup constraints.

---

### 3.4 Task Tracking Architecture: Two-Plane Isolation

#### Architectural Separation Invariant
Fleet maintains an absolute architectural wall between two independent task planes:
1. **Host-Side Platform Development Plane**:
   - **Tooling**: Beads (`bd`) backed by a local Dolt database (`.beads/metadata.json`, embedded Dolt mode).
   - **Scope**: Platform issue tracking for Fleet development (`sndbx-*` issue prefixes).
   - **Remote Synchronization**: Dedicated data refs (`refs/dolt/data`) synchronized to GitHub.
   - **Access Boundary**: Host only. Container environments never have access to `.beads/`, `.dolt/`, or host git credentials.
2. **In-Infrastructure Autonomous Fleet Workload Plane**:
   - **Tooling**: In-container Beads (`bd`) and helper CLI `/usr/local/bin/fleet-tasks`.
   - **Scope**: Task coordination, multi-agent work allocation, and bug triaging among autonomous running agents.
   - **Remote Synchronization**: Synchronized to the local air-gapped Forgejo server at `http://forgejo:3000/fleet/tasks.git` (or SSH `ssh://git@127.0.0.1:2223/fleet/tasks.git`).
   - **Perimeter Bounds**: Agents operate entirely within `/home/agent/tasks`. Local repository git identity (`user.name="${AGENT_NAME}"`, `user.email="${AGENT_NAME}@local.sndbx"`) isolates task commit provenance from mounted project git trees.

#### Audit Verification
Codebase inspection confirms:
- Zero references to host Dolt data references (`refs/dolt/data`) or host issue tokens exist within `agent/`, `images/`, or `mcp/`.
- `beads-mcp` executes `bd` strictly against the task directory specified by `FLEET_TASKS_DIR` or `/home/agent/tasks`, with command runner sanitization preventing arbitrary path escape.

---

## 4. Prioritized Evolution & Hardening Recommendations

### Phase 1: Immediate Decoupling & Build Hygiene (Current)
-  **Remediate Build Artifacts**: Update stale `sandbox/` directory references in `images/fleet-artifacts/Dockerfile`, `images/fleet-native-engine/Dockerfile`, `images/infra/openrouter-proxy/Dockerfile`, and `cli/cmd/fleet/update.go`.
-  **Maintain High Code Coverage**: Preserve >= 90.0% unit test statement coverage across all Go packages.

### Phase 2: Interface Decoupling (Planned Evolution)
- **Extract Shared API Clients**: Move `cli/pkg/forgejo` and `cli/pkg/sonar` into an independent package namespace (e.g. `pkg/client/forgejo` and `pkg/client/sonar`) or internalize the client interfaces within `mcp/` so that `mcp` has zero dependencies on `cli`.
- **Dynamic Workspace Introspection**: Refactor `cli/cmd/fleet/agent.go` to query the container workspace path dynamically from container metadata or `AgentConfig` rather than hardcoding `/home/agent/workspace`.

### Phase 3: Pod-as-Default Migration (ADR 00053 Progression)
- **Default Pod Orchestration**: Flip the default value of `AgentConfig.UsePods()` to `true` across `fleet agent create` and `fleet agent start`.
- **Port Conflict Safeguards**: Implement port reservation validation across member containers within the same pod namespace to prevent collisions on loopback `127.0.0.1`.
