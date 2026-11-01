# Fleet — Security Audits & Quality Findings

This directory contains security audit reports, vulnerability assessments, static analysis findings, and protocol hardening records across Fleet subsystems.

---

## Audits & Findings Index

| Audit | Target Subsystem | Scope & Key Findings |
| :--- | :--- | :--- |
| **[Architecture Review & Boundary Audit](architecture-review-findings.md)** | Entire Fleet Platform | Container vs host boundary integrity, module decoupling (`mcp/` -> `cli/`), Podman pod co-location (ADR 00053), two-plane task tracking isolation (Dolt `sndbx-*` vs Forgejo `fleet/tasks.git`), and wire protocol conformance. |
| **[Code Hygiene & Complexity Review](code-hygiene-and-complexity-review.md)** | Full Codebase | Full-codebase audit on pragmatism, dead code reachability, concurrency safety, and token budget efficiency. |
| **[Adversarial Review Findings](adversarial-review.md)** | Pull Requests & Core | Historical adversarial review findings, attack vectors, and vulnerability remediation records. |
| **[Backplane Audit Findings](backplane-audit-findings.md)** | `backplane/` | Cryptographic signature validation (Ed25519), RESP stream parser resilience, and concurrency hardening. |
| **[MCP Suite Audit Findings](mcp-suite-audit-findings.md)** | `mcp/` | Input sanitization, path traversal perimeter verification, and JSON-RPC 2.0 protocol compliance. |
| **[DevSecOps & Isolation Audit](devsecops-audit-findings.md)** | Full Platform & Runtime | Container perimeter, rootless Podman execution, zero secret leakage, Valkey ACLs, cryptographic integrity, and supply chain review. |
| **[Shell & CI Audit Findings](shell-and-ci-audit-findings.md)** | Scripts & Workflows | Shellcheck compliance, temporary file creation safety, and GitHub Actions workflow permissions. |

---

## Audit Protocol
When completing a security audit or subsystem review:
- Record findings here in `doc/ai/audits/<target>-audit-findings.md`.
- Register an entry in this index table with severity breakdown and remediation status.
