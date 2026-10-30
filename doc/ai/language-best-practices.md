---
name: fleet-language-best-practices
description: >-
  Language-specific engineering best practices, idioms, and conventions for Go, Python, TypeScript/Node.js, Shell/Bash, and Rust.
  Use when designing, writing, reviewing, or refactoring code in workspace projects.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Language-Specific Engineering Best Practices

This guide establishes language-specific engineering best practices and conventions optimized for autonomous agent workspaces. Agents query this guide on-demand when working on projects written in Go, Python, TypeScript / Node.js, Shell / Bash, or Rust.

---

## 1. Go

### Idiomatic Error Handling
- **Wrap Errors with Context**: Use `fmt.Errorf("doing action: %w", err)` to preserve causal error chains.
- **Custom Error Types & Inspection**: Use `errors.Is()` for value equality/sentinels and `errors.As()` for type assertions.
- **Avoid Naked/Ignored Errors**: Never ignore returned errors with `_` unless explicitly documented and intentional (e.g. `defer file.Close()`).
- **Keep Non-Error Flow Unindented**: Handle errors early (`if err != nil { return ... }`) to keep the primary happy path unindented.

### Context Cancellation
- **Pass `context.Context` First**: Make `ctx context.Context` the first parameter of I/O-bound or long-running functions.
- **Respect Context Done Channel**: Periodically check `ctx.Done()` or select on `ctx.Done()` during blocking loops.
- **Propagate Deadlines & Signals**: Avoid `context.Background()` inside requests or command executions; propagate parent contexts down the call stack. Always invoke `cancel()` returned by `context.WithCancel` or `context.WithTimeout` (typically using `defer cancel()`).

### Goroutine Lifecycle & Leak Avoidance
- **Explicit Lifecycle Ownership**: Never launch a goroutine without knowing how and when it will terminate.
- **Wait For Worker Completion**: Use `sync.WaitGroup`, `errgroup.Group`, or channel synchronization to ensure goroutines exit before returning or shutting down.
- **Avoid Blocking Channel Sends**: Ensure channels have appropriate buffer sizes or worker cancellation handlers to prevent goroutines from hanging indefinitely on channel sends.

### Mutex & Atomic Patterns
- **Lock Scoping**: Lock mutexes immediately before protected read/write operations and unlock with `defer mu.Unlock()` or explicit early unlock for tight loops.
- **Data Protection**: Guard data structures, not code paths. Keep critical sections short; avoid I/O or network calls while holding locks.
- **Atomics**: Use `sync/atomic` (e.g., `atomic.Int64`, `atomic.Bool`, `atomic.Pointer`) for simple counter, flag, or pointer updates without full mutex overhead.

### Table-Driven Subtesting with `t.Setenv`
- **Table-Driven Design**: Use a slice of structs containing `name`, inputs, expected outputs, and expected error condition.
- **Subtesting with `t.Run`**: Wrap subtests in `t.Run(tc.name, func(t *testing.T) { ... })` for isolated execution and clear failure reports.
- **Environment Isolation with `t.Setenv`**: Use `t.Setenv("KEY", "VALUE")` to modify environment variables during tests. It automatically restores original values upon test completion and prevents environment leaks across test cases.

---

## 2. Python

### Type Annotations
- **Type Everything**: Enforce type annotations on all function arguments, return values, and class attributes using standard library `typing` (or Python 3.9+ built-in generics like `list[str]`, `dict[str, Any]`).
- **Static Analysis Readiness**: Write code compatible with `mypy` or `pyright` strict modes.
- **Avoid `Any`**: Use `Union`, `Optional` (or `X | None`), `TypeVar`, or `Protocol` instead of bare `Any`.

### Virtual Environments
- **Isolated Environments**: Always run within an isolated virtual environment (`.venv` or `venv`).
- **Reproducible Dependencies**: Pin exact dependency versions in `requirements.txt` or `pyproject.toml` lockfiles (`poetry.lock`, `uv.lock`, `pipfile.lock`).

### Asyncio Task Cancellation
- **Handle `asyncio.CancelledError`**: When writing `async def` tasks, catch `asyncio.CancelledError` to perform cleanup, then re-raise or propagate the cancellation.
- **Task Management**: Track running background tasks in a `set` or `asyncio.TaskGroup` (Python 3.11+) and explicitly cancel them during shutdown:
  ```python
  async with asyncio.TaskGroup() as tg:
      task = tg.create_task(worker())
  ```
- **Timeouts**: Use `asyncio.timeout()` context managers rather than unhandled task hangs.

### Pytest Fixtures
- **Explicit Dependency Injection**: Leverage `pytest` fixtures for setup, teardown, and test dependencies rather than setup/teardown methods.
- **Fixture Scope**: Set appropriate fixture scopes (`function`, `module`, `session`) to minimize setup overhead.
- **Yield Fixtures for Cleanup**: Use `yield` inside fixtures to ensure cleanup resources are torn down cleanly post-test:
  ```python
  @pytest.fixture
  def temp_file():
      f = open("temp.txt", "w")
      yield f
      f.close()
      os.remove("temp.txt")
  ```

### Resource Management with Context Managers
- **`with` Statements**: Always wrap file handles, sockets, database connections, and locks in `with` or `async with` statements.
- **Custom Context Managers**: Create custom context managers using `@contextlib.contextmanager` or `@contextlib.asynccontextmanager` for custom teardown logic.

---

## 3. TypeScript / Node.js

### Strict Typing
- **Enable `strict: true`**: Ensure `tsconfig.json` enables strict mode (`strictNullChecks`, `noImplicitAny`, etc.).
- **Interfaces & Types**: Prefer `interface` for public API contracts and object structures; use `type` for unions, primitives, and mapped types.
- **Avoid `any`**: Use `unknown` for unknown inputs and perform runtime type narrowing (using `zod`, Type Guards, or `instanceof`).

### Async/Await Error Propagation
- **Always Await Promises**: Ensure async functions return awaited promises inside `try/catch` blocks (`return await ...`) so call stacks are preserved on rejection.
- **Avoid Floating Promises**: Unhandled promises lead to uncaught exceptions or unhandled rejections; use `void` explicitly only when fire-and-forget is intended.
- **Custom Error Hierarchies**: Extend `Error` with custom domain error classes including helpful metadata and error codes.

### Stream Backpressure
- **Respect Stream Backpressure**: Never pump data blindly into a writable stream. Check the return value of `stream.write()` and wait for the `'drain'` event when it returns `false`.
- **Use `pipeline`**: Use `stream.promises.pipeline` or `stream.pipeline` to pipe streams safely; it handles error propagation and stream cleanup automatically.

### Clean Package Scripts
- **Standardized `package.json`**: Define standard scripts: `build`, `test`, `lint`, `format`, `typecheck`.
- **No Side-Effect Invocations**: Package scripts should not mutate local development setups outside of build artifacts (`dist/`, `build/`).

### Esbuild / Tsup Packaging
- **Fast Bundling**: Use `esbuild` or `tsup` for rapid, zero-config TypeScript bundling and transpilation.
- **Target Configurations**: Specify explicit build targets (e.g., `node18`, `es2022`) and output formats (CJS / ESM) in build configuration.

---

## 4. Shell / Bash

### `set -euo pipefail`
- **Fail Fast Initialization**: Every executable Bash script must begin with:
  ```bash
  #!/usr/bin/env bash
  set -euo pipefail
  ```
  - `-e`: Exit immediately on command error.
  - `-u`: Treat unset variables as an error.
  - `-o pipefail`: Return exit code of the last command in a pipeline that failed.

### Shellcheck Compliance
- **Zero Warnings**: All shell scripts must pass `shellcheck` without errors or suppressions unless documented with specific `# shellcheck disable=SCxxxx` annotations.
- **Double-Quote Variables**: Always quote variable expansions (`"$VAR"`, `"$@"`), especially when handling file paths or user inputs.

### POSIX vs Bashisms
- **Explicit Shebang**: Use `#!/usr/bin/env bash` when using Bash-specific features (arrays, `[[ ... ]]`, process substitution `<(...)`). Use `#!/bin/sh` strictly for POSIX-compliant scripts (`[ ... ]`, `sh`).

### Temporary File Handling with `trap` Cleanup
- **Atomic Temp Files**: Create temp files using `mktemp` or `mktemp -d`.
- **Deterministic Teardown**: Always register a `trap` hook immediately after creation to guarantee cleanup on `EXIT`, `INT`, or `TERM`:
  ```bash
  TMPDIR="$(mktemp -d)"
  trap 'rm -rf "$TMPDIR"' EXIT INT TERM
  ```

---

## 5. Rust

### Error Propagation (`Result`, `anyhow`, `thiserror`)
- **Use `Result<T, E>`**: Return explicit `Result` types for fail-able operations; avoid `panic!`, `unwrap()`, or `expect()` in library or production code paths.
- **`thiserror` for Libraries**: Define strongly-typed, domain-specific error enums using `thiserror::Error` in library crates.
- **`anyhow` for Applications/CLIs**: Use `anyhow::Result` and `anyhow::Context` (`.context("failed to execute operation")`) in application binaries or CLI commands for error aggregation and backtraces.

### Ownership & Borrow Checker Patterns
- **Borrowing over Ownership**: Pass references (`&str`, `&[T]`) when functions only need read-only access, avoiding unnecessary `.clone()` calls.
- **RAII Pattern**: Capitalize on Rust's Resource Acquisition Is Initialization semantics; drop resources automatically when going out of scope.
- **Smart Pointers**: Use `Arc<T>` for thread-safe shared ownership, `Mutex<T>` or `RwLock<T>` for interior mutability in concurrent contexts.

### Concurrency with Tokio
- **Async/Await Directives**: Structure concurrent asynchronous runtimes using Tokio (`#[tokio::main]`).
- **Select & Cancellation**: Use `tokio::select!` for handling concurrent futures and cancellation signals cleanly.
- **Spawn Lifetimes**: Ensure tasks spawned via `tokio::spawn` own their data or use `'static` lifetimes with `Arc`.
