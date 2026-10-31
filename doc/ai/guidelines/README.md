# Fleet — Operational & Engineering Guidelines

This directory contains mandatory engineering standards, protocols, and workflows that all contributors and autonomous coding agents must follow.

---

## Guidelines Index

| Guide | Description | Skill Tag |
| :--- | :--- | :--- |
| **[Development Workflow & PR Protocol](dev-workflow.md)** | Branching rules, conventional commits, quality gates checklist, and PR submission. | `fleet-dev-workflow` |
| **[Code Review Protocol](code-review.md)** | 3-pillar inspection checklist and mandatory 2-agent PR separation protocol. | `fleet-code-review` |
| **[Testing Guidelines & Coverage Gates](testing-guidelines.md)** | Strict $\ge 90.0\%$ test coverage enforcement, rootless Podman cleanup patterns. | `fleet-testing-guidelines` |
| **[Diagnostic Doctor & Auto-Healing](doctor-diagnostics.md)** | `fleet agent doctor` and `fleet infra doctor` diagnostic checks and self-healing. | `fleet-doctor-diagnostics` |
| **[OCI Image Resolution & Tagging](image-resolution.md)** | 3-tier image resolution, well-known presets, local store discovery, remote OCI refs. | `fleet-image-resolution` |
| **[Language Best Practices](language-best-practices.md)** | Idiomatic practices, linters, and conventions for Go, Shell, YAML, and Dockerfiles. | `fleet-language-best-practices` |
| **[Jules Collaboration Protocol](jules-collaboration.md)** | Delegating tasks via GitHub issues, tracking in-flight work, and conducting PR reviews. | `fleet-jules-collaboration` |

---

## Core Operational Directives

1. **Strict 2-Agent PR Separation**: The agent that authors or commits code for a PR **must never** review or approve its own PR. An independent `adversarial-reviewer` subagent must be spawned to audit the diff and post the review comment.
2. **Never Commit Directly to `main`**: Always create a feature branch (`feat/`, `fix/`, `docs/`, `chore/`) and submit a PR via `gh pr create`.
3. **Quality Gates are Non-Negotiable**: Run `make check` before proposing or submitting any code changes; statement test coverage must remain $\ge 90.0\%$ across all packages.
4. **Preserve Container Boundary**: Agents operate in rootless Podman containers or confined baremetal `$HOME` perimeters; never bypass boundaries or access host secrets.
