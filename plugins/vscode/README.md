# Fleet — VS Code Extension

The Fleet VS Code extension provides visual management and one-click Remote-SSH attach for Fleet agent container sandboxes.

## Features
- **Sidebar Explorer**: Monitor active, stopped, degraded, and host agent instances directly from VS Code.
- **One-Click Remote-SSH Attach**: Click any running agent to attach to `/home/agent/workspace` using `ms-vscode-remote.remote-ssh`.
- **Lifecycle Controls**: Start and stop agents directly from the sidebar view or command palette.
- **Auto-Refresh**: Polls host Fleet CLI (`fleet agent list --json`) for real-time state synchronization.

## Extension Settings
- `fleet.cliPath`: Custom path to `fleet` CLI executable (default: `"fleet"`).
- `fleet.refreshInterval`: Auto-refresh interval in milliseconds (default: `5000`).

## Development & Building
1. Install dependencies:
   ```bash
   cd plugins/vscode
   npm install
   ```
2. Compile extension:
   ```bash
   npm run compile
   ```
