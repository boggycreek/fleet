---
name: fleet-image-resolution
description: >-
  Explains how agent container images are resolved across well-known presets, local Podman image storage, and remote OCI registries.
  Use when creating agents, configuring custom container harnesses, or debugging image resolution.
---

# OCI Image Resolution & Tagging (ADR 00043)

## Image Resolution Tiers
When creating agents (`fleet agent create <name> [as <target>]`):
1. **Tier 1 (Well-Known Presets)**:
   - `base` -> `fleet-base:latest` (neutral base harness for custom or manual operator build-up)
   - `opencode` -> `fleet-opencode:latest`
   - `claude` -> `fleet-claude:latest`
   - `agy` -> `fleet-agy:latest`
   - `pig` -> `fleet-pig:latest`
   *Note*: `base` is an explicit preset target, not an implicit fallback. When `<target>` cannot be resolved, `fleet agent create` should emit a warning/error.
2. **Tier 2 (Local Podman Store Discovery)**:
   - Evaluates whether custom images built on the workstation exist locally before querying remote registries.
   - Probes: `<target>`, `<target>:latest`, `localhost/<target>`, `localhost/<target>:latest`.
   - Uses matched local store tag without requiring remote registry publishing.
3. **Tier 3 (Remote OCI Reference)**:
   - External registry references (`docker.io/...`, `ghcr.io/...`, `quay.io/...`).
   - Standardizes untagged inputs to `:latest`.

## Building Native Images
- Build CLI binaries: `make build-cli`
- Build base image: `make build-image-base`
- Build all images: `make build-images`
- Remote base images in Dockerfiles must use fully qualified references (e.g. `docker.io/library/golang:1.27-bookworm`) to prevent unqualified-search errors on strict Podman hosts.

Further reading:
- [ADR 00006 — Layered OCI Container Hierarchy](../adr/00006-LayeredOciContainerHierarchy.md)
- [ADR 00043 — Base OCI Hierarchy](../adr/00043-BaseOciHierarchy.md)
