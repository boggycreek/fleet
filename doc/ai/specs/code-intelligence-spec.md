---
name: fleet-code-intelligence-spec
description: >-
  Architectural specification for the 3-Tier Code Intelligence Plugin API, Driver Registry,
  syntax pre-validation gate, and get_outline tool across Fleet autonomous agent runtimes.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Fleet Code Intelligence & AST Architecture

## Executive Summary & Problem Statement

Autonomous software engineering agents require deep, reliable understanding of source code structures to execute architectural refactoring, bug fixes, and feature additions safely. Historically, agents relied primarily on coarse tools like full-file reading (`read_file`) and regex scanning (`grep_search`). This approach incurs two severe architectural liabilities:

1. **Massive Context Budget Waste**: Ingesting multi-thousand line files consumes tens of thousands of LLM context tokens simply to discover symbol signatures, struct definitions, and receiver methods. In large codebases, this exhausts token quotas and induces context degradation.
2. **Silent Syntax Corruption**: Naive search-and-replace editing (`edit_file`) frequently introduces syntax errors (such as unclosed braces, dangling operators, or invalid import declarations). When written directly to disk, corrupted files break build states, trigger cascading compiler errors, and derail agent execution loops.

The **Fleet Code Intelligence Subsystem** resolves these problems through an extensible, multi-tiered architecture:
- A pluggable **CodeIntelligenceProvider API** and thread-safe **Driver Registry**.
- Built-in **Native Go AST Driver** using Go standard library parsers (zero Cgo, zero external dependencies).
- **Universal Text Fallback Driver** supporting Markdown, Python, JavaScript, Rust, and Shell heuristics.
- A **`get_outline` Tool** that extracts structural outlines, saving 90%+ context tokens compared to reading full files.
- A **Pre-Validation Syntax Gate** inside `edit_file` that parses in-memory replacements before committing changes to disk, rejecting invalid syntax before disk writes occur.

---

## 3-Tier Code Intelligence Architecture

The platform organizes language intelligence into a three-tier hierarchy based on fidelity, performance, and resource footprint:

```
+-----------------------------------------------------------------------------------+
|                            Autonomous Agent REPL Engine                           |
+-----------------------------------------------------------------------------------+
                                         │
                 ┌───────────────────────┴───────────────────────┐
                 ▼                                               ▼
         `get_outline` Tool                             `edit_file` Tool
    (Token-Efficient Outline)                      (Pre-Validation Syntax Gate)
                 │                                               │
                 └───────────────────────┬───────────────────────┘
                                         ▼
                   +───────────────────────────────────────────+
                   |     CodeIntelligence Registry Router      |
                   +───────────────────────────────────────────+
                                         │
        ┌────────────────────────────────┼────────────────────────────────┐
        ▼                                ▼                                ▼
  [Tier 1: Native AST]          [Tier 2: LSP Sidecar]           [Tier 3: Fallback]
  Built-in Go AST Parser        JSON-RPC Language Servers       Universal Regex Heuristics
  - Zero Cgo                    - gopls, pyright, tsserver      - Markdown Headings
  - Zero External Deps          - Sidecar Container / IPC       - Shell / Script Functions
  - Sub-millisecond latency     - Full Workspace Indexing       - Graceful Degradation
```

### Tier 1: Native In-Process AST Drivers
High-speed, zero-dependency abstract syntax tree parsers implemented directly in the agent harness (`agent/pkg/codeintel`).
- **Characteristics**: Instant startup (< 2ms), zero network overhead, zero Cgo bindings, completely thread-safe.
- **Implementations**: `NativeGoDriver` leveraging `go/parser`, `go/ast`, `go/token`, `go/printer`, and `go/scanner`.

### Tier 2: LSP Sidecar Drivers (Future Spike Extension)
Full-featured Language Server Protocol daemons running as isolated companion containers or background processes within the agent pod.
- **Characteristics**: Workspace-wide cross-file symbol indexing, type resolution, hover docs, and complex refactorings.
- **Implementations**: `pyright`, `gopls`, `rust-analyzer`, and `typescript-language-server`.

### Tier 3: Universal Fallback Driver
Resilient regular-expression and structural heuristic engine providing baseline coverage for any text or code file.
- **Characteristics**: Handles Markdown headings (`# ...`), Python definitions (`def`, `class`), JavaScript/TypeScript functions, Shell functions, and Rust constructs when no language-specific driver is configured.
- **Implementations**: `UniversalFallbackDriver` matching wildcard (`*`).

---

## Pluggable Driver API & Registry Specification

### Core Provider Interface

All code intelligence drivers satisfy the `CodeIntelligenceProvider` interface defined in `agent/pkg/codeintel/provider.go`:

```go
type CodeIntelligenceProvider interface {
    // Name returns the identifier of the provider (e.g. "native-go", "universal-fallback").
    Name() string

    // Languages returns file extensions (e.g. ".go", "go") or wildcard "*" supported by this provider.
    Languages() []string

    // GetOutline extracts a hierarchical symbol outline from the file content or file path.
    GetOutline(ctx context.Context, filePath string, content []byte) (*Outline, error)

    // FindDefinition locates symbol definitions matching coordinates (1-based line and column).
    FindDefinition(ctx context.Context, filePath string, line, col int) ([]Location, error)

    // FindReferences finds symbol usage occurrences matching coordinates (1-based line and column).
    FindReferences(ctx context.Context, filePath string, line, col int) ([]Location, error)

    // ValidateSyntax parses the content and checks for language syntax errors.
    ValidateSyntax(ctx context.Context, filePath string, content []byte) ([]Diagnostic, error)
}
```

### Domain Data Models

```go
type SymbolKind string

const (
    SymbolKindPackage   SymbolKind = "package"
    SymbolKindStruct    SymbolKind = "struct"
    SymbolKindInterface SymbolKind = "interface"
    SymbolKindFunction  SymbolKind = "function"
    SymbolKindMethod    SymbolKind = "method"
    SymbolKindConstant  SymbolKind = "constant"
    SymbolKindVariable  SymbolKind = "variable"
    SymbolKindType      SymbolKind = "type"
)

type SymbolNode struct {
    Name      string       `json:"name"`
    Kind      SymbolKind   `json:"kind"`
    Signature string       `json:"signature,omitempty"`
    Doc       string       `json:"doc,omitempty"`
    Line      int          `json:"line"`
    Col       int          `json:"col"`
    EndLine   int          `json:"end_line"`
    EndCol    int          `json:"end_col"`
    Children  []SymbolNode `json:"children,omitempty"`
}

type Outline struct {
    FilePath    string       `json:"file_path"`
    Language    string       `json:"language"`
    PackageName string       `json:"package_name,omitempty"`
    Symbols     []SymbolNode `json:"symbols"`
}

type DiagnosticSeverity string

const (
    SeverityError   DiagnosticSeverity = "error"
    SeverityWarning DiagnosticSeverity = "warning"
    SeverityInfo    DiagnosticSeverity = "info"
)

type Diagnostic struct {
    Message  string             `json:"message"`
    Severity DiagnosticSeverity `json:"severity"`
    Line     int                `json:"line"`
    Col      int                `json:"col"`
    EndLine  int                `json:"end_line,omitempty"`
    EndCol   int                `json:"end_col,omitempty"`
    Rule     string             `json:"rule,omitempty"`
}
```

---

## Registry Routing & Fallback Chains

The `Registry` (`agent/pkg/codeintel/registry.go`) is thread-safe and routes requests according to file extensions and names:

1. **Specific Extension Match**: Evaluates `filepath.Ext(filePath)` (e.g. `.go`). If a driver is registered for `.go`, it is invoked.
2. **Base Filename Match**: Evaluates `filepath.Base(filePath)` (e.g. `Dockerfile`, `Makefile`).
3. **Universal Fallback**: If no specialist driver matches, the registry routes to the fallback driver (`*`).
4. **Graceful Degradation**: If no driver is matched and no fallback exists, `ValidateSyntax` returns an empty diagnostic list (permitting execution), while `GetOutline` returns a clear error.

### Declarative Workspace Configuration

Workspaces can customize provider routing via `.fleet/intel.json` or `.agent_config.json`:

```json
{
  "default_provider": "universal-fallback",
  "providers": {
    ".go": "native-go",
    ".py": "pyright-lsp"
  },
  "disabled": [
    "experimental-driver"
  ]
}
```

---

## Agent Workspace Tooling Integration

### 1. The `get_outline` Tool

The `get_outline` tool exposes code intelligence directly to the LLM agent:
- **Parameter**: `path` (string, required): Relative or workspace-scoped path of the source file.
- **Behavior**:
  - Resolves path securely inside the workspace boundary using `ResolveWorkspacePath`.
  - Queries `Registry.GetOutline`.
  - Generates token-optimized Markdown outline via `FormatOutlineMarkdown`.

#### Example Output:
```markdown
## Outline: agent/pkg/tools/builtin.go
- **Language**: go
- **Package**: `tools`
- **Symbols**: 14 top-level declarations

- `type EditFileTool struct` [struct] (lines 344-347)
  - variable `workspaceDir string` (line 345)
  - variable `intelReg *codeintel.Registry` (line 346)
- `func (t *EditFileTool) Name() string` (lines 357-357)
- `func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (string, error)` (lines 382-442)
    // Execute performs exact string replacement with syntax pre-validation.
```

### 2. Syntax Pre-Validation Gate in `edit_file`

The `edit_file` tool guards against corrupted workspace state by validating syntax before committing changes:

```
[Agent Calls edit_file]
         │
         ▼
[Resolve Path & Read Existing File]
         │
         ▼
[Verify Target Chunk Uniqueness]
         │
         ▼
[Apply String Replacement in Memory]
         │
         ▼
[Query registry.ValidateSyntax(ctx, path, modifiedBytes)]
         │
    ┌────┴──────────────────────────┐
    │ Has SeverityError Diagnostic?  │
    └────┬──────────────────────────┘
         │
   YES ──┼──> [ABORT: Return "edit rejected: syntax error in %s at line %d: %s"]
         │    (File on disk remains UNTOUCHED)
         │
   NO  ──┴──> [WRITE modifiedBytes to Disk]
         │
         ▼
[Return "Successfully edited %s"]
```

---

## Performance & Context Token Efficiency

| Metric | Full File Read (`read_file`) | Structural Outline (`get_outline`) | Improvement |
| :--- | :--- | :--- | :--- |
| **Typical Go File (1,000 LOC)** | ~7,500 context tokens | ~380 context tokens | **~95% token reduction** |
| **Parsing & Validation Latency** | N/A | 0.8ms - 2.5ms | Instantaneous |
| **Disk Corruption on Syntax Error** | Possible (Syntax Broken) | **0% (Rejected Before Write)** | Complete Safety |

---

## Compliance & Quality Gate Verification

1. **Monorepo Test Coverage**: `agent/pkg/codeintel` maintains `>= 93%` statement coverage.
2. **Cgo-Free Guarantee**: Pure Go standard library dependencies ensure immediate cross-platform compilation on macOS and Linux.
3. **Perimeter Confinement**: All file resolution enforces `ResolveWorkspacePath` to prevent directory traversal outside the agent sandbox.
