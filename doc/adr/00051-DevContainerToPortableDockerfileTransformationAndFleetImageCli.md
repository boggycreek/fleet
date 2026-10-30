---
adr: "00051"
title: "Dev Container to Portable Dockerfile Transformation and Fleet Image CLI Domain"
topic: "Container Runtime & Storage"
theme: "THEME-RUNTIME"
status: "proposed"
version: "v0.1.0-alpha"
as_built: false
tags:
  - oci
  - devcontainer
  - dockerfile-generator
  - fleet-image
  - image-compiler
  - composable
executive_summary: "Establishes the fleet image CLI domain and declarative compilation pipeline transforming devcontainer.json configurations into portable, source-controlled Containerfiles pre-composed with selected Fleet platform and cognitive engine artifact layers."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# 00051. Dev Container to Portable Dockerfile Transformation and Fleet Image CLI Domain

## Context & Problem Statement

Development teams increasingly adopt Development Containers (`.devcontainer/devcontainer.json`) to standardize repository build environments, dependencies, and tooling. However, standard Dev Container workflows rely on dynamic runtime initialization—executing `postCreateCommand` scripts, dynamic extension installations, and lifecycle lifecycle hooks after container instantiation.

This dynamic approach introduces significant operational friction:
1. **Inefficient OCI Caching**: Dynamic post-creation initialization bypasses static OCI layer caching, leading to slow container startup times and redundant package installations across workstations and CI/CD pipelines.
2. **Lack of Inspectability & Reproducibility**: Dynamic setup scripts create opaque, non-deterministic container states that cannot be audited or verified as source-controlled build artifacts prior to execution.
3. **IDE Proprietary Coupling**: Image builds and lifecycle management are tightly coupled to specific IDE extensions (such as VS Code Dev Containers or GitHub Codespaces runners), hindering execution in headless CI/CD pipelines, autonomous agent runtimes, and standalone CLI environments.

To resolve these challenges, the Fleet platform requires a deterministic, declarative compilation pipeline that transforms `.devcontainer/devcontainer.json` specifications into standalone, portable, source-controlled Dockerfiles/Containerfiles while injecting Fleet platform utilities and cognitive engine harnesses via a dedicated host CLI domain (`fleet image`).

---

## Decision (What)

We establish the **`fleet image` CLI domain** and a **multi-stage Containerfile transformation pipeline**:

### 1. Unified `fleet image` CLI Domain

The host CLI provides dedicated subcommands under the `fleet image` domain to manage custom container image generation, compilation, inspection, and lifecycle:

- `fleet image list`
  Inspects locally built and cached Fleet OCI images, displaying image tags, creation timestamps, base Dev Container sources, and injected cognitive engine layers.

- `fleet image generate [--engine <type>] [--output <path>]`
  Parses `.devcontainer/devcontainer.json` and synthesizes a portable, multi-stage `Containerfile` (or `Dockerfile`) pre-composed with the specified cognitive engine layer (`fleet-agent`, `claude`, `opencode`, or `pig`). Defaults to outputting to `.devcontainer/Containerfile.fleet`.

- `fleet image build <name> [--engine <type>]`
  Executes the compilation pipeline: synthesizes the portable Containerfile, applies security sanitization, and compiles/tags the custom agent container image using unprivileged rootless Podman.

- `fleet image remove <name>`
  Removes cached custom agent OCI images and associated build artifacts from local Podman storage.

### 2. Multi-Stage Composition Model

The compilation pipeline transforms `.devcontainer/devcontainer.json` into a standardized, multi-stage Containerfile using four distinct composition stages:

```
┌─────────────────────────────────────────────────────────────────┐
│ Stage 1: Project Base Layer                                     │
│ Derived from devcontainer.json (`image` or `build` definition)  │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│ Stage 2: Fleet Platform Artifacts Injection                      │
│ Copies binaries from `fleet-artifacts:latest` (ADR 00048)        │
│ (bpd, bd, bp, fleet-agent, unprivileged agent user UID 1000)   │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│ Stage 3: Cognitive Engine Layer Injection                       │
│ Copies engine harness from `fleet-native-engine:latest`        │
│ (ADR 00049) or selected variant (`fleet-agent`, `claude`, etc.) │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│ Stage 4: Policy Sanitization Pass                               │
│ Applies security transformations (ADR 00050) prior to Podman    │
│ build (stripping privileged hooks, enforcing non-root bounds)   │
└─────────────────────────────────────────────────────────────────┘
```

1. **Base Layer**: Extracted directly from `.devcontainer/devcontainer.json` `image` reference or local `build.dockerfile` path, establishing the project-specific system packages and environment.
2. **Platform Artifacts Injection**: Injects core Fleet platform runtime binaries from `fleet-artifacts:latest` ([ADR 00048]), provisioning `bpd`, `bd`, `bp`, `fleet-agent`, system path layouts, and unprivileged `agent` user (UID/GID 1000) permissions.
3. **Cognitive Engine Injection**: Injects selected cognitive engine harness artifacts from `fleet-native-engine:latest` ([ADR 00049]) or engine-specific variants (`fleet-agent`, `claude`, `opencode`, `pig`), configuring dispatch contracts and tool dependencies.
4. **Policy Sanitization Pass**: Applies pre-compilation security transformations ([ADR 00050])—stripping prohibited sudo/root escalation directives, neutralizing unauthorized entrypoint hooks, enforcing non-root user bounds, and normalizing container entrypoints.

---

## Status

Proposed.

---

## Consequences

### Positive
- **Deterministic & Auditable**: Containerfiles are rendered as explicit source code artifacts that can be committed to git, inspected, and verified before image compilation.
- **Optimal Layer Caching**: Pre-compiles dependencies into static OCI image layers, significantly reducing container startup latency across developer workstations and CI/CD runs.
- **IDE Independence**: Decouples image creation from proprietary IDE extensions, enabling uniform execution via host CLI (`fleet image`) and rootless Podman.
- **Composable Architecture**: Seamlessly combines project-specific Dev Container configurations with Fleet platform infrastructure and modular cognitive engine harnesses (`fleet-agent`, `claude`, `opencode`, `pig`).
- **Enforced Security Sanitization**: Verifies and sanitizes container build instructions prior to execution, mitigating privilege escalation risks.

### Negative / Trade-offs
- **Parser Maintenance**: Requires maintaining a `devcontainer.json` specification parser and Containerfile template generator within the `fleet image` host CLI.
- **Artifact Image Synchronization**: Requires keeping downstream platform (`fleet-artifacts`) and cognitive engine (`fleet-native-engine`) base images updated across Fleet release cycles.
