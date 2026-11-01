# Fleet — Architecture & Subsystem Specifications

This directory contains technical specifications, architectural designs, and subsystem models governing the Fleet platform.

---

## Specifications Index

| Specification | Description | Skill Tag |
| :--- | :--- | :--- |
| **[Architecture & Runtime](architecture.md)** | Podman container runtime, security perimeter, layered OCI hierarchy, and LLM gateway. | `fleet-architecture` |
| **[Code Intelligence & AST Architecture](code-intelligence-spec.md)** | 3-Tier Code Intelligence Plugin API, Driver Registry, syntax pre-validation gate, and get_outline tool. | `fleet-code-intelligence-spec` |
| **[Memory Subsystem Specification](memory-subsystem-spec.md)** | BM25 lexical indexing, frontmatter schemas, and memory consolidation pipelines. | `fleet-memory-subsystem-spec` |
| **[Podman Pod Co-Location Architecture](podman-pod-colocation.md)** | Dedicated Podman pods per agent instance, localhost IPC, and network namespaces. | `fleet-podman-pod-colocation` |
| **[VS Code Extension Design](vscode-extension-design.md)** | Architecture and UX design guide for Fleet VS Code extension with Remote-SSH attach. | `fleet-vscode-extension-design` |

---

## Architectural Principles
1. **Container as the Primary Security Boundary**: Agents execute with full autonomy inside isolated Podman containers or confined baremetal `$HOME` perimeters without human approval prompts.
2. **Zero Host Credential Leakage**: Secrets and credentials are injected into runtime processes exclusively via environment variables or secret mounts—never on command line argument vectors (`argv`).
3. **Stateless Cross-Agent Messaging**: The Valkey backplane (`bp`) uses Ed25519-signed messages with canonical citations across isolated consumer streams.
