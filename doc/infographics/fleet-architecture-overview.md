---
name: fleet-architecture-overview-infographic
description: >-
  Visual architecture guide and slideshow infographic explaining Fleet's security perimeter,
  layered OCI hierarchy, cognitive runtime, and multi-agent backplane for human visual learners.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Fleet Platform — Visual Architecture & Capabilities Infographic

Welcome to the **Fleet Architecture Visual Walkthrough**. This guide uses structured visual diagrams, flowcharts, sequence illustrations, and architectural slides to explain how Fleet enables autonomous AI coding agents to operate safely and effectively on physical developer machines.

---

## 1. The Big Picture: Hard Security Perimeters

> [!IMPORTANT]
> **The Core Philosophy**: Fleet eliminates per-command human approval prompts without sacrificing host security. Agents operate with full autonomy inside isolated Podman containers while guaranteeing **zero leakage of host secrets**.

```mermaid
flowchart TB
    subgraph Host["Physical Host Machine (Linux / macOS)"]
        Operator["Human Developer / Operator"]
        HostCLI["Fleet CLI (fleet / ft)"]
        HostSSH["~/.ssh/config (Managed Include)"]
        HostDolt["Platform Dolt Backlog (sndbx-*)"]
        LocalLLM["Local Inference Gateway (Ollama, vLLM, llama.cpp on 127.0.0.1)"]

        Operator -->|Administers| HostCLI
        Operator -->|Opens in IDE| HostSSH
        HostCLI -->|Tracks Issues| HostDolt
    end

    subgraph AgentPod["Agent Pod Container (fleet-agent)"]
        AgentDaemon["fleet-agent Daemon (PID 1)"]
        REPLEngine["Cognitive REPL Engine"]
        Tools["Builtin Tools: bash, edit_file, get_outline"]
        MCPServers["MCP Servers: beads, forgejo, memory, sonar"]
        InContainerSSH["OpenSSH Server (Port 2222)"]

        AgentDaemon --> REPLEngine
        REPLEngine --> Tools
        REPLEngine --> MCPServers
    end

    subgraph SharedInfra["Shared Local Infrastructure (fleet-infra bridge)"]
        Valkey["Valkey 8 Backplane (fleet-valkey:6379 Streams and Pub/Sub)"]
        Forgejo["Forgejo 9 Local Forge (fleet-forgejo:3000 Tasks and Repos)"]
    end

    HostCLI -->|Provisions and Starts| AgentDaemon
    HostCLI -->|Manages Bridge| Valkey
    HostCLI -->|Manages Bridge| Forgejo
    HostSSH -->|Direct SSH over dynamic port| InContainerSSH
    REPLEngine -->|Queries via llm-gateway| LocalLLM
    AgentDaemon -->|Encrypted Wire Messaging| Valkey
    MCPServers -->|Task and Code Sync| Forgejo
```

---

## 2. Layered OCI Container Hierarchy

Fleet packages its containerized runtime as an immutable, composable OCI hierarchy built on rootless Podman (ADR 00006 and ADR 00043):

```mermaid
flowchart BT
    BaseImage["1. OS Base Layer (Debian Bookworm Minimal) - Non-root user (UID 1000), SSH daemon (2222)"]
    ToolBase["2. Tool Base Layer - Go 1.27, GCC, Make, Node.js/NVM, Deno, Git, ripgrep"]
    DaemonBase["3. Daemon and Supervisor Layer - bpd daemon, bp messaging CLI, beads CLI"]
    MCPSuite["4. Model Context Protocol Suite - beads-mcp, forgejo-mcp, memory-mcp, sonar-mcp"]
    EngineLayer["5. Native Cognitive Engine Layer - fleet-agent daemon, REPL engine, AST code intelligence"]

    BaseImage -->|Builds On| ToolBase
    ToolBase -->|Builds On| DaemonBase
    DaemonBase -->|Builds On| MCPSuite
    MCPSuite -->|Builds On| EngineLayer
```

---

## 3. Cognitive Agent Architecture: Inside `fleet-agent`

Inside each running agent container, the autonomous harness coordinates cognitive reasoning, safe file mutations, and multi-tier memory:

```mermaid
flowchart LR
    subgraph Inputs["Inbound Signals"]
        TaskGoal["Active Beads Task Goal"]
        BackplaneMsg["Backplane Inbox Message"]
        OperatorCmd["Operator Prompt via SSH or CLI"]
    end

    subgraph CognitiveCore["Cognitive Engine (agent/pkg/engine)"]
        PromptAssembler["Prompt Assembler (Pinned Invariants and Goal State)"]
        LLMQuery["Inference Call via llm-gateway (Dynamic Reasoning Budget)"]
        OutputParser["Tool Call Parser (JSON-RPC)"]
    end

    subgraph ExecutionPerimeter["Tool Execution Perimeter"]
        ASTGate{"Syntax Gate (edit_file)"}
        OutlineTool["get_outline (Go AST and Polyglot)"]
        WriteTool["write_file (Workspace Bound)"]
        BashTool["bash (Unprivileged Subshell)"]
        MCPCall["MCP Tool Execution (JSON-RPC 2.0)"]
    end

    subgraph Storage["Workspace Storage"]
        DiskWrite[("Workspace Disk (/home/agent/workspace)")]
        RejectSyntax["Syntax Error Rejected (Original File Untouched)"]
    end

    TaskGoal --> PromptAssembler
    BackplaneMsg --> PromptAssembler
    OperatorCmd --> PromptAssembler
    PromptAssembler --> LLMQuery
    LLMQuery --> OutputParser

    OutputParser --> OutlineTool
    OutputParser --> WriteTool
    OutputParser --> BashTool
    OutputParser --> MCPCall
    OutputParser --> ASTGate

    ASTGate -->|Valid Syntax| DiskWrite
    ASTGate -->|Invalid Syntax| RejectSyntax
    RejectSyntax -->|Feeds Diagnostics Back| PromptAssembler
    WriteTool --> DiskWrite
```

---

## 4. Cross-Agent Backplane Messaging Fabric

Agents collaborate asynchronously via the high-speed Valkey Streams and Pub/Sub backplane bus (`bp` / `libbp`):

```mermaid
sequenceDiagram
    autonumber
    participant Alice as Agent Alice (Coder)
    participant Bus as Valkey Backplane (fleet-valkey)
    participant BPD as Backplane Daemon (bpd)
    participant Bob as Agent Bob (Reviewer)

    Note over Alice,Bob: Cryptographically Signed Asymmetric Messaging (Ed25519)
    Alice->>Alice: Sign message with Ed25519 private key
    Alice->>Bus: XADD bob:inbox (Durable Stream Log)
    Alice->>Bus: PUBLISH ping:bob (Instant Wakeup Push)
    
    Bus-->>BPD: Push notification trigger (Sub-millisecond)
    BPD->>Bus: XREAD BLOCK bob:inbox (Drains stream)
    BPD->>Bob: Deliver verified event to Bob's cognitive loop
    
    Bob->>Bob: Verify Alice's Ed25519 signature against registered pubkey
    Bob->>Bob: Process review directive and execute tests
    Bob->>Bus: XADD alice:inbox (Signed reply citation alice#42)
    Bob->>Bus: PUBLISH ping:alice (Instant Wakeup)
    Bus-->>Alice: Alice's event loop wakes up reactively
```

---

## 5. Zero-Touch Desktop IDE Remote Development

Developers attach their desktop editors directly to isolated agent sandboxes without configuring ports or installing server software manually:

```mermaid
flowchart TD
    subgraph HostDev["Host Desktop Environment"]
        Developer["Software Engineer"]
        VSCode["VS Code / Cursor"]
        JetBrains["JetBrains Gateway"]
        FleetCLI["fleet agent start coder"]
    end

    subgraph SSHConfig["Managed OpenSSH Configuration"]
        MainConfig["~/.ssh/config (Include ~/.local/share/fleet/ssh_config)"]
        FleetStanzas["~/.local/share/fleet/ssh_config (Host fleet-coder on Port 39281)"]
    end

    subgraph ContainerTarget["Agent Pod (Rootless Podman)"]
        SSHD["OpenSSH Server (:2222)"]
        Workspace["/home/agent/workspace (Project Code and Git)"]
    end

    FleetCLI -->|1. Starts container and allocates port| SSHD
    FleetCLI -->|2. Auto-updates SSH stanzas| FleetStanzas
    MainConfig --> FleetStanzas
    Developer -->|3. 'fleet agent open coder in vscode'| VSCode
    VSCode -->|4. ssh fleet-coder| SSHD
    JetBrains -->|ssh fleet-coder| SSHD
    SSHD --> Workspace
```

---

## 6. Two-Plane Task Tracking: Complete Host Isolation

Fleet enforces strict physical separation between platform development and autonomous in-container execution:

| Dimension | Host Platform Plane (Contributors) | In-Container Agent Plane (Autonomous Agents) |
| :--- | :--- | :--- |
| **Tracking Engine** | **Dolt SQL Database** (`bd`) | **Forgejo Git Repository** (`fleet/tasks.git`) |
| **Issue Identifier** | `sndbx-*` (e.g. `sndbx-sd3`) | `task-*` (e.g. `task-104`) |
| **Remote Sync** | GitHub `refs/dolt/data` | Local Forgejo `http://forgejo:3000/fleet/tasks.git` |
| **Credential Boundary** | Host SSH keys & developer GitHub tokens | Ephemeral in-container Forgejo tokens (zero host secrets) |
| **Purpose** | Fleet platform codebase development | Application coding inside user workspace |

---

## 7. Command Quick-Reference Cheat Sheet

```bash
# === Agent Lifecycle ===
fleet agent create coder as go-dev          # Create new agent instance
fleet agent start coder                     # Start container & allocate SSH port
fleet agent open coder in vscode            # Attach desktop VS Code via Remote-SSH
fleet agent tmux coder                      # Attach interactively to supervisor
fleet agent stop coder                      # Halt agent container
fleet agent clean coder                     # Remove container (preserve home volume)
fleet agent retire coder --force            # Complete deprovisioning (purge all state)

# === Diagnostics & Self-Healing ===
fleet agent doctor coder                    # Audit agent health & auto-heal defects
fleet infra doctor                          # Audit Valkey, Forgejo, & bridge networks

# === Cross-Agent Backplane Messaging ===
bp whoami                                   # Inspect resolved cryptographic identity
bp say "Starting task sndbx-42"             # Broadcast public feed message
bp tell reviewer "PR #12 is ready for review" # Direct point-to-point inbox message
bp recv --wait                              # Reactive inbox push drain
bp peers                                    # List registered agents & live status
```
