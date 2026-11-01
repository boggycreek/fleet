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
        direction TB
        Operator["Human Developer / Operator"]
        HostCLI["Fleet CLI (fleet / ft)"]
        HostSSH["~/.ssh/config (Managed Include)"]
        HostDolt["Platform Dolt Backlog (sndbx-*)"]
        LocalLLM["Local Inference Gateway (Ollama / vLLM / llama.cpp)\n127.0.0.1:11434 / 8000"]

        Operator -->|Administers| HostCLI
        Operator -->|Opens in IDE| HostSSH
        HostCLI -->|Tracks Issues| HostDolt
    end

    subgraph PodmanSandbox["Rootless Podman Container Sandbox"]
        direction TB
        
        subgraph AgentPod["Agent Container (fleet-<name>)"]
            AgentDaemon["fleet-agent (Daemon PID 1)"]
            REPLEngine["Cognitive REPL Engine"]
            Tools["Builtin Tools (bash, edit_file, get_outline)"]
            MCPServers["MCP Servers (beads, forgejo, memory, sonar)"]
            InContainerSSH["OpenSSH Daemon (:2222)"]
            
            AgentDaemon --> REPLEngine
            REPLEngine --> Tools
            REPLEngine --> MCPServers
        end

        subgraph SharedInfra["Shared Local Infrastructure (fleet-infra bridge)"]
            Valkey["Valkey 8 Backplane (fleet-valkey:6379)\nStreams & Pub/Sub"]
            Forgejo["Forgejo 9 Local Forge (fleet-forgejo:3000)\nTasks & Shared Tools"]
        end
    end

    HostCLI -->|Orchestrates| AgentPod
    HostCLI -->|Manages| SharedInfra
    HostSSH -->|Direct SSH (Port 2222)| InContainerSSH
    REPLEngine -->|Queries LLM via llm-gateway| LocalLLM
    AgentDaemon -->|Encrypted Messaging| Valkey
    MCPServers -->|Task & Code Sync| Forgejo

    classDef host fill:#1e293b,stroke:#3b82f6,stroke-width:2px,color:#f8fafc;
    classDef container fill:#0f172a,stroke:#10b981,stroke-width:2px,color:#f8fafc;
    classDef infra fill:#1e1b4b,stroke:#8b5cf6,stroke-width:2px,color:#f8fafc;
    class Host host;
    class AgentPod container;
    class SharedInfra infra;
```

---

## 2. Layered OCI Container Hierarchy

Fleet packages its containerized runtime as an immutable, composable OCI hierarchy built on rootless Podman (ADR 00006 & ADR 00043):

```mermaid
flowchart TD
    BaseImage["1. OS Base Layer (Debian Bookworm Minimal)\n• Non-root agent user (UID/GID 1000)\n• Unprivileged OpenSSH daemon (:2222)\n• Security baselines & drop caps"]
    
    ToolBase["2. Tool Base Layer\n• Compilers (Go 1.27, GCC, Make)\n• Runtimes (Node.js/NVM, Deno)\n• Git, Podman client, jq, ripgrep"]
    
    DaemonBase["3. Daemon & Supervisor Layer\n• bpd (Autonomous backplane daemon)\n• bp (Messaging CLI)\n• beads (Task tracker CLI)"]
    
    MCPSuite["4. Model Context Protocol Suite\n• beads-mcp (Task management)\n• forgejo-mcp (Local git & PRs)\n• memory-mcp (Episodic BM25 memory)\n• sonar-mcp (Deterministic mechanical rules)"]
    
    EngineLayer["5. Native Cognitive Engine Layer\n• fleet-agent (Autonomous daemon)\n• REPL cognitive loop\n• Code intelligence AST provider\n• Dynamic reasoning budget"]

    BaseImage --> ToolBase
    ToolBase --> DaemonBase
    DaemonBase --> MCPSuite
    MCPSuite --> EngineLayer

    classDef l1 fill:#1e3a8a,stroke:#60a5fa,stroke-width:2px,color:#ffffff;
    classDef l2 fill:#064e3b,stroke:#34d399,stroke-width:2px,color:#ffffff;
    classDef l3 fill:#581c87,stroke:#c084fc,stroke-width:2px,color:#ffffff;
    classDef l4 fill:#701a75,stroke:#f472b6,stroke-width:2px,color:#ffffff;
    classDef l5 fill:#831843,stroke:#fb7185,stroke-width:2px,color:#ffffff;
    
    class BaseImage l1;
    class ToolBase l2;
    class DaemonBase l3;
    class MCPSuite l4;
    class EngineLayer l5;
```

---

## 3. Cognitive Agent Architecture: Inside `fleet-agent`

Inside each running agent container, the autonomous harness coordinates cognitive reasoning, safe file mutations, and multi-tier memory:

```mermaid
flowchart LR
    subgraph Inputs["Inbound Signals"]
        TaskGoal["Active Beads Task\n(Goal & Criteria)"]
        BackplaneMsg["Backplane Message\n(<peer>:inbox)"]
        OperatorCmd["Operator Prompt\n(SSH / CLI)"]
    end

    subgraph CognitiveCore["Cognitive Engine (agent/pkg/engine)"]
        PromptAssembler["Prompt Assembler\n• Pinned Invariants\n• Goal State Preservation\n• Observation Pruning"]
        LLMQuery["Inference Call\n(http://llm-gateway/v1)\nDynamic Reasoning Budget"]
        OutputParser["Tool Call Parser\n(Loose & Strict JSON)"]
    end

    subgraph ToolPerimeter["Tool Execution Perimeter"]
        ASTGate{"Syntax Gate\n(edit_file)"}
        OutlineTool["get_outline\n(Go AST & Polyglot)"]
        WriteTool["write_file\n(Workspace Bound)"]
        BashTool["bash\n(Unprivileged Subshell)"]
        MCPCall["MCP Tool Execution\n(STDIO JSON-RPC 2.0)"]
    end

    subgraph Verification["Verification Gate"]
        DiskWrite[("Workspace Disk\n/home/agent/workspace")]
        RejectSyntax["Syntax Error Rejected\n(Original Intact)"]
    end

    Inputs --> PromptAssembler
    PromptAssembler --> LLMQuery
    LLMQuery --> OutputParser
    OutputParser -->|Calls Tool| ToolPerimeter

    ToolPerimeter --> ASTGate
    ASTGate -->|Valid Syntax| DiskWrite
    ASTGate -->|Invalid Syntax| RejectSyntax
    RejectSyntax -->|Feeds Diagnostics Back| PromptAssembler

    classDef core fill:#0f172a,stroke:#38bdf8,stroke-width:2px,color:#f8fafc;
    classDef gate fill:#312e81,stroke:#a855f7,stroke-width:2px,color:#f8fafc;
    class CognitiveCore core;
    class ToolPerimeter gate;
```

---

## 4. Cross-Agent Backplane Messaging Fabric

Agents collaborate asynchronously via the high-speed Valkey Streams & Pub/Sub backplane bus (`bp` / `libbp`):

```mermaid
sequenceDiagram
    autonumber
    actor Alice as Agent "Alice" (Coder)
    participant Bus as Valkey Backplane (fleet-valkey)
    participant BPD as Backplane Daemon (bpd)
    actor Bob as Agent "Bob" (Reviewer)

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
        FleetCLI["fleet agent start <name>"]
    end

    subgraph SSHConfig["Managed OpenSSH Configuration"]
        MainConfig["~/.ssh/config\nInclude ~/.local/share/fleet/ssh_config"]
        FleetStanzas["~/.local/share/fleet/ssh_config\nHost fleet-coder\n  HostName 127.0.0.1\n  Port 39281\n  User agent\n  IdentityFile ~/.ssh/fleet"]
    end

    subgraph ContainerTarget["Agent Pod (Rootless Podman)"]
        SSHD["Dropbear / OpenSSH (:2222)"]
        Workspace["/home/agent/workspace\n(Project Code & Git)"]
    end

    FleetCLI -->|1. Starts container & dynamic port| ContainerTarget
    FleetCLI -->|2. Auto-updates SSH stanzas| FleetStanzas
    MainConfig --> FleetStanzas
    Developer -->|3. 'fleet agent open coder in vscode'| VSCode
    VSCode -->|4. ssh fleet-coder| SSHD
    JetBrains -->|ssh fleet-coder| SSHD
    SSHD --> Workspace

    classDef host fill:#0f172a,stroke:#38bdf8,stroke-width:2px,color:#f8fafc;
    classDef cfg fill:#1e1b4b,stroke:#818cf8,stroke-width:2px,color:#f8fafc;
    classDef target fill:#064e3b,stroke:#34d399,stroke-width:2px,color:#f8fafc;
    class HostDev host;
    class SSHConfig cfg;
    class ContainerTarget target;
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
