---
name: fleet-howto-ide-remote-dev
description: Guide for connecting VS Code, Cursor, and JetBrains Gateway to Fleet agents via Remote-SSH.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# IDE & Remote Development Integration

This guide explains how to attach desktop IDEs (VS Code, Cursor, and JetBrains Gateway) directly to running Fleet agent containers using OpenSSH Remote-SSH integration.

---

## Architecture Overview

Fleet provides zero-touch desktop IDE discovery by managing a dynamic SSH configuration file on the host machine:
- **Dedicated SSH Keypair**: Fleet installer creates `~/.ssh/fleet` and `~/.ssh/fleet.pub` specifically for agent authentication.
- **SSH Config Stanza**: Fleet automatically maintains `~/.local/share/fleet/ssh_config` with entry aliases (`Host fleet-<name>`) pointing to the dynamic host port mapped to port 2222 inside each running container.
- **`Include` Directive**: Fleet adds `Include ~/.local/share/fleet/ssh_config` to your primary `~/.ssh/config` file, making agent hosts immediately visible to any SSH client or IDE on your workstation.

---

## 1. Connecting VS Code & Cursor

### Automatic Launch via CLI
Use `fleet agent open <name> in vscode` to automatically start the agent container (if stopped) and launch VS Code attached via Remote-SSH:

```bash
fleet agent open dev-bot-1 in vscode
```
**Expected Output:**
```text
==> Verifying agent 'dev-bot-1' container state...
[✓] Agent 'dev-bot-1' is running (SSH port: 32768)
[✓] Generating SSH config stanza for 'fleet-dev-bot-1'
[✓] Launching VS Code Remote-SSH: vscode://vscode-remote/ssh-remote+fleet-dev-bot-1/home/agent
```

*(For **Cursor**, `fleet agent open <name> in vscode` opens through the default `code` handler or URI scheme).*

### Manual Connection in VS Code / Cursor
If you prefer connecting manually from inside VS Code or Cursor:
1. Open the **Command Palette** (`Ctrl+Shift+P` / `Cmd+Shift+P`).
2. Select **Remote-SSH: Connect to Host...**.
3. Choose **`fleet-dev-bot-1`** from the auto-completed host list.
4. Select `/home/agent` or `/home/agent/tasks` as the target workspace folder.

---

## 2. Connecting JetBrains Gateway

### Automatic Launch via CLI
Use `fleet agent open <name> in gateway` to launch JetBrains Gateway pre-configured for the target agent:

```bash
fleet agent open dev-bot-1 in gateway
```
**Expected Output:**
```text
==> Verifying agent 'dev-bot-1' container state...
[✓] Agent 'dev-bot-1' is running (SSH port: 32768)
[✓] Launching JetBrains Gateway for host 'fleet-dev-bot-1'...
```

### Manual Connection in JetBrains Gateway
To connect manually:
1. Open **JetBrains Gateway**.
2. Under **SSH**, click **New Connection**.
3. Select **`fleet-dev-bot-1`** from your SSH configuration hosts (or enter `HostName 127.0.0.1`, `Port 32768`, `User agent`, `IdentityFile ~/.ssh/fleet`).
4. Click **Check Connection and Continue**.
5. Select your preferred JetBrains IDE backend (GoLand, PyCharm, IntelliJ IDEA, or CLion) and set project directory to `/home/agent`.

---

## 3. Manual SSH Config Integration & Inspection

### Inspecting Managed SSH Config
To inspect the auto-generated stanzas for running agents:

```bash
fleet agent ssh-config dev-bot-1
```
**Expected Output:**
```text
Host fleet-dev-bot-1
    HostName 127.0.0.1
    Port 32768
    User agent
    IdentityFile ~/.ssh/fleet
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    ServerAliveInterval 15
    ServerAliveCountMax 3
```

To output stanzas for all provisioned agents:
```bash
fleet agent ssh-config --all
```

### Verifying Primary SSH `Include` Directive
Ensure your personal `~/.ssh/config` contains the Fleet include line at or near the top:

```text
Include ~/.local/share/fleet/ssh_config
```

If missing, you can re-install or re-link IDE integrations using:
```bash
fleet plugin add vscode
fleet plugin add gateway
```

---

## 4. Troubleshooting Tips

- **IDE displays "Host key verification failed"**:
  Fleet sets `StrictHostKeyChecking no` and `UserKnownHostsFile /dev/null` in `~/.local/share/fleet/ssh_config` because agent container SSH keys are generated dynamically per instance. Check if your main `~/.ssh/config` overrides these options for `Host fleet-*`.
- **Host `fleet-<name>` not recognized in VS Code host list**:
  Verify that `~/.ssh/config` has `Include ~/.local/share/fleet/ssh_config` on the first line. Run `cat ~/.local/share/fleet/ssh_config` to confirm the stanza exists.
- **SSH permission denied (`publickey`)**:
  Verify key permissions on host: `chmod 0600 ~/.ssh/fleet`. Run `fleet agent doctor <name>` to inspect key pairing and auto-repair permissions.
- **Opening IDE without auto-launching GUI**:
  Use the `--no-launch` flag to print the target connection URI without opening the desktop application:
  ```bash
  fleet agent open dev-bot-1 in vscode --no-launch
  ```
