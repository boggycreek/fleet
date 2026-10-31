---
name: fleet-dev-workflow
description: >-
  Standard development, branching, PR protocols, and engineering quality gates for the fleet repository.
  Use when planning, contributing, committing code, or creating pull requests.
---

# Development Workflow & Pull Request Protocol

## Branching & Protection Rules
- `main` is protected against direct pushes and deletions.
- All code changes must go through a feature branch (`feat/...`, `fix/...`, `test/...`, `docs/...`).
- Changes must be merged via Pull Requests using `gh pr create` or GitHub UI.
- Never force-push to shared branches.

## Architectural Decision Records (ADRs)
- Significant design, CLI interface changes, or protocol modifications require an ADR in `doc/adr/`.
- Format follows `NNNNN-TitleInCamelCase.md` with sections: Context, Decision, Status, Consequences.
- Add new ADR entries to `doc/adr/README.md`.

## Task Tracking for Repository Contributors (`bd`)
Contributors and agents developing the `fleet` platform track work using **Beads (`bd`)**:
- **Query Ready Work**: `bd ready` surfaces tasks with zero blocking dependencies.
- **Inspect Backlog**: `bd list` shows hierarchical epics and subtasks.
- **Claim Work**: `bd update <id> --claim` claims an issue.
- **Record & Close**: `bd close <id> --reason "Resolved in <commit>"` closes finished work.
- **Synchronize Remote**: `bd sync` reconciles local Dolt issue commits with `origin` on GitHub.

> **Isolation Rule**: Platform issues use prefix `sndbx-*` on GitHub. This is completely separate from in-container fleet task tracking (which uses local Forgejo at `http://forgejo:3000/fleet/tasks.git` per ADR 00028).

## Quality Gates Checklist Before PR
Every branch must satisfy all quality gates before submission:
1. `make format`: Auto-format all Go source files.
2. `make lint`: Check Go files (`golangci-lint`) and shell scripts (`shellcheck`).
3. `make test-coverage`: Verify strict **>= 90.0% statement coverage** across all packages.
4. `make check`: Run full quality gate suite (lint + test-coverage + sca).
5. All new files must include the standard Boggy Creek Software LLC MIT copyright header.
