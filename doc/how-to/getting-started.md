# How-To: Getting Started with Fleet

This guide takes you from a clean machine to running an autonomous, containerized AI agent using Fleet.

---

## 1. Prerequisites

Before installing Fleet, ensure your host system satisfies the following core requirements:

- **Operating System**: macOS 12+ (Apple Silicon or Intel) or Linux (Ubuntu, Debian, Fedora, Arch, etc.).
- **Container Engine**: [Podman](https://podman.io/) (required for rootless container execution).
- **Core Tools**: `git`, `curl`, `ssh-keygen`, `make`.
- **Go Toolchain**: Go 1.23+ (required if building host CLIs from source).

To check if Podman is running in rootless mode:

```bash
podman info --format '{{.Host.Security.Rootless}}'
# Expected output: true
```

---

## 2. Installation

Install Fleet using the official automated installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/boggycreek/fleet/main/install.sh | bash
```

Alternatively, if you have already cloned the repository locally:

```bash
git clone https://github.com/boggycreek/fleet.git
cd fleet
./install.sh
```

### What the Installer Does

1. Verifies system prerequisites (`podman`, `git`, `curl`, `ssh-keygen`).
2. Syncs the repository into `${XDG_DATA_HOME:-~/.local/share}/fleet/repo`.
3. Builds native static Go binaries for host management: `fleet` (aliased as `ft`) and `bp` in `~/.local/bin`.
4. Generates a dedicated SSH keypair at `~/.ssh/fleet` for isolated IDE container access.
5. Sets up the default configuration file at `~/.local/share/fleet/.env`.

Verify the installation by running:

```bash
fleet --version
# Expected output: fleet version 0.1.0-alpha
```

---

## 3. Start Shared Infrastructure

Fleet uses host-local infrastructure containers for inter-agent messaging (Valkey) and task tracking / git hosting (Forgejo).

Start shared infrastructure services:

```bash
fleet infra up
```

**Expected Output:**

```text
Starting shared infrastructure (Valkey, Forgejo & PostgreSQL)...
Shared infrastructure is online.
```

Verify that infrastructure services are healthy:

```bash
fleet infra list
```

**Expected Output:**

```text
Shared infrastructure status:
SERVICE                      STATUS
fleet-valkey                 running
fleet-forgejo                running
fleet-postgres               running
```

---

## 4. Provision Your First Agent

Create a named agent instance using the `fleet agent create` command. You can select an agent harness preset (`opencode`, `claude`, `agy`, `pig`, or `base`):

```bash
fleet agent create coder as opencode --role "Senior Go Software Engineer"
```

### Command Flags & Presets

- **`as <type>`**: Preset harness target (`base`, `opencode`, `claude`, `agy`, `pig`, `native`).
- **`--role <role>`**: Metadata description of the agent's role or focus area.
- **`--model-url <url>`**: Endpoint for an OpenAI-compatible inference server (defaults to local model gateway `http://llm-gateway:11434/v1` or `$OPENAI_BASE_URL`).
- **`--model-name <name>`**: Target LLM model name (e.g. `claude-3-5-sonnet`, `qwen2.5-coder`).
- **`--host`**: Provision a host-connected container agent with workspace projection.
- **`--no-container`**: Provision an un-containerized agent executing directly on the host (for light host tooling).

**Expected Output:**

```text
Agent "coder" created successfully.
  Image:      fleet-opencode:latest
  Role:       Senior Go Software Engineer
  Container:  fleet-agent-coder
  Volume:     fleet-agent-coder-home

To start: fleet agent start coder
```

---

## 5. Start and Manage the Agent

Launch the agent container:

```bash
fleet agent start coder
```

**Expected Output:**

```text
Agent "coder" started (fleet-agent-coder).
SSH:   fleet agent ssh coder
IDE:   fleet agent open coder
Tmux:  fleet agent tmux coder
```

List all configured agents and their container status:

```bash
fleet agent list
```

**Expected Output:**

```text
AGENT_NAME       ROLE           STATUS       IMAGE                      IDE_SSH
coder            Senior Go...   running      fleet-opencode:latest      agent@localhost:2222
```

---

## 6. Accessing the Agent Environment

### Option A: Direct Interactive Shell (SSH)

SSH directly into the agent container's unprivileged environment:

```bash
fleet agent ssh coder
```

Inside the container, you execute as user `agent` (UID 1000) with home workspace at `/home/agent/workspace`.

### Option B: Interactive Tmux Supervisor

Attach to the agent's internal tmux session supervisor to monitor background execution:

```bash
fleet agent tmux coder
```

*(Press `Ctrl+B` then `D` to detach from the tmux session without stopping the agent).*

### Option C: Remote IDE Integration

Launch VS Code, Cursor, or JetBrains Gateway attached directly to the agent container:

```bash
fleet agent open coder in code
```

---

## 7. Stopping and Retiring Agents

### Stop Container

Stop the agent container while preserving its persistent home directory volume (`fleet-agent-coder-home`):

```bash
fleet agent stop coder
```

### Clean Container Instance

Remove the container instance while retaining persistent workspace and configuration files:

```bash
fleet agent clean coder
```

### Retire Agent Completely

Fully deprovision the agent across the platform (deletes container instance, persistent home volume, local secrets, Valkey ACLs, and Forgejo accounts):

```bash
fleet agent retire coder
```

**Expected Output:**

```text
Agent "coder" retired and deprovisioned successfully.
```

---

## Next Steps

- Explore [Cross-Agent Communication](cross-agent-communication.md) to exchange messages via `bp`.
- Set up your favorite editor with [IDE Integration](ide-integration.md).
- Learn self-healing techniques with [Diagnostics & Troubleshooting](diagnostics-and-troubleshooting.md).
