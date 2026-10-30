// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

import Module from 'module';

export class TreeItem {
  constructor(public label: string, public collapsibleState?: any) {}
  description?: string;
  tooltip?: any;
  contextValue?: string;
  iconPath?: any;
  command?: any;
}

export enum TreeItemCollapsibleState {
  None = 0,
  Collapsed = 1,
  Expanded = 2
}

export class ThemeIcon {
  constructor(public id: string, public color?: any) {}
}

export class ThemeColor {
  constructor(public id: string) {}
}

export class MarkdownString {
  public value = '';
  appendMarkdown(str: string) {
    this.value += str;
    return this;
  }
}

export class EventEmitter {
  fire() {}
  event = () => {};
  dispose() {}
}

export const mockVscode = {
  TreeItem,
  TreeItemCollapsibleState,
  ThemeIcon,
  ThemeColor,
  MarkdownString,
  EventEmitter,
  workspace: {
    getConfiguration: () => ({
      get: (key: string) => {
        if (key === 'cliPath') {
          return 'fleet';
        }
        if (key === 'refreshInterval') {
          return 5000;
        }
        return undefined;
      }
    })
  },
  window: {
    showErrorMessage: () => {},
    showInformationMessage: () => {},
    showWarningMessage: () => {},
    createTreeView: () => ({ dispose: () => {} }),
    registerTreeDataProvider: () => ({ dispose: () => {} })
  },
  commands: {
    registerCommand: () => ({ dispose: () => {} })
  },
  env: {
    openExternal: async () => true
  },
  Uri: {
    parse: (s: string) => s
  }
};

// Register mock in Node module loader cache
const originalRequire = Module.prototype.require;
(Module.prototype as any).require = function (this: any, request: string) {
  if (request === 'vscode') {
    return mockVscode;
  }
  return originalRequire.apply(this, arguments as any);
};
