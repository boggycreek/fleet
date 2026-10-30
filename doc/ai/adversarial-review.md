---
name: fleet-adversarial-review
description: >-
  Guidelines and dual-agent protocol for adversarial code review, hypothesis generation,
  and critic verification to eliminate hallucinations and stress-test changes across
  architecture, devsecops, and pragmatic engineering perspectives.
---

# Adversarial Code Review, Hypothesis & Critic Protocol

This document defines the repeatable standard for adversarial code review within the Fleet platform. It implements a two-stage adversarial pipeline:
1. **Adversarial Reviewer**: Analyzes diffs through three critical lenses (Architecture, DevSecOps, Pragmatic Engineering) and formulates concrete, falsifiable hypotheses.
2. **Adversarial Critic**: Systematically attacks and stress-tests each hypothesis against the real codebase, compiler/runtime semantics, and container security boundaries to eliminate hallucinations and false positives.

---

## 1. Core Principles

- **No Unfalsifiable Feedback**: Vague feedback like "consider adding error handling" is prohibited. Every finding must be structured as an explicit hypothesis with a concrete trigger scenario and demonstrable impact.
- **Hypothesis-Critic Separation**: Reviewers hypothesize failure modes; Critics verify and challenge them. Separating generation from critique prevents confirmation bias and LLM hallucinations.
- **Actionable Pragmatism**: If an issue cannot be triggered under actual runtime constraints (rootless Podman, systemd cgroups, unprivileged agent UID 1000) or adds complexity with zero risk reduction, it must be discarded by the Critic.

---

## 2. The Tri-Perspective Inspection Matrix

When reviewing changes, the Reviewer inspects code across three orthogonal axes:

### A. Architectural Integrity
- **Boundary Discipline**: Code in `sandbox/` must not leak into `agent/` or `backplane/`. In-container daemons must operate autonomously without expecting host orchestration.
- **Monorepo Invariants**: Respect `go.work` multi-module declarations and Go module paths (`github.com/boggycreek/sandbox/...`).
- **Idempotency & Lifecycle**: State transitions (`create`, `start`, `stop`, `clean`, `retire`, `doctor`) must be resilient to partial failure and re-entrant.
- **Seams & Dependency Injection**: Avoid global mutable state, package-level function pointers, or unisolated environment variables that pollute parallel tests.

### B. DevSecOps & Runtime Boundaries
- **Container Perimeter**: All agent execution must occur inside rootless Podman containers. Host gateway exposure (`llm-gateway`) must only bind when necessary and never expose physical host admin sockets.
- **Zero Host Secret Leakage**:
  - Never pass tokens, passwords, or keys in process `argv` (e.g. `podman run -e KEY=val` exposes values in `/proc/<pid>/cmdline`). Always inject via environment files, stdin, or container secrets.
- **Filesystem Permissions**:
  - Sensitive files (`.env`, `signing-key.pem`, private keys, Valkey ACL files) must strictly enforce `0600` permissions.
  - SSH key directories and secrets directories must enforce `0700`.
- **Subuid/Subgid Cleanliness**: Tests using `t.TempDir()` with Podman must register `podman unshare rm -rf` cleanup hooks to prevent host permission errors.

### C. Pragmatic Engineering & Concurrency
- **Concurrency & Goroutines**:
  - Background workers must respect `context.Context` cancellation and terminate without leaking goroutines.
  - Channels must not deadlock on slow or missing consumers.
- **Socket & Stream Desynchronization**:
  - Network readers (Valkey RESP parser, HTTP clients) must never leave a shared connection in a partially read or corrupt state upon context timeout. Broken connections must be severed (`markBroken()`).
- **Hermetic Testing**:
  - Tests must never mutate live developer infrastructure (`fleet-valkey`, `fleet-forgejo`, host `~/.ssh/config`).
  - All test paths must reside within isolated `t.TempDir()` sandboxes.

---

## 3. Reviewer Protocol: Hypothesis Formulation

For every identified potential issue, the Reviewer must construct a structured hypothesis:

```markdown
### Hypothesis: [Concise Title]
- **Target File & Lines**: `path/to/file.go:L123-L145` (Symbol: `FunctionName`)
- **Perspective**: [Architecture | DevSecOps | Pragmatic Engineering]
- **Hypothesized Failure Mode**: Exactly what breaks or fails.
- **Trigger Scenario**: Step-by-step conditions required to provoke the failure.
- **Concrete Blast Radius / Risk**: Data corruption, credential leakage, deadlock, race condition, or test pollution.
```

---

## 4. Critic Protocol: Adversarial Verification

The Critic evaluates each hypothesis with aggressive skepticism to eliminate hallucinations:

### Verification Checklist
1. **Codebase Grounding**: Does the cited file and line number actually contain the referenced code? Does the referenced caller or parameter exist?
2. **Reachability**: Can the trigger condition actually occur in production or tests? Is the branch already unreachable due to earlier type checks, guards, or validations?
3. **Runtime Constraints**: Does rootless Podman, systemd cgroup isolation, or the Go runtime already prevent the hypothesized vulnerability?
4. **Pedantry vs. Pragmatism**: Does the proposed remediation introduce unnecessary abstraction or complexity for a purely theoretical non-issue?

### Verdict Taxonomy
- `CONFIRMED`: Verified defect with reproducible trigger and measurable impact. Requires remediation.
- `DISPROVED`: Factually inaccurate, hallucinated code/line references, or impossible trigger scenario. Rejected with counter-evidence.
- `PARTIALLY VALID`: The underlying concern has merit, but the scope or trigger scenario was mischaracterized. Calibrated and narrowed.

---

## 5. Review Output Format

The final consolidated report must be formatted as follows:

```markdown
## Adversarial Review & Critic Assessment

**Status: [APPROVED | CHANGES REQUESTED]**

### Summary of Hypotheses & Critic Verdicts

| # | Perspective | Target | Hypothesis | Critic Verdict | Action Required |
|---|---|---|---|---|---|
| 1 | DevSecOps | `pkg/plugin/plugin.go:340` | Secret file written with world-readable permissions | CONFIRMED | Enforce 0600 on created file |
| 2 | Pragmatic | `libbpd/daemon.go:120` | Goroutine leak on context cancel | DISPROVED | WaitGroup already tracked in defer |

### Detailed Evaluations

#### 1. [CONFIRMED] Secret file written with world-readable permissions
- **Hypothesis**: The temporary backup file in `writeSSHConfigFileAtomic` inherits default umask 0644 instead of 0600.
- **Critic Analysis**: Verified against `plugin.go` line 372. `os.CreateTemp` creates with 0600, but subsequent rename to `.bak` does not explicitly `Chmod`. Confirmed risk.
- **Remediation**: Call `os.Chmod(bakFile, 0600)` prior to atomic swap.

#### 2. [DISPROVED] Goroutine leak on context cancel
- **Hypothesis**: Long-polling loop does not terminate when parent context is canceled.
- **Critic Analysis**: Disproved. `daemon.go:145` explicitly selects on `ctx.Done()` in the tick runner and returns `ctx.Err()`, draining the worker pool cleanly.
- **Remediation**: None. Hypothesis dismissed.
```

---

## 6. Subagent Execution Template

When coordinating review subagents:

1. **Invoke Reviewer Subagent**:
   ```json
   {
     "TypeName": "adversarial-reviewer",
     "Role": "Adversarial Code Reviewer",
     "Prompt": "Perform adversarial review on the git diff between main and HEAD. Formulate explicit hypotheses for potential defects across Architecture, DevSecOps, and Pragmatic Engineering."
   }
   ```
2. **Invoke Critic Subagent with Reviewer Hypotheses**:
   ```json
   {
     "TypeName": "adversarial-critic",
     "Role": "Adversarial Review Critic",
     "Prompt": "Critically analyze each hypothesis formulated by the reviewer. Verify against the real codebase to confirm, disprove, or narrow each finding. Discard hallucinations."
   }
   ```
3. **Apply Confirmed Fixes**: Feed only `CONFIRMED` and calibrated `PARTIALLY VALID` remediations to the implementation subagent.
