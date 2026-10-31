# Fleet — Security Audits & Quality Findings

This directory contains security audit reports, vulnerability assessments, static analysis findings, and protocol hardening records across Fleet subsystems.

---

## Audits & Findings Index

| Audit | Target Subsystem | Scope & Key Findings |
| :--- | :--- | :--- |
| **[Adversarial Review Findings](adversarial-review.md)** | Pull Requests & Core | Historical adversarial review findings, attack vectors, and vulnerability remediation records. |
| **[Backplane Audit Findings](backplane-audit-findings.md)** | `backplane/` | Cryptographic signature validation (Ed25519), RESP stream parser resilience, and concurrency hardening. |
| **[MCP Suite Audit Findings](mcp-suite-audit-findings.md)** | `mcp/` | Input sanitization, path traversal perimeter verification, and JSON-RPC 2.0 protocol compliance. |
| **[Shell & CI Audit Findings](shell-and-ci-audit-findings.md)** | Scripts & Workflows | Shellcheck compliance, temporary file creation safety, and GitHub Actions workflow permissions. |

---

## Audit Protocol
When completing a security audit or subsystem review:
- Record findings here in `doc/ai/audits/<target>-audit-findings.md`.
- Register an entry in this index table with severity breakdown and remediation status.
