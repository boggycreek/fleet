// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

import * as vscode from 'vscode';
import { execFile } from 'child_process';
import { promisify } from 'util';
import { FleetTreeDataProvider, FleetAgentItem, AgentStatus } from './fleetTreeDataProvider';

const execFileAsync = promisify(execFile);

function getCliPath(): string {
  const config = vscode.workspace.getConfiguration('fleet');
  return config.get<string>('cliPath') || 'fleet';
}

export function activate(context: vscode.ExtensionContext) {
  const treeDataProvider = new FleetTreeDataProvider();
  context.subscriptions.push(treeDataProvider);

  const treeView = vscode.window.createTreeView('fleet-agents', {
    treeDataProvider,
    showCollapseAll: false
  });
  context.subscriptions.push(treeView);

  // Command: Refresh
  const refreshCmd = vscode.commands.registerCommand('fleet.refresh', () => {
    treeDataProvider.refresh();
  });
  context.subscriptions.push(refreshCmd);

  // Command: One-Click Attach via Remote-SSH
  const attachCmd = vscode.commands.registerCommand('fleet.attach', async (item?: FleetAgentItem | string) => {
    let agentName: string | undefined;
    let agentState: string | undefined;

    if (item instanceof FleetAgentItem) {
      agentName = item.agent.name;
      agentState = item.agent.container_state;
    } else if (typeof item === 'string') {
      agentName = item;
    } else {
      try {
        const statuses = await treeDataProvider.fetchAgentStatuses();
        if (statuses.length === 0) {
          vscode.window.showInformationMessage('No Fleet agents found. Create an agent using CLI: fleet agent create <name>');
          return;
        }
        const picked = await vscode.window.showQuickPick(
          statuses.map(s => ({
            label: s.name,
            description: `${s.role} (${s.container_state})`,
            status: s
          })),
          { placeHolder: 'Select a Fleet agent to attach' }
        );
        if (!picked) {
          return;
        }
        agentName = picked.status.name;
        agentState = picked.status.container_state;
      } catch (err: any) {
        vscode.window.showErrorMessage(`Failed to list agents: ${err.message || String(err)}`);
        return;
      }
    }

    if (!agentName) {
      return;
    }

    if (agentState === 'HOST') {
      vscode.window.showInformationMessage(`Agent '${agentName}' is a direct host execution agent (no container SSH required).`);
      return;
    }

    if (agentState === 'stopped' || agentState === 'exited') {
      const choice = await vscode.window.showWarningMessage(
        `Agent '${agentName}' is currently stopped. Would you like to start it before attaching?`,
        'Start and Attach',
        'Cancel'
      );
      if (choice !== 'Start and Attach') {
        return;
      }
      try {
        await vscode.window.withProgress(
          {
            location: vscode.ProgressLocation.Notification,
            title: `Starting Fleet agent '${agentName}'...`,
            cancellable: false
          },
          async () => {
            const cliPath = getCliPath();
            await execFileAsync(cliPath, ['agent', 'start', agentName!], { timeout: 30000 });
          }
        );
        treeDataProvider.refresh();
      } catch (err: any) {
        vscode.window.showErrorMessage(`Failed to start agent '${agentName}': ${err.message || String(err)}`);
        return;
      }
    }

    const hostAlias = `fleet-${agentName}`;
    const uriString = `vscode://vscode-remote/ssh-remote+${hostAlias}/home/agent/workspace`;
    const uri = vscode.Uri.parse(uriString);

    try {
      await vscode.env.openExternal(uri);
    } catch (err: any) {
      vscode.window.showErrorMessage(`Failed to open Remote-SSH session for '${agentName}': ${err.message || String(err)}`);
    }
  });
  context.subscriptions.push(attachCmd);

  // Command: Start Agent
  const startCmd = vscode.commands.registerCommand('fleet.start', async (item?: FleetAgentItem) => {
    const agentName = item ? item.agent.name : await promptForAgentName('start');
    if (!agentName) {
      return;
    }

    try {
      await vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: `Starting Fleet agent '${agentName}'...`,
          cancellable: false
        },
        async () => {
          const cliPath = getCliPath();
          await execFileAsync(cliPath, ['agent', 'start', agentName], { timeout: 30000 });
        }
      );
      vscode.window.showInformationMessage(`Fleet agent '${agentName}' started successfully.`);
      treeDataProvider.refresh();
    } catch (err: any) {
      vscode.window.showErrorMessage(`Failed to start agent '${agentName}': ${err.message || String(err)}`);
    }
  });
  context.subscriptions.push(startCmd);

  // Command: Stop Agent
  const stopCmd = vscode.commands.registerCommand('fleet.stop', async (item?: FleetAgentItem) => {
    const agentName = item ? item.agent.name : await promptForAgentName('stop');
    if (!agentName) {
      return;
    }

    try {
      await vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: `Stopping Fleet agent '${agentName}'...`,
          cancellable: false
        },
        async () => {
          const cliPath = getCliPath();
          await execFileAsync(cliPath, ['agent', 'stop', agentName], { timeout: 30000 });
        }
      );
      vscode.window.showInformationMessage(`Fleet agent '${agentName}' stopped.`);
      treeDataProvider.refresh();
    } catch (err: any) {
      vscode.window.showErrorMessage(`Failed to stop agent '${agentName}': ${err.message || String(err)}`);
    }
  });
  context.subscriptions.push(stopCmd);

  // Configuration Change Listener
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration(e => {
      if (e.affectsConfiguration('fleet.refreshInterval')) {
        treeDataProvider.startAutoRefresh();
      }
      if (e.affectsConfiguration('fleet.cliPath')) {
        treeDataProvider.refresh();
      }
    })
  );
}

async function promptForAgentName(action: string): Promise<string | undefined> {
  return vscode.window.showInputBox({
    prompt: `Enter Fleet agent name to ${action}`,
    placeHolder: 'agent-name'
  });
}

export function deactivate() {}
