---
adr: "00058"
title: "FreeBSD Host Support Strategy, Container Isolation, and Port Runtime"
topic: "Foundations & Architecture"
theme: "THEME-CORE"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
tags:
  - freebsd
  - host-support
  - linuxulator
  - jails
  - bhyve
  - ocijail
  - pf
  - zfs
executive_summary: "Evaluates FreeBSD 14.x/15.x host operating system support, comparing Linuxulator OCI execution, native FreeBSD jails, bhyve microVMs, and native host agent modes. Adopts a hybrid architecture deploying a bhyve Linux microVM for rootless OCI container parity alongside native GOOS=freebsd host CLI and agent binaries, backed by ZFS persistent storage and pf(4) firewall anchor integration."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
-->

# 00058. FreeBSD Host Support Strategy, Container Isolation, and Port Runtime

## Context

Fleet currently targets Linux distributions (Debian, Fedora, Ubuntu, Arch) and macOS (via rootless Podman machine). FreeBSD 14.x and 15.x represent attractive physical host platforms for high-performance workstation and server deployments due to first-class ZFS storage integration, the `pf(4)` firewall, fine-grained process isolation via jails, and superior kernel I/O throughput.

However, containerization and runtime isolation on FreeBSD operate under fundamentally different kernel primitives than Linux:

1. **Container Primitives**: FreeBSD Podman (`sysutils/podman-suite`) relies on `ocijail` and VNET FreeBSD Jails rather than Linux cgroups (cgroups v2) and kernel namespaces (`clone`/`unshare`).
2. **Linux Image Execution**: Linux OCI images (such as Fleet's Debian Bookworm agent base, Valkey, Forgejo, and SonarQube) run via the FreeBSD Linuxulator (`podman run --os=linux`), an in-kernel Linux ABI emulation layer rather than a hardware hypervisor.
3. **Rootless Security Boundary**: Standard FreeBSD jails require root privileges or specialized sysctl security delegations (`security.jail.param.allow.raw_sockets`, unprivileged VNET jail creation). Standard FreeBSD `ocijail` Podman integration does not support fully unprivileged, rootless jail creation out of the box without elevated host permissions or root-owned daemons, challenging Fleet's rootless-by-default security model ([ADR 00003](00003-PodmanAsRequiredContainerEngine.md), [ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)).
4. **Networking**: Port redirection and network isolation use `pf(4)` anchors rather than Linux `netavark`, `iptables`, or `nftables`.

This decision record evaluates technical options for bringing first-class FreeBSD host support to Fleet while adapting security boundaries and runtime performance.

## Options Evaluated

### Option A: Linuxulator OCI Containers (`podman run --os=linux`)

Run standard Linux OCI images directly on FreeBSD using FreeBSD's native `podman` + `ocijail` driver combined with the in-kernel Linuxulator ABI translation layer.

- **Pros**:
  - Preserves identical OCI image hierarchy (`fleet-core`, `fleet-daemon-base`, `fleet-tool-base`, derivative agents, and infrastructure containers) across Linux and FreeBSD platforms.
  - Zero hypervisor memory reservation overhead; containers execute as native host processes under ABI translation.
- **Cons**:
  - Requires elevated root permissions or setuid helpers for jail/VNET allocation, violating Fleet's strict rootless-by-default security model ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)).
  - Incomplete Linux ABI emulation for complex container workloads (e.g., overlayfs mount options, netlink sockets, io_uring, cgroups v2 control interfaces, and specific eBPF/tracing calls).
  - Potential instability or runtime divergence in multi-threaded container applications.
- **Verdict**: Rejected as the primary container runtime for agent workloads due to privilege escalation requirements and ABI edge-case vulnerabilities, though retained as an experimental tool testing option.

### Option B: FreeBSD-Native Jails with Native Ports (Valkey, Forgejo)

Re-architect Fleet's shared infrastructure (Valkey, Forgejo, SonarQube) and agent environments to execute as native FreeBSD processes inside native FreeBSD Jails provisioned via FreeBSD `pkg` or `ports`.

- **Pros**:
  - Maximum native performance with zero emulation or hypervisor overhead.
  - Full native integration with FreeBSD VNET networking, ZFS datasets, and FreeBSD jail isolation primitives.
- **Cons**:
  - Breaks runtime parity across host platforms: agent execution environment differs significantly from standard Linux OCI images (different libc, package names, path conventions, and tool availability).
  - High engineering and maintenance burden to build and support dual container artifact streams (OCI container images vs. FreeBSD jail tarballs/pkg manifests).
  - Still requires root or complex MAC (Mandatory Access Control) policies to create and manage jails dynamically.
- **Verdict**: Rejected as default due to high maintenance overhead, risk of agent behavioral drift between Linux and FreeBSD targets, and loss of standard OCI image distribution advantages.

### Option C: Bhyve MicroVM Isolation

Run a lightweight Linux microVM (e.g., minimal Linux kernel with rootless Podman) on FreeBSD using the native `bhyve` hypervisor. Fleet CLI communicates with the microVM's Podman socket over SSH or a UNIX domain socket.

- **Pros**:
  - **100% Linux ABI Compatibility**: All standard Fleet OCI container images (`fleet-core`, `fleet-daemon-base`, derivative agents, Valkey, Forgejo, SonarQube) execute identically with zero syscall emulation drift.
  - **Strict Rootless Security Boundary**: Podman runs rootless inside the guest Linux microVM, preserving the non-root privilege boundary ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)) while requiring no root privileges on the FreeBSD host for container execution.
  - **Hardware Virtualization Isolation**: Strong hypervisor-enforced memory and CPU boundary between the FreeBSD host and agent execution environments.
- **Cons**:
  - Hypervisor memory reservation overhead (typically 1–2 GB static RAM reservation for the microVM guest kernel).
  - Slightly higher container spin-up latency compared to native host processes.
  - File sharing between host ZFS datasets and VM guest requires VirtFS (9p) or NFS mounts.
- **Verdict**: Recommended and Accepted as the primary Tier-1 container isolation model for FreeBSD hosts.

### Option D: First-Class Host Agent Mode (`fleet agent create --no-container`)

Provide a native FreeBSD host agent execution mode where `fleet-agent`, `bpd`, and agent harnesses execute directly as unprivileged native FreeBSD processes (`GOOS=freebsd`) on the host machine (`--no-container`), while shared infrastructure containers (Valkey, Forgejo) run inside a background bhyve microVM or dedicated service jail.

- **Pros**:
  - Direct, zero-overhead execution for native FreeBSD development workflows.
  - Native access to host Go compiler, ZFS utilities, FreeBSD developer tools, and host hardware resources.
  - Zero virtualization or ABI translation overhead for agent execution.
- **Cons**:
  - Bypasses container isolation: agent actions execute directly with the permissions of the host operator account.
  - Requires explicit opt-in by the operator for trusted workloads.
- **Verdict**: Accepted as an opt-in Tier-2 mode (`fleet agent create --no-container`) for trusted local developer environments where native FreeBSD performance is prioritized.

---

## Decision Outcome

Fleet adopts a **Hybrid Two-Tier Architecture** for FreeBSD host support:

1. **Tier-1 Containerized Workloads (Default)**:
   - Shared infrastructure (`fleet-valkey`, `fleet-forgejo`, `fleet-postgres`, `fleet-sonarqube`) and containerized agents run inside a managed Linux microVM (`fleet-vm`) provisioned via `bhyve`.
   - Preserves 100% OCI image compatibility, rootless Podman execution boundaries ([ADR 00003](00003-PodmanAsRequiredContainerEngine.md), [ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)), and identical container behavior across Linux, macOS, and FreeBSD hosts.

2. **Tier-2 Native Host Mode (Opt-In)**:
   - Native FreeBSD Go binaries (`GOOS=freebsd`, `GOARCH=amd64|arm64`) are compiled for `fleet`, `ft`, `bp`, `bpd`, `fleet-agent`, and `sonar-mcp`.
   - Operators can execute agents natively on FreeBSD using `fleet agent create <name> --no-container` for unconstrained performance and direct ZFS/FreeBSD toolchain integration.

3. **Storage Adaptation (ZFS Datasets)**:
   - FreeBSD persistent volumes utilize dedicated ZFS datasets (e.g., `zroot/fleet/volumes/<name>`).
   - Leverages ZFS atomic snapshotting (`zfs snapshot`) for instantaneous agent checkpointing and state restoration.

4. **Network Adaptation (`pf.conf` Anchors)**:
   - Host SSH port forwarding and egress filtering on FreeBSD utilize `pf(4)` dynamic anchors (`anchor "fleet/*"` in `/etc/pf.conf`) managed via `pfctl`, substituting Linux `iptables`/`netavark` rules.

---

## Consequences

### Positive
- **Complete Platform Parity**: Enables Fleet deployment on FreeBSD 14.x/15.x workstations and servers without altering OCI image definitions or agent runtime specs.
- **Preserved Security Perimeter**: Bhyve microVM hosting guarantees rootless Podman boundaries ([ADR 00004](00004-NonRootContainerUserAndPermissionBounds.md)) without requiring root jail privileges on the FreeBSD host.
- **ZFS Integration**: Native ZFS dataset backing provides near-instantaneous volume snapshotting, cloning, and rollback capabilities for agent state management.
- **Flexibility**: Dual-tier approach provides max-security microVM containerization by default alongside high-performance native host execution (`--no-container`).

### Negative / Trade-offs
- **Host VM Overhead**: Tier-1 container execution requires running a background bhyve microVM, consuming ~1–2 GB RAM.
- **Tooling Divergence**: Managing `pf(4)` firewall anchors and ZFS datasets requires FreeBSD-specific host management logic inside `fleet infra` diagnostic subcommands.
