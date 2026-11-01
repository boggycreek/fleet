---
name: fleet-devsecops-audit-findings
description: >-
  Comprehensive DevSecOps, container sandbox perimeter, secret hygiene, cryptographic
  integrity, process lifecycle, and supply chain security audit findings across the
  Fleet platform.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Full-Codebase DevSecOps, Container Isolation, Secret Hygiene, and Supply Chain Security Review

## Executive Summary

A comprehensive, defense-in-depth DevSecOps, container isolation, secret hygiene, and supply chain audit was performed across the entire Fleet codebase. Fleet implements an autonomous AI coding agent platform that combines rootless Podman execution, private backplane messaging (Valkey Streams), local/frontier inference mediation, and developer workstation IDE integration.

The audit systematically evaluated five core security domains:
1. **Container Sandbox Perimeter & Rootless Podman**: Unprivileged container user bounds (enforced agent `UID 1000` / `GID 1000`), rootless user namespace mappings (`podman unshare`, `keep-id`), SELinux labeling (`:z`/`:Z`), file access boundaries, and network namespace isolation (`fleet-infra` bridge network, default-deny egress policies).
2. **Secret Hygiene & Zero Host Leakage**: Elimination of sensitive credentials in process argument lists (`/proc/<pid>/cmdline`), isolated reverse proxy token injection for OpenRouter (`fleet-openrouter`, ADR 00052), local model gateway routing (`llm-gateway`), and strict Valkey stream ACL partitioning (`~<name>:*` isolation and `~*:inbox` restricted append permissions).
3. **Cryptographic Integrity & File Permissions**: Ed25519 payload signing and public key registry verification in `backplane/pkg/libbp/`, constant-time enrollment token verification, and restrictive umasking (`0700`/`0600`) across private keys, SSH configurations, and runtime state.
4. **Process Lifecycle & Supervision**: Zombie process reaping, signal propagation, `--init` init process configuration, background supervisor daemon (`bpd`) lifecycles, and health checks.
5. **Software Supply Chain & Build Hygiene**: Dockerfile base image pinning, minimal build surfaces, Multi-stage OCI layer composition (`fleet-artifacts`, `fleet-native-engine`), and GitHub Actions release workflows with SBOM generation.

Overall Security Posture: **STRONG**. Fleet enforces zero-credential exposure in command-line arguments, strict rootless Podman execution, non-root in-container privileges, and fine-grained Valkey ACL permissions. Targeted hardening was applied to resolve legacy module references in Dockerfiles and eliminate non-portable utility flags.

---

## Audited Scope & Findings Summary

| Component | Category | Severity | Finding | Hardening Implemented / Recommended |
| :--- | :--- | :--- | :--- | :--- |
| `images/**/Dockerfile` | Supply Chain | High | Legacy `sandbox/` module path references broke container builds after ADR 00059 rename to `cli/`. | Remediated: Updated `openrouter-proxy`, `fleet-artifacts`, and `fleet-native-engine` Dockerfiles to reference `cli/`. |
| `cli/pkg/runtime/podman.go` | Secret Hygiene | Informational | Password and token parameters passed via `cmd.Env` and stdin, never in `argv`. | Verified: All sensitive env vars (`BP_PASSWORD`, `BP_SIGNING_KEY_PEM`, `VALKEY_PASSWORD`, `SONAR_TOKEN`) passed securely via environment injection. |
| `cli/pkg/runtime/podman.go` | Container Perimeter | Medium | Agent containers execute without `--init` flag, relying on entrypoint scripts to manage sub-processes. | Recommended: Explicitly pass `--init` to Podman run invocations to ensure reliable zombie reaping under heavy agent child process spawning. |
| `install-agent.sh` | Privilege / Files | Medium | Temporary enrollment script materialized prior to restrictive umask. | Recommended / Verified: Privilege drop to target user enforced; secure umask 077 used for secrets creation; credentials passed over stdin pipe. |
| `scripts/clean-test-env.sh` | Portability / Ops | Low | Non-portable `xargs -r` flag in pod removal causing potential failures on macOS/BSD. | Remediated: Removed `-r` flag since empty inputs are already guarded by non-empty shell checks. |
| `backplane/pkg/libbp/` | Cryptographic Integrity | Low | Ed25519 payload signing verifies fixed key and signature byte lengths; constant-time token comparison implemented in enrollment. | Verified: Correct use of `crypto/ed25519` and `crypto/subtle.ConstantTimeCompare`. |
| `cli/pkg/devcontainer/` | Container Perimeter | Low | DevContainer screening engine strips dangerous capabilities (`SYS_ADMIN`, `NET_ADMIN`) and prohibits host binds. | Verified: Comprehensive screening prevents host escape through `.devcontainer/devcontainer.json`. |
| `.github/workflows/` | CI/CD Supply Chain | Low | Release workflow generates SPDX and CycloneDX SBOMs and verifies SHA256 hashes. | Verified: Pinned action versions (`checkout@v4`, `setup-go@v5`, `upload-artifact@v4`) and deterministic checksum generation. |

---

## Deep-Dive Threat Models & Analysis

### 1. Container Sandbox Perimeter & Rootless Podman Execution

#### Threat Model
An autonomous agent running inside a container executes untrusted code or installs third-party dependencies from public repositories (npm, PyPI, Go modules). If the container executes with host root privileges or insecure volume mounts, malicious code could escape the sandbox, modify host binaries, or snoop on host processes.

#### Controls & Architecture
- **Rootless Podman Runtime**: Agents execute entirely within rootless user namespaces without a persistent root daemon (ADR 00003). Root in the container maps to unprivileged host UID via subuid/subgid ranges.
- **Unprivileged User Bounds**: The container default user is `agent` (UID 1000, GID 1000) (ADR 00004). Dropped Linux capabilities ensure `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`, and `CAP_SYS_RAWIO` are inaccessible.
- **SELinux Mount Labeling (`:z` / `:Z`)**: Persistent volumes and workspace projections use `:z` shared labeling (ADR 00044). The DevContainer screening engine (`cli/pkg/devcontainer/sanitize.go`) strips binds to sensitive host directories (`/`, `/etc`, `/var/run`, `/root`, `docker.sock`, `.ssh`).
- **Network Isolation**: Agent containers communicate over the isolated `fleet-infra` bridge network. Egress filtering sidecars (`images/egress-filter`) enforce default-deny iptables rules with dynamic DNS allowlisting via dnsmasq and ipset (ADR 00014).

#### Recommendation
Add `--init` to agent container execution arguments in `cli/pkg/runtime/podman.go`. When autonomous agents fork numerous compiler, linter, or test processes, an explicit init daemon ensures immediate zombie process reaping and clean signal propagation upon container termination.

---

### 2. Secret Hygiene & Zero Host Leakage

#### Threat Model
Exposure of cloud API keys (e.g., OpenRouter API key) or internal service credentials across process argument lists (`ps aux`, `/proc/<pid>/cmdline`), Git history, or container filesystems.

#### Controls & Architecture
- **argv Secret Elimination**: The Fleet runtime ensures zero credentials appear in CLI arguments. In `cli/pkg/runtime/podman.go`, sensitive keys (`BP_PASSWORD`, `VALKEY_PASSWORD`, `BP_SIGNING_KEY_PEM`, `SONAR_TOKEN`, `OPENAI_API_KEY`) are passed via `cmd.Env` or stdin pipes (e.g., `createAdminCmd.Stdin = strings.NewReader(adminPass + "\n")`).
- **Zero-Leakage OpenRouter Proxy (ADR 00052)**:
  - The master `OPENROUTER_API_KEY` is retained on the host at `secrets/openrouter.key` (mode 0600) or host environment variable.
  - The key is injected exclusively into the unprivileged `fleet-openrouter` proxy container.
  - Agent containers point to `http://fleet-openrouter:8080/v1` and supply dummy authorization tokens. The proxy injects the real bearer token upstream, ensuring agent code has zero access to raw cloud credentials.
- **Valkey Stream ACL Partitioning (ADR 00010, ADR 00042)**:
  - Default user is disabled (`user default off`).
  - Human operator and admin accounts use high-entropy passwords.
  - Agent ACLs enforce least privilege:
    - Full read/write to agent-owned streams: `~<agent>:*`
    - Read-only access to public broadcasts and registry: `%R~*:out`, `%R~identity:*`, `%R~human:*`, `%R~*:blob:*`, `%R~specialty:*`
    - Restricted write-only append access to peer mailboxes: `(+xadd ~*:inbox)`
    - Stripped administrative permissions: `-@admin`, `-@dangerous`

---

### 3. Cryptographic Integrity & File Permissions

#### Threat Model
Message spoofing, replay attacks, or unauthorized manipulation of backplane communications; file permission drift allowing unauthorized local users to read private keys or session configurations.

#### Controls & Architecture
- **Ed25519 Payload Signing**: All messages dispatched across the Valkey backplane are cryptographically signed using Ed25519 (`backplane/pkg/libbp/signing.go`). Public keys are registered in Valkey under `identity:<name>` and validated before processing payload commands (ADR 00011, ADR 00042).
- **Constant-Time Enrollment Verification**: Remote node enrollment in `cli/pkg/lifecycle/enrollment.go` verifies one-time tokens using `subtle.ConstantTimeCompare` to mitigate timing attacks.
- **Restrictive Umasking & Permissions**:
  - Private signing keys (`signing-key.pem`) and Valkey ACL files are created with explicit `0600` permissions.
  - Configuration directories (`~/.config/fleet`, `~/.local/share/fleet/secrets`) enforce `0700`.
  - Shell scripts (`install.sh`, `setup.sh`) wrap secret creation in `(umask 077 ...)` subshells to prevent exposure windows before chmod is invoked.

---

### 4. Process Lifecycle & Supervision

#### Threat Model
Unchecked child process spawning inside autonomous containers leading to process table exhaustion (PID exhaustion) or orphaned background processes leaking memory.

#### Controls & Architecture
- **Dual-Lobe Architecture (ADR 00040)**: Fast-lobe IDE interaction runs in foreground while deep-lobe background tasks operate under tmux sessions (`agent-worker-loop`).
- **Signal Handling & Supervision**: In-container backplane daemon (`bpd`) traps termination signals and flushes active subscriptions. `fleet-agent` and proxy containers use `signal.NotifyContext` with graceful HTTP server shutdowns (`srv.Shutdown`).

---

### 5. Supply Chain & Build Security

#### Threat Model
Compromised upstream base images, outdated Go dependencies, malicious GitHub Actions, or tampering of prebuilt binary releases.

#### Controls & Architecture
- **Zero External Go Dependencies**: Native Go binaries and libraries in `cli/`, `backplane/`, `agent/`, and `mcp/` rely solely on the Go standard library, completely eliminating third-party Go module supply chain vulnerabilities.
- **Release Verification & SBOMs**:
  - The GitHub Actions release workflow (`.github/workflows/release.yml`) generates Software Bill of Materials (SBOM) in SPDX and CycloneDX formats using Anchore Syft.
  - Binary releases generate SHA256 checksums (`checksums.txt`), which are cryptographically verified by `install.sh` prior to host execution.
- **Dockerfile Hardening**: Multi-stage Docker builds isolate compilation from runtime. Container images (`images/fleet-artifacts`, `images/fleet-native-engine`) use `FROM scratch` base layers containing only statically compiled binaries.

---

## Actionable Remediations & Status

1. **[RESOLVED] Dockerfile Module Paths**: Updated `images/infra/openrouter-proxy/Dockerfile`, `images/fleet-artifacts/Dockerfile`, and `images/fleet-native-engine/Dockerfile` to point to `cli/` instead of `sandbox/`.
2. **[RESOLVED] macOS Portability in Cleanup**: Removed non-portable `xargs -r` in `scripts/clean-test-env.sh`.
3. **[RECOMMENDED] Explicit Container Init**: Add `--init` flag to `podman run` calls for agent sandbox containers to guarantee PID 1 zombie reaping under heavy compilation workflows.
4. **[RECOMMENDED] Pin Dockerfile Base Digests**: Transition base image tags (e.g. `debian:bookworm-slim`, `golang:1.27-bookworm`) to explicit SHA256 digest pins in OCI image definitions to protect against upstream image mutation.
