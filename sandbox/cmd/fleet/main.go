// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/boggycreek/sandbox/pkg/config"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes the fleet command.
func Run(args []string, stdout, stderr io.Writer) int {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunWithContext(sigCtx, args, stdout, stderr)
}

// RunWithContext executes the fleet command using the provided context.
func RunWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 1
	}

	domain := strings.ToLower(args[0])
	domainArgs := args[1:]

	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(stderr, "%s warning: failed to ensure directories: %v\n", binaryName(), err)
	}
	paths.LoadEnv()

	switch domain {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0

	case "agent":
		return handleAgent(ctx, paths, domainArgs, stdout, stderr)

	case "project":
		projectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return handleProject(projectCtx, paths, domainArgs, stdout, stderr)

	case "infra":
		infraCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return handleInfra(infraCtx, paths, domainArgs, stdout, stderr)

	case "plugin":
		pluginCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return handlePlugin(pluginCtx, paths, domainArgs, stdout, stderr)

	case "image":
		imageCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
		defer cancel()
		return handleImage(imageCtx, paths, domainArgs, stdout, stderr)

	case "update":
		updateTimeout := 30 * time.Minute
		envTimeout := os.Getenv("FLEET_UPDATE_TIMEOUT")
		if envTimeout == "" {
			envTimeout = os.Getenv("SNDBX_UPDATE_TIMEOUT")
		}
		if envTimeout != "" {
			if d, err := time.ParseDuration(envTimeout); err == nil && d > 0 {
				updateTimeout = d
			}
		}
		updateCtx, updateCancel := context.WithTimeout(ctx, updateTimeout)
		defer updateCancel()
		return handleUpdate(updateCtx, paths, domainArgs, stdout, stderr)

	case "gui":
		return handleGUI(ctx, paths, domainArgs, stdout, stderr)

	default:
		bin := binaryName()
		fmt.Fprintf(stderr, "%s: unknown command %q (see '%s help')\n", bin, domain, bin)
		return 1
	}
}

// binaryName returns the invocation name of the CLI binary ("ft" or "fleet").
func binaryName() string {
	if len(os.Args) > 0 {
		base := filepath.Base(os.Args[0])
		if base == "ft" {
			return "ft"
		}
	}
	return "fleet"
}

func printUsage(out io.Writer) {
	bin := binaryName()
	fmt.Fprintf(out, `Fleet CLI (%s)

Usage:
  %s <domain|command> [args...]

Domains:
  agent       Manage agent instance provisioning and container lifecycles
  image       Generate, build, list, and manage custom Fleet OCI images
  project     Manage and link Forgejo repositories and SonarQube projects
  infra       Manage shared Valkey, Forgejo, and PostgreSQL infrastructure
  plugin      Install and manage IDE plugins (JetBrains Gateway/Toolbox, VS Code)
  gui         Launch native desktop Backplane GUI client

Commands:
  update      Synchronize git repo, rebuild host CLIs, and compile all OCI images

Agent Commands:
  %s agent create <name> [as <type|oci>] [--image <type|oci>] [--role <role>] [--model-url <url>] [--model-name <name>] [--model-key <key>]
  %s agent start <name>
  %s agent tmux <name>
  %s agent open <name> [in <ide>] [--no-launch]
  %s agent ssh <name>
  %s agent ssh-config [name] [--all]
  %s agent doctor <name>
  %s agent stop [name] [--all]
  %s agent list [--json]
  %s agent clean <name>
  %s agent retire <name> [--force]

Project Commands:
  %s project create <name> [--org <org>] [--key <key>] [--description <desc>]
  %s project list [--json]
  %s project doctor [name] [--org <org>]
  %s project link <forgejo-repo> [sonar-project]
  %s project delete <name> [--org <org>] [--force]

Infra Commands:
  %s infra up
  %s infra down
  %s infra list
  %s infra info [--show-secrets] [--json]
  %s infra doctor

Image Commands:
  %s image list [--json]
  %s image generate [--engine <type>] [--output <path>] [--devcontainer <path>]
  %s image build <name> [--engine <type>] [--dockerfile <path>] [--context <path>]
  %s image remove <name>

Plugin Commands:
  %s plugin add <gateway|toolbox|vscode>
  %s plugin remove <gateway|toolbox|vscode>
  %s plugin list

Update Command:
  %s update

Run '%s <domain> help' for more details on each command.
`, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin, bin)
}
