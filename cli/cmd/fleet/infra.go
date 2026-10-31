// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/doctor"
	"github.com/boggycreek/fleet/cli/pkg/lifecycle"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func handleInfra(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s infra <up|down|list|info|doctor|topology>\n", bin)
		return 1
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, `Usage: %s infra <command>

Commands:
  up        Start shared Valkey, Forgejo, and PostgreSQL services via Podman Compose
  down      Stop shared infrastructure services
  list      Show status of running infrastructure containers
  info      Display connection strings and web URLs for all shared infrastructure
  doctor    Diagnose shared infrastructure storage, containers, and services, and auto-heal defects
  topology  Display and generate 128k KV cache topology presets for local inference (ADR 00055)
`, bin)
		return 0

	case "up":
		fmt.Fprintln(stdout, "Starting shared infrastructure (Valkey, Forgejo & PostgreSQL)...")
		adminPass := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
		humanPass := os.Getenv("HUMAN_BACKPLANE_PASSWORD")
		humanName := os.Getenv("HUMAN_NAME")
		if err := runtime.StartInfraStack(ctx, paths, adminPass, humanPass, humanName); err != nil {
			fmt.Fprintf(stderr, "%s infra error: %v\n", bin, err)
			return 1
		}
		_, _ = lifecycle.EnsureOperatorKeys(ctx, paths, humanName)
		fmt.Fprintln(stdout, "Shared infrastructure is online.")
		return 0

	case "down":
		fmt.Fprintln(stdout, "Stopping shared infrastructure...")
		if err := runtime.StopInfraStack(ctx); err != nil {
			fmt.Fprintf(stderr, "%s infra error: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, "Shared infrastructure stopped.")
		return 0

	case "doctor":
		report, err := doctor.DiagnoseAndHealInfra(ctx, paths)
		if err != nil && report == nil {
			fmt.Fprintf(stderr, "%s infra doctor error: %v\n", bin, err)
			return 1
		}
		fmt.Fprint(stdout, doctor.FormatDoctorReport(report))
		if report.UnrepairableCount > 0 {
			return 1
		}
		return 0

	case "list":
		fmt.Fprintln(stdout, "Shared infrastructure status:")
		list, err := runtime.InspectInfraStack(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s infra error: %v\n", bin, err)
			return 1
		}
		fmtFmt := "%-28s %s\n"
		fmt.Fprintf(stdout, fmtFmt, "SERVICE", "STATUS")
		for _, item := range list {
			displayName := item.ID
			if len(item.Names) > 0 && item.Names[0] != "" {
				displayName = item.Names[0]
			}
			fmt.Fprintf(stdout, fmtFmt, displayName, item.State)
		}
		return 0

	case "info":
		showSecrets := false
		jsonOutput := false
		for _, arg := range args[1:] {
			switch arg {
			case "--show-secrets", "-s":
				showSecrets = true
			case "--json":
				jsonOutput = true
			default:
				fmt.Fprintf(stderr, "%s infra info: unknown flag %q\n", bin, arg)
				return 1
			}
		}

		info, err := runtime.GetInfraInfo(ctx, paths, showSecrets)
		if err != nil {
			fmt.Fprintf(stderr, "%s infra info error: %v\n", bin, err)
			return 1
		}

		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(info); err != nil {
				fmt.Fprintf(stderr, "%s infra info error: %v\n", bin, err)
				return 1
			}
			return 0
		}

		fmt.Fprint(stdout, runtime.FormatInfraInfo(info))
		return 0

	case "topology":
		return handleInfraTopology(args[1:], stdout, stderr)

	default:
		fmt.Fprintf(stderr, "%s infra: unknown command %q\n", bin, sub)
		return 1
	}
}

func handleInfraTopology(args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprint(stdout, config.FormatTopologyTable(config.GetTopologyPresets()))
		return 0
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, `Usage: %s infra topology [list|show <preset>|args <preset>] [--json]

Commands:
  list             Show comparison table of all available topology presets (default)
  show <preset>    Show full configuration, memory budget, and launch command for a preset
  args <preset>    Print only the llama-server CLI flags for the preset
`, bin)
		return 0

	case "list":
		if len(args) > 1 && args[1] == "--json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(config.GetTopologyPresets())
			return 0
		}
		fmt.Fprint(stdout, config.FormatTopologyTable(config.GetTopologyPresets()))
		return 0

	case "--json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(config.GetTopologyPresets())
		return 0

	case "show":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "Usage: %s infra topology show <preset> [--json]\n", bin)
			return 1
		}
		preset, err := config.GetTopologyPreset(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "%s infra topology error: %v\n", bin, err)
			return 1
		}
		if len(args) > 2 && args[2] == "--json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(preset)
			return 0
		}
		fmt.Fprint(stdout, config.FormatTopologyDetail(preset))
		return 0

	case "args":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "Usage: %s infra topology args <preset>\n", bin)
			return 1
		}
		preset, err := config.GetTopologyPreset(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "%s infra topology error: %v\n", bin, err)
			return 1
		}
		fmt.Fprintln(stdout, preset.LlamaServerArgsString())
		return 0

	default:
		preset, err := config.GetTopologyPreset(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "%s infra topology error: %v\n", bin, err)
			return 1
		}
		if len(args) > 1 && args[1] == "--json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(preset)
			return 0
		}
		fmt.Fprint(stdout, config.FormatTopologyDetail(preset))
		return 0
	}
}
