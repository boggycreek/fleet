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
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/project"
)

func handleProject(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s project <create|list|doctor|link|delete>\n", bin)
		return 1
	}

	sub := strings.ToLower(args[0])
	subArgs := args[1:]

	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, `Usage: %s project <command> [args...]

Commands:
  create <name> [--org <org>] [--key <key>] [--description <desc>]
          Create a new project linked across Forgejo and SonarQube
  list [--json]
          List all registered projects with live Quality Gate scan status
  doctor [name] [--org <org>]
          Diagnose and auto-heal project linkages, tokens, and Quality Gates
  link <forgejo-repo> [sonar-project]
          Link an existing Forgejo repository with a SonarQube project
  delete <name> [--org <org>] [--force]
          Delete project metadata and optionally remote assets
`, bin)
		return 0

	case "create":
		return handleProjectCreate(ctx, paths, subArgs, stdout, stderr)

	case "list":
		return handleProjectList(ctx, paths, subArgs, stdout, stderr)

	case "doctor":
		return handleProjectDoctor(ctx, paths, subArgs, stdout, stderr)

	case "link":
		return handleProjectLink(ctx, paths, subArgs, stdout, stderr)

	case "delete", "rm":
		return handleProjectDelete(ctx, paths, subArgs, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "%s project: unknown command %q (see '%s project help')\n", bin, sub, bin)
		return 1
	}
}

func handleProjectCreate(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var name, org, key, desc string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--org" && i+1 < len(args):
			org = args[i+1]
			i++
		case arg == "--key" && i+1 < len(args):
			key = args[i+1]
			i++
		case (arg == "--description" || arg == "-d") && i+1 < len(args):
			desc = args[i+1]
			i++
		case !strings.HasPrefix(arg, "-") && name == "":
			name = arg
		}
	}

	if name == "" {
		fmt.Fprintf(stderr, "Usage: %s project create <name> [--org <org>] [--key <key>] [--description <desc>]\n", bin)
		return 1
	}

	mgr := project.NewManager(paths)
	p, err := mgr.Create(ctx, name, org, key, desc)
	if err != nil {
		fmt.Fprintf(stderr, "%s project create error: %v\n", bin, err)
		return 1
	}

	repoURL := p.ForgejoURL
	if repoURL == "" {
		repoURL = p.GiteaURL
	}
	fmt.Fprintf(stdout, "Created project %s / %s:\n", p.Org, p.Name)
	fmt.Fprintf(stdout, "    Forgejo:     %s\n", repoURL)
	fmt.Fprintf(stdout, "    SonarQube:   %s\n", p.SonarURL)
	fmt.Fprintf(stdout, "    Scan Status: %s\n", p.ScanStatus)
	return 0
}

func handleProjectList(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}

	mgr := project.NewManager(paths)
	projects, err := mgr.List(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s project list error: %v\n", bin, err)
		return 1
	}

	if jsonMode {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(projects); err != nil {
			fmt.Fprintf(stderr, "%s project list JSON error: %v\n", bin, err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, project.FormatList(projects))
	return 0
}

func handleProjectDoctor(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	var name, org string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--org" && i+1 < len(args):
			org = args[i+1]
			i++
		case !strings.HasPrefix(arg, "-") && name == "":
			name = arg
		}
	}

	mgr := project.NewManager(paths)
	bin := binaryName()

	if name != "" {
		rep, err := mgr.Doctor(ctx, org, name)
		if err != nil {
			fmt.Fprintf(stderr, "%s project doctor error: %v\n", bin, err)
			return 1
		}
		fmt.Fprint(stdout, project.FormatDoctorReport(rep))
		if rep.UnrepairableCount > 0 {
			return 1
		}
		return 0
	}

	reports, err := mgr.DoctorAll(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s project doctor error: %v\n", bin, err)
		return 1
	}
	if len(reports) == 0 {
		fmt.Fprintln(stdout, "No projects registered to diagnose.")
		return 0
	}

	hasUnrepairable := false
	for i, rep := range reports {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprint(stdout, project.FormatDoctorReport(rep))
		if rep.UnrepairableCount > 0 {
			hasUnrepairable = true
		}
	}

	if hasUnrepairable {
		return 1
	}
	return 0
}

func handleProjectLink(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var repoRef, sonarProject string

	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			if repoRef == "" {
				repoRef = arg
			} else if sonarProject == "" {
				sonarProject = arg
			}
		}
	}

	if repoRef == "" {
		fmt.Fprintf(stderr, "Usage: %s project link <forgejo-repo> [sonar-project]\n", bin)
		return 1
	}

	mgr := project.NewManager(paths)
	p, err := mgr.Link(ctx, repoRef, sonarProject)
	if err != nil {
		fmt.Fprintf(stderr, "%s project link error: %v\n", bin, err)
		return 1
	}

	repoURL := p.ForgejoURL
	if repoURL == "" {
		repoURL = p.GiteaURL
	}
	fmt.Fprintf(stdout, "Linked project %s / %s:\n", p.Org, p.Name)
	fmt.Fprintf(stdout, "    Forgejo:     %s\n", repoURL)
	fmt.Fprintf(stdout, "    SonarQube:   %s\n", p.SonarURL)
	fmt.Fprintf(stdout, "    Scan Status: %s\n", p.ScanStatus)
	return 0
}

func handleProjectDelete(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var name, org string
	force := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--force" || arg == "-f"):
			force = true
		case arg == "--org" && i+1 < len(args):
			org = args[i+1]
			i++
		case !strings.HasPrefix(arg, "-") && name == "":
			name = arg
		}
	}

	if name == "" {
		fmt.Fprintf(stderr, "Usage: %s project delete <name> [--org <org>] [--force]\n", bin)
		return 1
	}

	mgr := project.NewManager(paths)
	if err := mgr.Delete(ctx, org, name, force); err != nil {
		fmt.Fprintf(stderr, "%s project delete error: %v\n", bin, err)
		return 1
	}

	if org == "" {
		org = config.GetDefaultProjectOrg()
	}
	if force {
		fmt.Fprintf(stdout, "Project %s / %s deleted (including remote assets).\n", org, name)
	} else {
		fmt.Fprintf(stdout, "Project %s / %s unlinked (metadata removed).\n", org, name)
	}
	return 0
}
