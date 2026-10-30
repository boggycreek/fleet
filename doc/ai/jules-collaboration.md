---
name: fleet-jules-collaboration
description: >-
  Standard operating guide and instructions for delegating audit, refactoring, and quality
  tasks to Jules (GitHub agent), tracking task progress, and conducting rigorous adversarial
  reviews on Jules Pull Requests.
---

# Jules Collaboration & PR Review Protocol

This skill outlines the standard operating procedures for delegating tasks to **Jules** (our asynchronous autonomous GitHub coding agent), tracking in-flight work, and performing thorough adversarial code reviews on Jules Pull Requests.

---

## 1. Overview & Operational Role

Jules operates as an autonomous background agent triggered via GitHub issues. Jules is optimized for:
- **Comprehensive audits**: Protocol compliance, cryptography checks, JSON-RPC validation, shell script safety.
- **Targeted refactorings**: Decomposing large test suites, eliminating dead code, standardizing interfaces.
- **Documentation & test expansion**: Filling coverage gaps, verifying edge cases, and updating developer guides.

### Division of Responsibility & Decoupled Execution
- **Lead Agent / Human Developer**: Drives core architectural evolution, runtime Podman pods, local CLI commands, and release orchestration on local branches.
- **Jules**: Runs asynchronously on GitHub in dedicated branches (`jules/*` or PRs authored by `@app/jules`).
- **Collision Avoidance Rule**: **Never** assign Jules tasks in packages or subsystems currently undergoing active architectural changes or local branch edits. Keep Jules tasks strictly isolated to distinct modules (e.g. MCP servers, shell scripts, backplane protocol parsers) while runtime infrastructure work proceeds.

---

## 2. Dispatching Tasks to Jules

Tasks are assigned to Jules by creating a GitHub Issue tagged with the `jules` label.

### A. Creating an Issue via GitHub CLI

```bash
gh issue create \
  --title "<type>(<scope>): <clear, imperative title>" \
  --label "jules" \
  --body "<structured prompt body>"
```

### B. Required Issue Structure

Every issue dispatched to Jules must include:

1. **Context & Objective**: Why this task is needed and what subsystem it targets.
2. **Explicit Scope & File Bounds**: List the specific directories, files, or packages Jules is authorized to touch.
3. **Repository Invariants (Must-Follow Constraints)**:
   - **Copyright Header**: Every new `.go`, `.sh`, `.yml` file must include the Boggy Creek Software LLC MIT copyright header.
   - **Quality Gates**: `make check` (or `make format && make lint && make test-coverage`) must pass.
   - **Strict Coverage**: Code coverage must remain $\ge 90.0\%$ statement coverage.
   - **Rootless Podman Hygiene**: Any tests creating temporary pod/container files must register rootless cleanup (`t.Cleanup(func() { _ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run() })`).
   - **Conventional Commits**: PR and commits must follow `feat:`, `fix:`, `refactor:`, `test:`, or `audit:`.
   - **No Regressions**: Do not introduce backward-compatibility shims or dead code unless explicitly requested.
4. **Concrete Acceptance Criteria**: Checkboxes defining exactly when the task is considered complete.

### C. Example Dispatch Template

```markdown
## Context & Objective
Conduct a targeted review and hardening of the MCP servers (`mcp/...`).
Focus on JSON-RPC 2.0 error handling, input sanitization, and graceful socket shutdown.

## Scope
- Focus packages: `mcp/cmd/beads-mcp/`, `mcp/cmd/bp-mcp/`, `mcp/cmd/forgejo-mcp/`, `mcp/cmd/sonar-mcp/`, `mcp/pkg/mcp/`
- Do not modify: `cli/pkg/runtime/` or `cli/cmd/fleet/`

## Constraints & Invariants
- All source files must start with Boggy Creek Software LLC MIT header.
- Maintain >= 90.0% statement test coverage across `mcp/...`.
- No global mutable state; all servers must respect parent `context.Context` cancellation.
- Run `make check` to verify before submitting PR.

## Acceptance Criteria
- [ ] Address any input validation weaknesses.
- [ ] Verify standard JSON-RPC 2.0 error codes on invalid requests.
- [ ] Ensure all unit tests pass with race detector enabled (`-race`).
- [ ] Submit PR with clean conventional commit message.
```

---

## 3. Monitoring In-Flight Jules Tasks

Monitor Jules' progress without blocking local development:

```bash
# List all active issues assigned to Jules
gh issue list --label jules --state open

# Check comments and progress on a specific issue
gh issue view <issue-number> --comments

# Search for Pull Requests submitted by Jules
gh pr list --search "author:app/jules"
# or
gh pr list --search "jules in:title"
```

---

## 4. Reviewing Jules Pull Requests

All code produced by Jules must undergo rigorous, adversarial peer review before merging. Follow the 3-pillar code review protocol defined in [`doc/ai/code-review.md`](code-review.md).

### A. Review Workflow

1. **Check CI & Status Checks**:
   ```bash
   gh pr checks <pr-number>
   ```
2. **Fetch and Inspect the PR Diff**:
   ```bash
   gh pr diff <pr-number>
   ```
3. **Check Out Locally for In-Depth Verification**:
   ```bash
   gh pr checkout <pr-number>
   ```
4. **Execute Monorepo Quality Gates**:
   ```bash
   make format
   make lint
   make test-coverage
   make check
   ```

### B. Adversarial Inspection Checklist

| Pillar | Inspection Focus | Red Flags / Common Jules Pitfalls |
|---|---|---|
| **Architectural Integrity** | Runtime boundaries, interface seams, no leaked host assumptions | Adding mock abstractions where real interfaces exist; introducing backward-compatibility wrappers for removed alpha features. |
| **Logic & Concurrency** | Context propagation, socket connection reuse, goroutine leaks | Detached contexts (`context.Background()`) in long-polling loops; broken RESP / JSON-RPC socket stream sync; race conditions on maps/slices. |
| **Security & Hygiene** | Zero secret leakage, proper file permissions (`0600`/`0700`), MIT headers | Passing tokens in CLI args (`-e TOKEN=foo` in `argv`); missing Boggy Creek copyright header; missing rootless Podman `unshare` cleanup in tests. |
| **Test Quality** | Meaningful behavioral assertions vs superficial coverage pads | Tests asserting only `err == nil` without checking return values; tests bypassing coverage gate with trivial mocks. |

### C. Review Verdicts & Actions

- **GO (Approve & Merge)**:
  - All invariants satisfied, tests pass with $\ge 90.0\%$ coverage, `make check` clean, zero security/architectural defects.
  - Approve PR:
    ```bash
    gh pr review <pr-number> --approve -b "Approved: architecture, logic, and security checks verified clean."
    ```
  - Merge PR using squash merge and delete the remote branch:
    ```bash
    gh pr merge <pr-number> --squash --delete-branch
    ```
- **CONDITIONAL GO (Minor Fixes Required)**:
  - Trivial issues (e.g. comment typo, missing test case, license header missing on a new test file).
  - Can be amended locally, pushed directly, and merged once green.
- **NO-GO (Changes Requested)**:
  - High or medium severity architectural, logical, or security defects.
  - Submit structured review feedback using the standard format with an executive summary table and detailed findings inside `<details>` tags:
    ```bash
    gh pr review <pr-number> --request-changes -b "$(cat review-notes.md)"
    ```

### D. Structured Feedback Format

When requesting changes on a PR or posting review findings, use a concise summary table followed by deep-dive `<details>` tags:

```markdown
## Adversarial Review: PR #<number>

**Assessment: [GO | CONDITIONAL GO | NO-GO]**

Summary sentence explaining the evaluation verdict, primary risks identified, or readiness for merge.

| # | Severity | Area | Finding |
|---|----------|------|---------|
| 1 | High     | `mcp/...` | Goroutine leak when JSON-RPC channel closes prematurely |
| 2 | Medium   | `pkg/...` | Missing rootless Podman cleanup hook in test |

<details>
<summary><b>1. [High] Goroutine leak on channel close</b> — <code>mcp/pkg/mcp/server.go</code></summary>

- **Defect**: The worker loop does not listen to `ctx.Done()` when reading request channel.
- **Impact**: In-container agent processes accumulate abandoned goroutines upon client disconnect.
- **Recommendation**: Add `select { case <-ctx.Done(): return; case req := <-ch: ... }`.

</details>

<details>
<summary><b>2. [Medium] Missing rootless Podman cleanup</b> — <code>cli/pkg/runtime/..._test.go</code></summary>

- **Defect**: `t.TempDir()` subuid files fail cleanup in rootless Podman.
- **Impact**: Leaves orphaned netns / volume files causing CI or developer machine errors.
- **Recommendation**: Register `t.Cleanup(func() { _ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run() })`.

</details>
```

---

## 5. Post-Merge Protocol

Once Jules' PR is merged into `main`:

1. **Close Linked GitHub Issue**:
   ```bash
   gh issue close <issue-number> --comment "Resolved by PR #<pr-number>"
   ```
2. **Reconcile Local Workspaces**:
   ```bash
   git checkout main
   git pull --rebase origin main
   ```
3. **Sync Beads Issue Tracking**:
   If the Jules task was tracked via a platform bead (`sndbx-*`):
   ```bash
   bd close <bead-id> --reason "Resolved in Jules PR #<pr-number>"
   bd sync
   ```
