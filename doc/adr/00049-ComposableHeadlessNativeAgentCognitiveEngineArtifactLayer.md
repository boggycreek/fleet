---
adr: "00049"
title: "Composable Headless Native Agent Cognitive Engine Artifact Layer"
topic: "Agent Runtime & Container Architecture"
theme: "THEME-RUNTIME"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - oci
  - cognitive-engine
  - composable
  - headless-agent
  - fleet-native-engine
  - artifacts
executive_summary: "Establishes a dedicated, zero-dependency static cognitive engine artifact OCI image (fleet-native-engine:latest) containing the headless fleet-agent cognitive daemon and native entrypoints for zero-friction composition into any developer toolchain base image."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# 00049. Composable Headless Native Agent Cognitive Engine Artifact Layer

## Context & Problem Statement

As specified in [ADR 00048](00048-FleetPlatformSharedArtifactsLayer.md), platform utility binaries (`bp`, `bpd`, `bd`, and the Model Context Protocol suite) are distributed via a shared `fleet-artifacts` OCI image layer. However, autonomous cognitive agent runtimes require an independent, lightweight, and composable artifact layer dedicated to the standalone headless cognitive engine daemon ([`fleet-agent` / `sndbx-agent`](00041-PurposeBuiltSandboxAgentRuntime.md)).

Previous agent image build patterns frequently compiled the native cognitive agent runtime inside individual toolchain base images or relied on thick container layers that bundled Go compilers, heavy Python wheels, dynamic C libraries, and full Linux OS distributions. This monolithic bundling created several architectural problems:

1. **Compiler and Runtime Bloat**: Including Go toolchains (`go build`) or build dependencies inside downstream toolchain base images significantly increased OCI image size and attack surface.
2. **Brittle Multi-Stage Build Chains**: Downstream toolchain images (e.g., Node.js, Python, Rust, or Java development environments) were forced to re-compile `fleet-agent` or pull large intermediate build stages, slowing down image matrix builds and CI/CD pipelines.
3. **Coupling Engine and Toolchain Releases**: Updating the standalone cognitive engine required rebuilding and redeploying every language-specific developer container image, preventing atomic engine upgrades.
4. **Architectural Separation**: Shared platform operational utilities (`bp`, `bpd`, `bd`) served by `fleet-artifacts` perform host/container messaging, process supervision, and issue tracking. In contrast, `fleet-agent` represents the core cognitive loop daemon responsible for LLM reasoning, priority event queues, and multi-threaded subsystem orchestration.

A dedicated, zero-dependency static cognitive engine artifact image (`fleet-native-engine:latest` / `agent-sandbox-native-engine:latest`) is needed to allow zero-friction composition of the cognitive engine into any developer toolchain base image without forcing toolchain maintainers to install Go or compile source code.

---

## Decision (What)

We establish **`fleet-native-engine:latest`** (with alias `agent-sandbox-native-engine:latest`) as a dedicated, zero-dependency static cognitive engine OCI artifact layer.

```
                  ┌─────────────────────────────────────────────────────────┐
                  │              fleet-native-engine:latest                 │
                  │              (FROM scratch, footprint <30MB)            │
                  │                                                         │
                  │   /usr/local/bin/fleet-agent (static CGO_ENABLED=0)     │
                  │   /usr/local/bin/sndbx-agent (legacy compatibility shim) │
                  └────────────────────────────┬────────────────────────────┘
                                               │
                                 Multi-Stage COPY --from
                                               │
      ┌────────────────────────────────────────┼────────────────────────────────────────┐
      │                                        │                                        │
      ▼                                        ▼                                        ▼
┌───────────────────────────┐    ┌───────────────────────────┐    ┌───────────────────────────┐
│   Node.js Toolchain Image │    │    Python / PyTorch Image │    │     Rust / Cargo Image    │
│  (COPY --from=engine ...) │    │  (COPY --from=engine ...) │    │  (COPY --from=engine ...) │
└───────────────────────────┘    └───────────────────────────┘    └───────────────────────────┘
```

### 1. Minimal OCI Image Architecture (`FROM scratch`)
- **Base Layer**: Built `FROM scratch` to ensure zero runtime dependencies, zero shell utilities, and zero OS package manager overhead.
- **Image Size Limit**: Kept strictly under **30 MB** (typically ~15–20 MB compressed), consisting entirely of statically compiled binaries.
- **Multi-Architecture Support**: Native support for `linux/amd64` and `linux/arm64` container architectures.

### 2. Static Go Compilation Standards
- Built from `agent/cmd/fleet-agent` using pure Go compilation:
  ```bash
  CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /build/fleet-agent ./agent/cmd/fleet-agent
  ```
- Debug symbols are stripped (`-s -w`) to minimize binary footprint and deter runtime reverse-engineering in untrusted environments.
- Zero dynamic library linkage (`ldd` yields `not a dynamic executable`), allowing the binary to execute on any Linux kernel version without glibc/musl dependencies.

### 3. Standardized Binary Paths and Entrypoint Shims
- Primary binary destination within the artifact layer: `/usr/local/bin/fleet-agent`.
- Compatibility symlink / shim provided at `/usr/local/bin/sndbx-agent` per [ADR 00041](00041-PurposeBuiltSandboxAgentRuntime.md) and [ADR 00047](00047-ProjectRenameFromSandboxToFleet.md).
- Downstream Dockerfile / Containerfile integration pattern:
  ```dockerfile
  COPY --from=fleet-native-engine:latest /usr/local/bin/fleet-agent /usr/local/bin/fleet-agent
  ```

### 4. Interoperability with Platform Components
- **`fleet-artifacts` (ADR 00048)**: Complements `fleet-artifacts` by separating cognitive loop binaries from general backplane and task tracking utilities (`bp`, `bd`).
- **Backplane Supervisor `bpd` (ADR 00008, ADR 00040)**: `fleet-agent` can execute as PID 1 directly in headless native containers ([ADR 00041](00041-PurposeBuiltSandboxAgentRuntime.md)) or be launched and supervised by `bpd` when deployed inside multi-process daemon containers ([ADR 00043](00043-BaseOciHierarchy.md)).

### 5. Non-Root Security and User Bounds
- Complies strictly with unprivileged non-root execution guidelines ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)).
- Binaries in the image are owned by root (`0:0`) with `0755` permissions, allowing unprivileged container user `agent` (UID/GID 1000) to execute `fleet-agent` without privilege escalation or permission errors.

---

## Status

Proposed.

---

## Consequences

### Positive
- **Zero Build-Time Dependencies**: Downstream container toolchains (Python, Node, Go, Rust) can integrate the native agent cognitive engine via a single Dockerfile `COPY` line without installing Go or compiler toolchains.
- **Ultra-Fast Container Assembly**: Layer copying from `FROM scratch` takes milliseconds during image builds and layer distribution across fleet nodes.
- **Minimal Footprint & Reduced Attack Surface**: Eliminating OS packages, shells, and dynamic libraries from the artifact image ensures zero CVE vulnerability noise in security scans.
- **Decoupled Release Lifecycles**: Engine improvements, bug fixes, and prompt logic updates in `fleet-agent` can be published independently of language toolchain image updates.

### Negative / Trade-offs
- **Multiple Artifact Images**: Introduces an additional image asset (`fleet-native-engine:latest`) to the platform OCI distribution matrix alongside `fleet-artifacts`.
- **Static Binary Restrictions**: CGO features (such as C-based SQLite or native host PAM authentication) cannot be embedded directly in `fleet-agent` without breaking `FROM scratch` compatibility; pure Go replacements must be utilized.
