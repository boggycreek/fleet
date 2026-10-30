// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/runtime"
)

func TestFleetImageHelpAndEmpty(t *testing.T) {
	paths := config.GetPaths()
	var stdout, stderr bytes.Buffer

	// Empty args
	code := handleImage(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Usage: fleet image") {
		t.Errorf("expected exit code 1 and usage on empty args, got code %d, stderr: %s", code, stderr.String())
	}

	// Help flags
	for _, h := range []string{"help", "-h", "--help"} {
		stdout.Reset()
		stderr.Reset()
		code = handleImage(context.Background(), paths, []string{h}, &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "Usage: fleet image <command>") {
			t.Errorf("expected help output for %s, got code %d, stdout: %s", h, code, stdout.String())
		}
	}

	// Unknown subcommand
	stdout.Reset()
	stderr.Reset()
	code = handleImage(context.Background(), paths, []string{"unknowncmd"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unknown command \"unknowncmd\"") {
		t.Errorf("expected error for unknown subcommand, got: %s", stderr.String())
	}

	// Domain routing via runFleet
	code, out, _ := runFleet([]string{"image", "help"})
	if code != 0 || !strings.Contains(out, "Usage: fleet image <command>") {
		t.Errorf("expected runFleet image help to succeed, got: %s", out)
	}
}

func TestFleetImageList(t *testing.T) {
	paths := config.GetPaths()

	// Mock podman images
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "images" {
			// Return dummy podman images JSON
			jsonOutput := `[
				{
					"Repository": "localhost/custom-agent",
					"Tag": "latest",
					"Id": "a1b2c3d4e5f67890123456",
					"Created": "2 hours ago",
					"Size": "450MB"
				},
				{
					"names": ["localhost/other-agent:v1"],
					"Id": "shortid",
					"CreatedAt": "1 hour ago",
					"Size": "200MB"
				}
			]`
			return exec.Command("echo", jsonOutput)
		}
		return exec.Command("true")
	}))
	defer restore()

	var stdout, stderr bytes.Buffer

	// 1. Text format via "ls" alias
	code := handleImage(context.Background(), paths, []string{"ls"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "localhost/custom-agent") {
		t.Errorf("expected image in text list output, got code %d, stdout: %s", code, stdout.String())
	}

	// 2. JSON format
	stdout.Reset()
	stderr.Reset()
	code = handleImageList(context.Background(), paths, []string{"--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"repository": "localhost/custom-agent"`) {
		t.Errorf("expected json output, got code %d, stdout: %s", code, stdout.String())
	}

	// 3. Empty image list
	restoreEmpty := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "[]")
	}))
	defer restoreEmpty()

	stdout.Reset()
	stderr.Reset()
	code = handleImageList(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "No custom Fleet images found") {
		t.Errorf("expected no images found message, got: %s", stdout.String())
	}

	// 4. Podman error
	restoreErr := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}))
	defer restoreErr()

	stdout.Reset()
	stderr.Reset()
	code = handleImageList(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "failed listing Podman images") {
		t.Errorf("expected error on podman failure, got: %s", stderr.String())
	}
}

func TestFleetImageGenerate(t *testing.T) {
	paths := config.GetPaths()
	tmpDir := t.TempDir()

	devDir := filepath.Join(tmpDir, ".devcontainer")
	_ = os.MkdirAll(devDir, 0755)
	devPath := filepath.Join(devDir, "devcontainer.json")
	// Include a runArg that triggers a sanitization warning
	_ = os.WriteFile(devPath, []byte(`{ "name": "Test", "image": "golang:1.24", "runArgs": ["--privileged"] }`), 0644)

	// Change working directory to temp dir
	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origWd) }()

	var stdout, stderr bytes.Buffer

	// Default generate
	code := handleImageGenerate(context.Background(), paths, []string{"--engine", "claude"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Synthesized multi-stage Containerfile") {
		t.Errorf("expected generate to succeed, got code %d, stdout: %s, stderr: %s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "Sanitization: removed dangerous runArg") {
		t.Errorf("expected sanitization warning in stderr, got: %s", stderr.String())
	}

	outputFile := filepath.Join(devDir, "Containerfile.fleet")
	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("expected generated file %s to exist", outputFile)
	}

	content, _ := os.ReadFile(outputFile)
	if !strings.Contains(string(content), "FROM fleet-native-engine-claude:latest AS engine-harness") {
		t.Errorf("expected claude engine harness in generated file")
	}

	// Custom output path via -o and custom devcontainer file via -d
	stdout.Reset()
	stderr.Reset()
	customOut := filepath.Join(tmpDir, "custom", "Containerfile.custom")
	code = handleImageGenerate(context.Background(), paths, []string{"-d", devPath, "-o", customOut, "--engine", "opencode"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), customOut) {
		t.Errorf("expected generate custom output to succeed, got: %s", stdout.String())
	}

	// Error when devcontainer file is invalid JSON
	invalidDevPath := filepath.Join(tmpDir, "invalid.json")
	_ = os.WriteFile(invalidDevPath, []byte(`{ invalid json`), 0644)
	stdout.Reset()
	stderr.Reset()
	code = handleImageGenerate(context.Background(), paths, []string{"-d", invalidDevPath}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "failed parsing devcontainer.json") {
		t.Errorf("expected error on invalid devcontainer json, got: %s", stderr.String())
	}

	// Error when devcontainer missing
	emptyDir := t.TempDir()
	_ = os.Chdir(emptyDir)
	stdout.Reset()
	stderr.Reset()
	code = handleImageGenerate(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "no devcontainer.json found") {
		t.Errorf("expected error when no devcontainer.json found, got: %s", stderr.String())
	}
}

func TestFleetImageBuild(t *testing.T) {
	paths := config.GetPaths()
	tmpDir := t.TempDir()

	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origWd) }()

	// Mock podman build
	var recordedCmds []string
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		recordedCmds = append(recordedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		return exec.Command("true")
	}))
	defer restore()

	var stdout, stderr bytes.Buffer

	// 1. Missing image name error
	code := handleImageBuild(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Usage: fleet image build") {
		t.Errorf("expected error on missing image name, got: %s", stderr.String())
	}

	// 2. Build with explicit --dockerfile and --context
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	_ = os.WriteFile(dockerfilePath, []byte("FROM alpine\n"), 0644)

	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"my-fleet-image", "-f", dockerfilePath, "-c", tmpDir}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Successfully built and tagged image my-fleet-image") {
		t.Errorf("expected successful build, got code %d, stdout: %s, stderr: %s", code, stdout.String(), stderr.String())
	}

	// 3. Build with existing default Containerfile.fleet (no -f specified)
	devDir := filepath.Join(tmpDir, ".devcontainer")
	_ = os.MkdirAll(devDir, 0755)
	defaultContainerfile := filepath.Join(devDir, "Containerfile.fleet")
	_ = os.WriteFile(defaultContainerfile, []byte("FROM ubuntu:22.04\n"), 0644)

	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"existing-default-image"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Successfully built and tagged image existing-default-image") {
		t.Errorf("expected successful build using existing Containerfile.fleet, got: %s", stdout.String())
	}

	// 4. Build with auto-generated Containerfile from devcontainer.json (when Containerfile.fleet doesn't exist)
	_ = os.Remove(defaultContainerfile)
	_ = os.WriteFile(filepath.Join(devDir, "devcontainer.json"), []byte(`{ "name": "BuildDev", "image": "ubuntu:22.04", "runArgs": ["--privileged"] }`), 0644)

	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"auto-built-image", "--engine", "pig"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Successfully built and tagged image auto-built-image") {
		t.Errorf("expected successful auto-build, got code %d, stdout: %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Sanitization: removed dangerous runArg") {
		t.Errorf("expected sanitization warning in stderr, got: %s", stderr.String())
	}

	// 5. Auto-generation error during build (corrupted devcontainer.json)
	_ = os.Remove(defaultContainerfile)
	_ = os.WriteFile(filepath.Join(devDir, "devcontainer.json"), []byte(`{ corrupted json`), 0644)
	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"corrupted-dev-image"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "generating Containerfile") {
		t.Errorf("expected generation error during build, got: %s", stderr.String())
	}

	// 6. Missing dockerfile and devcontainer file error
	emptyDir := t.TempDir()
	_ = os.Chdir(emptyDir)
	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"no-devcontainer-image"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "no .devcontainer/devcontainer.json found") {
		t.Errorf("expected missing devcontainer error, got: %s", stderr.String())
	}

	// 7. Podman build failure
	restoreErr := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}))
	defer restoreErr()

	stdout.Reset()
	stderr.Reset()
	code = handleImageBuild(context.Background(), paths, []string{"fail-image", "-f", dockerfilePath}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "podman build failed") {
		t.Errorf("expected podman build failure message, got: %s", stderr.String())
	}
}

func TestFleetImageRemove(t *testing.T) {
	paths := config.GetPaths()

	var recordedCmds []string
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		recordedCmds = append(recordedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		return exec.Command("true")
	}))
	defer restore()

	var stdout, stderr bytes.Buffer

	// 1. Missing image name error
	code := handleImageRemove(context.Background(), paths, []string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Usage: fleet image remove") {
		t.Errorf("expected error on missing image name, got: %s", stderr.String())
	}

	// 2. Successful removal via "rm" and "delete" aliases
	for _, alias := range []string{"rm", "delete", "remove"} {
		stdout.Reset()
		stderr.Reset()
		code = handleImage(context.Background(), paths, []string{alias, "my-fleet-image"}, &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "Removed image my-fleet-image") {
			t.Errorf("expected success message on remove alias %s, got: %s", alias, stdout.String())
		}
	}

	// 3. Podman rmi error
	restoreErr := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}))
	defer restoreErr()

	stdout.Reset()
	stderr.Reset()
	code = handleImageRemove(context.Background(), paths, []string{"nonexistent-image"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "failed removing image") {
		t.Errorf("expected error message on remove failure, got: %s", stderr.String())
	}
}
