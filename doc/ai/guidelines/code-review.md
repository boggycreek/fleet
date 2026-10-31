---
name: fleet-code-review
description: >-
  Standard protocol and instructions for performing architectural, logical, and security
  code reviews on Pull Requests in the fleet repository.
---

# Code Review Protocol & Skill

This skill defines the methodology, inspection checklist, and output format for performing thorough peer reviews of Pull Requests on the `fleet` repository.

---

## 0. Mandatory 2-Agent PR Separation Protocol

To prevent author bias and eliminate blindspots, all code reviews in this repository must strictly adhere to the **2-Agent Separation Principle**:

1. **Independent Adversarial Reviewer**:
   - The agent/subagent that authored or committed the code for a PR **must never** review or approve its own PR.
   - An independent subagent (`adversarial-reviewer`) must be spawned with clean context, receiving only the PR diff, commit history, and this review protocol.
   - The reviewer's explicit objective is adversarial: actively probe for security perimeter escapes, data races, socket desyncs, missing test coverage on error branches, and architectural drift.

2. **Gated Merge Authorization**:
   - Neither the PR author nor the orchestrator may squash-merge a PR until the independent adversarial reviewer has posted a formal review with an assessment of **GO**.
   - If findings of severity **High** or **Critical** are identified, the verdict is **NO-GO**, and control returns to an implementation worker for remediation.

---

## 1. Scope & Review Focus

When reviewing Pull Requests, focus primarily on non-test production code (`.go`, `.sh`, `.yml`, Dockerfiles), cross-referencing against tests to ensure behavioral validity without getting bogged down by boilerplate test mechanics.

Inspect changes across three essential dimensions:

### A. Architectural Integrity
- **Separation of Concerns:** Ensure runtime execution boundaries (host vs. container vs. infra) are preserved.
- **Seams & Extensibility:** Verify dependency injection and interfaces (e.g. `CommandExecutor`) avoid brittle monkey-patching and global mutable state that leaks across goroutines or parallel tests.
- **Protocol & Lifecycle Consistency:** Confirm API methods and lifecycle phases (`create`, `start`, `provision`, `deprovision`, `doctor`, `retire`) remain idempotent and handle edge cases gracefully.
- **Alpha-Stage Pruning vs. Backward Compatibility:** Note that during rapid alpha development, explicit maintainer instructions to remove obsolete flags, deprecated code, or non-existent backward compatibility shims take precedence; avoid flagging intentional removals as issues unless they break active architectural invariants.

### B. Logical Correctness & Concurrency
- **I/O & Socket State Safety:** In network and stream clients (e.g., Valkey/RESP, HTTP clients), verify that connection timeouts (`context.Context`), cancellations, and deadlines never leave the connection stream partially read or desynchronized for subsequent callers. Broken connections must be severed and lazily reconnected.
- **Signal & Context Propagation:** Check that root context cancellations (SIGINT, SIGTERM) propagate down through subcommands, loops, and long-polling operations (such as `bp recv --block`). Context deadlines should never be derived from a detached background context unless explicitly wrapping parent signals.
- **Atomic Operations & Idempotency:** File writes to security-sensitive or shared files (`~/.ssh/config`, keypairs, configuration) must be atomic (tempfile + `os.Rename`), non-destructive to existing user configuration, and idempotent.

### C. Security & Secret Protection
- **Zero Host Leakage:** Container commands and execution args must never expose secrets or tokens on command-line argument vectors (`-e TOKEN=secret` in `argv` leaks via `ps` / `/proc/<pid>/cmdline`). Always inject via environment variables (`cmd.Env`), secrets, or environment files.
- **Permissive Path & Pattern Matching:** Avoid overly broad regexes or substring checks on user files (e.g., matching any `Include` containing `ssh_config`). Always tokenize and normalize paths.
- **Filesystem Permissions:** Ensure private keys, auth credentials, and configuration files enforce strict permission bits (`0600` for private keys/configs, `0700` for SSH directories, `0644` for public keys).

---

## 2. Review Methodology

1. **Extract & Isolate Changes:**
   - Fetch the PR diff and inspect commit history.
   - Separate production source changes from unit tests and formatting diffs.
2. **Trace Callers & Side Effects:**
   - Trace impacted call sites across CLIs (`cmd/fleet`, `cmd/bp`), daemons (`bpd`), and MCP servers (`bp-mcp`, `sonar-mcp`).
   - Check if new configuration fields or socket timeouts unexpectedly affect existing long-polling callers.
3. **Determine Verdict (Go / Conditional Go / No-Go):**
   - **No-Go:** High-severity findings present (data loss, credential leakage in argv, socket state desynchronization, broken core workflow).
   - **Conditional Go:** High-severity items resolved; only minor/medium fixes or unverified third-party integrations remain.
   - **Go:** All functional, logical, architectural, and security invariants are satisfied.

---

## 3. Output & Comment Format

Post code review findings directly to the PR or review summary using a concise executive table followed by expandable `<details>` sections for deep-dive technical findings.

```markdown
## Review: architecture, logic, security

**Assessment: [GO | CONDITIONAL GO | NO-GO]**

Summary sentence explaining overall direction and primary blocking or verified points.

| # | Severity | Area | Finding |
|---|----------|------|---------|
| 1 | High     | `pkg/...` | Concise description of defect and impact |
| 2 | Medium   | `cmd/...` | Concise description of defect and impact |

<details>
<summary><b>1. [High] Concise Title</b> — <code>path/to/file.go</code> (<code>functionName</code>)</summary>

<detail>

- Explanation of defect, failure mechanism, and reproduction vector.
- Why this breaks architecture, logic, or security invariants.
- **Recommendation:** Concrete remediation steps.

</detail>
</details>
```

When reviewing revision rounds or corrections, summarize resolved items in a status table, state the updated Go/No-Go verdict clearly, and detail any remaining open issues.
