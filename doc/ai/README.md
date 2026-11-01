# Fleet — AI Agent Knowledge Base

This directory provides progressive disclosure of architectural principles, engineering constraints, specifications, and operational runbooks for this repository. It is optimized for consumption by autonomous coding agents and human contributors.

---

## Knowledge Domains & Entry Points

To maintain clean separation between active engineering instructions and research or audit logs, all documentation is organized into four purpose-oriented directories, each with its own self-indexing entry point:

```
doc/ai/
├── README.md               # Master AI Knowledge Index and Navigation Guide (This file)
├── guidelines/README.md    # Operational, workflow, testing, and code review standards
├── specs/README.md         # Architecture and subsystem design specifications
├── evaluations/README.md   # Architectural spikes, model benchmarks, and hardware evaluations
└── audits/README.md        # Security audits, protocol verifications, and findings
```

### 1. [Operational Guidelines](guidelines/README.md) (`doc/ai/guidelines/`)
Mandatory protocols, engineering standards, and quality gates that all contributors and autonomous coding agents must follow:
- **[Development Workflow & PR Protocol](guidelines/dev-workflow.md)** (`fleet-dev-workflow`)
- **[Code Review Protocol & 2-Agent PR Separation](guidelines/code-review.md)** (`fleet-code-review`)
- **[Testing Guidelines & Coverage Gates](guidelines/testing-guidelines.md)** (`fleet-testing-guidelines`)
- **[Diagnostic Doctor & Auto-Healing](guidelines/doctor-diagnostics.md)** (`fleet-doctor-diagnostics`)
- **[OCI Image Resolution & Tagging](guidelines/image-resolution.md)** (`fleet-image-resolution`)
- **[Language Best Practices](guidelines/language-best-practices.md)** (`fleet-language-best-practices`)
- **[Jules Collaboration Protocol](guidelines/jules-collaboration.md)** (`fleet-jules-collaboration`)

### 2. [Architecture & Subsystem Specifications](specs/README.md) (`doc/ai/specs/`)
Structural blueprints, runtime constraints, and subsystem designs governing the Fleet platform:
- **[Architecture & Runtime](specs/architecture.md)** (`fleet-architecture`)
- **[Memory Subsystem Specification](specs/memory-subsystem-spec.md)** (`fleet-memory-subsystem-spec`)
- **[Podman Pod Co-Location Architecture](specs/podman-pod-colocation.md)** (`fleet-podman-pod-colocation`)
- **[VS Code Extension Design](specs/vscode-extension-design.md)** (`fleet-vscode-extension-design`)

### 3. [Hardware & Inference Evaluations](evaluations/README.md) (`doc/ai/evaluations/`)
Empirical research spikes, benchmarks, and exploratory evaluations covering local inference engines, models, topologies, and networking:
- **[Colibri MoE Inference Evaluation](evaluations/colibri-moe-inference.md)**
- **[Distributed Inference with Exo](evaluations/distributed-inference-exo.md)**
- **[FreeBSD Host Support & Runtime Porting Guide](evaluations/freebsd-host-support.md)**
- **[Liquid AI LFM Model Evaluation](evaluations/liquid-ai-lfm-evaluation.md)**
- **[Multi-Machine Local Network Federation & Gateway DNS](evaluations/multi-machine-network-federation.md)**
- **[NVIDIA OpenShell Evaluation](evaluations/nvidia-openshell-evaluation.md)**
- **[Valkey Stream Guard Evaluation](evaluations/valkey-stream-guard-evaluation.md)**

### 4. [Security Audits & Quality Findings](audits/README.md) (`doc/ai/audits/`)
Adversarial security audits, vulnerability assessments, and protocol hardening records across Fleet subsystems:
- **[Adversarial Review Findings](audits/adversarial-review.md)**
- **[Backplane Audit Findings](audits/backplane-audit-findings.md)**
- **[MCP Suite Audit Findings & Security Hardening](audits/mcp-suite-audit-findings.md)**
- **[Shell and CI Audit Findings](audits/shell-and-ci-audit-findings.md)**

---

## Core Directives for Autonomous Agents

1. **Strict 2-Agent PR Separation**: An agent authoring or committing code for a PR **must never** review or approve its own PR. An independent `adversarial-reviewer` subagent must be spawned to audit the diff and post the review comment.
2. **Never commit directly to `main`**: Create a descriptive feature branch and submit a PR via `gh pr create`.
3. **Quality Gates are non-negotiable**: Run `make check` before proposing or submitting any code changes; monorepo statement test coverage must remain $\ge 90.0\%$.
4. **Preserve Security Perimeter**: Agents operate in rootless Podman containers or confined baremetal `$HOME` perimeters; never bypass boundaries or access host secrets.
5. **Documentation Taxonomy Integrity**: Never write evaluation spikes, benchmark dumps, or audit logs into the `doc/ai/` root or `doc/ai/guidelines/`. Always route them to their respective child directories and update the local child index.
6. **License & Copyright**: Always maintain the Boggy Creek Software LLC MIT copyright header in all source files.
