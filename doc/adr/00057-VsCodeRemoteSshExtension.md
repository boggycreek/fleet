---
adr: "00057"
title: "VS Code Remote-SSH Extension for Fleet Agent Management"
topic: "IDE Plugin Management"
theme: "THEME-PLUGINS"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
tags:
  - vscode
  - remote-ssh
  - plugins
  - ide
  - extension
executive_summary: "Decision to implement a lightweight host-side VS Code management extension in plugins/vscode calling fleet agent list --json and triggering Remote-SSH connection URI (vscode://vscode-remote/ssh-remote+fleet-<name>/home/agent/workspace)."
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# 00057. VS Code Remote-SSH Extension for Fleet Agent Management

## Context

Fleet automatically manages OpenSSH configuration stanzas (`~/.local/share/fleet/ssh_config`) included into `~/.ssh/config` for each running agent container (`Host fleet-<name>`). Previously, operators attached to agent workspace environments using the command-line interface (`fleet agent open <name> in vscode` or `ssh fleet-<name>`).

While the CLI workflow provides robust terminal-based interaction, operators working within Visual Studio Code benefit from a native IDE management interface. A native sidebar view allows developers to visualize active and stopped agent instances, inspect container runtime states, start or stop agents, and trigger direct IDE workspace attachment without leaving VS Code.

## Decision (What)

Implement a native, lightweight VS Code extension in `plugins/vscode` (`com.boggycreek.fleet.vscode`) that provides:

1. **Sidebar Agent Explorer (`fleet-agents`)**:
   - Executes `fleet agent list --json` via host CLI execution to discover configured agent containers.
   - Renders a hierarchical TreeDataProvider reflecting real-time agent container status (`running`, `stopped`, `degraded`, or `HOST`).

2. **One-Click Remote-SSH Attachment**:
   - Triggers direct VS Code Remote-SSH workspace connection using the standard URI schema:
     `vscode://vscode-remote/ssh-remote+fleet-<name>/home/agent/workspace`
   - Leverages existing OpenSSH configuration managed by Fleet (`Include ~/.local/share/fleet/ssh_config`), avoiding duplicate connection management.

3. **Command Palette & Context Actions**:
   - `fleet.refresh`: Manual tree view refresh.
   - `fleet.attach`: One-click attach to running container workspace (or prompt/start if stopped).
   - `fleet.start`: Shell execution of `fleet agent start <name>` with notification feedback.
   - `fleet.stop`: Shell execution of `fleet agent stop <name>` with notification feedback.

4. **Zero-Dependency CLI Integration**:
   - Calls the `fleet` host binary directly via `child_process.execFile`, ensuring complete consistency with the host CLI runtime without requiring embedded heavy RPC daemons or duplicate container management logic.

## Status

Accepted (Alpha as-built).

## Consequences

### Positive
- **Visual Developer Workflow**: Direct visibility of active/stopped Fleet agent containers inside the VS Code sidebar.
- **Seamless Attachment**: One-click attach opens container workspace `/home/agent/workspace` via VS Code Remote - SSH (`ms-vscode-remote.remote-ssh`).
- **Architectural Reuse**: Completely reuses Fleet's existing OpenSSH config inclusion (`Include ~/.local/share/fleet/ssh_config`) and host CLI status inspection (`fleet agent list --json`).

### Negative / Trade-offs
- **Dependency on VS Code Remote - SSH Extension**: Direct connection relies on `ms-vscode-remote.remote-ssh` being installed in VS Code.
- **CLI Dependency**: Requires the `fleet` binary to be present on the operator's system `PATH` (or configured via extension settings).
