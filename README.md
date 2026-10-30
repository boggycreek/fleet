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

## Core Architecture & Components

- **Unprivileged Rootless Podman Boundary**: Agent containers run under unprivileged user namespaces (`agent`, UID/GID 1000) with Linux capability drops. Agents cannot escalate privileges to `root` or access host files outside explicitly projected paths.
- **Host LLM Gateway Isolation**: Containerized agents communicate with host-local inference models (Ollama, llama.cpp, vLLM, LiteLLM) via a dedicated bridge target (`http://llm-gateway:<port>/v1`), translating `localhost` requests without exposing host network listeners.
- **Zero Host Credential Leakage**: Host environment variables, Cloud API tokens, and SSH keys are shielded from agent containers. Sensitive parameters are injected securely into process environments (`cmd.Env`) without leaking onto process command-line vectors (`argv`).
- **Layered OCI Container Hierarchy**:
  - `images/agent-core/`: Minimal base image with Debian Bookworm, unprivileged user (`agent`, UID 1000), and base utilities.
  - `images/agent-daemon-base/`: Daemon base image with internal SSH daemon (port 2222), tmux supervisor, and the `bpd` entrypoint daemon.
  - `images/agent-tool-base/`: Ephemeral foreground tool container base image.
  - `images/agents/`: Derivative agent harnesses (`opencode`, `claude`, `agy`, `pig`, `native`).
- **Native Go Monorepo & Zero-Dependency Binaries**:
  - `fleet` (or `ft`): Unified host management CLI for managing agent lifecycles, container image builds, shared infrastructure, and IDE plugins.
  - `bp`: Cross-agent backplane messaging CLI for inter-agent communication, broadcast channels, synaptic state, and agent ensembles.
  - `bpd`: In-container backplane daemon handling background message polling, headless command execution, and turn dispatching.
  - `libbp`: Core backplane client library with RESP protocol parser and Ed25519 signing.
- **Valkey Communication Backplane**: Asynchronous cross-agent messaging bus utilizing Redis Streams (`<id>:out`, `<id>:inbox`, `<id>:seq`), per-agent Valkey ACLs, Ed25519 digital signatures, and threaded reply citations.
- **Local Forgejo Git Forge**: A host-local, rootless Forgejo server on the `fleet-infra` network providing Git hosting (`fleet/tools.git`, `fleet/tasks.git`), inter-agent pull requests, and distributed task tracking with Beads (`bd`).
- **In-Container Task Tracking (Beads)**: Task graph and dependency tracking (`~/tasks`, `fleet-tasks ready`, `bd ready`) backed by `http://forgejo:3000/fleet/tasks.git`.
- **Inference Routing & Proxy Sidecars**:
  - **Local Model Gateway (`llm-gateway`)**: Host gateway routing (`http://llm-gateway:<port>/v1`) allowing containerized agents to query local OpenAI-compatible inference servers (Ollama, llama.cpp, vLLM, LiteLLM) running on the host.
  - **Local OpenRouter Proxy Sidecar (`fleet-openrouter`)**: Dedicated sidecar service handling header/key injection and secure API key isolation for cloud LLM routing.
- **Model Context Protocol (MCP) Suite**: Standardized MCP tools integrated for agent operations:
  - `beads-mcp`: Inter-agent graph task tracking and dependency management.
  - `gitea-mcp`: Local Forgejo repository management and PR operations.
  - `sonar-mcp`: Deterministic mechanical code analysis and Quality Gate status.
  - `test-runner-mcp`: In-container isolated unit and integration test execution.
- **IDE Remote Access**: Direct remote development from VS Code and JetBrains IDEs over unprivileged SSH with isolated per-agent host aliases (`fleet-<name>`).

---

## Host CLI (`fleet` / `ft`) Command Reference

```bash
# Agent Instance Lifecycle
fleet agent create <name> [as <type>] [--role <role>] [--dockerfile <path>] [--engine podman|docker] [--host|--no-container] [--model-url <url>] [--model-name <name>] [--model-key <key>]
fleet agent start <name>                                 # Start the agent container with Podman
fleet agent stop [name]                                  # Stop running agent container(s)
fleet agent tmux <name>                                  # Attach interactively to the container tmux session
fleet agent ssh <name>                                   # SSH directly into unprivileged environment
fleet agent open <name> [in <ide>]                       # Launch desktop IDE remote development (VS Code, JetBrains)
fleet agent doctor <name>                                # Diagnose configuration, keys, storage, and auto-heal
fleet agent clean <name>                                 # Remove container (preserves persistent home volume)
fleet agent retire <name>                                # Fully deprovision agent (container, volume, Valkey, Forgejo)

# Container Image Management
fleet image build <path>                                 # Build custom OCI agent image
fleet image list                                         # List available local agent OCI images
fleet image inspect <tag>                                # Inspect image manifest and layer details
fleet image prune                                        # Clean up dangling and unused build images

# Shared Infrastructure (Valkey Backplane + Forgejo Forge)
fleet infra up                                           # Start Valkey, Forgejo, and proxy containers
fleet infra list                                         # View service status and connection endpoints
fleet infra doctor                                       # Diagnose shared infrastructure and auto-heal defects
fleet infra down                                         # Stop shared services (preserves data volumes)

# IDE Plugins & Integration
fleet plugin add <gateway|toolbox|vscode>                # Install & configure IDE plugin / SSH integration
fleet plugin remove <gateway|toolbox|vscode>             # Uninstall & unlink IDE plugin configuration
fleet plugin list                                        # List supported and installed IDE plugins

# System Updates
fleet update                                             # Synchronize git repo, rebuild host CLIs, and compile images
```

---

## Backplane Messaging CLI (`bp`) Command Reference

```bash
# Identity & Discovery
bp whoami                                                # Print current resolved identity, mode, and public key
bp profile list                                          # List configured backplane connection profiles
bp help --ai                                             # Print backplane documentation formatted for AI agents

# Broadcasts & Direct Messaging
bp say <message> [--file <path>]                         # Broadcast message to all agents on the fleet
bp tell <agent> <message> [--file <path>]                # Direct point-to-point message to a specific agent
bp reply <id> <recipient> <message>                      # Reply in-thread to a specific message ID
bp recv [--json] [--block <sec>]                         # Read new messages since last cursor
bp human [--count <n>] [--json]                          # Read authoritative human operator broadcast log

# Team-Scoped Ensemble Channels
bp ensemble create <name>                                # Create team-scoped agent ensemble channel
bp ensemble join <name>                                  # Join an ensemble channel
bp ensemble leave <name>                                 # Leave an ensemble channel
bp ensemble list                                         # List active ensemble channels
bp ensemble say <name> <message>                         # Broadcast message to ensemble channel
bp ensemble recv <name>                                  # Read messages from ensemble channel

# Synaptic State & Queue Management
bp state get [<key>]                                     # Read field from shared synaptic state
bp state set <key> <val>                                 # Update field in shared synaptic state
bp state show                                            # Display entire shared synaptic state JSON
bp state queue push <val>                                # Push item onto shared inter-lobe directive queue
bp state queue pop                                       # Pop item from shared directive queue
bp state queue list                                      # List items in shared directive queue
bp state queue clear                                     # Clear all items from shared directive queue
```

---

## Practical How-To Guides

Task-oriented guides and step-by-step workflows are available in `doc/how-to/`:

- **[Operational How-To Guides](doc/how-to/README.md)**: Scenario-oriented guides for agent lifecycles, IDE connections, shared infrastructure, backplane messaging, and diagnostics.
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
- **[Architecture Decision Records](doc/adr/README.md)**: Complete index of architectural decision records (`00001` through `00056`).

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
