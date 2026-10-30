---
name: fleet-memory-subsystem-spec
description: >-
  Architectural evaluation and specification for standardizing cognitive agent memory across Fleet and Lokol platforms.
  Defines the 5 cognitive memory pillars, 3-tier progressive disclosure model, on-disk storage layout, MCP tool contracts,
  shared module vs adapter strategies, and platform-specific integration patterns.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Memory Subsystem Specification & Peered Shared Module Architecture

## Executive Summary

As autonomous AI coding agents evolve from ephemeral command executors into long-lived cognitive peers, retaining context across execution turns, container recreations, and cross-platform agent handoffs becomes paramount. This document provides an architectural specification and comparative evaluation for standardizing agent memory across the **Fleet** containerized sandbox execution platform and the **Lokol** edge desktop/workstation platform.

This specification addresses Beads spike **`sndbx-cpm.7`** (blocking the core memory server implementation in **`sndbx-cpm.3`**).

---

## 1. Cognitive Memory Architecture

Human cognition relies on distinct memory systems specialized for different temporal scales, retrieval mechanics, and operational purposes. To prevent context window saturation while maintaining long-term recall, the memory subsystem establishes a 5-pillar cognitive classification paired with a 3-tier progressive disclosure model.

```
+-----------------------------------------------------------------------------------+
|                           5 Cognitive Memory Pillars                              |
|  +--------------+  +--------------+  +--------------+  +------------+  +---------+  |
|  |   Episodic   |  |   Semantic   |  |  Procedural  |  |Prospective |  |Evaluative|  |
|  +--------------+  +--------------+  +--------------+  +------------+  +---------+  |
+-----------------------------------------------------------------------------------+
                                          |
                                          v
+-----------------------------------------------------------------------------------+
|                        3-Tier Progressive Disclosure                              |
|  +-----------------------------------------------------------------------------+  |
|  | Tier 1: Abstract (~20 tokens)    --> Stream Inspection & Fast Indexing     |  |
|  +-----------------------------------------------------------------------------+  |
|  | Tier 2: Summary (~150 tokens)   --> Conversational Prompt Injection        |  |
|  +-----------------------------------------------------------------------------+  |
|  | Tier 3: Full Detail (Unbounded)  --> On-Demand Payload & Deep Execution    |  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
```

### 1.1 The 5 Cognitive Memory Pillars

1. **Episodic Memory**:
   - **Definition**: Time-stamped event history, past interaction logs, tool invocation outputs, session summaries, and agent trajectory records.
   - **Characteristics**: High volume, strictly temporal, high decay rate unless reinforced.
   - **Primary Use**: Contextual reconstruction of past actions ("What steps did I take when fixing issue #42?").

2. **Semantic Memory**:
   - **Definition**: Fact-based knowledge, environment invariants, domain definitions, API contracts, architectural rules, and project patterns.
   - **Characteristics**: Low decay, high stability, structured relationships.
   - **Primary Use**: Domain knowledge lookup ("What are the security constraints of the Valkey backplane?").

3. **Procedural Memory**:
   - **Definition**: How-to playbooks, step-by-step workflow sequences, build and test routines, runbooks, and error recovery procedures.
   - **Characteristics**: Action-oriented, condition-triggered, reusable across tasks.
   - **Primary Use**: Execution guidance ("How do I run coverage analysis and resolve rootless Podman cleanup failures?").

4. **Prospective Memory**:
   - **Definition**: Deferred goals, pending tasks, scheduled intentions, background monitors, and conditional triggers.
   - **Characteristics**: Event-driven, future-oriented, temporal or state-based deadlines.
   - **Primary Use**: Task resumption and deferred execution ("When the build finishes, run SonarQube analysis").

5. **Evaluative Memory**:
   - **Definition**: Self-critique feedback, performance ratings, outcome assessments, user corrections, and learned operational heuristics.
   - **Characteristics**: Meta-cognitive, subjective scoring, quality guidance.
   - **Primary Use**: Strategy optimization and error avoidance ("Previous attempt to edit Makefile directly failed linting; prefer updating generate.go").

---

### 1.2 3-Tier Progressive Disclosure Model

Context windows are a constrained resource. Indiscriminately stuffing complete memory contents into LLM prompts leads to token bloat, high latency, and attentional degradation ("lost in the middle"). The 3-tier progressive disclosure model enforces token-efficient retrieval:

| Tier | Name | Target Size | Primary Purpose & Usage |
| :--- | :--- | :--- | :--- |
| **Tier 1** | **Abstract** | ~20 tokens | Ultra-compact text signature. Used for high-speed indexing, Valkey stream Pub/Sub inspection, and preliminary relevance filtering without consuming context. |
| **Tier 2** | **Summary** | ~150 tokens | Contextually rich summary. Injected directly into LLM prompts during conversational interaction or multi-memory synthesis. Provides sufficient context for reasoning. |
| **Tier 3** | **Full Detail** | Unbounded | The complete, untruncated payload (Markdown, code, logs, YAML metadata). Fetched on demand only when an agent explicitly calls `get_memory`. |

#### Progressive Resolution Flow
1. **Recall Phase**: Query returns a list of Tier 1 abstracts or Tier 2 summaries matching search criteria.
2. **Triage Phase**: Agent or system inspects summaries to determine which memories are directly relevant to the active subtask.
3. **Resolution Phase**: Agent invokes `get_memory(id)` to retrieve Tier 3 details for only the critical memories needed for execution.

---

## 2. On-Disk Storage & MCP Tool Contracts

### 2.1 Storage Layout & File Format

Memory entries reside on disk as Git-friendly, human-readable Markdown files augmented with YAML frontmatter.

#### File Path Conventions
```
/home/agent/.memory/
├── index.json                   # Local fast-lookup index (cached metadata)
├── episodic/
│   └── 20260330-143022-ep-01.md
├── semantic/
│   └── architecture-valkey-acl.md
├── procedural/
│   └── podman-test-cleanup.md
├── prospective/
│   └── post-build-sonar-check.md
└── evaluative/
    └── makefile-lint-fix-reflection.md
```

#### On-Disk Frontmatter & Schema Specification
```markdown
---
id: "mem-20260330-ep-0192"
pillar: "episodic"
title: "Podman permission issue during test cleanup"
tags:
  - podman
  - testing
  - permissions
created_at: "2026-03-30T14:30:22Z"
updated_at: "2026-03-30T14:30:22Z"
access_count: 12
salience_score: 0.85
decay_rate: 0.05
tier1_abstract: "Rootless Podman t.TempDir cleanup fails without podman unshare rm -rf."
tier2_summary: "When running containerized unit tests, standard os.RemoveAll fails on rootless subuid mounts. Always use podman unshare in t.Cleanup hooks."
associations:
  - "mem-20260325-pr-0041"
  - "mem-20260328-se-0012"
---

# Podman Rootless Cleanup Resolution

## Context
During unit testing in `pkg/agent`, subuid files created inside rootless containers were unremovable by standard test teardown routines.

## Resolution Pattern
Register a cleanup hook in Go tests:
```go
tmpDir := t.TempDir()
t.Cleanup(func() {
    _ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
})
```
```

---

### 2.2 Model Context Protocol (MCP) Tool Contracts

The `memory-mcp` server exposes 4 primary tools via STDIO JSON-RPC 2.0.

#### Tool 1: `store_memory`
Stores or updates a cognitive memory entry.

```json
{
  "name": "store_memory",
  "description": "Store or update an agent cognitive memory record across the 5 pillars with progressive disclosure tiers.",
  "parameters": {
    "type": "object",
    "properties": {
      "pillar": {
        "type": "string",
        "enum": ["episodic", "semantic", "procedural", "prospective", "evaluative"],
        "description": "The cognitive memory pillar classification."
      },
      "title": {
        "type": "string",
        "description": "Short descriptive title of the memory."
      },
      "content": {
        "type": "string",
        "description": "Full Tier 3 detailed Markdown payload."
      },
      "tier1_abstract": {
        "type": "string",
        "description": "Optional ~20 token abstract. Auto-generated if omitted."
      },
      "tier2_summary": {
        "type": "string",
        "description": "Optional ~150 token summary. Auto-generated if omitted."
      },
      "tags": {
        "type": "array",
        "items": { "type": "string" },
        "description": "Keywords or category tags for retrieval."
      },
      "salience_score": {
        "type": "number",
        "description": "Initial importance rating between 0.0 and 1.0."
      },
      "associations": {
        "type": "array",
        "items": { "type": "string" },
        "description": "List of related memory IDs."
      }
    },
    "required": ["pillar", "title", "content"]
  }
}
```

#### Tool 2: `recall_memory`
Queries memories using BM25 keyword matching, vector similarity, or hybrid scoring.

```json
{
  "name": "recall_memory",
  "description": "Recall memories matching query terms or semantic concepts across pillars.",
  "parameters": {
    "type": "object",
    "properties": {
      "query": {
        "type": "string",
        "description": "Search query or natural language prompt."
      },
      "pillar": {
        "type": "string",
        "enum": ["episodic", "semantic", "procedural", "prospective", "evaluative"],
        "description": "Optional filter by cognitive pillar."
      },
      "tags": {
        "type": "array",
        "items": { "type": "string" },
        "description": "Filter by specific tags."
      },
      "max_results": {
        "type": "integer",
        "default": 5,
        "description": "Maximum number of memories to return."
      },
      "projection_tier": {
        "type": "integer",
        "enum": [1, 2, 3],
        "default": 2,
        "description": "Detail tier returned: 1 (Abstract ~20t), 2 (Summary ~150t), 3 (Full)."
      }
    },
    "required": ["query"]
  }
}
```

#### Tool 3: `get_memory`
Fetches a specific memory record by ID.

```json
{
  "name": "get_memory",
  "description": "Retrieve exact memory record by ID, with optional tier projection.",
  "parameters": {
    "type": "object",
    "properties": {
      "id": {
        "type": "string",
        "description": "Unique memory identifier."
      },
      "projection_tier": {
        "type": "integer",
        "enum": [1, 2, 3],
        "default": 3,
        "description": "Requested detail tier."
      }
    },
    "required": ["id"]
  }
}
```

#### Tool 4: `list_memories`
Lists and browses memory entries with filtering and pagination.

```json
{
  "name": "list_memories",
  "description": "List stored memories filtered by pillar, tag, or salience threshold.",
  "parameters": {
    "type": "object",
    "properties": {
      "pillar": {
        "type": "string",
        "enum": ["episodic", "semantic", "procedural", "prospective", "evaluative"]
      },
      "tag": {
        "type": "string"
      },
      "min_salience": {
        "type": "number",
        "default": 0.0
      },
      "limit": {
        "type": "integer",
        "default": 20
      },
      "offset": {
        "type": "integer",
        "default": 0
      }
    }
  }
}
```

---

## 3. Shared Module vs. Adapter Strategy Evaluation

Standardizing cognitive memory across **Fleet** (Go-native monorepo running in rootless Podman containers) and **Lokol** (desktop/edge platform) requires choosing an architectural sharing model.

### 3.1 Option A: Dedicated External Shared Repository (`boggycreek/memory`)
Extract all memory logic, data structures, frontmatter parsers, BM25 indexing, and storage engine interfaces into an independent repository (`github.com/boggycreek/memory`).

- **Pros**:
  - Enforces strict interface decoupling and single-responsibility boundaries.
  - Guarantees 100% binary-level code sharing between Fleet and Lokol if both run Go.
  - Can be tagged and versioned independently via `go.mod` releases.
- **Cons**:
  - High operational overhead during alpha development (multi-repo PR orchestration, release tagging, dependency bumping).
  - Version drift between Fleet and Lokol if updates are not synchronized.
  - Complicates monorepo zero-dependency guarantees if `boggycreek/memory` introduces external dependencies.

### 3.2 Option B: Specification-Only Alignment with Independent Adapters
Publish a formal specification (this document) defining JSON schemas, on-disk directory layouts, and MCP contracts. Each platform builds its own storage engine adapter tailored to its local runtime environment.

- **Pros**:
  - Zero cross-repo coordination overhead or dependency coupling.
  - Platforms can optimize storage engines for local constraints (e.g., Lokol using local SQLite/vector extensions, Fleet using pure-Go stdlib + Valkey).
  - Rapid iteration without blocking partner platforms.
- **Cons**:
  - Code duplication across Go codebases.
  - Risk of subtle behavioral drift or parsing divergences over time.

### 3.3 Option C: Hybrid Peered Architecture (Recommended Strategy)
Establish a **peered zero-dependency core library** inside Fleet's monorepo (`mcp/pkg/memory` or `pkg/memory`), exported as a clean, stand-alone Go module without external third-party dependencies. Lokol can either import `github.com/boggycreek/fleet/mcp/pkg/memory` directly as a Go module or consume the specification contracts.

```
+-------------------------------------------------------------------------+
|                  Peered Shared Core Library (pkg/memory)                |
|  - Zero external dependencies (Go standard library only)                |
|  - YAML Frontmatter Parser & Memory Document Model                      |
|  - Tier 1 / Tier 2 Truncation & Summarization Interfaces                 |
|  - Pure-Go In-Memory BM25 Indexer                                       |
+-------------------------------------------------------------------------+
                                 |
         +-----------------------+-----------------------+
         |                                               |
         v                                               v
+---------------------------------+             +----------------------------------+
|      Fleet Platform Adapter     |             |      Lokol Platform Adapter      |
|  - STDIO JSON-RPC MCP Server    |             |  - Native Host Integration       |
|  - Container Persistent Vol     |             |  - Desktop SQLite / Vector Store |
|  - Forgejo Git Sync             |             |  - Local Git Workspace Backup    |
|  - Valkey Pub/Sub Cache         |             |  - Direct GUI Visualizer         |
+---------------------------------+             +----------------------------------+
```

#### Strategic Trade-off Matrix

| Metric | Option A: Dedicated Repo | Option B: Spec-Only | Option C: Hybrid Peered (Recommended) |
| :--- | :--- | :--- | :--- |
| **Development Velocity** | Low (Multi-repo sync) | High (Independent) | **High** (Single monorepo source of truth) |
| **Zero External Deps Compliance**| Medium (Requires vigilance) | High (Platform specific)| **Maximum** (Enforced by `go.mod` in monorepo) |
| **Schema Uniformity** | 100% Guaranteed | Risk of drift | **100% Guaranteed** via shared core pkg |
| **Storage Optimization** | Uniform / rigid | Tailored per platform | **Tailored** via pluggable adapters |

---

## 4. Platform Integration Architecture

### 4.1 Host / Lokol Integration

On host workstations and edge desktop environments running Lokol:

```
+-----------------------------------------------------------------------------+
|                             Lokol Workstation                               |
|                                                                             |
|  +---------------------+      +---------------------+      +-------------+  |
|  | Lokol Desktop Agent | ---> |  Pure-Go BM25 Engine| ---> | Local Disk  |  |
|  +---------------------+      +---------------------+      | ~/.memory/  |  |
|                                                            +-------------+  |
|                                                                   |         |
|                                                                   v         |
|                                                            +-------------+  |
|                                                            |  Local Git  |  |
|                                                            | Auto-Commit |  |
|                                                            +-------------+  |
+-----------------------------------------------------------------------------+
```

1. **Storage Location**: `~/.lokol/memory/` or project-local `.lokol/memory/`.
2. **Search Engine**: Pure-Go in-memory BM25 indexer backed by disk-persisted TF-IDF terms, augmented with optional local vector embeddings if a local LLM gateway is connected (ADR 00035).
3. **Backup & Synchronization**: Background file watcher automatically creates local Git commits on memory mutation (`git commit -m "memory: update procedural/podman.md"`), preserving revision history and rollbacks.

---

### 4.2 Fleet In-Container Integration (`memory-mcp`)

Inside containerized Fleet sandboxes:

```
+------------------------------------------------------------------------------------+
|                         Fleet Agent Container (Podman)                             |
|                                                                                    |
|  +--------------------+   STDIO JSON-RPC   +------------------------------------+  |
|  | Agent Harness      | <================> | /usr/local/bin/memory-mcp          |  |
|  | (claude/pig/opencode)                     +------------------------------------+  |
|  +--------------------+                                      |                     |
|                                                              v                     |
|                                                +----------------------------+      |
|                                                | Volume: /home/agent/.memory|      |
|                                                +----------------------------+      |
+--------------------------------------------------------------|---------------------+
                                                               |
                                     +-------------------------+-------------------------+
                                     | Sync Worker                                       |
                                     v                                                   v
                       +---------------------------+                       +---------------------------+
                       | Local Forgejo Service     |                       | Valkey Backplane          |
                       | fleet/agent-name-memory.git                       | Key: memory:<agent>:tier1 |
                       +---------------------------+                       +---------------------------+
```

#### 1. Container Runtime & Storage
- Binary path: `/usr/local/bin/memory-mcp` (compiled zero-dependency Go static binary).
- Storage path: `/home/agent/.memory`, mounted on persistent named volume `fleet-<name>-home` (ADR 00005) so memories survive container restarts, updates, and recreation.

#### 2. Forgejo Git Sync Engine
- Every container agent maintains a dedicated memory repository in local Forgejo: `http://forgejo:3000/fleet/agent-<name>-memory.git` (ADR 00046).
- Upon initialization, `memory-mcp` clones or pulls the latest memory repository into `/home/agent/.memory`.
- On `store_memory` invocations, `memory-mcp` writes the Markdown file, stages it, and commits it locally. A background worker periodically pushes commits to Forgejo, guaranteeing durable cloud/forge backup.

#### 3. Valkey Pub/Sub & Fast-Path Cache
- For multi-agent ensembles, `memory-mcp` publishes Tier 1 abstracts to Valkey Streams (`stream:memory:broadcast`) (ADR 00010 / ADR 00042).
- Allows peer agents to monitor live event abstract streams in real time without calling heavy file search routines.

---

## 5. Implementation Recommendations for `memory-mcp`

To realize this specification in Fleet (Beads issue **`sndbx-cpm.3`**), the implementation should proceed in three distinct phases:

### Phase 1: Core Core Engine & Stdio Server (`sndbx-cpm.3`)
1. Create `mcp/pkg/memory`:
   - Memory document data model and frontmatter YAML parser.
   - Truncation / summarization heuristics for Tier 1 and Tier 2.
   - Pure-Go in-memory BM25 indexer operating over `/home/agent/.memory`.
2. Create `mcp/cmd/memory-mcp`:
   - JSON-RPC 2.0 handler over STDIO implementing `store_memory`, `recall_memory`, `get_memory`, and `list_memories`.
   - Comprehensive unit test suite with `>= 90.0%` statement coverage.

### Phase 2: Forgejo Git Backup Integration
1. Implement background Git sync worker in `mcp/pkg/memory/sync.go` using `os/exec` against `git`.
2. Automatic repository creation via Forgejo REST API v1 (`/api/v1/user/repos`) if `agent-<name>-memory.git` does not exist.

### Phase 3: Valkey Tier 1 Fast-Path & Ensembling
1. Integrate `libbp` connection in `memory-mcp` to publish Tier 1 abstracts to Valkey Streams on memory storage events.
2. Enable peer agents to query fleet-wide Tier 1 abstracts across backplane channels.

---

## 6. Verification & Quality Gates

To satisfy platform quality standards (ADR 00025):
- All Go packages associated with memory management must enforce `>= 90.0%` statement test coverage.
- All temporary test directories created during unit testing of Podman/git memory hooks must use `podman unshare` teardown handlers.
- All secrets and tokens passed during git sync operations must be injected via context or environment variables, avoiding command-line argument leakage.
