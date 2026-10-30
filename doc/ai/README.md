# Fleet — AI Agent Knowledge Base

This directory provides progressive disclosure of architectural principles, engineering constraints, and operational runbooks for this repository. It is designed for optimal consumption by autonomous coding agents and doubles as agent skills with standard YAML frontmatter.

## Knowledge Index & Skills
- **[Development Workflow & PR Protocol](dev-workflow.md)** (`fleet-dev-workflow`): Branching rules, PR requirements, conventional commits, quality gates checklist.
- **[Architecture & Runtime](architecture.md)** (`fleet-architecture`): Podman container runtime, security perimeter, layered OCI hierarchy, local LLM gateway.
- **[OCI Image Resolution & Tagging](image-resolution.md)** (`fleet-image-resolution`): 3-tier image resolution, well-known presets, local store discovery, remote OCI refs (ADR 00029).
- **[Testing Guidelines & Coverage Gates](testing-guidelines.md)** (`fleet-testing-guidelines`): Strict >=90% test coverage enforcement, rootless Podman cleanup patterns, containerized smoke testing.
- **[Diagnostic Doctor & Auto-Healing](doctor-diagnostics.md)** (`fleet-doctor-diagnostics`): `fleet agent doctor` and `fleet infra doctor` architecture, check categories, healing behavior.
- **[Code Review Protocol](code-review.md)** (`fleet-code-review`): Protocol, inspection checklist, and reporting format for architectural, logical, and security PR reviews.
- **[Shell & CI Security Audit Findings](shell-and-ci-audit-findings.md)** (`fleet-shell-and-ci-audit-findings`): Targeted security, error handling, and portability audit findings for Fleet's shell scripts, installer, setup, and GitHub Actions CI toolchains.

## Core Directives for Autonomous Agents
1. **Never commit directly to `main`**: Create a descriptive feature branch and submit a PR via `gh pr create`.
2. **Quality Gates are non-negotiable**: Run `make check` before proposing or submitting any code changes; statement test coverage must remain >= 90.0%.
3. **Preserve Security Perimeter**: Agents operate in rootless Podman containers; never bypass Podman or access host secrets.
4. **License & Copyright**: Always maintain the Boggy Creek Software LLC MIT copyright header in all source files.
