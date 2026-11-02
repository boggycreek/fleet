---
adr: "00043"
title: "Base OCI Hierarchy"
topic: "Container Runtime & Storage"
theme: "THEME-RUNTIME"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
supersedes:
  - "00006"
tags:
  - images
  - oci
  - layering
  - inheritance
  - daemon-base
  - tool-base
executive_summary: "Refactors the agent OCI image inheritance hierarchy into a neutral foundation (agent-sandbox-core) and two specialized intermediate base layers: daemon-base (providing background process supervision, SSH, and multiplexed sessions for fleet workers) and tool-base (providing direct, foreground CLI tool execution without daemon overhead for interactive pair programming). Supersedes ADR 00006."
---

# 00043. Base OCI Hierarchy

## Context

[ADR 00006](00006-LayeredOciContainerHierarchy.md) established a single, linear image inheritance hierarchy rooted in `agent-sandbox-base:latest`. That base image combined general development utilities (Debian, Deno, git, backplane CLI) with background service infrastructure (rootless `sshd`, `tmux`, and the `bpd` supervisor daemon as PID 1).

While this monolithic base image served autonomous, background fleet agents effectively, it created an architectural mismatch when running interactive pair-programming agents (such as Claude Code, Antigravity, or PiG). Interactive CLI agents require direct foreground execution attached to the user's terminal STDIN/STDOUT, with no requirement or desire for background process supervision (`bpd`), persistent rootless OpenSSH daemons, or detached `tmux` sessions.

Attempting to adapt the single base image to both use cases would require runtime flag inspection, conditional entrypoint branching, or container command overrides, introducing brittle heuristics into the container startup sequence.

---

## Decision (What)

We replace the monolithic base image hierarchy with a **three-tier, decoupled OCI inheritance model**, superseding [ADR 00006](00006-LayeredOciContainerHierarchy.md):

```
                         ┌──────────────────────────────────┐
                         │    agent-sandbox-core:latest     │
                         │    (Debian Bookworm, bd, git,    │
                         │     bp CLI, agent user 1000)     │
                         └─────────────────┬────────────────┘
                                           │
                  ┌────────────────────────┴────────────────────────┐
                  │                                                 │
                  ▼                                                 ▼
   ┌───────────────────────────────┐               ┌─────────────────────────────────┐
   │   agent-sandbox-daemon-base   │               │    agent-sandbox-tool-base      │
   │ (Fleet workers:               │               │   (Interactive pairing:         │
   │  bpd supervisor, sshd daemon, │               │    NO bpd, NO sshd, NO tmux,    │
   │  fleet-tasks, tmux session,   │               │    direct foreground exec of    │
   │  idle event loop)             │               │    agent runner / tool binary)  │
   └──────────────┬────────────────┘               └────────────────┬────────────────┘
                  │                                                 │
        ┌─────────┴─────────┐                             ┌─────────┴─────────┐
        ▼                   ▼                             ▼                   ▼
┌───────────────┐   ┌───────────────┐             ┌───────────────┐   ┌───────────────┐
│ claude-daemon │   │  agy-daemon   │             │  claude-tool  │   │   agy-tool    │
│ (Fleet agent) │   │ (Fleet agent) │             │ (Host agent)  │   │ (Host agent)  │
└───────────────┘   └───────────────┘             └───────────────┘   └───────────────┘
```

### 1. Foundational Core (`agent-sandbox-core:latest`)
- Minimal Debian Bookworm rootless foundation.
- Configures unprivileged user `agent` (UID/GID 1000).
- Installs universal runtimes and utilities: Beads (`bd`), backplane CLI (`bp`), `git`, `jq`, `ripgrep`, and standard developer utilities.
- Contains no process supervisors, no OpenSSH server, and no multiplexers.

### 2. Autonomous Fleet Base (`agent-sandbox-daemon-base:latest`)
- Extends `agent-sandbox-core`.
- Adds the Backplane Daemon (`bpd`) as PID 1 entrypoint, rootless OpenSSH server on port 2222, `fleet-tasks` coordinator, and `tmux` session configuration.
- Serves as the parent image for autonomous 24/7 fleet workers (`*-daemon:latest`).

### 3. Interactive Tool Base (`agent-sandbox-tool-base:latest`)
- Extends `agent-sandbox-core`.
- Omits `bpd`, `sshd`, and `tmux` entirely.
- Sets its entrypoint to execute the target agent harness directly in the foreground attached to standard input/output.
- Serves as the parent image for interactive pair-programming agents (`*-tool:latest`).

---

## Status

Accepted (Supersedes ADR 00006).

---

## Consequences

### Positive
- **Architectural Clarity**: Eliminates conditional runtime branching and entrypoint hijacking; images have static, immutable operational intents.
- **Minimal Footprint for Interactive Tools**: Interactive agents execute without zombie process monitors, idle network listeners, or multiplexing layers.
- **Layer Reuse**: Downstream tool and daemon images share identical cached root layers from `agent-sandbox-core`.

### Negative / Trade-offs
- **Build Pipeline Scope**: Increases the number of base build targets from one to three.
