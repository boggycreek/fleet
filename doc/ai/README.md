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
- **[NVIDIA OpenShell Evaluation](nvidia-openshell-evaluation.md)** (`fleet-openshell-evaluation`): Architectural evaluation of NVIDIA OpenShell runtime, Landlock/seccomp sandboxing, egress proxying, and Podman comparison.
- **[Distributed Inference Evaluation: exo](distributed-inference-exo.md)** (`fleet-exo-evaluation`): Technical evaluation of exo for local-network LLM inference behind Fleet's local model gateway.
- **[Thinking Machines Inkling Evaluation](thinking-machines-inkling-evaluation.md)** (`fleet-inkling-evaluation`): Architectural evaluation of Thinking Machines Inkling (975B/41B MoE) with controllable reasoning budget and Colibri NVMe streaming.
- **[Liquid Foundation Models Evaluation](liquid-ai-lfm-evaluation.md)** (`fleet-lfm-evaluation`): Architecture, serving engine compatibility, agentic tool calling, and local gateway recommendations for Liquid AI LFMs.
- **[Colibri MoE Inference Evaluation](colibri-moe-inference.md)** (`fleet-colibri-evaluation`): Architectural evaluation of Colibri for NVMe-streamed frontier MoE inference on Fleet developer workstations.
- **[Adversarial Review & Critic Protocol](adversarial-review.md)** (`fleet-adversarial-review`): Dual-agent adversarial review, hypothesis generation, and critic verification to eliminate hallucinations.
- **[Language-Specific Best Practices](language-best-practices.md)** (`fleet-language-best-practices`): Language-specific engineering best practices and conventions for Go, Python, TypeScript/Node.js, Shell/Bash, and Rust.
- **[OpenClaw Concurrency Evaluation](openclaw-concurrency-patterns.md)** (`fleet-openclaw-evaluation`): Comparative architectural analysis of OpenClaw cognitive/concurrency patterns against Fleet's native Go agent runtime (ADR 00041).
- **[Memory Subsystem Specification & Architecture](memory-subsystem-spec.md)** (`fleet-memory-subsystem-spec`): Standardizing agent cognitive memory across Fleet and Lokol platforms, 5 cognitive pillars, 3-tier progressive disclosure, MCP contracts, and sync strategy.

## Core Directives for Autonomous Agents
1. **Never commit directly to `main`**: Create a descriptive feature branch and submit a PR via `gh pr create`.
2. **Quality Gates are non-negotiable**: Run `make check` before proposing or submitting any code changes; statement test coverage must remain >= 90.0%.
3. **Preserve Security Perimeter**: Agents operate in rootless Podman containers; never bypass Podman or access host secrets.
4. **License & Copyright**: Always maintain the Boggy Creek Software LLC MIT copyright header in all source files.
