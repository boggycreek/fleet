// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/plugin"
)

func handlePlugin(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPluginUsage(stderr)
		return 1
	}

	action := strings.ToLower(args[0])
	switch action {
	case "help", "-h", "--help":
		printPluginUsage(stdout)
		return 0

	case "list":
		plugins := plugin.ListPlugins(paths)
		w := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "PLUGIN\tTARGET\tSTATUS\tDESCRIPTION")
		for _, p := range plugins {
			status := "Not Installed"
			if p.Installed {
				status = "Installed"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Target, status, p.Description)
		}
		_ = w.Flush()
		return 0

	case "add":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "Usage: %s plugin add <gateway|toolbox|vscode>\n", binaryName())
			return 1
		}
		return handlePluginAdd(ctx, paths, strings.ToLower(args[1]), stdout, stderr)

	case "remove":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "Usage: %s plugin remove <gateway|toolbox|vscode>\n", binaryName())
			return 1
		}
		return handlePluginRemove(ctx, paths, strings.ToLower(args[1]), stdout, stderr)

	default:
		bin := binaryName()
		fmt.Fprintf(stderr, "%s plugin: unknown command %q (see '%s plugin help')\n", bin, action, bin)
		return 1
	}
}

func handlePluginAdd(ctx context.Context, paths config.Paths, target string, stdout, stderr io.Writer) int {
	bin := binaryName()
	switch target {
	case "gateway":
		res, err := plugin.InstallGatewayPlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error installing gateway plugin: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet JetBrains Gateway Plugin")
		fmt.Fprintln(stdout, "=====================================")
		fmt.Fprintf(stdout, "Plugin:     %s (v%s)\n", res.PluginName, res.Version)
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigLinked {
			fmt.Fprintln(stdout, "SSH Config: Linked managed config into ~/.ssh/config")
		} else {
			fmt.Fprintln(stdout, "SSH Config: Already linked in ~/.ssh/config")
		}
		fmt.Fprintln(stdout, "\nInstalled Locations:")
		for _, loc := range res.InstalledPaths {
			fmt.Fprintf(stdout, "  - %s\n", loc)
		}
		fmt.Fprintln(stdout, "\nNext Steps:")
		fmt.Fprintln(stdout, "  1. Launch JetBrains Gateway or your JetBrains IDE (GoLand, CLion, WebStorm, PyCharm).")
		fmt.Fprintln(stdout, "  2. Connect to your running agents via the 'Fleet' provider")
		fmt.Fprintln(stdout, "     or select any 'fleet-<name>' host under SSH Connections.")
		return 0

	case "toolbox":
		res, err := plugin.InstallToolboxPlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error installing toolbox plugin: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet JetBrains Toolbox Plugin")
		fmt.Fprintln(stdout, "=====================================")
		fmt.Fprintf(stdout, "Plugin:     %s (v%s)\n", res.PluginName, res.Version)
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigLinked {
			fmt.Fprintln(stdout, "SSH Config: Linked managed config into ~/.ssh/config")
		} else {
			fmt.Fprintln(stdout, "SSH Config: Already linked in ~/.ssh/config")
		}
		fmt.Fprintln(stdout, "\nInstalled Locations:")
		for _, loc := range res.InstalledPaths {
			fmt.Fprintf(stdout, "  - %s\n", loc)
		}
		fmt.Fprintln(stdout, "\nNext Steps:")
		fmt.Fprintln(stdout, "  1. Restart JetBrains Toolbox.")
		fmt.Fprintln(stdout, "  2. Connect to running agents under 'SSH Connections' or the 'Fleet' provider.")
		return 0

	case "vscode":
		res, err := plugin.InstallVSCodePlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error configuring VS Code integration: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet VS Code Integration")
		fmt.Fprintln(stdout, "=========================")
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigLinked {
			fmt.Fprintln(stdout, "SSH Config: Linked managed config into ~/.ssh/config")
		} else {
			fmt.Fprintln(stdout, "SSH Config: Already linked in ~/.ssh/config")
		}
		fmt.Fprintln(stdout, "\nNext Steps:")
		fmt.Fprintf(stdout, "  Run '%s agent open <name>' to launch an agent in VS Code.\n", bin)
		return 0

	default:
		fmt.Fprintf(stderr, "%s plugin: unknown plugin %q (see '%s plugin help')\n", bin, target, bin)
		return 1
	}
}

func handlePluginRemove(ctx context.Context, paths config.Paths, target string, stdout, stderr io.Writer) int {
	bin := binaryName()
	switch target {
	case "gateway":
		res, err := plugin.RemoveGatewayPlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error removing gateway plugin: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet JetBrains Gateway Plugin")
		fmt.Fprintln(stdout, "=====================================")
		fmt.Fprintf(stdout, "Plugin:     %s\n", res.PluginName)
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigUnlinked {
			fmt.Fprintln(stdout, "SSH Config: Unlinked managed config from ~/.ssh/config")
		}
		if len(res.RemovedPaths) > 0 {
			fmt.Fprintln(stdout, "\nRemoved Locations:")
			for _, loc := range res.RemovedPaths {
				fmt.Fprintf(stdout, "  - %s\n", loc)
			}
		}
		return 0

	case "toolbox":
		res, err := plugin.RemoveToolboxPlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error removing toolbox plugin: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet JetBrains Toolbox Plugin")
		fmt.Fprintln(stdout, "=====================================")
		fmt.Fprintf(stdout, "Plugin:     %s\n", res.PluginName)
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigUnlinked {
			fmt.Fprintln(stdout, "SSH Config: Unlinked managed config from ~/.ssh/config")
		}
		if len(res.RemovedPaths) > 0 {
			fmt.Fprintln(stdout, "\nRemoved Locations:")
			for _, loc := range res.RemovedPaths {
				fmt.Fprintf(stdout, "  - %s\n", loc)
			}
		}
		return 0

	case "vscode":
		res, err := plugin.RemoveVSCodePlugin(ctx, paths, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%s error removing VS Code integration: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Fleet VS Code Integration")
		fmt.Fprintln(stdout, "=========================")
		fmt.Fprintf(stdout, "Status:     %s\n", res.Message)
		if res.SSHConfigUnlinked {
			fmt.Fprintln(stdout, "SSH Config: Unlinked managed config from ~/.ssh/config")
		}
		return 0

	default:
		fmt.Fprintf(stderr, "%s plugin: unknown plugin %q (see '%s plugin help')\n", bin, target, bin)
		return 1
	}
}

func printPluginUsage(out io.Writer) {
	fmt.Fprintf(out, `Fleet Plugin Manager

Usage:
  %s plugin <command> [target]

Commands:
  add <plugin>       Install and configure the specified IDE plugin
  remove <plugin>    Uninstall and remove the specified IDE plugin
  list               List supported and installed IDE plugins
  help               Show this help message

Supported Targets:
  gateway            JetBrains Gateway and IntelliJ IDEs (GoLand, CLion, WebStorm, PyCharm)
  toolbox            JetBrains Toolbox desktop app provider & native SSH sync
  vscode             VS Code Remote-SSH and Claude Code integration
`, binaryName())
}
