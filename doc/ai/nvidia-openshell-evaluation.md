<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->
---
name: fleet-openshell-evaluation
description: >-
  Architectural evaluation of NVIDIA OpenShell runtime, sandboxing patterns, kernel-enforced isolation,
  credential proxying, and policy verification in comparison with Fleet's rootless Podman architecture.
---

# NVIDIA OpenShell Evaluation & Fleet Integration Analysis

## Executive Summary
This document presents an architectural evaluation of **NVIDIA OpenShell** (part of NVIDIA's Open Agent Safety Platform) and evaluates its sandboxing patterns, kernel-enforced isolation primitives, egress network proxying, dynamic credential masking, and formal policy verification against **Fleet's rootless Podman architecture**.

Fleet currently employs a multi-tenant rootless Podman runtime with dedicated user namespaces (subuid/subgid mapping), network namespace isolation, host directory projection, and local LLM/Backplane credential management. This evaluation investigates whether OpenShell components should be adopted, integrated as complementary security layers, or if Fleet's existing architecture remains the optimal design choice.

---

## 1. Core OpenShell Architecture

NVIDIA OpenShell is designed as an agent runtime and sandboxing framework tailored for autonomous AI agent execution. Its security and isolation architecture rests on four core pillars:

### 1.1 Kernel-Enforced Sandbox Isolation
- **Linux Landlock LSM (Linux Security Module)**: Landlock enables unprivileged processes to restrict their own file path access (read, write, execute, directory traversal) directly at the kernel VFS layer without requiring root privileges or capabilities like `CAP_SYS_ADMIN`. OpenShell applies fine-grained Landlock rulesets to restrict an agent process to pre-approved workspace paths.
- **Seccomp BPF Syscall Filters**: OpenShell configures Berkeley Packet Filter (BPF) programs to restrict system calls available to the agent process tree. Dangerous syscalls (e.g., `ptrace`, `kexec_load`, `process_vm_writev`, `unshare`, custom module loading) are blocked or intercepted, minimizing kernel attack surface and preventing sandbox escape or privilege escalation attempts.

### 1.2 Network Egress Proxy
- **Interception Mechanism**: Outbound network traffic initiated by agent processes is redirected through a local HTTP/SOCKS/eBPF egress proxy.
- **Declarative Policies**: Network policies define domain allowlists/denylists, HTTP method restrictions, and endpoint rate limits.
- **Traffic Inspection & Interception**: The proxy intercepts connection attempts, validating target IP addresses and SNI hostnames before forwarding requests. Unapproved egress traffic is dropped with granular policy audit logs.

### 1.3 Dynamic Credential Masking
- **Token Redaction & Replacement**: Agents are intentionally **not** provided with raw API secrets, OAuth tokens, or database credentials in environment variables or configuration files.
- **On-the-Fly Injection**: When an agent sends an outbound API request to an authorized destination (e.g., an LLM provider or remote service), the OpenShell egress proxy inspects the request headers and injects the actual API bearer token or credential header prior to egress.
- **Zero-Exposure Agent Boundary**: The agent process strictly sees placeholder tokens (e.g., `OPENAI_API_KEY=openshell-proxy-managed`), preventing credential leakage via process memory dumps, prompt injection attacks, or accidental stdout/stderr logging.

### 1.4 Formal Policy Prover
- **Mathematical Policy Verification**: OpenShell incorporates formal verification models (e.g., SMT solvers or logic provers) to verify security policy modifications.
- **Safety Assurance**: Before dynamic runtime policy updates are applied (such as loosening network access for a specific domain), the policy prover checks for policy collisions, unintended permission expansion, or violation of invariant safety invariants (e.g., ensuring secrets endpoint access remains strictly denied).

---

## 2. Comparison Matrix: OpenShell vs. Fleet

| Dimension | NVIDIA OpenShell | Fleet (Rootless Podman Architecture) | Evaluation & Trade-offs |
| :--- | :--- | :--- | :--- |
| **Security Perimeter** | Process-level isolation via **Linux Landlock** path restrictions & **seccomp BPF** syscall filters in the host namespace. | **Rootless OCI Container boundary**: Podman user namespaces (subuid/subgid UID 1000 mapping), cgroups v2, pivot_root mount namespaces, rootless netns. | Podman provides standard container multi-tenant isolation and complete rootless namespace isolation. Landlock is kernel process-level; inside a container, process isolation is redundant unless sandboxing sub-processes. |
| **Credential Management** | **Egress Proxy Injection**: Agents use dummy tokens; egress proxy injects real credentials dynamically based on target URL allowlists. | **Zero-leakage host secrets & local gateways**: Host-bridged LLM Gateway (`llm-gateway:host-gateway`), Valkey pub/sub, Ed25519 backplane message signing, and local Forgejo/Dolt auth. | OpenShell proxy injection prevents token theft via prompt injection. Fleet achieves similar protection for LLM calls via local LLM gateway proxy without raw keys in containers. |
| **Network Egress Isolation** | Application-layer egress proxy intercepting HTTP/HTTPS requests with declarative URL policies. | Container-level `slirp4netns`/passt network namespace isolation, default-deny network egress filtering (ADR 00014), host routing rules. | Fleet blocks network access at the network interface layer (Layer 3/4). OpenShell provides inspectable Layer 7 policy proxying. |
| **Developer & Tooling Usability** | Native process execution. Can conflict with complex developer environments (compilers, git, external toolchains) if Landlock rules are overly restrictive. | **Full OCI Developer Suite**: IDE Remote-SSH daemon (port 2222), tmux, Deno/Node, Go multi-version toolchains, SSH/git synchronization (ADR 00016, 00024, 00034). | Fleet's OCI container approach delivers a full Linux development environment required by interactive IDEs (VS Code, JetBrains Gateway) and language runtimes. |
| **Policy Enforcement & Verification** | Formal mathematical policy prover for dynamic rule verification before applying policy state changes. | Declarative JSON/YAML configuration, deterministic `fleet agent doctor` diagnostics, and statically validated agent profiles. | Formal policy proving adds mathematical rigor for highly dynamic rule updates, but increases complexity for standard workspace operations. |

---

## 3. Integration Scenarios for Fleet

We evaluate three potential integration scenarios for OpenShell within Fleet's architecture:

### Scenario A: OpenShell as an In-Container Jail / Sub-Sandbox
- **Concept**: Run OpenShell (Landlock + seccomp) *inside* Fleet's rootless Podman containers to sandbox untrusted third-party agent sub-processes, code execution tools, or shell commands invoked by the agent.
- **Feasibility**:
  - Unprivileged Landlock requires kernel support (Linux 5.13+), which is available on modern hosts and propagates inside rootless user namespaces without requiring root inside the container.
  - Seccomp filters can be nested inside containers if allowed by the outer Podman container seccomp profile.
- **Value**: High for multi-agent workflows or scenarios where an agent executes arbitrary, unvetted user code inside the container (e.g., Python code interpreter or bash sub-shell) without risking the container's internal services (`bpd`, `sshd`, local git repo).

### Scenario B: OpenShell Egress Proxy Integrated into Fleet Network Boundary
- **Concept**: Embed OpenShell's egress proxy into Fleet's `fleet-infra` network bridge or as an in-container sidecar to inspect outbound LLM and web requests from agents.
- **Feasibility**:
  - Fleet already routes LLM traffic through a host-bridged local gateway (`llm-gateway:host-gateway`, ADR 00022/00035).
  - Integrating OpenShell's dynamic credential masking and dynamic URL filtering into Fleet's local proxy layer would allow fine-grained domain-level policy enforcement beyond IP/port default-deny rules (ADR 00014).
- **Value**: High for external web browsing or third-party API integration by agent harnesses (e.g., OpenCode, Claude Code) while keeping raw host API keys masked.

### Scenario C: OpenShell as an Alternative Standalone Container Runtime
- **Concept**: Replace Podman entirely with OpenShell's native Linux process sandboxing as Fleet's core container/agent execution engine.
- **Feasibility & Trade-offs**:
  - OpenShell operates at the process level rather than providing full OCI container filesystem, layered image hierarchy (ADR 00013, 00043), or multi-version toolchains (Go, Deno, Node).
  - Removing Podman would break Fleet's core capabilities: persistent home volumes (`fleet-<name>-home`), in-container SSH daemon / JetBrains / VS Code Remote development (ADR 00016, 00020, 00024), and rootless user namespace isolation.
- **Value**: Low. Replacing Podman would degrade system security perimeters and interactive developer tooling compatibility.

---

## 4. Strategic Recommendation & Verdict

### Verdict: **Integrate Patterns & Selected Components**
Do **not** replace Fleet's rootless Podman container architecture with OpenShell as a standalone runtime. Instead, **maintain Podman as the primary container security perimeter and selectively integrate OpenShell's sandboxing and proxying patterns.**

### Strategic Action Plan

1. **Maintain Rootless Podman as Primary Perimeter**:
   - Keep Podman user namespaces, rootless mount/netns isolation, and layered OCI hierarchy as Fleet's primary isolation boundary (ADR 00003, 00004, 00019). This guarantees full OCI toolchain compatibility, multi-tenant isolation, and support for IDE Remote-SSH workflows.

2. **Adopt In-Container Landlock Sub-Sandboxing (Pattern from Scenario A)**:
   - For agent harnesses executing untrusted bash/python scripts (e.g., `pig` agent runner or code execution plugins), evaluate applying process-level Landlock restrictions to prevent untrusted subprocesses from accessing sensitive container paths (such as `~/.ssh/`, backplane socket keys, or `bpd` entrypoint binaries).

3. **Incorporate Dynamic Credential Proxying in Fleet LLM Gateway (Pattern from Scenario B)**:
   - Enhance Fleet's local LLM Gateway (`pkg/llmproxy` / ADR 00022, 00035) with OpenShell-style credential injection and egress domain masking.
   - Inject host credentials at the gateway boundary, ensuring agent containers receive dummy tokens for all external LLM/API endpoints.

4. **Policy Prover Principles for Fleet Task Policy Changes**:
   - Adopt declarative schema validation and dynamic policy checking within `fleet agent doctor` and `bpd` when modifying network egress allowlists or agent capability profiles.
