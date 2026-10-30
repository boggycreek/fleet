---
name: fleet-vscode-extension-design
description: >-
  Architecture and UX design guide for the host-side Fleet VS Code management extension,
  covering extension lifecycle, TreeDataProvider design, Remote-SSH attachment, command palette actions,
  and status indicators.
---

<!-- Copyright (c) 2026 Boggy Creek Software LLC. Use of this source code is governed by an MIT-style license that can be found in the LICENSE file. -->

# Fleet VS Code Extension — Architecture & UX Guide

## Overview
The Fleet VS Code extension (`plugins/vscode`) provides a visual sidebar tree view within Visual Studio Code to monitor, manage, and attach to active and stopped Fleet agent sandboxes using VS Code Remote - SSH (`ms-vscode-remote.remote-ssh`).

## Extension Lifecycle
1. **Activation**:
   - Activated automatically when the `fleet-sidebar` view container or `fleet-agents` view is accessed (`onView:fleet-agents`).
   - Registers `FleetTreeDataProvider` with VS Code's tree view API.
   - Binds command palette and inline menu handlers (`fleet.refresh`, `fleet.attach`, `fleet.start`, `fleet.stop`).
   - Initializes a background polling timer (default 5 seconds) to automatically update agent statuses in real time.

2. **Deactivation**:
   - Clears background refresh polling timers to prevent memory or process leaks upon extension host teardown.

## Architecture & Data Flow

```
+------------------------------------+
|        VS Code Extension           |
|  (FleetTreeDataProvider & View)    |
+-----------------+------------------+
                  |
         execFile('fleet agent list --json')
                  |
                  v
+-----------------+------------------+
|          Fleet Host CLI            |
|       (fleet agent list)           |
+-----------------+------------------+
                  |
        JSON Agent Status List
                  |
                  v
+-----------------+------------------+
|    FleetTreeDataProvider Parser    |
+-----------------+------------------+
                  |
       Renders FleetAgentItems
                  |
      User Clicks "Attach (Plug)"
                  |
                  v
+-----------------+------------------+
| VS Code Remote - SSH Connection    |
| vscode://vscode-remote/ssh-remote  |
| +fleet-<name>/home/agent/workspace |
+------------------------------------+
```

## TreeDataProvider Design (`FleetTreeDataProvider`)
- **JSON Input Structure**:
  Parses the JSON array emitted by `fleet agent list --json`:
  ```json
  [
    {
      "name": "dev-bot",
      "role": "coding-agent",
      "image": "fleet-native:latest",
      "container_name": "fleet-agent-dev-bot",
      "container_state": "running",
      "ssh_port": 41235,
      "ide_connect": "agent@localhost:41235"
    }
  ]
  ```

- **Tree Items (`FleetAgentItem`)**:
  - `label`: Agent name (e.g., `dev-bot`).
  - `description`: Role and status summary (e.g. `coding-agent (running)` or `HOST`).
  - `tooltip`: Rich Markdown string displaying agent details (role, image, container name, SSH port, connect endpoint).
  - `iconPath`: ThemeIcon indicators:
    - **Running**: `new vscode.ThemeIcon('play-circle', new vscode.ThemeColor('testing.iconPassed'))` (Green)
    - **Stopped**: `new vscode.ThemeIcon('circle-outline')` (Muted)
    - **Degraded**: `new vscode.ThemeIcon('warning', new vscode.ThemeColor('problemsWarningIcon.foreground'))` (Yellow)
    - **HOST**: `new vscode.ThemeIcon('desktop-download')` (Blue)
  - `contextValue`: `agent-running`, `agent-stopped`, `agent-degraded`, `agent-host`.
  - `command`: Default click handler pointing to `fleet.attach`.

## Command Palette & Context Actions

| Command | Action | Inline Context | UX Behavior |
| :--- | :--- | :--- | :--- |
| **`fleet.refresh`** | `treeDataProvider.refresh()` | View Title (`$(refresh)`) | Immediately re-queries `fleet agent list --json`. |
| **`fleet.attach`** | One-Click Attach | Item Inline (`$(plug)`) | Launches `vscode://vscode-remote/ssh-remote+fleet-<name>/home/agent/workspace`. If stopped, prompts operator to start first. |
| **`fleet.start`** | Start Agent | Item Inline (`$(play)`) | Runs `fleet agent start <name>`, shows progress notification, and refreshes view. |
| **`fleet.stop`** | Stop Agent | Item Inline (`$(stop-circle)`) | Runs `fleet agent stop <name>`, shows progress notification, and refreshes view. |

## Status Indicators
- **Running (`running`)**: Container active with dynamic SSH port mapped. Ready for direct one-click Remote-SSH attach.
- **Stopped (`stopped`)**: Container stopped. One-click attach prompts to start the agent.
- **Degraded (`degraded`)**: Container or network bridge error. Prompts operator to run `fleet agent doctor <name>`.
- **HOST (`HOST`)**: Direct host execution mode (no container). Remote-SSH is non-applicable.

## Auto-Refresh Strategy
- Configurable polling interval (`fleet.refreshInterval`, default 5000ms).
- Pauses polling when VS Code window loses focus or when execution fails repeatedly.
- Supports manual refresh at any time via view title button `$(refresh)`.
