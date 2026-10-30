# Technical Implementation Specification: `fleet-agent` Native Go Agent Runtime Engine

## 1. Overview & System Goals

The `fleet-agent` is a purpose-built, multi-threaded native Go agent runtime binary engineered to run as **PID 1** inside unprivileged, rootless OCI container sandboxes (executing as user `agent`, UID/GID 1000). It completely eliminates legacy interactive CLI harness wrappers (`bpd`, `agent-runner`, `tmux`, terminal emulation, STDIN/STDOUT scraping) in favor of a headless, autonomous execution model.

### Core Requirements
* **PID 1 Process Leadership**: Acts as container init, OS signal handler (SIGTERM/SIGINT), sub-process reaper, and runtime supervisor.
* **Autonomous "YOLO" Execution Mode**: Automatically approves and executes tool calls (shell commands, file reads/writes, edits) within `/home/agent/workspace` without requiring human-in-the-loop approval.
* **Zero TUI / Structured Telemetry**: Logs strictly as line-delimited JSON (`ndjson`) to `stdout`/`stderr`.
* **Rootless Security Perimeter**: Bound by unprivileged non-root Linux subuid mappings and dropped capabilities; zero `sudo` access inside the container.
* **Sub-Millisecond Fast Reflexes**: Answers P3 status/telemetry pings instantly over the Valkey backplane from in-memory state without invoking LLM tokens or modifying the primary context window.

---

## 2. System Architecture & Concurrency Model

The engine adopts a **Modular Subsystem Supervisor Architecture**. The master PID 1 supervisor manages concurrent `Subsystem` workers via Go contexts, channels, and a unified in-memory event bus.


```

+---------------------------------------------------------------------------------------+
| CONTAINER SECURITY PERIMETER (UID/GID 1000 - Non-Root / Rootless Subuid)              |
|                                                                                       |
|  +---------------------------------------------------------------------------------+  |
|  |             NATIVE AGENT RUNTIME PROCESS (`fleet-agent` - PID 1)                |  |
|  |                                                                                 |  |
|  |  +---------------------------------------------------------------------------+  |  |
|  |  |           RUNTIME SUPERVISOR (`pkg/runtime/supervisor.go`)                |  |  |
|  |  |      (errgroup Lifecycle, Signal Handling, Subsystem Panic Recovery)      |  |  |
|  |  +-------------------------------------+-------------------------------------+  |  |
|  |                                        |                                        |  |
|  |               Registers & Supervises Subsystems (`Worker` Interface)             |  |  |
|  |                                        |                                        |  |
|  |   +-----------------------+  +---------+-------------+  +--------------------+  |  |
|  |   | Fast-Reflex Gateway   |  | Executive REPL Engine |  | In-Process Sub-Agent|  |  |
|  |   | (Valkey / libbp)      |  | (Deep-Lobe REPL)      |  | (Worker Goroutine) |  |  |
|  |   +-----------+-----------+  +-----------+-----------+  +----------+---------+  |  |
|  |               |                          |                         |            |  |
|  |               |  +-----------------------+---+                     |            |  |
|  |               |  | Callback / Webhook        |                     |            |  |
|  |               |  | Handler (HTTP / gRPC)     |                     |            |  |
|  |               |  +-----------+---------------+                     |            |  |
|  |               |              |                                     |            |  |
|  |               |              |   +-----------------------+         |            |  |
|  |               |              |   | Background Watcher    |         |            |  |
|  |               |              |   | (Sonar / Git / Timer) |         |            |  |
|  |               |              |   +-----------+-----------+         |            |  |
|  |               |              |               |                     |            |  |
|  |               v              v               v                     v            |  |
|  |  +---------------------------------------------------------------------------+  |  |
|  |  |              UNIFIED IN-MEMORY EVENT BUS & PRIORITY DISPATCHER            |  |  |
|  |  |                  (Priority Channels: P0, P1, P2, P3)                      |  |  |
|  |  +-------------------------------------+-------------------------------------+  |  |
|  |                                        |                                        |  |
|  |                                        v                                        |  |
|  |  +---------------------------------------------------------------------------+  |  |
|  |  |           THREAD-SAFE SHARED STATE & WORKSPACE LOCK MANAGER               |  |  |
|  |  |             (`sync.RWMutex` | Single-Writer Workspace Lock)               |  |  |
|  |  +-------------------------------------+-------------------------------------+  |  |
|  +----------------------+---------------------------------------+------------------+  |
|                         |                                       |                     |
|                         v                                       v                     |
|            [ Valkey Streams Messaging ]            [ Local LLM Inference Gateway ]    |
|            (:inbox / :out)           (http://llm-gateway:11434/v1)      |
+---------------------------------------------------------------------------------------+

```

### Concurrency Mechanics
1. **Master Lifecycle (`golang.org/x/sync/errgroup`)**: Used in `main.go` to bind OS signal handlers (SIGTERM, SIGINT) and manage the core runtime lifespan.
2. **Subsystem Fault-Tolerance**: Individual subsystems are wrapped in supervisor restart loops with `defer/recover` blocks. Transient errors or panics in background watchers (e.g., SonarQube poller) log errors to the event bus and auto-restart without crashing the Executive REPL or canceling the master `errgroup` context.
3. **Single-Writer Workspace Lock (`sync.RWMutex`)**: Only one subsystem (e.g., active Executive REPL or an active sub-agent) may acquire the mutating workspace lock (`/home/agent/workspace`) at any given time.

---

## 3. Go Package & Directory Structure

Place all code within the Go monorepo structure as follows:

```
agent/
cmd/
fleet-agent/
main.go                   # PID 1 entrypoint, flag parsing, errgroup lifecycle
pkg/
runtime/
supervisor.go             # Subsystem lifecycle manager & restart wrappers
subsystem.go              # Core Subsystem & Event interfaces
bus.go                    # Priority-based event bus dispatcher
state.go                  # Thread-safe SharedState & disk snapshot flusher
types.go                  # Enums (PriorityLevel), BackplaneMessage, config
agent/
gateway/
valkey_gateway.go       # Fast-reflex libbp stream poller & P3 auto-responder
verifier.go             # Ed25519 signature validator
engine/
repl_engine.go          # Multi-turn cognitive LLM loop
prompt.go               # Context window assembler & prompt compaction
llm_client.go           # OpenAI-compatible API client HTTP wrapper
tools/
registry.go             # Tool dispatcher & JSON-Schema exporter
builtin.go              # Native Go tools (bash, read_file, write_file, edit_file)
mcp_adapter.go          # STDIO JSON-RPC 2.0 MCP server manager (bp-mcp, beads-mcp, etc.)
watchers/
sonar_watcher.go        # Background static analysis poller
git_watcher.go          # Background repo status monitor
```

---

## 4. Core Interfaces & Type Definitions

### `pkg/runtime/subsystem.go`
```go
package runtime

import (
	"context"
)

// Subsystem defines a concurrent worker managed by the Supervisor.
type Subsystem interface {
	// ID returns the unique name of the worker (e.g., "gateway-valkey", "repl-main").
	ID() string

	// Start executes the worker loop. It must run until ctx is canceled or an unrecoverable error occurs.
	Start(ctx context.Context, bus *EventBus, state *SharedState) error
}
```

### `pkg/runtime/types.go`

```go
package runtime

import "time"

type PriorityLevel int

const (
	P0_Control       PriorityLevel = iota // Emergency stop, cancellation
	P1_HighPriority                       // Direct user directive, task pivot
	P2_StandardAsync                      // Peer updates, background notices
	P3_TelemetryQuery                     // Ping, health audit, status check
)

type Event struct {
	ID        string        `json:"id"`
	Priority  PriorityLevel `json:"priority"`
	Source    string        `json:"source"` // e.g., "valkey:inbox", "watcher:sonar"
	Target    string        `json:"target"` // e.g., "repl-main", "state-store", "*"
	Payload   any           `json:"payload"`
	Timestamp time.Time     `json:"timestamp"`
}

type BackplaneEnvelope struct {
	ID        string    `json:"id"`
	Sender    string    `json:"sender"`
	Recipient string    `json:"recipient"`
	Payload   string    `json:"payload"`
	Signature string    `json:"signature"`
	Timestamp time.Time `json:"timestamp"`
}
```

### `pkg/runtime/state.go`

```go
package runtime

import (
	"sync"
	"time"
)

type AgentState struct {
	AgentID         string    `json:"agent_id"`
	Status          string    `json:"status"`           // "idle", "working", "interrupted", "error"
	CurrentTaskID   string    `json:"current_task_id"`  // e.g., "task-104.2"
	TaskDescription string    `json:"task_description"`
	ActiveBranch    string    `json:"active_branch"`
	CurrentActivity string    `json:"current_activity"`
	LastTestResult  string    `json:"last_test_result"`
	QueueDepth      int       `json:"queue_depth"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SharedState struct {
	mu           sync.RWMutex
	workspaceMu  sync.RWMutex // Mutex protecting /home/agent/workspace mutations
	data         AgentState
	stateFilePath string
}

func NewSharedState(filePath string, agentID string) *SharedState {
	return &SharedState{
		data: AgentState{
			AgentID:   agentID,
			Status:    "idle",
			UpdatedAt: time.Now(),
		},
		stateFilePath: filePath,
	}
}

func (s *SharedState) Read() AgentState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *SharedState) Update(fn func(data *AgentState)) {
	s.mu.Lock()
	fn(&s.data)
	s.data.UpdatedAt = time.Now()
	s.mu.Unlock()
	s.flushToDisk()
}

func (s *SharedState) LockWorkspace() {
	s.workspaceMu.Lock()
}

func (s *SharedState) UnlockWorkspace() {
	s.workspaceMu.Unlock()
}
```

---

## 5. Subsystem Detailed Designs

### A. Fast-Reflex Gateway Subsystem (`pkg/agent/gateway`)

* **Responsibilities**:
1. Polls incoming stream messages over Valkey (`<agent>:inbox`) via `libbp`.
2. Verifies cryptographic Ed25519 signatures against sender public keys (`identity:<sender>`).
3. **P3 Handling**: If payload is a status query or health check, reads `SharedState.Read()` and immediately publishes a signed JSON response to `<agent>:out` without involving LLM tokens or modifying the context window.
4. **P0 Handling**: If payload is a `CANCEL` or `HALT` directive, triggers `context.CancelFunc` on active child tool execution contexts and posts `Event{Priority: P0_Control}` to the bus.
5. **P1/P2 Handling**: Pushes payload to the internal event bus `InboundQueue`.

### B. Executive REPL Engine Subsystem (`pkg/agent/engine`)

* **Responsibilities**:
1. Manages the autonomous cognitive loop against `OPENAI_BASE_URL` (`http://llm-gateway:<port>/v1`).
2. Drains `InboundQueue` at natural turn boundaries (between tool execution steps) to build system/user prompts.
3. Constructs the prompt: `System Base` + `Active Task Graph (bd ready)` + `Buffered P1/P2 Messages` + `Conversation Context History`.
4. Dispatches request to LLM, receives tool calls, executes them via `pkg/agent/tools`, appends observations, and loops.
5. Enforces context window compaction when token usage exceeds limits (summarizing older history while retaining active system instructions and tool schemas).

### C. Tools & MCP Protocol Subsystem (`pkg/agent/tools`)

* **Built-in System Tools**:
* `bash`: Executes shell commands inside `/home/agent/workspace`. Features enforced timeout (default 60s), environment variable sanitization, and output truncation (max 10,000 characters).
* `read_file`: Reads text/binary file content with path traversal prevention outside `/home/agent/workspace`.
* `write_file`: Overwrites file content atomically.
* `edit_file`: Performs exact string or line-based replacement.

* **In-Container MCP Client Adapter**:
* Spawns installed MCP binaries (`/usr/local/bin/bp-mcp`, `/usr/local/bin/beads-mcp`, `/usr/local/bin/sonar-mcp`) over STDIO JSON-RPC 2.0.
* Queries `tools/list` on launch, converts JSON schemas into standard OpenAI tool definitions, and routes `tools/call` JSON-RPC requests natively.

---

## 6. Implementation Checklist for Coding Agent

* [ ] **Phase 1: Subsystem & Runtime Foundations**
* [ ] Implement `pkg/runtime/types.go`, `subsystem.go`, and `state.go`.
* [ ] Implement `pkg/runtime/bus.go` with priority channels (P0, P1, P2, P3).
* [ ] Implement `pkg/runtime/supervisor.go` with worker registration, panic recovery, and `golang.org/x/sync/errgroup` lifecycle management.

* [ ] **Phase 2: Fast-Reflex Gateway & Valkey Integration**
* [ ] Implement `pkg/agent/gateway/verifier.go` for Ed25519 signature validation using standard `crypto/ed25519`.
* [ ] Implement `pkg/agent/gateway/valkey_gateway.go` polling Valkey inbox streams via `libbp` bindings.
* [ ] Wire instant P3 status replies directly from `SharedState`.

* [ ] **Phase 3: Tools & MCP Client Adapter**
* [ ] Implement `pkg/agent/tools/builtin.go` (`bash`, `read_file`, `write_file`, `edit_file`) with strict workspace path bounds.
* [ ] Implement `pkg/agent/tools/mcp_adapter.go` managing STDIO JSON-RPC 2.0 subprocesses.
* [ ] Implement `pkg/agent/tools/registry.go` aggregating built-in and MCP tool definitions into OpenAI function format.

* [ ] **Phase 4: Executive REPL Engine**
* [ ] Implement `pkg/agent/engine/llm_client.go` using `net/http` targeting OpenAI `/v1/chat/completions`.
* [ ] Implement `pkg/agent/engine/prompt.go` for context assembly, `bd ready` task graph injection, and turn compaction.
* [ ] Implement `pkg/agent/engine/repl_engine.go` implementing the main turn loop and non-blocking event bus polling.

* [ ] **Phase 5: Entrypoint & Integration**
* [ ] Implement `agent/cmd/fleet-agent/main.go` setting up PID 1 signal handlers, parsing flags/env variables, and starting supervisor.
* [ ] Validate non-root execution (UID 1000) and structured `ndjson` logging to `stdout`.
