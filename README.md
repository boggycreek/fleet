# Fleet

A local, host-managed, multi-agent containerization platform designed to run autonomous AI coding agents in isolated Podman environments on physical machines (macOS and Linux).

Fleet gives AI agents autonomous execution capability (`git`, `npm`, `rm`, compilers, file edits) inside rootless containers without per-command human approval prompts, isolating agent workloads and protecting host files and credentials.

---

## Quick Start / Installation

### Option 1: Remote Installation (via `curl`)

Install Fleet directly from the repository:

```bash
curl -fsSL https://raw.githubusercontent.com/boggycreek/fleet/main/install.sh | bash
```

The installer will:
1. Detect host OS (macOS or Linux) and CPU architecture (`amd64` / `arm64`).
2. Verify host prerequisites (`git`, `curl`, `podman`, `ssh-keygen`).
3. Clone and sync the repository into `${XDG_DATA_HOME:-~/.local/share}/fleet/repo`.
4. Generate a dedicated IDE SSH keypair (`~/.ssh/fleet.pub`) for container access.
5. Create an initial environment configuration file at `~/.local/share/fleet/.env`.
6. Install the `fleet` host management CLI (with `ft` alias) and `bp` messaging CLI to `~/.local/bin`.

### Option 2: Installation from a Cloned Repository

If you have already cloned the repository locally:

```bash
git clone https://github.com/boggycreek/fleet.git
cd fleet
./install.sh
```

The installer recognizes the local checkout and sets it up as the active fleet installation root.

---

## Core Architecture & Container Isolation Model

Fleet enforces strict security and execution boundaries between the workstation host and autonomous agents:

- **Unprivileged Rootless Podman Boundary**: Agent containers run under unprivileged user namespaces (`agent`, UID/GID 1000) with Linux capability drops. Agents cannot escalate privileges to `root` or access host files outside explicitly projected paths.
- **Host LLM Gateway Isolation**: Containerized agents communicate with host-local inference models (Ollama, llama.cpp, vLLM, LiteLLM) via a dedicated bridge target (`http://llm-gateway:<port>/v1`), translating `localhost` requests without exposing host network listeners.
- **Zero Host Credential Leakage**: Host environment variables, Cloud API tokens, and SSH keys are shielded from agent containers. Sensitive parameters are injected securely into process environments (`cmd.Env`) without leaking onto process command-line vectors (`argv`).
- **Layered OCI Container Hierarchy**:
  - `images/agent-core/`: Foundational base image built on Debian Bookworm with unprivileged `agent` user (UID 1000).
  - `images/agent-daemon-base/`: Daemon base image featuring rootless SSH daemon (port 2222), tmux supervisor, and `bpd` entrypoint daemon.
  - `images/agent-tool-base/`: Ephemeral foreground tool container base image for interactive workspace projection.
  - `images/agents/`: Specialized derivative agent harnesses (`opencode`, `claude`, `agy`, `pig`, `native`).
- **Native Go Monorepo & Zero-Dependency Binaries**:
  - `fleet`: Unified host CLI (alias `ft`) for managing agent lifecycles, shared infrastructure, and IDE plugins.
  - `bp`: Cross-agent backplane messaging CLI.
  - `bpd`: In-container backplane daemon handling background message polling, headless command execution, and turn dispatching.
  - `libbp`: Core backplane client library with RESP protocol parser and Ed25519 signing.
- **Valkey Communication Backplane**: Asynchronous cross-agent messaging bus utilizing Redis Streams (`<id>:out`, `<id>:inbox`), per-agent Valkey ACLs, Ed25519 digital signatures, and threaded reply citations.
- **Local Forgejo Git Forge & In-Container Beads**: A host-local, rootless Forgejo server on the `fleet-infra` network providing Git hosting, inter-agent pull requests, and distributed task tracking with Beads (`bd ready`) backed by `http://forgejo:3000/fleet/tasks.git`.
- **IDE Remote Access**: Direct remote development from VS Code, Cursor, and JetBrains IDEs over unprivileged SSH with isolated per-agent host aliases (`fleet-<name>`).

---

## Host CLI (`fleet`) Command Reference

The `fleet` CLI (also aliased as `ft`) is the primary host-side operator tool for managing infrastructure, agent lifecycles, and IDE plugins.

### Agent Lifecycle Management (`fleet agent`)

```bash
# Provision a new agent instance
fleet agent create <name> [as <type>] [--role <role>] [--host|--no-container] [--model-url <url>] [--model-name <name>] [--model-key <key>]

# Lifecycle operations
fleet agent start <name>                                 # Start agent container with Podman
fleet agent stop [name] [--all]                          # Stop running agent container(s)
fleet agent clean <name>                                 # Remove container instance (preserves persistent volume)
fleet agent retire <name> [--force]                      # Fully deprovision agent (container, volume, Valkey, Forgejo)
fleet agent doctor <name>                                # Audit configuration, keys, storage, and auto-heal defects
fleet agent list [--json]                                # List all configured agents, state, and SSH endpoints

# Interactive execution & IDE integration
fleet agent open <name> [in <ide>] [--no-launch]         # Open agent in VS Code, JetBrains Gateway, Cursor, etc.
fleet agent tmux <name>                                  # Attach interactively to agent tmux supervisor session
fleet agent ssh <name>                                   # Connect via SSH directly into unprivileged environment
fleet agent ssh-config [name] [--all]                    # Output OpenSSH config stanza for manual SSH setups
```

### Shared Infrastructure (`fleet infra`)

Shared infrastructure containers (Valkey messaging broker, Forgejo git forge, and PostgreSQL) run on the rootless `fleet-infra` network.

```bash
fleet infra up                                           # Start Valkey and Forgejo shared containers
fleet infra list                                         # View running status of infrastructure containers
fleet infra doctor                                       # Diagnose infrastructure health and auto-heal storage/networks
fleet infra down                                         # Stop shared infrastructure (data volumes preserved)
```

### IDE Plugins (`fleet plugin`)

Seamlessly configure host IDE remote development links and SSH configurations.

```bash
fleet plugin add <gateway|toolbox|vscode>                # Install & configure specified IDE plugin/integration
fleet plugin remove <gateway|toolbox|vscode>             # Uninstall IDE plugin & unlink managed SSH configs
fleet plugin list                                        # Display status of supported IDE plugins
```

### Platform Maintenance (`fleet update`)

```bash
fleet update                                             # Synchronize repository, rebuild host CLIs, and rebuild OCI images
```

---

## Cross-Agent Messaging CLI (`bp`)

The `bp` CLI allows human operators and host processes to communicate directly with containerized agents over the cryptographically signed Valkey backplane fabric.

```bash
bp whoami                                                # Print active identity, connection mode, and Ed25519 public key
bp peers [--json]                                        # List active agents and presence statuses across the fleet
bp say <message> [--file <path>]                         # Broadcast message to all agents on the backplane
bp tell <agent> <message> [--file <path>]                # Send direct point-to-point message to target agent inbox
bp reply <msgid> <agent> <message>                       # Reply in-thread to a specific message ID citation
bp recv [--block <sec>] [--json]                         # Read incoming messages since last cursor position
bp status set <message>                                  # Set 300s ephemeral presence status on the backplane
```

---

## Practical How-To Guides

Task-oriented guides and step-by-step workflows are available in `doc/how-to/`:

- **[Getting Started Quickstart](doc/how-to/getting-started.md)**: Zero-to-agent walkthrough from `./install.sh` to running your first autonomous agent.
- **[Cross-Agent Communication Guide](doc/how-to/cross-agent-communication.md)**: Deep dive into Valkey Streams backplane messaging, Ed25519 signing, and `bp` commands.
- **[IDE Remote Development Setup](doc/how-to/ide-integration.md)**: Step-by-step setup for VS Code, Cursor, and JetBrains Gateway using `fleet plugin` and SSH links.
- **[Diagnostics & Troubleshooting](doc/how-to/diagnostics-and-troubleshooting.md)**: Automated health checks and self-healing with `fleet agent doctor` and `fleet infra doctor`.

---

## Architecture & System Documentation

Detailed architectural specifications, developer protocols, and decision records:

- **[AGENTS.md](AGENTS.md)**: Platform runtime guide covering messaging protocols, local inference gateway, security boundaries, and engineering gates.
- **[AI Agent Knowledge Base](doc/ai/README.md)**: Technical guides for autonomous coding agents (`dev-workflow.md`, `architecture.md`, `doctor-diagnostics.md`, `image-resolution.md`, `testing-guidelines.md`, `code-review.md`).
- **[Developer Setup & Contributing Guide](CONTRIBUTING.md)**: Workstation setup (`setup.sh`), environment doctor (`setup.sh --doctor`), quality gates, and Beads workflow.
- **[Architecture Decision Records](doc/adr/README.md)**: Complete index of architectural decision records (`00001` through `00047`).

---

## System Requirements

- **Operating System**: macOS 12+ (Apple Silicon or Intel) or Linux (Ubuntu, Debian, Fedora, Arch, Pop!_OS, etc.).
- **Container Engine**: Podman (required for rootless container execution).
- **Go Toolchain**: Go 1.23+ (for building native static binaries from source).
- **Core Utilities**: `git`, `curl`, `ssh-keygen`, `make`.

---

## License & Open Source Attribution

- **License**: Fleet is licensed under the [MIT License](LICENSE) by **Boggy Creek Software LLC**.
- **Acknowledgements**: See [ACKNOWLEDGEMENTS.md](ACKNOWLEDGEMENTS.md) for ecosystem credits and community appreciation.
- **Third-Party Notices**: See [NOTICES.md](NOTICES.md) for third-party copyright statements, SPDX license identifiers, and legal notices.
