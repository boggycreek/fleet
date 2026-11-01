---
name: fleet-howto-baremetal-agent-setup
description: Operational guide for enrolling and deploying baremetal agents on physical machines using install-agent.sh and fleet agent create --baremetal.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Baremetal Agent Enrollment and Deployment

This guide describes how to enroll, bootstrap, and operate Fleet agents running directly on baremetal physical host machines (such as dedicated lab workstations, laptops, or remote servers) outside container runtimes.

---

## Architecture Overview

While standard Fleet agents run inside isolated rootless Podman containers, baremetal agents execute directly on physical hosts under systemd user services. This runtime model allows agents to leverage specialized host hardware (such as local GPUs, physical USB test rigs, or native performance profilers) while remaining connected to the central Fleet backplane.

- **Controller & Node Topology**: The Fleet workstation acts as the controller, managing Valkey messaging streams, local Forgejo repositories, and credentials.
- **Single-Use Ephemeral Enrollment**: When a baremetal agent is created with `fleet agent create <name> --baremetal`, Fleet provisions keys and credentials locally and spins up a temporary HTTP enrollment server with a single-use token (15-minute TTL).
- **Bootstrap Script (`install-agent.sh`)**: The target machine fetches and executes `install-agent.sh`, which securely downloads the environment secrets payload, installs `fleet-agent` and `bp` binaries, sets up systemd user services, and reports completion back to the controller.
- **Headless Execution & Lingering**: On systemd Linux distributions, lingering (`loginctl enable-linger`) ensures the background agent daemon (`fleet-agent.service`) continues operating autonomously without an active interactive login session.

---

## 1. Controller-Side Agent Creation (`fleet agent create --baremetal`)

To provision a baremetal agent from your primary Fleet controller workstation, run `fleet agent create` specifying `--baremetal` and the target machine's SSH address via `--ssh-host`:

```bash
fleet agent create worker-node-1 --baremetal --ssh-host 192.168.1.150 --role "CUDA Hardware Benchmarking"
```

### Supported Baremetal Flags

- **`--baremetal`**: Directs Fleet to configure the agent for physical host execution rather than a Podman container.
- **`--ssh-host <host>`**: Target machine IP address or hostname reachable from the controller (required).
- **`--ssh-port <port>`**: Target machine SSH port (defaults to `22`).
- **`--ssh-user <user>`**: Target SSH username (defaults to the current controller user).
- **`--controller-host <ip|hostname>`**: Overrides the controller IP address announced to the baremetal node (defaults to auto-detected LAN IP).
- **`--model-url <url>`**: Custom OpenAI-compatible model gateway endpoint.
- **`--model-name <name>`**: Target LLM model name.
- **`--no-enroll`**: Pre-provisions agent configuration without launching the interactive single-use enrollment server.

### Example Controller Output

```text
Agent "worker-node-1" created successfully.
  Runtime:    baremetal (physical host execution)
  SSH Target: agent@192.168.1.150:22
  Role:       CUDA Hardware Benchmarking
  BP Profile: /home/operator/.local/state/bp/profiles/worker-node-1.env

Infrastructure Provisioning:
  [✓] Keypair Generated (Ed25519)
  [✓] Valkey ACL Rules Configured (~worker-node-1:*)
  [✓] Forgejo User Account Provisioned ('worker-node-1')
  [✓] SSH Stanza Added to ~/.local/share/fleet/ssh_config

============================================================
 BAREMETAL ENROLLMENT ACTIVE (Single-Use Token)
============================================================
Controller listening at: http://192.168.1.100:8765 (TTL: 15m)
To bootstrap and enroll on 192.168.1.150, run:

  curl -sSf http://192.168.1.100:8765/install/worker-node-1?token=a1b2c3d4... | bash

Waiting for baremetal agent to collect credentials...
```

The controller CLI pauses while awaiting the node's bootstrap callback.

---

## 2. Bootstrapping the Target Machine (`install-agent.sh`)

Log into the physical target machine (e.g., via SSH or local console) and run the generated enrollment command.

### Mode A: Standard Unprivileged Installation

If running as an unprivileged user that already has local login access:

```bash
curl -sSf http://192.168.1.100:8765/install/worker-node-1?token=a1b2c3d4... | bash
```

**What occurs:**
1. Verifies token validity with the controller enrollment server.
2. Creates confined local directories:
   - Configuration & secrets: `~/.config/fleet/agent.env` (`0600`) and `~/.config/fleet/signing-key.pem` (`0600`).
   - Binaries: `~/.local/bin/`.
   - Workspace: `~/workspace/`.
   - User systemd unit: `~/.config/systemd/user/fleet-agent.service`.
3. Downloads native platform binaries (`fleet-agent` and `bp`) from the controller or release store.
4. Adds the controller workstation's public SSH key (`~/.ssh/fleet.pub`) into the node's `~/.ssh/authorized_keys`.
5. Enables and starts the user systemd service: `fleet-agent.service`.
6. Sends an HTTP POST completion notification to the controller, terminating the enrollment server.

### Mode B: Root / Sudo Provisioning with Dedicated User

If running on a fresh host as `root` (or with `sudo`), pass the script to `sudo bash`:

```bash
curl -sSf http://192.168.1.100:8765/install/worker-node-1?token=a1b2c3d4... | sudo bash
```

**What occurs:**
1. Detects `EUID == 0` and automatically provisions a dedicated system user (defaults to the agent name, e.g. `worker-node-1`).
2. Copies authorized SSH keys from `SUDO_USER` to allow direct operator SSH access.
3. Automatically executes `loginctl enable-linger <user>` so systemd user daemons persist across reboots and logouts.
4. Drops privileges cleanly to the target user and runs userspace enrollment.

---

## 3. Managing the Baremetal Daemon

Once enrolled, the agent runs as a systemd user daemon on the baremetal machine.

### Node-Side Inspection Commands

```bash
# Check daemon status
systemctl --user status fleet-agent

# Stream live agent logs
journalctl --user -u fleet-agent -f

# Restart or stop the agent daemon
systemctl --user restart fleet-agent
systemctl --user stop fleet-agent
```

### Enabling Lingering Manually

On headless Linux machines (Debian, Ubuntu, RHEL, Fedora), ensure user services stay alive after SSH sessions disconnect:

```bash
loginctl enable-linger $USER
```

---

## 4. Controller-Side Management & IDE Remote-SSH

From the controller workstation, manage the baremetal node using standard `fleet` commands:

### SSH Shell Access

Because `fleet agent create --baremetal` registers the node in `~/.local/share/fleet/ssh_config`, you can SSH into the node immediately:

```bash
fleet agent ssh worker-node-1
# Or directly:
ssh fleet-worker-node-1
```

### Remote IDE Attachment

Attach desktop IDEs (VS Code, Cursor, JetBrains Gateway) directly to the physical machine workspace:

```bash
fleet agent open worker-node-1 in vscode
```

### Lifecycle Commands with Baremetal Agents

- **`fleet agent list`**: Displays the agent with `IMAGE: baremetal` and host SSH routing.
- **`fleet agent start <name>`**: Informs the operator that the agent runs under systemd on the physical host.
- **`fleet agent stop <name>`**: Baremetal daemons run under the node's systemd supervisor; stopped via node SSH or systemctl.
- **`fleet agent clean <name>`**: Skipped (no container runtime to clean).
- **`fleet agent retire <name>`**: Revokes Valkey ACLs, purges streams, cleans up local credentials and SSH host stanzas.

---

## 5. Troubleshooting Tips

- **Enrollment token expired**:
  Single-use enrollment tokens expire after 15 minutes. If enrollment times out, re-run `fleet agent create <name> --baremetal --ssh-host <host>` (or `fleet agent provision <name>`) to generate a fresh token.
- **Firewall blocks port 8765 or Valkey port 6379**:
  Ensure the controller's firewall allows incoming connections from the baremetal host's IP address on the enrollment port (default `8765`) and Valkey backplane port (default `6379`).
- **Daemon terminates when operator logs out**:
  Run `loginctl enable-linger <username>` on the target node to ensure systemd user managers remain active headlessly.
