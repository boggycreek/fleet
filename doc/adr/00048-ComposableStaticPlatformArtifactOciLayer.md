---
adr: "00048"
title: "Composable Static Platform Artifact OCI Layer"
topic: "Container Runtime & Storage"
theme: "THEME-RUNTIME"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - oci
  - artifacts
  - composable
  - byoi
  - static-binary
  - layering
  - fleet-artifacts
executive_summary: "Establishes a zero-dependency static platform artifact OCI image (fleet-artifacts:latest / agent-sandbox-artifacts:latest) enabling seamless injection of Fleet's core platform binaries and tooling into arbitrary base images (Bring Your Own Image) without requiring OS-level package managers."
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# 00048. Composable Static Platform Artifact OCI Layer

## Context & Problem Statement

Existing agent images in Fleet inherit from a structured, rigid base image hierarchy ([ADR 00006](00006-LayeredOciContainerHierarchy.md), [ADR 00043](00043-BaseOciHierarchy.md), [ADR 00047](00047-ProjectRenameFromSandboxToFleet.md)). While this hierarchy (`fleet-core:latest` -> `fleet-daemon-base:latest` / `fleet-tool-base:latest`) provides a standard Debian Bookworm foundation for standard fleet agents, it imposes friction when operating in specialized runtime environments.

Users and developers needing to run specialized or enterprise environments—such as PyTorch/CUDA GPU workloads, custom Rust toolchains, Alpine-based Node.js stacks, or pre-configured Debian/RHEL enterprise images—currently face a difficult trade-off: they must either rebuild and port the entire Fleet container stack onto their custom base layer or forfeit native Fleet backplane coordination, task tracking, and MCP tool access.

Injecting statically linked Linux ELF binaries directly from a dedicated, minimal OCI artifact layer using standard multi-stage copy directives (`COPY --from=fleet-artifacts / /usr/local/bin/`) enables instantaneous, zero-overhead Fleet enablement on any base image without requiring OS-level package managers (`apt`, `apk`, `dnf`) or runtime library installations.

---

## Decision (What)

We establish the **Composable Static Platform Artifact OCI Layer** (`fleet-artifacts:latest`, aliased as `agent-sandbox-artifacts:latest`), a zero-dependency `FROM scratch` OCI image containing Fleet's pre-compiled platform executables, Model Context Protocol (MCP) servers, entrypoint shims, and task supervision helpers.

### 1. Artifact Composition

The artifact layer packages all core Fleet in-container binaries into a flat or structured executable directory hierarchy:

- **Core Fleet Executables**:
  - `bp`: Backplane messaging and event CLI ([ADR 00010](00010-ValkeyPubSubMessagingAndAclIsolation.md), [ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)).
  - `bpd`: Backplane supervisor daemon ([ADR 00008](00008-BackplaneDaemonAsPrimaryEntrypoint.md), [ADR 00040](00040-DualLobeSingleIdentityAgentArchitecture.md)).
  - `bd`: Distributed task and dependency tracker (Beads, [ADR 00028](00028-InContainerFleetTaskCoordinationViaLocalGiteaBackedBeads.md)).
- **Model Context Protocol (MCP) Suite** ([ADR 00032](00032-FleetModelContextProtocolSuiteForAgentWorkspaces.md)):
  - `bp-mcp`: Native backplane messaging MCP server.
  - `beads-mcp`: In-container task management MCP server.
  - `forgejo-mcp` (with `gitea-mcp` fallback symlink): Code repository and memory MCP server ([ADR 00046](00046-MigrateLocalForgeFromGiteaToForgejo.md)).
  - `sonar-mcp`: SonarQube mechanical analysis MCP server ([ADR 00027](00027-LocalSonarQubeDeterministicMechanicalAnalysisAndQualityGateInfra.md)).
- **Entrypoint Supervisor & Helper Scripts**:
  - `fleet-tasks`: Automated in-container task coordination helper script.
  - Runtime entrypoint shims and lifecycle wrappers (`agent-runner` dispatch contract shims, [ADR 00039](00039-HarnessAgnosticAgentRunnerDispatchContract.md)).

### 2. Multi-Architecture Support

The platform artifact image is published as a multi-architecture manifest supporting both primary target architectures:
- `linux/amd64`
- `linux/arm64`

### 3. Build & Distribution Architecture

- **Root Image**: Built strictly `FROM scratch` to guarantee zero base OS overhead, no embedded package managers, and zero shell vulnerabilities.
- **Go Static Compilation**: All Go binaries are compiled using strict static linking flags:
  ```bash
  CGO_ENABLED=0 go build -ldflags="-s -w -extldflags '-static'"
  ```
- **Footprint**: The overall image size footprint is kept under **50MB total**, allowing ultra-fast layer pulling and caching in build pipelines.
- **Image Identifiers**: Published as `fleet-artifacts:latest` and backward-compatible alias `agent-sandbox-artifacts:latest`.

### 4. BYOI (Bring Your Own Image) Composition Pattern

Downstream image maintainers convert any custom base image into a fully functional Fleet agent by adding a single multi-stage `COPY` instruction to their Dockerfile:

```dockerfile
FROM pytorch/pytorch:2.2.0-cuda12.1-cudnn8-runtime

# Inject Fleet static binaries into system path
COPY --from=fleet-artifacts / /usr/local/bin/

# Execute as unprivileged user (UID 1000)
USER 1000
WORKDIR /home/agent/workspace
ENTRYPOINT ["/usr/local/bin/bpd"]
```

### 5. Security & Permissions

- **Executable File Permissions**: All binaries, scripts, and shims in the artifact layer are stored with explicit executable file permissions (`0755`).
- **Non-Root User Compatibility**: Designed for unprivileged container execution ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)). Binaries require no `root` or `sudo` privileges to run and function seamlessly when executed by user `agent` (UID 1000) or any arbitrary non-root UID mapped by the container runtime.

---

## Status

Proposed.

---

## Consequences

### Positive
- **Extreme Flexibility & BYOI**: Enables instantaneous Fleet capability injection across arbitrary base images (GPU/CUDA, PyTorch, Node Alpine, Rust, Python, Enterprise RHEL/Debian) without rebuilding Fleet base image trees.
- **Zero Package Manager Pollution**: Does not require `apt-get`, `apk`, or `dnf` in downstream containers; avoids polluting target image package databases.
- **Fast Build Times & Caching**: Minimal size footprint (<50MB) allows near-instant image pulls and efficient layer caching.
- **Architectural Decoupling**: Decouples Fleet platform binary evolution from OS distribution releases and system library dependencies.

### Negative / Trade-offs
- **Static Linking Constraints**: Pure Go static binaries (`CGO_ENABLED=0`) cannot dynamically resolve host dynamic libraries (`libc` / `glibc`), requiring Go standard library implementations for DNS resolution and network calls.
- **Multi-Architecture Build Requirements**: Requires compiling and verifying binaries for both `linux/amd64` and `linux/arm64` in release workflows.
