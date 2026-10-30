# How-To: IDE Remote Development Integration

This guide describes how to connect host desktop IDEs—including VS Code, Cursor, JetBrains Gateway, GoLand, CLion, WebStorm, and PyCharm—to isolated agent containers.

---

## How It Works

Fleet provisions a rootless OpenSSH daemon (port 2222 inside container) for each running agent and assigns a dedicated local host SSH port.

- **Managed SSH Config**: Fleet automatically generates host aliases (`fleet-<name>`) in `~/.local/share/fleet/ssh_config`.
- **Zero-Touch Linking**: `Include ~/.local/share/fleet/ssh_config` is added to your workstation's `~/.ssh/config`.
- **Dedicated Keys**: All connections authenticate securely using `~/.ssh/fleet` without exposing your personal host SSH keys to the container.

---

## 1. Installing IDE Plugins & SSH Integration

Use the `fleet plugin add` command to enable IDE integration:

### VS Code & Cursor Integration

```bash
fleet plugin add vscode
```

**Expected Output:**

```text
Fleet VS Code Integration
=========================
Status:     VS Code Remote-SSH configuration verified
SSH Config: Linked managed config into ~/.ssh/config

Next Steps:
  Run 'fleet agent open <name>' to launch an agent in VS Code.
```

### JetBrains Gateway & Remote Development Integration

```bash
fleet plugin add gateway
```

**Expected Output:**

```text
Fleet JetBrains Gateway Plugin
=====================================
Plugin:     Fleet JetBrains Gateway Provider (v0.1.0)
Status:     Installed
SSH Config: Linked managed config into ~/.ssh/config
```

### JetBrains Toolbox Integration

```bash
fleet plugin add toolbox
```

### Check Plugin Status

View supported and installed IDE integrations:

```bash
fleet plugin list
```

**Expected Output:**

```text
PLUGIN      TARGET      STATUS       DESCRIPTION
gateway     jetbrains   Installed    JetBrains Gateway and IntelliJ IDEs (GoLand, CLion, WebStorm, PyCharm)
toolbox     jetbrains   Installed    JetBrains Toolbox desktop app provider & native SSH sync
vscode      vscode      Installed    VS Code Remote-SSH and Claude Code integration
```

---

## 2. Launching Agents in Desktop IDEs

Launch a thin-client IDE session against a running agent using `fleet agent open`:

### Opening in VS Code

```bash
fleet agent open coder in code
```

This launches VS Code configured to open `vscode-remote://ssh-remote+fleet-coder/home/agent/workspace`.

### Opening in Cursor

```bash
fleet agent open coder in cursor
```

### Opening in JetBrains IDEs (GoLand, CLion, PyCharm)

```bash
fleet agent open coder in goland
```

Or target JetBrains Gateway directly:

```bash
fleet agent open coder in gateway
```

### Interactive IDE Selection

If you omit `in <ide>`, Fleet scans your workstation `PATH` for installed IDEs and prompts you interactively:

```bash
fleet agent open coder
```

**Expected Output:**

```text
Select an IDE to open agent "coder":
  1) Visual Studio Code (/usr/bin/code)
  2) GoLand (/usr/bin/goland)
Enter selection [1-2]: 1
Opening coder in Visual Studio Code (Host: fleet-coder)...
```

---

## 3. Preparing SSH Configuration Without Launching (`--no-launch`)

To generate SSH connection parameters or URI targets without automatically launching the desktop application:

```bash
fleet agent open coder in code --no-launch
```

**Expected Output:**

```text
SSH host alias prepared: fleet-coder (Port: 38472)
Visual Studio Code Remote URI: vscode-remote://ssh-remote+fleet-coder/home/agent/workspace
Launch command: code --file-uri vscode-remote://ssh-remote+fleet-coder/home/agent/workspace
```

---

## 4. Manual SSH Terminal Access

Because Fleet maintains host stanzas in `~/.ssh/config`, you can connect to any running agent directly from your favorite terminal using standard `ssh`:

```bash
ssh fleet-coder
```

---

## 5. Removing IDE Integrations

To remove managed IDE links and un-link configuration entries:

```bash
fleet plugin remove vscode
fleet plugin remove gateway
fleet plugin remove toolbox
```

**Expected Output:**

```text
Fleet VS Code Integration
=========================
Status:     Uninstalled
SSH Config: Unlinked managed config from ~/.ssh/config
```
