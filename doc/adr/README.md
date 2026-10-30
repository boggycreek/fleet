# Architecture Decision Records (ADRs)

This directory documents the foundational architectural decisions governing the **Agent Sandbox** system for the **v0.1.0-alpha** release.

Records are numbered serially (`00001` through `00038`) and organized by topic domain to reflect the current **as-built** architecture. Each decision record includes machine-readable YAML front matter (with standardized thematic markers, tags, and executive summaries) for consumption by automated agents and tooling.

---

## Thematic Groupings

| Theme Code | Topic Domain | Scope |
| :--- | :--- | :--- |
| **`THEME-CORE`** | Foundations & Architecture | Monorepo structure, native static binaries, single host CLI, rootless engine requirement. |
| **`THEME-RUNTIME`** | Container Runtime & Storage | Unprivileged user boundaries, persistent named volumes, layered image hierarchy. |
| **`THEME-LIFECYCLE`** | Agent Lifecycle & Process Model | Phase separation, Backplane Daemon (`bpd`) as PID 1, non-destructive cleaning vs. retirement. |
| **`THEME-SECURITY`** | Security, Backplane & Messaging | Valkey ACL isolation, Ed25519 digital signatures, `libbp` C ABI library. |
| **`THEME-NETWORKING`** | Network Isolation & Perimeter | Dedicated rootless netns, default-deny egress filtering sidecar, netns self-healing. |
| **`THEME-DEVEXP`** | Developer Experience & IDEs | One-shot IDE launching (`open`), managed OpenSSH config include, root-owned settings protection. |
| **`THEME-PLUGINS`** | IDE Plugin Management | Strict three-verb plugin interface, JetBrains Gateway vs. Toolbox decoupling & native SSH sync. |
| **`THEME-FLEET`** | Shared Fleet Services | Local Gitea git hosting & memory backup, standardized local OpenAI inference proxy. |
| **`THEME-OPERATIONS`** | Operations, Diagnostics & Quality | Diagnostic doctor self-healing (`--fix`), unified update pipeline, >=90% test coverage gate, SBOM generation. |
| **`THEME-GOVERNANCE`** | Compliance & Governance | Open-source attribution standards, third-party licensing compliance, legal notices. |

---

## Architectural Decision Records

### Foundations & Architecture (`THEME-CORE`)
- **[00001 — Go Monorepo and Native Static Binaries](00001-GoMonorepoAndNativeStaticBinaries.md)**  
  *Executive Summary:* All Agent Sandbox host and in-container utilities are developed in a unified Go monorepo and compiled into statically linked, zero-dependency native binaries.
- **[00002 — Unified Host CLI (sndbx)](00002-UnifiedHostCliSndbx.md)** *(Superseded by 00047)*  
  *Executive Summary:* A single multi-command binary (`sndbx`) acts as the sole operator entry point for fleet management, diagnostics, plugin setup, and orchestration.
- **[00003 — Podman as Required Container Engine](00003-PodmanAsRequiredContainerEngine.md)**  
  *Executive Summary:* Rootless Podman is the mandatory container runtime dependency, eliminating root-owned daemon requirements and enforcing user-space privilege boundaries.
- **[00034 — Multi-Version Go Toolchain Management via XDG Standard](00034-MultiVersionGoToolchainManagementViaXdgStandard.md)**  
  *Executive Summary:* Standardizes automated Go toolchain provisioning and multi-version management under the XDG Base Directory specification (`~/.local/share/go/sdk`), ensuring reproducible builds and quality gate execution across host environments without requiring root/sudo privileges or system-level modifications.
- **[00047 — Project Rename from Sandbox to Fleet](00047-ProjectRenameFromSandboxToFleet.md)**  
  *Executive Summary:* Formally renames the project from Agent Sandbox ('sandbox', 'sndbx') to Fleet ('fleet', short alias 'ft'). As an alpha-stage project, this transition constitutes a clean break without backward compatibility shims. Migrates host CLI binary to 'fleet' (with 'ft' alias), in-container binary to 'fleet-agent', shared infrastructure containers/networks to 'fleet-*', host paths to ~/.local/share/fleet and ~/.ssh/fleet, SSH host aliases to fleet-<name>, and upstream GitHub repository to boggycreek/fleet. Supersedes ADR 00002.

### Container Runtime & Storage (`THEME-RUNTIME`)
- **[00004 — Non-Root Container User and Permission Bounds](00004-NonRootContainerUserAndPermissionBounds.md)**  
  *Executive Summary:* Sandboxes execute strictly as unprivileged user `agent` (UID/GID 1000) under restricted Linux capabilities and rootless subuid mappings.
- **[00005 — Persisted Home Volume Across Container Recreation](00005-PersistedHomeVolumeAcrossRecreation.md)**  
  *Executive Summary:* Agent persistent state and memory reside in a dedicated named volume mounted to `/home/agent` that survives container restarts, updates, and recreation.
- **[00006 — Layered OCI Container Hierarchy and Resolution](00006-LayeredOciContainerHierarchy.md)** *(Superseded by 00043)*  
  *Executive Summary:* Agent images follow a strict inheritance chain (`sndbx-base` -> preset variants) resolved from local storage before falling back to external registries.
- **[00026 — Deno as Standard JavaScript Runtime for OCI Container Agents](00026-DenoAsStandardJsRuntimeForOciContainerAgents.md)** *(Superseded by 00045)*  
  *Executive Summary:* Deprecated. Deno was previously adopted as the standard in-container JS/TS runtime. Following Cloudflare's acquisition of Deno and the planned 1-year abandonment timeline, Deno has been superseded by Node Version Manager (NVM) scoped to the persistent agent home.
- **[00036 — PiG Derivative Agent OCI Image](00036-PiGDerivativeAgentOciImage.md)**  
  *Executive Summary:* Introduces PiG (Pi in Go) as a first-class derivative agent harness OCI image (`agent-sandbox-pig:latest`), integrating single-binary Go execution, non-root isolation, tmux session supervision, and native sndbx lifecycle preset management.
- **[00043 — Base OCI Hierarchy](00043-BaseOciHierarchy.md)**  
  *Executive Summary:* Refactors the agent OCI image inheritance hierarchy into a neutral foundation (agent-sandbox-core) and two specialized intermediate base layers: daemon-base (providing background process supervision, SSH, and multiplexed sessions for fleet workers) and tool-base (providing direct, foreground CLI tool execution without daemon overhead for interactive pair programming). Supersedes ADR 00006.
- **[00044 — Host Working Directory Projection and User ID Preservation](00044-HostWorkingDirectoryProjectionAndUserIdPreservation.md)**  
  *Executive Summary:* Enables interactive host-connected agent containers to bind the operator's current working directory directly into /home/agent/workspace using Podman user namespace mapping (--userns=keep-id), ensuring zero file ownership or permission drift on the physical host.
- **[00045 — Node Version Manager (NVM) Scoped to Agent Persistent Home](00045-NodeVersionManagerInAgentHome.md)**  
  *Executive Summary:* Adopts Node Version Manager (NVM) scoped to the unprivileged agent's persistent home directory (/home/agent/.nvm) with the greatest LTS release pre-installed as default. Decoupled from OCI image creation, allowing the operator and agent to manage and switch Node versions dynamically without rebuilding OCI images. Supersedes ADR 00026.
- **[00049 — Composable Headless Native Agent Cognitive Engine Artifact Layer](00049-ComposableHeadlessNativeAgentCognitiveEngineArtifactLayer.md)**
  *Executive Summary:* Establishes a dedicated, zero-dependency static cognitive engine artifact OCI image (`fleet-native-engine:latest`) containing the headless `fleet-agent` cognitive daemon and native entrypoints for zero-friction composition into any developer toolchain base image.

### Agent Lifecycle & Process Model (`THEME-LIFECYCLE`)
- **[00007 — Agent Lifecycle Phase Separation](00007-AgentLifecyclePhaseSeparation.md)**  
  *Executive Summary:* Decouples agent specification and credential generation (`create`) from container execution (`start`), halting (`stop`), and runtime status reporting.
- **[00008 — Backplane Daemon (bpd) as Primary Entrypoint](00008-BackplaneDaemonAsPrimaryEntrypoint.md)**  
  *Executive Summary:* Sandboxes run `bpd` as PID 1 entrypoint managing background workers; interactive `tmux` is an on-demand debug attachment tool rather than the container entrypoint.
- **[00009 — Deprovisioning and Retirement Phases](00009-DeprovisioningAndRetirementPhases.md)**  
  *Executive Summary:* Enforces clear teardown boundaries: `clean` destroys ephemeral container instances while preserving data; `retire` purges volumes, keys, Valkey credentials, and Gitea accounts.
- **[00039 — Harness-Agnostic Agent Runner Dispatch Contract](00039-HarnessAgnosticAgentRunnerDispatchContract.md)**  
  *Executive Summary:* Decouples `bpd` from harness-specific CLI invocation by establishing a canonical in-container `/usr/local/bin/agent-runner` dispatch contract across derivative agent images.
- **[00040 — Dual-Lobe Single-Identity Agent Architecture](00040-DualLobeSingleIdentityAgentArchitecture.md)**  
  *Executive Summary:* Establishes a dual-lobe cognitive model for containerized agents: a Fast-Reflex Front Lobe (`bpd` + minimal triage context) for immediate signed backplane communication, and a Deep-Focus Worker (24/7 persistent session) for heavy multi-turn execution, sharing state via `~/.agent/state.json` under a single cryptographic identity.

### Security, Backplane & Messaging (`THEME-SECURITY`)
- **[00010 — Valkey Streams Messaging Bus and ACL Isolation](00010-ValkeyPubSubMessagingAndAclIsolation.md)** *(Superseded by 00042)*  
  *Executive Summary:* Inter-agent and telemetry communication utilizes a shared Valkey Streams message bus governed by strict per-agent ACL rules and isolated stream keys.
- **[00011 — Ed25519 Cryptographic Message Signing](00011-Ed25519CryptographicMessageSigning.md)** *(Superseded by 00042)*  
  *Executive Summary:* All backplane events and commands require cryptographic Ed25519 signatures verified against the sending agent's public key to guarantee authenticity.
- **[00012 — libbp Core Client Library and Shared C ABI](00012-LibbpCoreClientLibraryAndCAbi.md)**  
  *Executive Summary:* Backplane IPC protocol logic is implemented in a native Go library (`libbp`) and planned for export as a shared C ABI (`libbp.so`) for polyglot agent runtimes.
- **[00042 — Universal Cryptographic Backplane Fabric and Stateless Identity Profiles](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)**  
  *Executive Summary:* Establishes a universal, topology-agnostic backplane communication fabric governed by non-repudiable cryptographic identity, strict capability-bounded message streams, and stateless connection profile resolution. Supersedes ADR 00010 and ADR 00011.
- **[00050 — Dev Container Security Screening, Diagnostics, and Privilege Escalation Sanitizer](00050-DevContainerSecurityScreeningAndPrivilegeEscalationSanitizer.md)**
  *Executive Summary:* Establishes automated static policy screening and sanitization for devcontainer.json configurations, proactively detecting and neutralizing privilege escalation vectors, forbidden capabilities, and dangerous host mounts before agent container instantiation.

### Network Isolation & Perimeter Defense (`THEME-NETWORKING`)
- **[00013 — Per-Instance Network Isolation](00013-PerInstanceNetworkIsolation.md)**  
  *Executive Summary:* Sandboxes run in isolated rootless network namespaces with independent bridge interfaces and dedicated localhost SSH port allocations.
- **[00014 — Default-Deny Network Egress Filtering](00014-DefaultDenyNetworkEgressFiltering.md)**  
  *Executive Summary:* Outbound container network traffic is restricted by default via an egress sidecar filter, permitting only approved LLM API endpoints and package repositories.
- **[00015 — Rootless Netns Runtime Directory Auto-Healing](00015-RootlessNetnsRuntimeDirectoryAutoHealing.md)**  
  *Executive Summary:* Runtime preflight hooks, transparent failure interception, and test cleanup routines validate directory permissions and reconcile desynchronized rootless network namespace mounts.

### Developer Experience & IDE Ensembling (`THEME-DEVEXP`)
- **[00016 — One-Shot IDE Remote Development and Host Ensembling](00016-OneShotIdeRemoteDevelopmentAndHostEnsembling.md)**  
  *Executive Summary:* A unified command (`sndbx agent open`) launches host thin-client IDEs against in-container `sshd`, supporting simultaneous multi-IDE attachment to a single agent.
- **[00017 — Managed OpenSSH Configuration Include](00017-ManagedOpenSshConfigurationInclude.md)**  
  *Executive Summary:* `sndbx` maintains a dedicated managed `ssh_config` file and idempotently links it into `~/.ssh/config` via an `Include` directive for zero-configuration host SSH access.
- **[00018 — Root-Owned IDE Settings Protection](00018-RootOwnedIdeSettingsProtection.md)**  
  *Executive Summary:* In-container IDE configuration directories (`.vscode`, `.cursor`) are planned to be root-owned and read-only to agent UID 1000, preventing unauthorized extensions or policy tampering.

### IDE Plugin Management (`THEME-PLUGINS`)
- **[00019 — Explicit Plugin Manager Interface](00019-ExplicitPluginManagerInterface.md)**  
  *Executive Summary:* IDE plugin lifecycles are governed strictly through `sndbx plugin <add|remove|list> <target>` without aliases or shorthand commands.
- **[00020 — JetBrains Gateway vs. Toolbox Decoupling and Native SSH Sync](00020-JetBrainsGatewayVsToolboxDecouplingAndNativeSshSync.md)**  
  *Executive Summary:* Decouples IntelliJ Gateway plugins (`gateway`) from JetBrains Toolbox App integration (`toolbox`), synchronizing running sandboxes directly to Toolbox's native `ssh/settings.json` and inlined host blocks.

### Shared Fleet Services (`THEME-FLEET`)
- **[00021 — Local Gitea Fleet Collaboration and Memory Backup](00021-LocalGiteaFleetCollaborationAndBackup.md)** *(Superseded by 00046)*  
  *Executive Summary:* Deprecated. An internal rootless Gitea service was previously deployed for local git hosting and memory backup. Superseded by ADR 00046, migrating the local forge to Forgejo.
- **[00046 — Migrate Local Git Forge from Gitea to Forgejo](00046-MigrateLocalForgeFromGiteaToForgejo.md)**  
  *Executive Summary:* Migrates the Agent Sandbox local git forge infrastructure from Gitea to the community-governed Forgejo hard fork (`codeberg.org/forgejo/forgejo:9-rootless`). Replaces all as-built Gitea container names, volumes, packages, and tooling while preserving drop-in backward compatibility with existing SQLite databases and REST API v1 endpoints. Supersedes ADR 00021.
- **[00022 — Local OpenAI-Compatible Inference Proxy](00022-LocalOpenAiCompatibleInferenceProxy.md)**  
  *Executive Summary:* Sandboxes access LLM inference through a standardized local OpenAI-compatible HTTP gateway, shielding agents from direct external API credentials.
- **[00027 — Local SonarQube Deterministic Mechanical Analysis and Quality Gate Infrastructure](00027-LocalSonarQubeDeterministicMechanicalAnalysisAndQualityGateInfra.md)**  
  *Executive Summary:* Integrates SonarQube Server Community Edition into the shared sandbox infrastructure alongside Valkey and Gitea. Agents utilize SonarQube for multi-tiered deterministic mechanical analysis — contrasting with probabilistic LLM code review — through automated defect discovery via a native Model Context Protocol (MCP) server and strict Quality Gate verification across fleet repositories.
- **[00028 — In-Container Fleet Task Coordination via Local Gitea-Backed Beads](00028-InContainerFleetTaskCoordinationViaLocalGiteaBackedBeads.md)**  
  *Executive Summary:* Establishes an air-gapped, distributed task and dependency tracking architecture for autonomous in-container agents using Beads (`bd`) backed by the local Gitea server (`http://gitea:3000/fleet/tasks.git`), maintaining strict architectural separation from host-level platform development on GitHub.
- **[00029 — Per-Agent SonarQube User Account and Analysis Token Provisioning Lifecycle](00029-SonarQubeUserAccountAndTokenProvisioningLifecycle.md)**  
  *Executive Summary:* Automates the provisioning and deprovisioning of dedicated SonarQube user accounts and analysis tokens for sandbox agents. During agent creation, a scoped analysis token is generated and injected into the container environment as `SONAR_TOKEN`. Agent retirement revokes active analysis tokens and deactivates the SonarQube user account.
- **[00032 — Fleet Model Context Protocol (MCP) Suite for Agent Workspaces](00032-FleetModelContextProtocolSuiteForAgentWorkspaces.md)**  
  *Executive Summary:* Defines the in-container Model Context Protocol (MCP) suite over STDIO JSON-RPC 2.0, equipping autonomous sandbox agents with schema-validated tools for inter-agent communication (`bp-mcp`), task management, episodic memory, and diagnostics while maintaining Ed25519 cryptographic signing and ACL boundaries.
- **[00035 — Host-Bridged Local LLM Inference Gateway](00035-HostBridgedLocalLlmInferenceGateway.md)**  
  *Executive Summary:* Resolves host-bound local LLM inference engines (Ollama, llama.cpp, vLLM, LiteLLM) from unprivileged rootless containers via Podman host-gateway resolution (`--add-host=llm-gateway:host-gateway`) and automatic configuration URL translation.
- **[00037 — Dynamic Piglet Synthesis from Hardware Probes](00037-DynamicPigletSynthesisFromHardwareProbes.md)**  
  *Executive Summary:* Automates the generation of specialized PiG "Piglet" YAML profiles based on dynamic host GPU and inference engine probes, tuning context windows, thinking levels, tool allowlists, and system prompts to physical workstation hardware.
- **[00038 — Specialized Fleet Bug Probing Ensemble](00038-SpecializedFleetBugProbingEnsemble.md)**  
  *Executive Summary:* Establishes a three-tier collaborative agent topology (Fuzzer, Auditor, Verifier) inside isolated sandbox containers, coordinating via the Valkey backplane (`bp`) and tracking findings in local Gitea (`bd`).

### Operations, Diagnostics & Quality (`THEME-OPERATIONS`)
- **[00023 — Comprehensive Diagnostic Doctor and Self-Healing](00023-ComprehensiveDoctorAndSelfHealing.md)**  
  *Executive Summary:* Dedicated diagnostic doctor domains (`sndbx agent doctor` and `sndbx infra doctor`) audit configuration, keys, storage, network bridges, and shared services with intrinsic automated remediation.
- **[00024 — Unified Update Pipeline (sndbx update)](00024-UnifiedUpdatePipeline.md)**  
  *Executive Summary:* The `sndbx update` command executes an atomic three-stage local deployment: git repository synchronization, native CLI compilation to `~/.local/bin`, and OCI image rebuilding.
- **[00025 — Quality Gates and Coverage Enforcement](00025-QualityGatesAndCoverageEnforcement.md)**  
  *Executive Summary:* Enforces continuous quality gates requiring >=90% statement test coverage (`make test-coverage`), static analysis (`golangci-lint`), and isolated ephemeral integration test fixtures.
- **[00031 — Automated Software Bill of Materials (SBOM) Generation and Release Publishing](00031-AutomatedSoftwareBillOfMaterialsAndReleasePublishing.md)**  
  *Executive Summary:* Automates the generation and distribution of machine-readable Software Bill of Materials (SBOM) across release pipelines and local build targets. Leverages Syft to produce both SPDX and CycloneDX JSON formats for source repositories and compiled release archives, publishing them directly as release assets.
- **[00033 — Host-Side Graph Task Tracking via Dolt-Backed Beads](00033-HostSideGraphTaskTrackingViaDoltBackedBeads.md)**  
  *Executive Summary:* Adopts Beads (`bd`) backed by a local Dolt database and synchronized with upstream GitHub (`refs/dolt/data`) for host-side platform task tracking (`sndbx-*`), maintaining strict two-plane isolation from in-container fleet task workloads.

### Compliance, Licensing & Governance (`THEME-GOVERNANCE`)
- **[00030 — Open Source Attribution and Third-Party Licensing Notices](00030-OpenSourceAttributionAndThirdPartyLicensingNotices.md)**  
  *Executive Summary:* Establishes a clear boundary between human-centric open-source recognition (`ACKNOWLEDGEMENTS.md`) and formal legal license texts (`NOTICES.md`). Reinforces our zero-dependency standard library Go architecture while systematically preserving third-party copyright notices and disclaimers for all container runtime, infrastructure, and session tooling components.


