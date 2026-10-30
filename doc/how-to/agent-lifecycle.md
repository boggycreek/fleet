---
name: fleet-howto-agent-lifecycle
description: Guide for provisioning, starting, inspecting, attaching to, stopping, cleaning, and retiring agent instances.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Agent Lifecycle Operations

This guide covers the complete operational lifecycle of Fleet agents, from initial creation and container execution to interactive debugging, safe state cleanup, and permanent retirement.

---

## 1. Creating Agents

Use `fleet agent create` (or `ft agent create`) to provision new agent configurations.

### Basic Syntax
```bash
fleet agent create <name> [as <type>] [--role <role>] [--host|--no-container]
```

### Agent Types
- **`native`** (default): Native Go agent harness equipped with core developer toolchains.
- **`opencode`**: OpenCode agent container setup.
- **`claude`**: Claude Code agent harness.
- **`agy`**: Antigravity agent setup.

### Examples

#### Provision a standard native agent
```bash
fleet agent create dev-bot-1 as native --role "Backend Go Refactoring"
```
**Expected Output:**
```text
==> Provisioning agent 'dev-bot-1' (type: native)...
[✓] Configuration generated at ~/.local/share/fleet/agents/dev-bot-1.json
[✓] Generated Ed25519 identity keypair
[✓] Created Forgejo account 'dev-bot-1' on local forge
[✓] Registered Valkey backplane ACLs (~dev-bot-1:*)
[✓] Updated SSH config stanza (~/.local/share/fleet/ssh_config)
[✓] Agent 'dev-bot-1' created successfully.
```

#### Provision a Claude Code agent
```bash
fleet agent create reviewer as claude --role "Automated Code Reviewer"
```

#### Provision a host-first agent (no container boundary)
```bash
fleet agent create host-runner as native --host
```

---

## 2. Starting, Inspecting, and Stopping Agents

### Starting an Agent Container
Start the container runtime for a provisioned agent:
```bash
fleet agent start dev-bot-1
```
**Expected Output:**
```text
==> Starting agent 'dev-bot-1'...
[✓] Persistent volume 'fleet-dev-bot-1-home' ready
[✓] Podman container 'fleet-dev-bot-1' started on network 'fleet-infra'
[✓] Dynamic SSH port assigned: 2222 -> host port 32768
[✓] Agent 'dev-bot-1' is running.
```

### Inspecting Running Agents
List all provisioned agents, their runtime status, container state, and SSH port mappings:
```bash
fleet agent list
```
**Expected Output:**
```text
NAME         TYPE     STATUS    CONTAINER        SSH PORT   ROLE
dev-bot-1    native   running   fleet-dev-bot-1  32768      Backend Go Refactoring
reviewer     claude   stopped   -                -          Automated Code Reviewer
host-runner  native   running   HOST             -          Host Execution Mode
```

To render JSON output for scripts or automation:
```bash
fleet agent list --json
```

### Stopping Agents
Stop a running agent container:
```bash
fleet agent stop dev-bot-1
```

To stop all running agent containers simultaneously:
```bash
fleet agent stop --all
```

---

## 3. Interactive Shell & Tmux Supervisor

Each agent container runs an unprivileged internal SSH daemon and a background `tmux` supervisor session.

### Attaching to the Interactive Tmux Supervisor
To observe agent execution in real-time or interact directly with its supervisor terminal:
```bash
fleet agent tmux dev-bot-1
```
*(Press `Ctrl+B` then `D` to detach from the tmux session without stopping the agent).*

### SSH Shell Access
To open an interactive shell session as the unprivileged `agent` user (UID 1000) inside the container:
```bash
fleet agent ssh dev-bot-1
```
**Expected Output:**
```text
agent@fleet-dev-bot-1:~$ pwd
/home/agent
agent@fleet-dev-bot-1:~$
```

---

## 4. Safe Cleanup vs. Permanent Retirement

Fleet distinguishes between clearing temporary container runtimes and permanently deprovisioning an agent.

### Safe Cleanup (`fleet agent clean`)
`fleet agent clean` stops and removes the agent container while **preserving** the agent's persistent home volume (`fleet-<name>-home`), configuration files, Valkey ACLs, and Forgejo account.

```bash
fleet agent clean dev-bot-1
```
**Expected Output:**
```text
==> Cleaning container for agent 'dev-bot-1'...
[✓] Container 'fleet-dev-bot-1' stopped and removed.
[i] Persistent volume 'fleet-dev-bot-1-home' preserved.
[i] Run 'fleet agent start dev-bot-1' to recreate container with existing data.
```

### Permanent Retirement (`fleet agent retire`)
`fleet agent retire` **permanently deprovisions** the agent across all platform subsystems:
1. Stops and removes the Podman container.
2. Deletes the persistent home volume (`fleet-<name>-home`).
3. Deletes local configuration files (`~/.local/share/fleet/agents/<name>.json`) and Ed25519 identity keys.
4. Revokes Valkey ACLs and removes backplane message streams.
5. Deprovisions the Forgejo user account and public SSH keys.
6. Removes OpenSSH stanzas from `~/.local/share/fleet/ssh_config`.

```bash
fleet agent retire dev-bot-1
```
**Confirmation Prompt & Output:**
```text
WARNING: Permanent retirement will destroy agent home volume and secrets for 'dev-bot-1'.
Are you sure? [y/N]: y
==> Retiring agent 'dev-bot-1'...
[✓] Container removed
[✓] Volume 'fleet-dev-bot-1-home' deleted
[✓] Valkey ACLs revoked & streams purged
[✓] Forgejo user account deleted
[✓] Agent 'dev-bot-1' fully retired.
```

To bypass confirmation in non-interactive pipelines:
```bash
fleet agent retire dev-bot-1 --force
```

---

## 5. Troubleshooting Tips

- **Agent fails to start due to port conflicts**:
  Run `fleet agent doctor <name>` to diagnose port assignments and network bindings.
- **SSH connection refused (`fleet agent ssh <name>`)**:
  Verify container status with `fleet agent list`. If the container stopped unexpectedly, attach to container logs via `podman logs fleet-<name>`.
- **Restoring state after accidental container removal**:
  Because `fleet agent clean` preserves the home volume, running `fleet agent start <name>` automatically re-attaches the existing volume with all workspace files intact.
