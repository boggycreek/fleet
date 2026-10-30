---
adr: "00059"
title: "Rename Sandbox Module to CLI and Modernize Go Module Paths"
topic: "Foundations & Architecture"
theme: "THEME-CORE"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
supersedes: []
tags:
  - refactor
  - cli
  - naming
  - modules
  - architecture
executive_summary: "Completes the project renaming initiative by moving the host CLI module directory from sandbox/ to cli/, updating its Go module path to github.com/boggycreek/fleet/cli, modernizing all internal monorepo modules (agent, backplane, mcp) to github.com/boggycreek/fleet/*, and eliminating all legacy boggycreek/sandbox import paths."
---

# 00059. Rename Sandbox Module to CLI and Modernize Go Module Paths

## Context

Under ADR 00047, the platform and its host CLI were formally renamed from "Agent Sandbox" (`sandbox` / `sndbx`) to "Fleet" (`fleet` / `ft`). That change migrated binary names, container prefixes, volumes, host configuration paths, and the upstream GitHub repository to `boggycreek/fleet`.

However, the host CLI and runtime module remained in the `sandbox/` directory with module path `github.com/boggycreek/sandbox`, and peer monorepo modules retained module paths under `github.com/boggycreek/sandbox/*` (`agent`, `backplane`, `mcp`). This residual legacy naming caused confusion for contributors and autonomous agents (such as Jules and coding agents), leading to path mismatches and failed import resolutions.

Bead `sndbx-cqp.6` mandates finalizing this transition by renaming `sandbox/` to `cli/` via `git mv`, updating all Go module definitions to `github.com/boggycreek/fleet/*`, and eliminating all remaining `boggycreek/sandbox` import paths across the repository.

## Decision (What)

1. **Directory Migration**:
   - The host management module directory `sandbox/` is moved to `cli/` using `git mv` to preserve commit history.

2. **Go Module Path Modernization**:
   - `cli/go.mod`: Defined as `module github.com/boggycreek/fleet/cli`.
   - `agent/go.mod`: Defined as `module github.com/boggycreek/fleet/agent`.
   - `backplane/go.mod`: Defined as `module github.com/boggycreek/fleet/backplane`.
   - `mcp/go.mod`: Defined as `module github.com/boggycreek/fleet/mcp`.

3. **Workspace Configuration (`go.work`)**:
   - `go.work` is updated to include `./agent`, `./backplane`, `./cli`, and `./mcp`.

4. **Import Path Migration**:
   - All internal package imports across all Go source files are migrated:
     - `github.com/boggycreek/sandbox/pkg/...` -> `github.com/boggycreek/fleet/cli/pkg/...`
     - `github.com/boggycreek/sandbox/cmd/...` -> `github.com/boggycreek/fleet/cli/cmd/...`
     - `github.com/boggycreek/sandbox/test/...` -> `github.com/boggycreek/fleet/cli/test/...`
     - `github.com/boggycreek/sandbox/backplane/...` -> `github.com/boggycreek/fleet/backplane/...`
     - `github.com/boggycreek/sandbox/agent/...` -> `github.com/boggycreek/fleet/agent/...`
     - `github.com/boggycreek/sandbox/mcp/...` -> `github.com/boggycreek/fleet/mcp/...`

5. **Build and CI Tooling Updates**:
   - `Makefile`: Updated to target `./cli/...` for linting, testing, coverage, SCA analysis, and binary builds.
   - `.github/workflows/release.yml`: Updated build path to `./cli/cmd/fleet`.
   - `install.sh`: Updated source build command to `./cli/cmd/fleet`.
   - `scripts/deadcode-check.sh`: Updated module paths to include `./cli/...`.
   - Dockerfiles (`images/agent-core/Dockerfile`, `images/agent-daemon-base/Dockerfile`, `images/agents/native/Dockerfile`): Updated `COPY` commands from `sandbox/` to `cli/`.

## Status

Accepted.

## Consequences

### Positive
- **Cohesive Identity**: Completely eliminates the legacy `boggycreek/sandbox` import paths and `sandbox/` directory.
- **Developer & Agent Clarity**: Standardizes all monorepo modules under `github.com/boggycreek/fleet/*`, avoiding import ambiguity in autonomous coding agents.
- **Historical Integrity**: Full Git history for all files in `sandbox/` is preserved via `git mv`.

### Negative / Trade-offs
- Internal branches authored prior to this change will need to rebase and adapt import paths to `github.com/boggycreek/fleet/cli/...`.
