// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/devcontainer"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

// FleetImageInfo summarizes details of a custom Fleet OCI image.
type FleetImageInfo struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	ImageID    string `json:"image_id"`
	Created    string `json:"created"`
	Size       string `json:"size"`
}

func handleImage(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s image <list|generate|build|remove>\n", bin)
		return 1
	}

	sub := strings.ToLower(args[0])
	subArgs := args[1:]

	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, `Usage: %s image <command> [args...]

Commands:
  list [--json]
          List locally built and registered custom Fleet OCI images
  generate [--engine <type>] [--output <path>] [--devcontainer <path>]
          Synthesize a standalone multi-stage Containerfile from devcontainer.json
  build <name> [--engine <type>] [--dockerfile <path>] [--context <path>]
          Generate Containerfile and build/tag custom OCI image via rootless Podman
  remove <name>
          Remove custom image from local Podman storage
`, bin)
		return 0

	case "list", "ls":
		return handleImageList(ctx, paths, subArgs, stdout, stderr)

	case "generate":
		return handleImageGenerate(ctx, paths, subArgs, stdout, stderr)

	case "build":
		return handleImageBuild(ctx, paths, subArgs, stdout, stderr)

	case "remove", "rm", "delete":
		return handleImageRemove(ctx, paths, subArgs, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "%s image: unknown command %q (see '%s image help')\n", bin, sub, bin)
		return 1
	}
}

func handleImageList(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}

	cmd := runtime.CommandContext(ctx, "podman", "images", "--format", "json")
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s image list error: failed listing Podman images: %v\n", bin, err)
		return 1
	}

	var rawImages []struct {
		Repository string `json:"Repository"`
		Tag        string `json:"Tag"`
		ID         string `json:"Id"`
		Created    string `json:"Created"`
		CreatedAt  string `json:"CreatedAt"`
		Size       string `json:"Size"`
		Names      []string `json:"names"`
	}

	_ = json.Unmarshal(outBuf.Bytes(), &rawImages)

	var images []FleetImageInfo
	for _, item := range rawImages {
		repo := item.Repository
		tag := item.Tag
		if repo == "" && len(item.Names) > 0 {
			parts := strings.Split(item.Names[0], ":")
			repo = parts[0]
			if len(parts) > 1 {
				tag = parts[1]
			}
		}
		id := item.ID
		if len(id) > 12 {
			id = id[:12]
		}
		created := item.Created
		if created == "" {
			created = item.CreatedAt
		}

		images = append(images, FleetImageInfo{
			Repository: repo,
			Tag:        tag,
			ImageID:    id,
			Created:    created,
			Size:       item.Size,
		})
	}

	if jsonMode {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(images); err != nil {
			fmt.Fprintf(stderr, "%s image list JSON error: %v\n", bin, err)
			return 1
		}
		return 0
	}

	if len(images) == 0 {
		fmt.Fprintln(stdout, "No custom Fleet images found in local Podman storage.")
		return 0
	}

	fmt.Fprintf(stdout, "%-30s %-15s %-15s %-20s %-10s\n", "REPOSITORY", "TAG", "IMAGE ID", "CREATED", "SIZE")
	fmt.Fprintf(stdout, "%s\n", strings.Repeat("-", 92))
	for _, img := range images {
		fmt.Fprintf(stdout, "%-30s %-15s %-15s %-20s %-10s\n", img.Repository, img.Tag, img.ImageID, img.Created, img.Size)
	}
	return 0
}

func handleImageGenerate(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	engineType := "fleet-agent"
	outputPath := ""
	devcontainerPath := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--engine" && i+1 < len(args):
			engineType = args[i+1]
			i++
		case (arg == "--output" || arg == "-o") && i+1 < len(args):
			outputPath = args[i+1]
			i++
		case (arg == "--devcontainer" || arg == "-d") && i+1 < len(args):
			devcontainerPath = args[i+1]
			i++
		}
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "%s image generate error: failed getting current directory: %v\n", bin, err)
		return 1
	}

	if devcontainerPath == "" {
		foundPath, err := devcontainer.FindDevcontainerFile(workDir)
		if err != nil {
			fmt.Fprintf(stderr, "%s image generate error: %v\n", bin, err)
			return 1
		}
		devcontainerPath = foundPath
	}

	if outputPath == "" {
		outputPath = filepath.Join(filepath.Dir(devcontainerPath), "Containerfile.fleet")
	}

	content, res, err := devcontainer.GenerateContainerfileFromFile(devcontainerPath, engineType)
	if err != nil {
		fmt.Fprintf(stderr, "%s image generate error: %v\n", bin, err)
		return 1
	}

	if res != nil && len(res.Warnings) > 0 {
		for _, w := range res.Warnings {
			fmt.Fprintf(stderr, "%s image generate warning: %s\n", bin, w)
		}
	}

	if dir := filepath.Dir(outputPath); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(stderr, "%s image generate error: failed creating output directory %s: %v\n", bin, dir, err)
			return 1
		}
	}

	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(stderr, "%s image generate error: failed writing output Containerfile %s: %v\n", bin, outputPath, err)
		return 1
	}

	fmt.Fprintf(stdout, "Synthesized multi-stage Containerfile:\n")
	fmt.Fprintf(stdout, "    Source: %s\n", devcontainerPath)
	fmt.Fprintf(stdout, "    Engine: %s\n", devcontainer.ResolveEngineImage(engineType))
	fmt.Fprintf(stdout, "    Output: %s\n", outputPath)
	return 0
}

func handleImageBuild(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var imageName, engineType, dockerfilePath, contextPath string

	engineType = "fleet-agent"

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--engine" && i+1 < len(args):
			engineType = args[i+1]
			i++
		case (arg == "--dockerfile" || arg == "-f") && i+1 < len(args):
			dockerfilePath = args[i+1]
			i++
		case (arg == "--context" || arg == "-c") && i+1 < len(args):
			contextPath = args[i+1]
			i++
		case !strings.HasPrefix(arg, "-") && imageName == "":
			imageName = arg
		}
	}

	if imageName == "" {
		fmt.Fprintf(stderr, "Usage: %s image build <name> [--engine <type>] [--dockerfile <path>] [--context <path>]\n", bin)
		return 1
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "%s image build error: failed getting working directory: %v\n", bin, err)
		return 1
	}

	if contextPath == "" {
		contextPath = workDir
	}

	if dockerfilePath == "" {
		defaultContainerfile := filepath.Join(contextPath, ".devcontainer", "Containerfile.fleet")
		if _, err := os.Stat(defaultContainerfile); err == nil {
			dockerfilePath = defaultContainerfile
		} else if devPath, err := devcontainer.FindDevcontainerFile(contextPath); err == nil {
			// Auto-generate Containerfile
			genOut := filepath.Join(filepath.Dir(devPath), "Containerfile.fleet")
			content, res, genErr := devcontainer.GenerateContainerfileFromFile(devPath, engineType)
			if genErr != nil {
				fmt.Fprintf(stderr, "%s image build error generating Containerfile: %v\n", bin, genErr)
				return 1
			}
			if res != nil && len(res.Warnings) > 0 {
				for _, w := range res.Warnings {
					fmt.Fprintf(stderr, "%s image build warning: %s\n", bin, w)
				}
			}
			_ = os.MkdirAll(filepath.Dir(genOut), 0755)
			if writeErr := os.WriteFile(genOut, []byte(content), 0644); writeErr != nil {
				fmt.Fprintf(stderr, "%s image build error saving Containerfile: %v\n", bin, writeErr)
				return 1
			}
			dockerfilePath = genOut
		} else {
			fmt.Fprintf(stderr, "%s image build error: no --dockerfile specified and no .devcontainer/devcontainer.json found in %s\n", bin, contextPath)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Building Fleet image %s using %s...\n", imageName, dockerfilePath)

	buildCmd := runtime.CommandContext(ctx, "podman", "build", "-t", imageName, "-f", dockerfilePath, contextPath)
	buildCmd.Stdout = stdout
	buildCmd.Stderr = stderr

	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s image build error: podman build failed: %v\n", bin, err)
		return 1
	}

	fmt.Fprintf(stdout, "Successfully built and tagged image %s\n", imageName)
	return 0
}

func handleImageRemove(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var imageName string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && imageName == "" {
			imageName = arg
		}
	}

	if imageName == "" {
		fmt.Fprintf(stderr, "Usage: %s image remove <name>\n", bin)
		return 1
	}

	rmCmd := runtime.CommandContext(ctx, "podman", "rmi", imageName)
	rmCmd.Stdout = stdout
	rmCmd.Stderr = stderr

	if err := rmCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s image remove error: failed removing image %s: %v\n", bin, imageName, err)
		return 1
	}

	fmt.Fprintf(stdout, "Removed image %s from local Podman storage\n", imageName)
	return 0
}
