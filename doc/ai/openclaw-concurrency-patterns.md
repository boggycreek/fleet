---
name: fleet-openclaw-evaluation
description: >-
  Architectural evaluation comparing OpenClaw concurrency patterns and cognitive architecture against Fleet's native Go agent runtime (sndbx-agent / ADR 00041).
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Architectural Evaluation: OpenClaw Concurrency Patterns vs. Fleet Native Runtime (`sndbx-agent`)

## Executive Summary & Background

As autonomous software engineering agents evolve from chat-assisted interactive tools into continuous headless execution engines, their underlying cognitive and concurrency architectures dictate operational reliability, token efficiency, and security boundaries.

This document presents a technical evaluation comparing **OpenClaw**—a Node.js/TypeScript-based multi-agent assistant harness—with **Fleet's purpose-built native Go agent runtime** (`sndbx-agent` per [ADR 00041](../adr/00041-PurposeBuiltSandboxAgentRuntime.md)). We examine the structural trade-offs between OpenClaw's conversational worker-threading paradigm and Fleet's multi-threaded Go supervisor architecture (`pkg/runtime`), focusing on four critical cognitive runtime dimensions:

1. **Dynamic Sub-Agent Spawning & Delegation**
2. **Structured Working Memory vs. FIFO Compaction**
3. **Multi-Context Separation & Derailment Prevention**
4. **Dynamic Tool & Skill Gating**

---

## Comparative Architecture Matrix

| Architectural Dimension | OpenClaw Architecture | Fleet Native Go Runtime (`sndbx-agent`) | Architectural Delta & Tradeoff |
| :--- | :--- | :--- | :--- |
| **Mission** | General-purpose personal assistant & multi-agent conversation coordinator. | Headless, continuous software engineering agent operating inside OCI sandboxes. | OpenClaw targets chat/assistant workflows; Fleet targets unattended repo mutation and task completion. |
| **Runtime Engine** | Node.js / TypeScript event loop (async/await, worker threads, sub-processes). | Native Go binary (`sndbx-agent`) running as PID 1 inside unprivileged containers. | Fleet eliminates Node.js VM overhead, ANSI/TUI scraping, and process-level wrapper bloat. |
| **Security Perimeter** | Application-level isolation; process permissions bounded by user shell execution. | Unprivileged Podman container sandbox (UID/GID 1000, dropped Linux caps, rootless netns). | Fleet enforces hard kernel isolation boundaries; OpenClaw relies on host process constraints. |
| **Autonomy Mode** | Human-in-the-loop / interactive approval with optional continuous mode. | Autonomous "YOLO" execution within `/home/agent/workspace` bounded by security perimeter. | Fleet optimizes for unattended loop speed; OpenClaw defaults to interactive prompt gating. |
| **Event Bus & Messaging** | Async message passing via JSON IPC, WebSockets, or process STDIN/STDOUT streams. | Universal Valkey stream backplane (`pkg/libbp`) with Ed25519 cryptographic envelope verification. | Fleet provides end-to-end authenticated multi-agent backplane routing with sub-millisecond latency. |
| **Concurrency Model** | Single-threaded main event loop with worker threads or spawned child subprocesses. | Multi-threaded Go goroutines managed by `pkg/runtime/supervisor.go` and `errgroup`. | Fleet leverages Go's native CSP concurrency model, channels, and atomic synchronization. |
| **Tool Execution** | In-process JavaScript function calls and external shell script spawns. | Native Go tools (`bash`, `read_file`, `write_file`) + STDIO JSON-RPC 2.0 MCP adapters. | Fleet standardizes on Model Context Protocol (MCP) and path-traversal-bounded Go execution. |
| **Memory Architecture** | Conversational message history buffer with sliding FIFO context compaction. | Two-tier working memory: active task graph (`bd`), `memory-mcp` knowledge base, and state flusher. | Fleet preserves critical objectives across compaction; OpenClaw relies on chat history truncation. |

---

## Deep Dive: Core Architectural Dimensions

### 1. Dynamic Sub-Agent Spawning & Delegation

#### OpenClaw Approach
OpenClaw delegates complex or secondary sub-tasks by instantiating secondary worker agents as async sub-processes or worker thread loops. Communication between parent and child agents occurs via JSON IPC or shared message arrays. However, sub-agents often share the same filesystem workspace without explicit mutex locks, leading to file race conditions, dirty workspace writes, or git index lock conflicts when sub-agents execute concurrent terminal commands.

#### Fleet Approach (`sndbx-agent`)
Fleet implements in-process sub-agent delegation directly within the native Go runtime (`pkg/runtime`). The Executive REPL Engine can request the Runtime Supervisor (`supervisor.go`) to spawn lightweight sub-agent goroutines:

```
+-----------------------------------------------------------------------------------+
| EXECUTIVE REPL ENGINE (Parent Goroutine)                                          |
|  - Holds active task context & token budget                                       |
|  - Spawns child sub-agent via Supervisor.SpawnSubAgent(ctx, spec)                 |
+-----------------------------------------+-----------------------------------------+
                                          |
                                          v
+-----------------------------------------------------------------------------------+
| CHILD SUB-AGENT GOROUTINE (`pkg/runtime/subagent.go`)                              |
|  - Isolated context: context.WithTimeout(parentCtx, subagentTimeout)              |
|  - Dedicated Priority Channel / Bus Queue                                         |
|  - Scoped Workspace Sub-directory: /home/agent/workspace/subtask-104               |
+-----------------------------------------+-----------------------------------------+
                                          |
                        Requires Workspace Lock for Core Workspace Mutations
                                          v
+-----------------------------------------------------------------------------------+
| SHARED STATE & WORKSPACE LOCK MANAGER (`pkg/runtime/state.go`)                    |
|  - state.LockWorkspace()   --> Exclusive single-writer lock for git/core files    |
|  - state.RLockWorkspace()  --> Concurrent read-only inspection for static analysis|
+-----------------------------------------------------------------------------------+
```

Key guarantees in Fleet:
- **Thread-Safe Workspace Access**: All filesystem operations that mutate shared git state or core files must acquire `state.LockWorkspace()`. Sub-agents performing isolated tasks operate in dedicated sub-directories or hold read locks (`state.RLockWorkspace()`).
- **Context Lifetime Propagation**: Parent agents pass derived Go `context.Context` handles to child sub-agents. A P0 cancellation signal received by the parent instantly propagates down the context tree, canceling all child sub-agent routines cleanly.
- **Resource & Token Bounding**: Sub-agents are configured with strict turn caps and timeouts to prevent runaway LLM execution loops.

---

### 2. Structured Working Memory vs. FIFO Compaction

#### OpenClaw Approach
OpenClaw manages model context length using standard conversational FIFO (First-In, First-Out) compaction. As turn history grows, older system messages, user prompts, and tool output pairs are dropped or naive summaries are appended. This pattern suffers from **context erosion**: critical initial task constraints, file boundaries, and non-negotiable architectural rules defined early in the session are lost during compaction, causing the model to hallucinate or violate previously satisfied requirements.

#### Fleet Approach (`memory-mcp` & Task Graph Integration)
Fleet replaces naive FIFO compaction with a **Structured Working Memory Architecture** combining active task graph injection (`bd ready`) with persistent memory tools (`memory-mcp` per ADR 00032):

```
+-----------------------------------------------------------------------------------+
| PROMPT ASSEMBLY LOOP (`pkg/agent/engine/prompt.go`)                               |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  | 1. PINNED SYSTEM DIRECTIVES & ARCHITECTURAL INVARIANTS                      |  |
|  |    - Non-negotiable instructions, security bounds, quality gates            |  |
|  +-----------------------------------------------------------------------------+  |
|  | 2. ACTIVE TASK GRAPH SNAPSHOT (`bd ready` / Dolt-backed task state)          |  |
|  |    - Active task objective, dependency list, expected deliverables          |  |
|  +-----------------------------------------------------------------------------+  |
|  | 3. STRUCTURED WORKING MEMORY & EPISODIC LEARNINGS (`memory-mcp`)             |  |
|  |    - Retrieved learnings, architectural discoveries, post-mortem notes       |  |
|  +-----------------------------------------------------------------------------+  |
|  | 4. COMPACTED CONVERSATION HISTORY                                           |  |
|  |    - Summarized recent turns (older observations stripped of raw output)     |  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
```

Key memory mechanisms in Fleet:
- **Task Invariant Preservation**: Task goals, issue descriptions (`sndbx-*`), and git branch states are re-injected at every prompt assembly pass directly from `SharedState` and `bd ready`, making them immune to turn compaction.
- **Episodic Knowledge Retrieval**: Long-term learnings and repo insights are stored in structured memory via `memory-mcp` (`/home/agent/.memory`) and retrieved dynamically based on relevance, rather than cluttering raw prompt history.
- **Observation Pruning**: Large stdout/stderr payloads from `bash` execution are truncated in conversation history while retaining execution return codes and error summaries.

---

### 3. Multi-Context Separation & Derailment Prevention

#### OpenClaw Approach
OpenClaw routes all incoming events—including casual chat messages, system telemetry pings, peer agent notifications, and user directives—into a single conversational stream. When a user asks "What is your status?" or a system monitor pings the agent during a multi-step code refactoring task, the inquiry enters the LLM prompt history. The model must process the query, generate a response, and attempt to resume its previous reasoning chain. This frequently leads to **prompt derailment**, where the model abandons its active tool execution plan or loses track of multi-step task context.

#### Fleet Approach (Priority Event Bus & Fast-Reflex Gateway)
Fleet enforces strict multi-context separation using a 4-tier priority event bus (`pkg/runtime/bus.go`) and the Fast-Reflex Gateway Subsystem (`pkg/agent/gateway/valkey_gateway.go`):

```
+-----------------------------------------------------------------------------------+
| INBOUND VALKEY BACKPLANE STREAM (`<agent>:inbox`)                                 |
+-----------------------------------------+-----------------------------------------+
                                          |
                                          v
+-----------------------------------------------------------------------------------+
| FAST-REFLEX GATEWAY SUBSYSTEM (`pkg/agent/gateway/valkey_gateway.go`)             |
|  - Ed25519 Cryptographic Signature Verification                                   |
|  - Classifies Message Priority (P0, P1, P2, P3)                                   |
+---+----------------------------+---------------------------+----------------------+
    |                            |                           |                      |
    | P3 (Telemetry/Ping)        | P0 (Control Interrupt)    | P1/P2 (Directives)   |
    v                            v                           v                      |
+-----------------------+  +-----------------------+  +-----------------------+     |
| INSTANT IN-MEMORY     |  | CANCEL CONTEXT        |  | PRIORITY EVENT BUS    |     |
| RESPONSE              |  | - Triggers            |  | (P1/P2 Channels)      |     |
| - Reads SharedState   |  |   context.CancelFunc  |  | - Queued in memory    |     |
| - Publishes to        |  | - Aborts active       |  | - Drained strictly at |     |
|   <agent>:out         |  |   tool execution      |  |   turn boundaries     |     |
+-----------------------+  +-----------------------+  +-----------------------+     |
    |                                                            |                  |
    v                                                            v                  |
 ZERO LLM TOKENS                                        EXECUTIVE REPL ENGINE       |
 ZERO CONTEXT POLLUTION                                 (Drains queue when idle)    |
```

Priority Level Dispatch Mechanics:
- **P3 (Telemetry Query / Health Ping)**: Handled **100% in-memory** by the Fast-Reflex Gateway. Reads `SharedState.Read()` under an RLock and immediately publishes a signed JSON response over Valkey. **Zero LLM tokens are consumed, and the Executive REPL's context window is completely untouched.**
- **P0 (Emergency Control / Cancellation)**: Instantly cancels the Go `context.Context` of running tool subprocesses or sub-agents, halting execution in sub-milliseconds.
- **P1/P2 (Directives & Peer Messages)**: Enqueued on the priority bus. The Executive REPL Engine non-blockingly drains this queue **only at natural turn boundaries** (after a tool call completes), preventing mid-turn prompt derailment.

---

### 4. Dynamic Tool & Skill Gating

#### OpenClaw Approach
OpenClaw exposes all registered tools and skill definitions to the LLM on every turn. As the agent gains capabilities (git tools, browser tools, filesystem tools, database tools, API integration skills), the JSON schema array sent to the model expands exponentially. For local or smaller open-weight models (e.g., Llama-3, Qwen-2.5-Coder), presenting 30+ complex tool schemas leads to **schema confusion**, invalid tool arguments, hallucinated parameters, and wasted prompt token budget.

#### Fleet Approach (Progressive Schema Disclosure & Facet Filtering)
Fleet introduces **Dynamic Tool & Skill Gating** managed through the MCP Adapter Registry (`pkg/agent/tools/mcp_adapter.go` and `registry.go`):

```
+-----------------------------------------------------------------------------------+
| TASK LIFECYCLE & EXECUTION PHASE DETECTOR                                         |
|  (e.g., Phase: "Code Editing", "Testing/Verification", "Documentation/Review")    |
+-----------------------------------------+-----------------------------------------+
                                          |
                                          v
+-----------------------------------------------------------------------------------+
| FACET FILTER & PROGRESSIVE TOOL DISCLOSURE (`pkg/agent/tools/registry.go`)        |
|                                                                                   |
|  [ CORE SYSTEM TOOLS ]       [ PHASE-SPECIFIC MCP TOOLS ]    [ HIDDEN / GATED ]   |
|   - bash                      - test_runner (if testing)      - admin_tools       |
|   - read_file                 - sonar_quality (if audit)      - destroy_volume    |
|   - edit_file                 - beads_task (if planning)                          |
+-----------------------------------------+-----------------------------------------+
                                          |
                                          v
+-----------------------------------------------------------------------------------+
| OPTIMIZED LLM JSON-SCHEMA PAYLOAD                                                 |
|  - Minimal schema footprint tailored strictly to active operational phase         |
+-----------------------------------------------------------------------------------+
```

Gating Principles in Fleet:
- **Core Tool Baseline**: Only essential filesystem (`read_file`, `edit_file`, `write_file`) and execution (`bash`) tools are exposed unconditionally.
- **Facet-Based Schema Filtering**: Specialized MCP tool sets (`sonar-mcp`, `beads-mcp`, `bp-mcp`) are dynamically filtered into the active schema set based on the agent's current task phase and active skills.
- **Skill Gating via AGENTS.md**: Skills defined in `doc/ai/` double as agent instructions and are lazily loaded into context only when relevant files or tasks are accessed.

---

## Concrete Implementation Roadmap & Deltas for Fleet

To fully realize these advanced concurrency and cognitive patterns in `sndbx-agent`, the following roadmap details the specific implementation deltas for Fleet's Go agent runtime:

### Phase 1: In-Process Sub-Agent Lifecycle Manager (`pkg/runtime`)
- **Delta**: Extend `pkg/runtime/supervisor.go` to support dynamic worker goroutine registration at runtime via `SpawnSubAgent(ctx context.Context, spec SubAgentSpec) (SubAgentHandle, error)`.
- **Implementation**:
  - Implement `pkg/runtime/subagent.go` defining sub-agent execution context, isolated task channels, and execution timeout bounds.
  - Integrate strict workspace lock checking: require sub-agents to claim `SharedState.LockWorkspace()` for mutating git operations or operate inside isolated sub-directories (`/home/agent/workspace/.subagents/<id>`).

### Phase 2: Memory-MCP & Structured Context Assembly (`pkg/agent/engine`)
- **Delta**: Enhance `pkg/agent/engine/prompt.go` to construct structured system prompts that maintain clear separation between task invariants, episodic memory, and conversational history.
- **Implementation**:
  - Integrate `memory-mcp` STDIO JSON-RPC client into `pkg/agent/tools/mcp_adapter.go`.
  - Implement explicit prompt compaction in `prompt.go`: when conversation history exceeds token thresholds, summarize tool interaction turns while preserving pinned task goals (`bd ready`) and episodic memory notes retrieved from `/home/agent/.memory`.

### Phase 3: Dynamic Tool Schema Faceting (`pkg/agent/tools`)
- **Delta**: Upgrade `pkg/agent/tools/registry.go` with schema faceting and progressive disclosure capabilities.
- **Implementation**:
  - Add `FacetFilter` interface in `registry.go` allowing the REPL loop to request tool definitions filtered by tag or task phase (e.g., `tools.GetSchemasForPhase(PhaseCodeEditing)`).
  - Reduce JSON-Schema token overhead by 40-60% for local inference endpoints (`llm-gateway`).

### Phase 4: Fast-Reflex Gateway & Priority Bus Refinement (`pkg/agent/gateway`)
- **Delta**: Finalize Valkey stream integration in `valkey_gateway.go` to enforce zero-token P3 responses and immediate P0 context cancellations.
- **Implementation**:
  - Validate Ed25519 signature checks using `crypto/ed25519` against backplane sender public keys.
  - Implement automated health and status JSON serializing directly from `SharedState.Read()`.

---

## References & Further Reading
- [ADR 00041 — Purpose-Built Headless Native Agent Runtime Engine](../adr/00041-PurposeBuiltSandboxAgentRuntime.md)
- [ADR 00040 — Dual-Lobe Single-Identity Agent Architecture](../adr/00040-DualLobeSingleIdentityAgentArchitecture.md)
- [ADR 00032 — Fleet Model Context Protocol (MCP) Suite](../adr/00032-FleetModelContextProtocolSuiteForAgentWorkspaces.md)
- [ADR 00010 — Valkey PubSub Messaging & ACL Isolation](../adr/00010-ValkeyPubSubMessagingAndAclIsolation.md)
