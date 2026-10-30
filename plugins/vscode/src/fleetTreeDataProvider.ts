// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

import * as vscode from 'vscode';
import { execFile } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);

export interface AgentStatus {
  name: string;
  role: string;
  image: string;
  container_name: string;
  container_state: string;
  ssh_port: number;
  ide_connect: string;
}

export class FleetAgentItem extends vscode.TreeItem {
  constructor(public readonly agent: AgentStatus) {
    super(agent.name, vscode.TreeItemCollapsibleState.None);

    this.description = this.formatDescription(agent);
    this.tooltip = this.formatTooltip(agent);
    this.contextValue = this.determineContextValue(agent);
    this.iconPath = this.determineIcon(agent);

    // Default click behavior: trigger one-click attach
    this.command = {
      command: 'fleet.attach',
      title: 'Attach Remote-SSH',
      arguments: [this]
    };
  }

  private formatDescription(agent: AgentStatus): string {
    if (agent.container_state === 'HOST') {
      return `${agent.role || 'agent'} (HOST)`;
    }
    const state = agent.container_state || 'unknown';
    if (agent.ssh_port > 0) {
      return `${agent.role || 'agent'} (${state}, port:${agent.ssh_port})`;
    }
    return `${agent.role || 'agent'} (${state})`;
  }

  private formatTooltip(agent: AgentStatus): vscode.MarkdownString {
    const markdown = new vscode.MarkdownString();
    markdown.appendMarkdown(`**Fleet Agent:** \`${agent.name}\`\n\n`);
    markdown.appendMarkdown(`- **Role:** ${agent.role || '-'}\n`);
    markdown.appendMarkdown(`- **Status:** ${agent.container_state || '-'}\n`);
    markdown.appendMarkdown(`- **Image:** \`${agent.image || '-'}\`\n`);
    if (agent.container_name && agent.container_name !== '-') {
      markdown.appendMarkdown(`- **Container:** \`${agent.container_name}\`\n`);
    }
    if (agent.ssh_port > 0) {
      markdown.appendMarkdown(`- **SSH Port:** \`${agent.ssh_port}\`\n`);
    }
    if (agent.ide_connect && agent.ide_connect !== '-') {
      markdown.appendMarkdown(`- **Connect:** \`${agent.ide_connect}\`\n`);
    }
    return markdown;
  }

  private determineContextValue(agent: AgentStatus): string {
    switch (agent.container_state?.toLowerCase()) {
      case 'running':
        return 'agent-running';
      case 'stopped':
      case 'exited':
        return 'agent-stopped';
      case 'host':
        return 'agent-host';
      default:
        return 'agent-degraded';
    }
  }

  private determineIcon(agent: AgentStatus): vscode.ThemeIcon {
    switch (agent.container_state?.toLowerCase()) {
      case 'running':
        return new vscode.ThemeIcon('play-circle', new vscode.ThemeColor('testing.iconPassed'));
      case 'stopped':
      case 'exited':
        return new vscode.ThemeIcon('circle-outline');
      case 'host':
        return new vscode.ThemeIcon('desktop-download');
      default:
        return new vscode.ThemeIcon('warning', new vscode.ThemeColor('problemsWarningIcon.foreground'));
    }
  }
}

export class FleetTreeDataProvider implements vscode.TreeDataProvider<FleetAgentItem>, vscode.Disposable {
  private _onDidChangeTreeData: vscode.EventEmitter<FleetAgentItem | undefined | null | void> =
    new vscode.EventEmitter<FleetAgentItem | undefined | null | void>();
  readonly onDidChangeTreeData: vscode.Event<FleetAgentItem | undefined | null | void> =
    this._onDidChangeTreeData.event;

  private refreshTimer: NodeJS.Timeout | undefined;

  constructor() {
    this.startAutoRefresh();
  }

  public refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  public getTreeItem(element: FleetAgentItem): vscode.TreeItem {
    return element;
  }

  public async getChildren(element?: FleetAgentItem): Promise<FleetAgentItem[]> {
    if (element) {
      return [];
    }

    try {
      const statuses = await this.fetchAgentStatuses();
      return statuses.map(s => new FleetAgentItem(s));
    } catch (err: any) {
      const errorMessage = err?.message || String(err);
      vscode.window.showErrorMessage(`Fleet Extension Error: ${errorMessage}`);
      return [];
    }
  }

  public async fetchAgentStatuses(): Promise<AgentStatus[]> {
    const config = vscode.workspace.getConfiguration('fleet');
    const cliPath = config.get<string>('cliPath') || 'fleet';

    try {
      const { stdout } = await execFileAsync(cliPath, ['agent', 'list', '--json'], {
        timeout: 10000,
        env: { ...process.env }
      });

      const trimmed = stdout.trim();
      if (!trimmed) {
        return [];
      }

      const parsed = JSON.parse(trimmed);
      if (!Array.isArray(parsed)) {
        return [];
      }

      return parsed.map((item: any) => ({
        name: item.name || 'unnamed',
        role: item.role || '',
        image: item.image || '',
        container_name: item.container_name || '',
        container_state: item.container_state || 'unknown',
        ssh_port: typeof item.ssh_port === 'number' ? item.ssh_port : 0,
        ide_connect: item.ide_connect || ''
      }));
    } catch (err: any) {
      if (err.code === 'ENOENT') {
        throw new Error(`Fleet CLI binary '${cliPath}' not found. Please verify Fleet installation or update 'fleet.cliPath' setting.`);
      }
      throw err;
    }
  }

  public startAutoRefresh(): void {
    this.stopAutoRefresh();
    const config = vscode.workspace.getConfiguration('fleet');
    const interval = config.get<number>('refreshInterval') || 5000;

    if (interval > 0) {
      this.refreshTimer = setInterval(() => {
        this.refresh();
      }, interval);
    }
  }

  public stopAutoRefresh(): void {
    if (this.refreshTimer) {
      clearInterval(this.refreshTimer);
      this.refreshTimer = undefined;
    }
  }

  public dispose(): void {
    this.stopAutoRefresh();
    this._onDidChangeTreeData.dispose();
  }
}
