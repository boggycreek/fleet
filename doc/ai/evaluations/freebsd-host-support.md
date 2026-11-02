---
name: fleet-freebsd-host-support
description: >-
  FreeBSD 14.x/15.x host operating system support, bhyve microVM runtime isolation, pkg package management, ZFS dataset storage, pf firewall anchors, and GOOS=freebsd Go toolchain porting guide.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
-->

# FreeBSD Host Support & Runtime Porting Guide

This guide provides a comprehensive technical breakdown of FreeBSD host operating system support, bhyve microVM setup, `pkg` package manager integration, ZFS dataset storage volume preservation, `pf.conf` firewall anchor networking, and `GOOS=freebsd` Go cross-compilation for Fleet.

---

## 1. Architectural Runtime Models

Fleet supports FreeBSD 14.x and 15.x hosts through a **hybrid dual-tier isolation model** (see [ADR 00058](../adr/00058-FreeBsdHostSupportStrategy.md)):

| Workload Tier | Execution Mode | Container Engine / Isolation | Security & Parity Boundary |
| :--- | :--- | :--- | :--- |
| **Tier 1 (Default)** | Containerized Agents & Infra | Rootless Podman inside bhyve Linux microVM (`fleet-vm`) | 100% Linux ABI parity; non-root user boundary ([ADR 00004](../adr/00004-NonRootContainerUserAndPermissionBounds.md)). |
| **Tier 2 (Opt-In)** | Native Host Agent (`--no-container`) | Unprivileged native FreeBSD process (`GOOS=freebsd`) | Direct native host performance; native ZFS/toolchain access. |

---

## 2. Shell Scripts & Package Manager Audit (`setup.sh` / `install.sh`)

### Platform Detection & `pkg` Integration
FreeBSD operating system detection relies on `uname -s` returning `FreeBSD`. On FreeBSD hosts, package installation utilizes the native `pkg(8)` package manager:

```bash
# Package manager detection in setup.sh / install.sh
if [ "${OS_RAW}" = "FreeBSD" ]; then
  OS="freebsd"
  OS_PRETTY="FreeBSD $(freebsd-version 2>/dev/null || uname -r)"
  PKG_MGR="pkg"
fi
```

Required FreeBSD packages installed via `pkg install -y`:
- `git`: Version control.
- `curl`: Asset fetching and API queries.
- `gmake`: GNU Make (required because FreeBSD's system `make` is BSD Make, which interprets Makefiles differently).
- `pkgconf`: Package configuration tool (provides `pkg-config`).
- `ca_root_nss`: TLS root CA bundle.
- `sudo` / `doas`: Privilege escalation helper for system package installation.
- `bash`: GNU Bourne-Again Shell.

### Portable Shell Interpreter Path
FreeBSD places non-base binaries under `/usr/local/bin` or `/usr/site/bin`. Consequently, `bash` resides at `/usr/local/bin/bash` on FreeBSD rather than `/bin/bash` (Linux) or `/opt/homebrew/bin/bash` (macOS).

All shell scripts across the repository must use the portable shebang:
```bash
#!/usr/bin/env bash
```
This guarantees that `env` dynamically resolves `bash` regardless of whether it is located in `/bin`, `/usr/bin`, or `/usr/local/bin`.

### XDG Multi-Version Go SDK Management
The `setup.sh` script installs official Go toolchains into the XDG Base Directory structure (`~/.local/share/go/sdk/go<version>`).

For FreeBSD hosts, `setup.sh` resolves official FreeBSD Go release archives from `https://go.dev/dl/`:
- **x86_64 / amd64**: `go<ver>.freebsd-amd64.tar.gz`
- **aarch64 / arm64**: `go<ver>.freebsd-arm64.tar.gz`

Checksums are verified against Google's official SHA256 release manifests (`https://dl.google.com/go/go<ver>.freebsd-<arch>.tar.gz.sha256`) using `sha256sum` or FreeBSD's native `sha256 -q`.

---

## 3. Go Build Matrix & `libbp.so` C-Shared ABI

### Cross-Compilation Matrix
All Fleet host CLI tools (`fleet`, `ft`), backplane utilities (`bp`, `bpd`), and MCP servers (`sonar-mcp`) support native compilation for FreeBSD:

```bash
# Cross-compiling Fleet binaries for FreeBSD amd64
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/freebsd_amd64/fleet ./sandbox/cmd/fleet

# Cross-compiling Fleet binaries for FreeBSD arm64
GOOS=freebsd GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/freebsd_arm64/fleet ./sandbox/cmd/fleet
```

### Shared C ABI (`libbp.so`)
The core backplane client library (`libbp`) exports a shared C ABI for polyglot runtime integration ([ADR 00012](../adr/00012-LibbpCoreClientLibraryAndCAbi.md)). On FreeBSD, compiling Go code with `-buildmode=c-shared` produces a dynamic ELF shared object named `libbp.so` (matching Linux conventions, in contrast to macOS `.dylib`):

```bash
# Building C-shared libbp library on FreeBSD host (requires system clang)
GOOS=freebsd CGO_ENABLED=1 go build -buildmode=c-shared -o dist/lib/libbp.so ./backplane/cmd/libbp-c
```

`Makefile` target resolution automatically identifies FreeBSD `.so` output:
```makefile
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    LIBBP_EXT := dylib
else
    LIBBP_EXT := so
endif
```

---

## 4. Storage: ZFS Datasets & Persistent Volumes

FreeBSD workstations and servers overwhelmingly utilize ZFS (`zroot`). Fleet leverages ZFS primitives for persistent agent home volumes ([ADR 00005](../adr/00005-PersistedHomeVolumeAcrossRecreation.md)) and infrastructure state preservation.

### ZFS Dataset Layout
Fleet automatically provisions a root dataset under the host pool during setup:

```
zroot/
└── fleet/
    ├── config/                 -> ~/.config/fleet
    ├── cache/                  -> ~/.cache/fleet
    └── volumes/                -> Persistent named volumes
        ├── fleet-valkey-data
        ├── fleet-forgejo-data
        ├── fleet-agent-alpha-home
        └── fleet-agent-beta-home
```

### Volume Creation & Mounting
When creating a persistent volume on a FreeBSD ZFS host, `fleet` creates an unprivileged dataset mounted directly into the XDG data directory:

```bash
# Create dedicated ZFS dataset for agent persistent home
zfs create -o mountpoint=/home/operator/.local/share/fleet/volumes/fleet-agent-alpha-home zroot/fleet/volumes/fleet-agent-alpha-home

# Set permissions for unprivileged agent UID 1000
chown -R 1000:1000 /home/operator/.local/share/fleet/volumes/fleet-agent-alpha-home
```

### Snapshotting & Workspace Replication
ZFS dataset backing enables zero-cost atomic snapshots and instant workspace cloning for agent checkpoints:

```bash
# Create instant checkpoint snapshot before running risky agent tasks
zfs snapshot zroot/fleet/volumes/fleet-agent-alpha-home@pre-refactor

# Instant rollback to previous checkpoint
zfs rollback zroot/fleet/volumes/fleet-agent-alpha-home@pre-refactor

# Clone agent workspace state for parallel experimentation
zfs clone zroot/fleet/volumes/fleet-agent-alpha-home@pre-refactor zroot/fleet/volumes/fleet-agent-alpha-fork-home
```

---

## 5. Network Configuration: `pf.conf` Anchor Rules

Network filtering and SSH port redirection on FreeBSD are governed by `pf(4)` (Packet Filter). Fleet manages host SSH port forwarding and egress filtering by appending dedicated dynamic anchors to `/etc/pf.conf`.

### Base `/etc/pf.conf` Reservation
During installation on FreeBSD, `install.sh` ensures the following anchor directives exist in `/etc/pf.conf`:

```pf
# Fleet dynamic firewall anchors
rdr-anchor "fleet/*"
anchor "fleet/*"
```

### Dynamic SSH Port Redirection
When an agent container or bhyve microVM publishes a host SSH port (e.g. host port `10022` mapped to container SSH port `2222`), `fleet` dynamically injects redirect rules into the `fleet/agents` anchor using `pfctl`:

```bash
# Inject SSH port forwarding rule for agent 'alpha'
echo "rdr pass on inet proto tcp from any to self port 10022 -> 127.0.0.1 port 2222" | pfctl -a "fleet/agents/alpha" -f -
```

### Default-Deny Egress Anchor Filtering
For default-deny network egress control ([ADR 00014](../adr/00014-DefaultDenyNetworkEgressFiltering.md)), `fleet` applies outbound rules in the `fleet/egress` anchor:

```pf
# Block all egress traffic from agent bridge interface by default
block out on vnet0 from any to any

# Allow DNS resolution
pass out on vnet0 proto udp to any port 53

# Allow HTTP/HTTPS egress to approved LLM API gateway addresses
pass out on vnet0 proto tcp to <approved_llm_gateways> port { 80, 443 }
```

---

## 6. Diagnostic Doctor & Verification (`fleet agent doctor`)

The `fleet agent doctor` and `fleet infra doctor` commands ([ADR 00023](../adr/00023-ComprehensiveDoctorAndSelfHealing.md)) include platform-specific checks for FreeBSD host environments:

1. **Kernel & OS Check**: Validates FreeBSD version (`14.x` or `15.x`) and kernel module loading (`vmm.ko` for bhyve, `pf.ko` for Packet Filter).
2. **Package Tooling**: Confirms presence of `pkg`, `gmake`, `git`, and `curl`.
3. **Storage Health**: Verifies ZFS pool status (`zpool status`) and dataset mountpoint accessibility.
4. **Firewall Status**: Audits `pfctl -s Anchors` to ensure `fleet/*` anchors are active in `pf(4)`.
5. **MicroVM Engine**: Confirms bhyve availability and Podman SSH connectivity to `fleet-vm`.
